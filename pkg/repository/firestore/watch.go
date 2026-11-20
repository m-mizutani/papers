package firestore

import (
	"context"
	"errors"
	"sync"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/utils/async"
	"github.com/m-mizutani/papers/pkg/utils/errutil"
)

// sink is the channel side of one watch. It closes the channel once: when
// the watch ends, when the paper is deleted, or when the consumer falls
// behind (the consumer then reloads).
type sink struct {
	mu     sync.Mutex
	ch     chan *model.Event
	closed bool
	cancel context.CancelFunc
	ws     model.WorkspaceID
	paper  model.PaperID
}

func (s *sink) send(ev *model.Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	ev.WorkspaceID, ev.PaperID = s.ws, s.paper
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

// WatchPaper runs three Firestore listeners (the paper document, its blocks
// and its locks) for as long as ctx lives. It returns only after every
// listener has delivered its initial snapshot, so a caller that reads the
// paper after WatchPaper returns cannot miss a change in between. The
// initial snapshots themselves are not forwarded.
func (r *Repository) WatchPaper(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) (<-chan *model.Event, error) {
	if validID(string(ws)) != nil || validID(string(paper)) != nil {
		return nil, notFound("paper", paper)
	}
	wctx, cancel := context.WithCancel(ctx)
	s := &sink{ch: make(chan *model.Event, r.eventBuffer), cancel: cancel, ws: ws, paper: paper}

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

	blocksIt := r.blocksCol(ws, paper).Snapshots(wctx)
	if _, err := blocksIt.Next(); err != nil {
		paperIt.Stop()
		blocksIt.Stop()
		cancel()
		return nil, goerr.Wrap(err, "failed to watch blocks")
	}
	locksIt := r.locksCol(ws, paper).Snapshots(wctx)
	if _, err := locksIt.Next(); err != nil {
		paperIt.Stop()
		blocksIt.Stop()
		locksIt.Stop()
		cancel()
		return nil, goerr.Wrap(err, "failed to watch locks")
	}

	var wg sync.WaitGroup
	wg.Add(3)
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
				s.send(&model.Event{Type: model.EventPaperDeleted})
				s.close()
				return nil
			}
			var p model.Paper
			if err := snap.DataTo(&p); err != nil {
				fail(ctx, goerr.Wrap(err, "failed to decode paper"), "paper")
				return nil
			}
			if !s.send(&model.Event{Type: model.EventPaperUpdated, Paper: &p}) {
				return nil
			}
		}
	})

	async.DispatchCancelable(wctx, func(ctx context.Context) error {
		defer wg.Done()
		defer blocksIt.Stop()
		for {
			qs, err := blocksIt.Next()
			if err != nil {
				fail(ctx, err, "blocks")
				return nil
			}
			for _, ch := range qs.Changes {
				var ev *model.Event
				if ch.Kind == firestore.DocumentRemoved {
					ev = &model.Event{Type: model.EventBlockDeleted, BlockID: model.BlockID(ch.Doc.Ref.ID)}
				} else {
					var b model.Block
					if err := ch.Doc.DataTo(&b); err != nil {
						fail(ctx, goerr.Wrap(err, "failed to decode block"), "blocks")
						return nil
					}
					ev = &model.Event{Type: model.EventBlockPut, Block: &b}
				}
				if !s.send(ev) {
					return nil
				}
			}
		}
	})

	async.DispatchCancelable(wctx, func(ctx context.Context) error {
		defer wg.Done()
		defer locksIt.Stop()
		for {
			qs, err := locksIt.Next()
			if err != nil {
				fail(ctx, err, "locks")
				return nil
			}
			for _, ch := range qs.Changes {
				var l model.BlockLock
				if err := ch.Doc.DataTo(&l); err != nil {
					fail(ctx, goerr.Wrap(err, "failed to decode lock"), "locks")
					return nil
				}
				ev := &model.Event{Type: model.EventLock, Lock: &l}
				if ch.Kind == firestore.DocumentRemoved {
					ev = &model.Event{Type: model.EventUnlock, BlockID: l.BlockID, HolderKey: l.HolderKey}
				}
				if !s.send(ev) {
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
