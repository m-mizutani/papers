package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

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
