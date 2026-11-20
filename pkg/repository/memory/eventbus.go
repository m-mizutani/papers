package memory

import (
	"context"
	"sync"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

// EventBus delivers events to subscribers in this process only. A
// multi-instance deployment needs a shared bus (Firestore listeners,
// Postgres LISTEN/NOTIFY or Pub/Sub); this one is for development and tests.
type EventBus struct {
	mu     sync.Mutex
	nextID int
	subs   map[paperKey]map[int]chan *model.Event
	buffer int
}

var _ interfaces.EventBus = (*EventBus)(nil)

// NewEventBus creates a bus whose subscriber channels hold up to buffer
// events; an event for a full subscriber is dropped for that subscriber.
func NewEventBus(buffer int) *EventBus {
	return &EventBus{subs: map[paperKey]map[int]chan *model.Event{}, buffer: buffer}
}

func (b *EventBus) Publish(_ context.Context, event *model.Event) error {
	if event == nil || event.WorkspaceID == "" || event.PaperID == "" {
		return goerr.Wrap(model.ErrInvalidArgument, "event target is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs[paperKey{event.WorkspaceID, event.PaperID}] {
		select {
		case ch <- clone(event):
		default:
			// Drop for a slow subscriber; it resynchronises via commit seq.
		}
	}
	return nil
}

func (b *EventBus) Subscribe(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) (<-chan *model.Event, func(), error) {
	b.mu.Lock()
	key := paperKey{ws, paper}
	if b.subs[key] == nil {
		b.subs[key] = map[int]chan *model.Event{}
	}
	id := b.nextID
	b.nextID++
	ch := make(chan *model.Event, b.buffer)
	b.subs[key][id] = ch
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			delete(b.subs[key], id)
			if len(b.subs[key]) == 0 {
				delete(b.subs, key)
			}
			close(ch)
		})
	}
	stop := context.AfterFunc(ctx, cancel)
	return ch, func() { stop(); cancel() }, nil
}
