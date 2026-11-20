package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func TestLocks(t *testing.T) {
	pe := newPaperEnv(t)
	ctx := context.Background()
	r := pe.commit(t, pe.editor, insertOp("x", nil))
	id := r.Commit.Changes[0].BlockID

	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := gt.R1(pe.uc.Watch(wctx, pe.viewer, pe.ws, pe.paper)).NoError(t)

	l := gt.R1(pe.uc.AcquireLock(ctx, pe.editor, pe.ws, pe.paper, id, "tab-1")).NoError(t)
	gt.Equal(t, l.Holder.UserID, pe.editor.User.ID)
	gt.True(t, l.ExpiresAt.Sub(l.AcquiredAt) == 30*time.Second)
	ev := <-ch
	gt.Equal(t, ev.Type, model.EventLock)
	gt.Equal(t, ev.Lock.BlockID, id)

	cur, err := pe.uc.AcquireLock(ctx, pe.owner, pe.ws, pe.paper, id, "tab-9")
	gt.True(t, errors.Is(err, model.ErrBlockLocked))
	gt.Equal(t, cur.HolderKey, "tab-1")

	view := gt.R1(pe.uc.GetPaper(ctx, pe.viewer, pe.ws, pe.paper)).NoError(t)
	gt.A(t, view.Locks).Length(1)

	// viewers cannot lock, and missing blocks cannot be locked
	_, err = pe.uc.AcquireLock(ctx, pe.viewer, pe.ws, pe.paper, id, "v")
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))
	_, err = pe.uc.AcquireLock(ctx, pe.editor, pe.ws, pe.paper, "missing", "tab-1")
	gt.True(t, errors.Is(err, model.ErrNotFound))
	_, err = pe.uc.AcquireLock(ctx, pe.editor, pe.ws, pe.paper, id, "")
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// releasing someone else's lock does nothing
	gt.NoError(t, pe.uc.ReleaseLock(ctx, pe.owner, pe.ws, pe.paper, id, "tab-9"))
	gt.A(t, gt.R1(pe.uc.GetPaper(ctx, pe.viewer, pe.ws, pe.paper)).NoError(t).Locks).Length(1)

	gt.NoError(t, pe.uc.ReleaseLock(ctx, pe.editor, pe.ws, pe.paper, id, "tab-1"))
	ev = <-ch
	gt.Equal(t, ev.Type, model.EventUnlock)
	gt.Equal(t, ev.HolderKey, "tab-1")
	gt.A(t, gt.R1(pe.uc.GetPaper(ctx, pe.viewer, pe.ws, pe.paper)).NoError(t).Locks).Length(0)

	gt.Equal(t, pe.uc.LockTTL(), int64(30000))
}

func TestWatchAndCanRead(t *testing.T) {
	pe := newPaperEnv(t)
	ctx := context.Background()
	outsider := pe.user(t, "outsider")
	_, err := pe.uc.Watch(ctx, outsider, pe.ws, pe.paper)
	gt.True(t, errors.Is(err, model.ErrNotFound))

	gt.True(t, gt.R1(pe.uc.CanRead(ctx, pe.viewer, pe.ws, pe.paper)).NoError(t))
	gt.False(t, gt.R1(pe.uc.CanRead(ctx, outsider, pe.ws, pe.paper)).NoError(t))

	// restricting the paper revokes the viewer's access
	gt.R1(pe.uc.UpdatePaperAccess(ctx, pe.owner, pe.ws, pe.paper, model.PaperAccess{Restricted: true})).NoError(t)
	gt.False(t, gt.R1(pe.uc.CanRead(ctx, pe.viewer, pe.ws, pe.paper)).NoError(t))
}
