package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
)

func TestGroups(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.user(t, "admin")
	admin.SystemAdmin = true
	member := e.user(t, "member")

	_, err := e.uc.CreateGroup(ctx, member, usecase.GroupInput{Name: "eng"})
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))

	g := gt.R1(e.uc.CreateGroup(ctx, admin, usecase.GroupInput{Name: "eng", Members: []model.UserID{member.User.ID, member.User.ID}})).NoError(t)
	gt.Equal(t, g.Members, []model.UserID{member.User.ID})

	_, err = e.uc.CreateGroup(ctx, admin, usecase.GroupInput{Name: "x", Members: []model.UserID{"ghost"}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// group grants workspace access
	owner := e.user(t, "owner")
	wsID := e.workspace(t, owner, model.RoleBinding{Subject: model.Subject{Kind: model.SubjectGroup, ID: string(g.ID)}, Role: model.RoleEditor})
	member.Groups = []model.GroupID{g.ID}
	ws := gt.R1(e.uc.GetWorkspace(ctx, member, wsID)).NoError(t)
	gt.Equal(t, ws.Role, model.RoleEditor)

	updated := gt.R1(e.uc.UpdateGroup(ctx, admin, g.ID, usecase.GroupInput{Name: "platform"})).NoError(t)
	gt.Equal(t, updated.Name, "platform")
	gt.A(t, updated.Members).Length(0)

	groups := gt.R1(e.uc.ListGroups(ctx, member)).NoError(t)
	gt.A(t, groups).Length(1)

	gt.NoError(t, e.uc.DeleteGroup(ctx, admin, g.ID))
	gt.True(t, errors.Is(e.uc.DeleteGroup(ctx, admin, g.ID), model.ErrNotFound))

	users := gt.R1(e.uc.ListUsers(ctx, member)).NoError(t)
	gt.A(t, users).Length(3)
}
