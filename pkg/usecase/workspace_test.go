package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/repository/memory"
	"github.com/m-mizutani/papers/pkg/usecase"
)

func TestWorkspaceLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob, carol := e.user(t, "alice"), e.user(t, "bob"), e.user(t, "carol")

	ws := gt.R1(e.uc.CreateWorkspace(ctx, alice, usecase.CreateWorkspaceInput{Name: " Security ", Emoji: "🛡"})).NoError(t)
	gt.Equal(t, ws.Workspace.Name, "Security")
	gt.Equal(t, ws.Role, model.RoleOwner)

	// bob cannot see it yet
	list := gt.R1(e.uc.ListWorkspaces(ctx, bob)).NoError(t)
	gt.A(t, list).Length(0)
	_, err := e.uc.GetWorkspace(ctx, bob, ws.Workspace.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))

	bindings := append(ws.Workspace.Bindings, userBinding(bob, model.RoleViewer))
	gt.R1(e.uc.UpdateWorkspace(ctx, alice, ws.Workspace.ID, usecase.UpdateWorkspaceInput{Bindings: &bindings})).NoError(t)

	got := gt.R1(e.uc.GetWorkspace(ctx, bob, ws.Workspace.ID)).NoError(t)
	gt.Equal(t, got.Role, model.RoleViewer)

	// viewer cannot administer
	_, err = e.uc.UpdateWorkspace(ctx, bob, ws.Workspace.ID, usecase.UpdateWorkspaceInput{Name: ptr("x")})
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))

	// unknown users in bindings are rejected
	bad := append(bindings, model.RoleBinding{Subject: model.Subject{Kind: model.SubjectUser, ID: "nobody"}, Role: model.RoleViewer})
	_, err = e.uc.UpdateWorkspace(ctx, alice, ws.Workspace.ID, usecase.UpdateWorkspaceInput{Bindings: &bad})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// removing every owner is rejected
	noOwner := []model.RoleBinding{userBinding(carol, model.RoleEditor)}
	_, err = e.uc.UpdateWorkspace(ctx, alice, ws.Workspace.ID, usecase.UpdateWorkspaceInput{Bindings: &noOwner})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestWorkspaceCreationPolicy(t *testing.T) {
	repo := memory.New()
	uc := gt.R1(usecase.New(repo, usecase.Config{WorkspaceCreation: usecase.WorkspaceCreationAdmin, MaxOpsPerCommit: 10})).NoError(t)
	ctx := context.Background()
	u := &model.User{ID: model.NewUserID(), Email: "u@example.com"}
	gt.NoError(t, repo.PutUser(ctx, u))

	_, err := uc.CreateWorkspace(ctx, &model.Principal{User: u}, usecase.CreateWorkspaceInput{Name: "x"})
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))

	gt.R1(uc.CreateWorkspace(ctx, &model.Principal{User: u, SystemAdmin: true}, usecase.CreateWorkspaceInput{Name: "x"})).NoError(t)

	_, err = uc.CreateWorkspace(ctx, &model.Principal{User: u, SystemAdmin: true, Token: &model.APIToken{Scope: model.ScopeWrite}}, usecase.CreateWorkspaceInput{Name: "x"})
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))

	_, err = uc.CreateWorkspace(ctx, nil, usecase.CreateWorkspaceInput{Name: "x"})
	gt.True(t, errors.Is(err, model.ErrUnauthenticated))
}
