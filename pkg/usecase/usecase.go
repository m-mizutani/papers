// Package usecase implements the application logic shared by the Web UI
// (REST + WebSocket), the API and the MCP server. Every method takes the
// calling principal explicitly and enforces authorization itself, so no
// transport can bypass it.
package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

// WorkspaceCreation decides who may create workspaces.
type WorkspaceCreation string

const (
	WorkspaceCreationEveryone WorkspaceCreation = "everyone"
	WorkspaceCreationAdmin    WorkspaceCreation = "admin"
)

// Config holds the knobs of the use cases. Defaults are owned by the CLI
// layer, never by this package.
type Config struct {
	WorkspaceCreation WorkspaceCreation
	// MaxOpsPerCommit bounds one commit request.
	MaxOpsPerCommit int
	// PaperTouchInterval is how stale a paper's UpdatedAt may get before a
	// commit refreshes it. Refreshing on every commit would make the paper
	// record a write hotspot (see docs/design.html).
	PaperTouchInterval time.Duration
}

func (c Config) validate() error {
	switch c.WorkspaceCreation {
	case WorkspaceCreationEveryone, WorkspaceCreationAdmin:
	default:
		return goerr.New("invalid workspace creation policy", goerr.V("value", c.WorkspaceCreation))
	}
	if c.MaxOpsPerCommit <= 0 {
		return goerr.New("max ops per commit must be positive")
	}
	if c.PaperTouchInterval < 0 {
		return goerr.New("paper touch interval must not be negative")
	}
	return nil
}

// UseCases bundles the application services.
type UseCases struct {
	repo interfaces.Repository
	cfg  Config
	now  func() time.Time
}

type Option func(*UseCases)

// WithClock replaces the clock (tests).
func WithClock(now func() time.Time) Option {
	return func(u *UseCases) { u.now = now }
}

func New(repo interfaces.Repository, cfg Config, opts ...Option) (*UseCases, error) {
	if repo == nil {
		return nil, goerr.New("repository is required")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	u := &UseCases{repo: repo, cfg: cfg, now: func() time.Time { return time.Now().UTC() }}
	for _, opt := range opts {
		opt(u)
	}
	return u, nil
}

func requirePrincipal(p *model.Principal) error {
	if p == nil || p.User == nil {
		return goerr.Wrap(model.ErrUnauthenticated, "principal is required")
	}
	return nil
}

// denied hides existence: callers without read access get NotFound, those
// with read but insufficient access get PermissionDenied.
func denied(role model.Role, kind string, id any) error {
	if role == model.RoleNone {
		return goerr.Wrap(model.ErrNotFound, kind+" not found", goerr.V("id", id))
	}
	return goerr.Wrap(model.ErrPermissionDenied, "insufficient role", goerr.V(kind+"_id", id), goerr.V("role", role))
}

func (u *UseCases) workspaceFor(ctx context.Context, p *model.Principal, id model.WorkspaceID, need model.Role) (*model.Workspace, model.Role, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, model.RoleNone, err
	}
	ws, err := u.repo.GetWorkspace(ctx, id)
	if err != nil {
		return nil, model.RoleNone, goerr.Wrap(err, "failed to get workspace", goerr.V("workspace_id", id))
	}
	role := p.WorkspaceRole(ws)
	if !role.AtLeast(need) {
		return nil, role, denied(role, "workspace", id)
	}
	return ws, role, nil
}

func (u *UseCases) paperFor(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, id model.PaperID, need model.Role) (*model.Workspace, *model.Paper, model.Role, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, nil, model.RoleNone, err
	}
	ws, err := u.repo.GetWorkspace(ctx, wsID)
	if err != nil {
		return nil, nil, model.RoleNone, goerr.Wrap(err, "failed to get workspace", goerr.V("workspace_id", wsID))
	}
	paper, err := u.repo.GetPaper(ctx, wsID, id)
	if err != nil {
		return nil, nil, model.RoleNone, goerr.Wrap(err, "failed to get paper", goerr.V("paper_id", id))
	}
	role := p.PaperRole(ws, paper)
	if !role.AtLeast(need) {
		return nil, nil, role, denied(role, "paper", id)
	}
	return ws, paper, role, nil
}

// validateBindingSubjects checks that user and group subjects exist, so a
// typo cannot silently grant nothing.
func (u *UseCases) validateBindingSubjects(ctx context.Context, bs []model.RoleBinding) error {
	for _, b := range bs {
		switch b.Subject.Kind {
		case model.SubjectUser:
			if _, err := u.repo.GetUser(ctx, model.UserID(b.Subject.ID)); err != nil {
				return unknownSubject(err, "user", b.Subject.ID)
			}
		case model.SubjectGroup:
			if _, err := u.repo.GetGroup(ctx, model.GroupID(b.Subject.ID)); err != nil {
				return unknownSubject(err, "group", b.Subject.ID)
			}
		}
	}
	return nil
}

func unknownSubject(err error, kind, id string) error {
	if errors.Is(err, model.ErrNotFound) {
		return goerr.Wrap(model.ErrInvalidArgument, "binding refers to an unknown "+kind, goerr.V("id", id))
	}
	return goerr.Wrap(err, "failed to look up binding subject", goerr.V("kind", kind), goerr.V("id", id))
}

func isAccessError(err error) bool {
	return errors.Is(err, model.ErrNotFound) || errors.Is(err, model.ErrPermissionDenied)
}
