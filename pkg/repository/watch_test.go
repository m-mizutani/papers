package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

func next(t *testing.T, ch <-chan *model.Event, want model.EventType) *model.Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("watch closed while waiting for %s", want)
			}
			if ev.Type == want {
				return ev
			}
		case <-deadline:
			t.Fatalf("no %s event", want)
		}
	}
}

func TestWatchPaper(t *testing.T) {
	runRepositoryTest(t, "streams block and paper changes", func(t *testing.T, repo interfaces.Repository) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p := newPaper(t, repo)
		existing := newBlock(p, "V", "before watch")
		gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error { return tx.PutBlock(existing) }))

		ch := gt.R1(repo.WatchPaper(ctx, p.WorkspaceID, p.ID)).NoError(t)

		b := newBlock(p, "k", "new")
		gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error { return tx.PutBlock(b) }))
		ev := next(t, ch, model.EventBlockPut)
		gt.Equal(t, ev.Block.ID, b.ID)
		gt.Equal(t, ev.Block.Content, "new")
		gt.Equal(t, ev.PaperID, p.ID)
		gt.Equal(t, ev.WorkspaceID, p.WorkspaceID)

		gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error { return tx.DeleteBlock(existing.ID) }))
		gt.Equal(t, next(t, ch, model.EventBlockDeleted).BlockID, existing.ID)

		now := time.Now().UTC()

		gt.R1(repo.UpdatePaperAccess(ctx, p.WorkspaceID, p.ID, model.PaperAccess{Restricted: false}, humanActor("u"), now)).NoError(t)
		gt.Equal(t, next(t, ch, model.EventPaperUpdated).Paper.Access.Restricted, false)

		gt.NoError(t, repo.DeletePaper(ctx, p.WorkspaceID, p.ID))
		next(t, ch, model.EventPaperDeleted)
	})

	runRepositoryTest(t, "cancel closes the channel", func(t *testing.T, repo interfaces.Repository) {
		ctx, cancel := context.WithCancel(context.Background())
		p := newPaper(t, repo)
		ch := gt.R1(repo.WatchPaper(ctx, p.WorkspaceID, p.ID)).NoError(t)
		cancel()
		select {
		case _, ok := <-ch:
			for ok {
				_, ok = <-ch
			}
		case <-time.After(10 * time.Second):
			t.Fatal("channel not closed")
		}
	})

	runRepositoryTest(t, "missing paper", func(t *testing.T, repo interfaces.Repository) {
		_, err := repo.WatchPaper(context.Background(), model.NewWorkspaceID(), model.NewPaperID())
		gt.Error(t, err)
	})
}

func TestWatchPaperOverflowClosesChannel(t *testing.T) {
	runRepositoryTest(t, "slow watcher is dropped", func(t *testing.T, repo interfaces.Repository) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p := newPaper(t, repo)
		ch := gt.R1(repo.WatchPaper(ctx, p.WorkspaceID, p.ID)).NoError(t)
		// The test backends buffer 64 events; write more without reading.
		for i := 0; i < 80; i++ {
			b := newBlock(p, "V", "x")
			gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error { return tx.PutBlock(b) }))
		}
		deadline := time.After(10 * time.Second)
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return
				}
			case <-deadline:
				t.Fatal("overflowing watch was not closed")
			}
		}
	})
}
