package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

func TestFeed(t *testing.T) {
	runRepositoryTest(t, "transaction events and presence reach watchers", func(t *testing.T, repo interfaces.Repository) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, p := homeAndChild(t, repo)
		ws := p.WorkspaceID
		start := time.Now().UTC().Add(-time.Second)

		ch := gt.R1(repo.Watch(ctx, ws, p.ID)).NoError(t)
		b := blockIn(p, "", "a1", "x")
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			if err := tx.PutBlock(b); err != nil {
				return err
			}
			return tx.AddEvent(&model.Event{Type: model.EventBlockPut, WorkspaceID: ws, PaperID: p.ID, At: time.Now().UTC(), Block: b})
		}))
		gt.NoError(t, repo.PutEvent(ctx, &model.Event{
			Type: model.EventPresence, WorkspaceID: ws, PaperID: p.ID, At: time.Now().UTC(),
			Presence: &model.Presence{BlockID: b.ID, Actor: p.CreatedBy, Until: time.Now().UTC().Add(model.PresenceTTL)},
		}))

		var got []model.EventType
		for len(got) < 2 {
			select {
			case ev, ok := <-ch:
				gt.True(t, ok).Required()
				got = append(got, ev.Type)
			case <-ctx.Done():
				t.Fatal("feed timed out")
			}
		}
		gt.Equal(t, got, []model.EventType{model.EventBlockPut, model.EventPresence})

		list := gt.R1(repo.ListEvents(ctx, ws, p.ID, start)).NoError(t)
		gt.A(t, list).Length(2)
		gt.Equal(t, list[0].Block.ID, b.ID)
	})

	runRepositoryTest(t, "cancel closes the channel", func(t *testing.T, repo interfaces.Repository) {
		ctx, cancel := context.WithCancel(context.Background())
		_, p := homeAndChild(t, repo)
		ch := gt.R1(repo.Watch(ctx, p.WorkspaceID, p.ID)).NoError(t)
		cancel()
		select {
		case _, ok := <-ch:
			gt.False(t, ok)
		case <-time.After(10 * time.Second):
			t.Fatal("channel was not closed")
		}
	})
}
