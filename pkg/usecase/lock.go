package usecase

import (
	"context"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// AcquireLock soft-locks a block for holderKey. Re-acquiring renews it. On
// conflict it returns the current lock with an error wrapping
// model.ErrBlockLocked; the caller may still write with Force.
func (u *UseCases) AcquireLock(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, paperID model.PaperID, blockID model.BlockID, holderKey string) (*model.BlockLock, error) {
	if _, _, _, err := u.paperFor(ctx, p, wsID, paperID, model.RoleEditor); err != nil {
		return nil, err
	}
	if holderKey == "" {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "holder key is required")
	}
	if _, err := u.repo.GetBlock(ctx, wsID, paperID, blockID); err != nil {
		return nil, goerr.Wrap(err, "failed to get block", goerr.V("block_id", blockID))
	}
	now := u.now()
	lock := &model.BlockLock{
		WorkspaceID: wsID, PaperID: paperID, BlockID: blockID,
		HolderKey: holderKey, Holder: p.Actor(),
		AcquiredAt: now, ExpiresAt: now.Add(u.cfg.LockTTL),
	}
	got, err := u.repo.AcquireLock(ctx, lock, now)
	if err != nil {
		return got, err
	}
	u.publish(ctx, &model.Event{Type: model.EventLock, WorkspaceID: wsID, PaperID: paperID, Lock: got})
	return got, nil
}

// ReleaseLock releases the holder's lock; releasing a lock one does not
// hold is a no-op.
func (u *UseCases) ReleaseLock(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, paperID model.PaperID, blockID model.BlockID, holderKey string) error {
	if err := requirePrincipal(p); err != nil {
		return err
	}
	released, err := u.repo.ReleaseLock(ctx, wsID, paperID, blockID, holderKey)
	if err != nil {
		return goerr.Wrap(err, "failed to release lock")
	}
	if released {
		u.publish(ctx, &model.Event{Type: model.EventUnlock, WorkspaceID: wsID, PaperID: paperID, BlockID: blockID, HolderKey: holderKey})
	}
	return nil
}

// LockTTL reports how long a lock lives without renewal, so clients know
// how often to heartbeat.
func (u *UseCases) LockTTL() int64 { return u.cfg.LockTTL.Milliseconds() }

// Subscribe opens a realtime subscription to a paper for a reader.
func (u *UseCases) Subscribe(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, paperID model.PaperID) (<-chan *model.Event, func(), error) {
	if _, _, _, err := u.paperFor(ctx, p, wsID, paperID, model.RoleViewer); err != nil {
		return nil, nil, err
	}
	ch, cancel, err := u.bus.Subscribe(ctx, wsID, paperID)
	if err != nil {
		return nil, nil, goerr.Wrap(err, "failed to subscribe")
	}
	return ch, cancel, nil
}

// CanRead re-checks read access; long-lived subscriptions call it when the
// paper's access changes.
func (u *UseCases) CanRead(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, paperID model.PaperID) (bool, error) {
	_, _, _, err := u.paperFor(ctx, p, wsID, paperID, model.RoleViewer)
	if err == nil {
		return true, nil
	}
	if isAccessError(err) {
		return false, nil
	}
	return false, err
}
