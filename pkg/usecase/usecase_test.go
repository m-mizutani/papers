package usecase_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/repository/memory"
	"github.com/m-mizutani/papers/pkg/usecase"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type env struct {
	repo  *memory.Repository
	bus   *memory.EventBus
	uc    *usecase.UseCases
	clock *clock
}

func newEnv(t *testing.T) *env {
	t.Helper()
	repo := memory.New()
	bus := memory.NewEventBus(64)
	c := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	uc, err := usecase.New(repo, bus, usecase.Config{
		LockTTL: 30 * time.Second, WorkspaceCreation: usecase.WorkspaceCreationEveryone, MaxOpsPerCommit: 500,
	}, usecase.WithClock(c.Now))
	gt.NoError(t, err)
	return &env{repo: repo, bus: bus, uc: uc, clock: c}
}

func (e *env) user(t *testing.T, name string) *model.Principal {
	t.Helper()
	u := &model.User{ID: model.NewUserID(), Email: name + "@example.com", Name: name}
	gt.NoError(t, e.repo.PutUser(context.Background(), u))
	return &model.Principal{User: u, HolderKey: "conn-" + name}
}

func (e *env) workspace(t *testing.T, owner *model.Principal, bindings ...model.RoleBinding) model.WorkspaceID {
	t.Helper()
	ctx := context.Background()
	ws := gt.R1(e.uc.CreateWorkspace(ctx, owner, usecase.CreateWorkspaceInput{Name: "WS"})).NoError(t)
	if len(bindings) > 0 {
		all := append(ws.Workspace.Bindings, bindings...)
		gt.R1(e.uc.UpdateWorkspace(ctx, owner, ws.Workspace.ID, usecase.UpdateWorkspaceInput{Bindings: &all})).NoError(t)
	}
	return ws.Workspace.ID
}

func userBinding(p *model.Principal, role model.Role) model.RoleBinding {
	return model.RoleBinding{Subject: model.Subject{Kind: model.SubjectUser, ID: string(p.User.ID)}, Role: role}
}

func ptr[T any](v T) *T { return &v }

func TestNewValidatesConfig(t *testing.T) {
	repo, bus := memory.New(), memory.NewEventBus(1)
	_, err := usecase.New(repo, bus, usecase.Config{LockTTL: 0, WorkspaceCreation: usecase.WorkspaceCreationEveryone, MaxOpsPerCommit: 1})
	gt.Error(t, err)
	_, err = usecase.New(repo, bus, usecase.Config{LockTTL: time.Second, WorkspaceCreation: "x", MaxOpsPerCommit: 1})
	gt.Error(t, err)
	_, err = usecase.New(repo, bus, usecase.Config{LockTTL: time.Second, WorkspaceCreation: usecase.WorkspaceCreationAdmin, MaxOpsPerCommit: 0})
	gt.Error(t, err)
	_, err = usecase.New(nil, bus, usecase.Config{LockTTL: time.Second, WorkspaceCreation: usecase.WorkspaceCreationAdmin, MaxOpsPerCommit: 1})
	gt.Error(t, err)
}
