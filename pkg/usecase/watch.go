package usecase

import (
	"context"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// Watch opens a realtime change feed of a paper for a reader. The channel
// closes when ctx ends or when the watcher must reload the paper.
func (u *UseCases) Watch(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, paperID model.PaperID) (<-chan *model.Event, error) {
	if _, _, _, err := u.paperFor(ctx, p, wsID, paperID, model.RoleViewer); err != nil {
		return nil, err
	}
	ch, err := u.repo.WatchPaper(ctx, wsID, paperID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to watch paper")
	}
	return ch, nil
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
