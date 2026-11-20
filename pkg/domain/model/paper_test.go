package model_test

import (
	"strings"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func TestPaperValidate(t *testing.T) {
	actor := model.Actor{Kind: model.ActorHuman, UserID: "u1"}
	base := func() *model.Paper {
		return &model.Paper{ID: "p", WorkspaceID: "ws", Title: "T", CreatedBy: actor, UpdatedBy: actor}
	}
	gt.NoError(t, base().Validate())

	p := base()
	p.Title = strings.Repeat("x", model.MaxTitleLength+1)
	gt.Error(t, p.Validate())

	p = base()
	p.Title = "a\nb"
	gt.Error(t, p.Validate())

	p = base()
	p.CreatedBy = model.Actor{}
	gt.Error(t, p.Validate())
}
