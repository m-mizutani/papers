package firestore

import (
	"context"
	"errors"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/utils/async"
	"github.com/m-mizutani/papers/pkg/utils/errutil"
)

// PutEvent appends a feed entry outside a transaction (presence).
func (r *Repository) PutEvent(ctx context.Context, ev *model.Event) error {
	if !validKeys(string(ev.WorkspaceID), string(ev.PaperID)) {
		return notFound("paper", ev.PaperID)
	}
	if _, err := r.GetPaper(ctx, ev.WorkspaceID, ev.PaperID); err != nil {
		return err
	}
	if _, err := r.eventsCol(ev.WorkspaceID, ev.PaperID).NewDoc().Create(ctx, newEventDoc(ev)); err != nil {
		return goerr.Wrap(err, "failed to put event")
	}
	return nil
}

func (r *Repository) ListEvents(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, since time.Time) ([]*model.Event, error) {
	if _, err := r.GetPaper(ctx, ws, paper); err != nil {
		return nil, err
	}
	q := r.eventsCol(ws, paper).Where("Event.At", ">=", since).OrderBy("Event.At", firestore.Asc)
	docs, err := all[eventDoc](q.Documents(ctx), "events")
	if err != nil {
		return nil, err
	}
	out := make([]*model.Event, len(docs))
	for i, d := range docs {
		ev := d.Event
		out[i] = &ev
	}
	return out, nil
}

// sink is the channel side of one watch. It closes the channel once: when
// the watch ends, when the paper is purged, or when the consumer falls
// behind (the consumer then reloads).
type sink struct {
	mu     sync.Mutex
	ch     chan *model.Event
	closed bool
	cancel context.CancelFunc
}

func (s *sink) send(ev *model.Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	select {
	case s.ch <- ev:
		return true
	default:
		s.closeLocked()
		return false
	}
}

func (s *sink) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}

func (s *sink) closeLocked() {
	if !s.closed {
		s.closed = true
		close(s.ch)
		s.cancel()
	}
}

func stopped(ctx context.Context, err error) bool {
	return ctx.Err() != nil || status.Code(err) == codes.Canceled || errors.Is(err, context.Canceled)
}

// Watch listens to the paper's feed collection for as long as ctx lives.
// It returns only after the listener has delivered its initial snapshot, so
// an entry written after Watch returns is delivered. The initial snapshot
// itself is not forwarded. The paper document is watched too, so that a
// purge closes the channel.
func (r *Repository) Watch(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) (<-chan *model.Event, error) {
	if !validKeys(string(ws), string(paper)) {
		return nil, notFound("paper", paper)
	}
	wctx, cancel := context.WithCancel(ctx)
	s := &sink{ch: make(chan *model.Event, r.eventBuffer), cancel: cancel}

	paperIt := r.paperDoc(ws, paper).Snapshots(wctx)
	first, err := paperIt.Next()
	if err != nil {
		paperIt.Stop()
		cancel()
		return nil, goerr.Wrap(err, "failed to watch paper")
	}
	if !first.Exists() {
		paperIt.Stop()
		cancel()
		return nil, notFound("paper", paper)
	}

	since := time.Now().UTC().Add(-time.Second)
	eventsIt := r.eventsCol(ws, paper).Where("Event.At", ">=", since).OrderBy("Event.At", firestore.Asc).Snapshots(wctx)
	initial, err := eventsIt.Next()
	if err != nil {
		paperIt.Stop()
		eventsIt.Stop()
		cancel()
		return nil, goerr.Wrap(err, "failed to watch events")
	}
	seen := map[string]bool{}
	for _, ch := range initial.Changes {
		seen[ch.Doc.Ref.ID] = true
	}

	var wg sync.WaitGroup
	wg.Add(2)
	fail := func(ctx context.Context, err error, what string) {
		if !stopped(ctx, err) {
			errutil.Handle(ctx, goerr.Wrap(err, "firestore listener failed", goerr.V("listener", what), goerr.V("paper_id", paper)), "firestore listener failed")
		}
		s.close()
	}

	async.DispatchCancelable(wctx, func(ctx context.Context) error {
		defer wg.Done()
		defer paperIt.Stop()
		for {
			snap, err := paperIt.Next()
			if err != nil {
				fail(ctx, err, "paper")
				return nil
			}
			if !snap.Exists() {
				s.send(&model.Event{Type: model.EventPaperDeleted, WorkspaceID: ws, PaperID: paper, At: time.Now().UTC()})
				s.close()
				return nil
			}
		}
	})

	async.DispatchCancelable(wctx, func(ctx context.Context) error {
		defer wg.Done()
		defer eventsIt.Stop()
		for {
			qs, err := eventsIt.Next()
			if err != nil {
				fail(ctx, err, "events")
				return nil
			}
			for _, ch := range qs.Changes {
				// A listener reports each document as added once; only the
				// entries of the initial snapshot are skipped.
				if ch.Kind != firestore.DocumentAdded || seen[ch.Doc.Ref.ID] {
					continue
				}
				d, err := decode[eventDoc](ch.Doc, "event")
				if err != nil {
					fail(ctx, err, "events")
					return nil
				}
				ev := d.Event
				if !s.send(&ev) {
					return nil
				}
			}
		}
	})

	async.DispatchCancelable(context.WithoutCancel(wctx), func(context.Context) error {
		<-wctx.Done()
		wg.Wait()
		s.close()
		return nil
	})
	return s.ch, nil
}
