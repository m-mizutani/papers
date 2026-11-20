package model_test

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func TestWorkspaceValidate(t *testing.T) {
	base := func() *model.Workspace {
		return &model.Workspace{
			ID: "ws", Name: "Team", CreatedBy: "u1",
			Bindings: []model.RoleBinding{bind(model.SubjectUser, "u1", model.RoleOwner)},
		}
	}
	gt.NoError(t, base().Validate())

	w := base()
	w.Name = "  "
	gt.Error(t, w.Validate())

	w = base()
	w.Bindings = []model.RoleBinding{bind(model.SubjectUser, "u1", model.RoleEditor)}
	gt.Error(t, w.Validate()) // no owner

	w = base()
	w.Bindings = append(w.Bindings, bind(model.SubjectUser, "u1", model.RoleViewer))
	gt.Error(t, w.Validate()) // duplicated subject
}
