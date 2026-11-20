package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

func TestEventBus(t *testing.T) {
	runEventBusTest(t, "delivers to subscribers of the same paper only", func(t *testing.T, bus interfaces.EventBus) {
		ctx := context.Background()
		ws, paper := model.NewWorkspaceID(), model.NewPaperID()

		ch1, cancel1, err := bus.Subscribe(ctx, ws, paper)
		gt.NoError(t, err)
		defer cancel1()
		ch2, cancel2, err := bus.Subscribe(ctx, ws, paper)
		gt.NoError(t, err)
		defer cancel2()
		other, cancelOther, err := bus.Subscribe(ctx, ws, model.NewPaperID())
		gt.NoError(t, err)
		defer cancelOther()

		gt.NoError(t, bus.Publish(ctx, &model.Event{Type: model.EventUnlock, WorkspaceID: ws, PaperID: paper, BlockID: "b1"}))

		for _, ch := range []<-chan *model.Event{ch1, ch2} {
			select {
			case ev := <-ch:
				gt.Equal(t, ev.Type, model.EventUnlock)
				gt.Equal(t, ev.BlockID, model.BlockID("b1"))
			case <-time.After(5 * time.Second):
				t.Fatal("event not delivered")
			}
		}
		select {
		case ev := <-other:
			t.Fatalf("unexpected event: %+v", ev)
		case <-time.After(50 * time.Millisecond):
		}
	})

	runEventBusTest(t, "cancel closes the channel", func(t *testing.T, bus interfaces.EventBus) {
		ch, cancel, err := bus.Subscribe(context.Background(), "w", "p")
		gt.NoError(t, err)
		cancel()
		cancel() // idempotent
		_, ok := <-ch
		gt.False(t, ok)
	})

	runEventBusTest(t, "context cancellation ends subscription", func(t *testing.T, bus interfaces.EventBus) {
		ctx, cancelCtx := context.WithCancel(context.Background())
		ch, cancel, err := bus.Subscribe(ctx, "w", "p")
		gt.NoError(t, err)
		defer cancel()
		cancelCtx()
		select {
		case _, ok := <-ch:
			gt.False(t, ok)
		case <-time.After(5 * time.Second):
			t.Fatal("channel not closed")
		}
	})

	runEventBusTest(t, "rejects event without target", func(t *testing.T, bus interfaces.EventBus) {
		gt.Error(t, bus.Publish(context.Background(), &model.Event{Type: model.EventCommit}))
	})
}
