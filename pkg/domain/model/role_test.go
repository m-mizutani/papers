package model_test

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func TestRoleOrdering(t *testing.T) {
	gt.True(t, model.RoleOwner.AtLeast(model.RoleEditor))
	gt.True(t, model.RoleEditor.AtLeast(model.RoleViewer))
	gt.True(t, model.RoleViewer.AtLeast(model.RoleNone))
	gt.False(t, model.RoleViewer.AtLeast(model.RoleEditor))
	gt.False(t, model.RoleNone.AtLeast(model.RoleViewer))

	gt.Equal(t, model.RoleViewer.Max(model.RoleEditor), model.RoleEditor)
	gt.Equal(t, model.RoleOwner.Min(model.RoleViewer), model.RoleViewer)
	gt.Equal(t, model.RoleNone.Max(model.RoleNone), model.RoleNone)
}

func TestRoleValidate(t *testing.T) {
	gt.NoError(t, model.RoleViewer.Validate())
	gt.NoError(t, model.RoleEditor.Validate())
	gt.NoError(t, model.RoleOwner.Validate())
	gt.Error(t, model.RoleNone.Validate())
	gt.Error(t, model.Role("admin").Validate())
}

func TestRoleBindingValidate(t *testing.T) {
	cases := []struct {
		name string
		b    model.RoleBinding
		ok   bool
	}{
		{"user", model.RoleBinding{Subject: model.Subject{Kind: model.SubjectUser, ID: "u1"}, Role: model.RoleEditor}, true},
		{"group", model.RoleBinding{Subject: model.Subject{Kind: model.SubjectGroup, ID: "g1"}, Role: model.RoleViewer}, true},
		{"domain", model.RoleBinding{Subject: model.Subject{Kind: model.SubjectDomain, ID: "example.com"}, Role: model.RoleViewer}, true},
		{"authenticated", model.RoleBinding{Subject: model.Subject{Kind: model.SubjectAuthenticated}, Role: model.RoleViewer}, true},
		{"authenticated with id", model.RoleBinding{Subject: model.Subject{Kind: model.SubjectAuthenticated, ID: "x"}, Role: model.RoleViewer}, false},
		{"user without id", model.RoleBinding{Subject: model.Subject{Kind: model.SubjectUser}, Role: model.RoleViewer}, false},
		{"unknown kind", model.RoleBinding{Subject: model.Subject{Kind: "robot", ID: "x"}, Role: model.RoleViewer}, false},
		{"no role", model.RoleBinding{Subject: model.Subject{Kind: model.SubjectUser, ID: "u1"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.b.Validate()
			if c.ok {
				gt.NoError(t, err)
			} else {
				gt.Error(t, err)
			}
		})
	}
}
