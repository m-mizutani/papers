package usecase

import (
	"context"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// StepJob runs one step of a job, as the instance holding it would.
func StepJob(ctx context.Context, u *UseCases, ws model.WorkspaceID, id model.JobID) error {
	j, err := u.repo.GetJob(ctx, ws, id)
	if err != nil {
		return err
	}
	if err := u.advance(ctx, j); err != nil {
		return u.recordFailure(ctx, j, err)
	}
	return nil
}
