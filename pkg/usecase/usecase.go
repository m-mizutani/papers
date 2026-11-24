// Package usecase implements the application logic shared by the Web UI
// (REST + WebSocket), the API and the MCP server. Every method takes the
// calling principal explicitly and enforces access itself, so no transport
// can bypass it.
//
// Access has two parts (docs/model.md, "Access"): whether the principal
// may use the workspace (the workspace's rule, narrowed by the token), and
// whether the paper is public or the principal's own private paper. A
// principal that may not see something gets ErrNotFound; one that may see but
// not write (a read-only token) gets ErrPermissionDenied.
package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/utils/logging"
)

// Config holds the knobs of the use cases. Defaults are owned by the CLI
// layer, never by this package.
type Config struct {
	// MaxOpsPerBatch bounds the operations of one request.
	MaxOpsPerBatch int
	// PaperTouchInterval is how stale a paper's UpdatedAt may get before a
	// write refreshes it. Refreshing on every write would make the paper
	// document a write hotspot (D6).
	PaperTouchInterval time.Duration
	// TrashRetention is how long a trashed paper is kept before it is
	// purged.
	TrashRetention time.Duration
	// JobLease is how long an instance holds a job without storing a step;
	// after it, another instance takes the job over.
	JobLease time.Duration
	// JobStep is how many papers (or blocks of a copy) one step of a job
	// handles in one transaction.
	JobStep int
}

func (c Config) validate() error {
	if c.MaxOpsPerBatch <= 0 {
		return goerr.New("max ops per batch must be positive")
	}
	if c.PaperTouchInterval < 0 {
		return goerr.New("paper touch interval must not be negative")
	}
	if c.TrashRetention <= 0 {
		return goerr.New("trash retention must be positive")
	}
	if c.JobLease <= 0 || c.JobStep <= 0 {
		return goerr.New("job lease and step must be positive")
	}
	return nil
}

// Limits of one batch (docs/model.md, "Batch rules").
const (
	MaxBlocksPerBatch = 240
	MaxBatchBytes     = 3 << 20
)

// UseCases bundles the application services.
type UseCases struct {
	repo       interfaces.Repository
	workspaces interfaces.Workspaces
	store      interfaces.AttachmentStore
	cfg        Config
	now        func() time.Time
	// runner names this instance as the holder of the jobs it runs.
	runner string
	// background runs the jobs a request starts after it answers; sync
	// runs them before (tests).
	background bool
	sync       bool
}

type Option func(*UseCases)

// WithClock replaces the clock (tests).
func WithClock(now func() time.Time) Option {
	return func(u *UseCases) { u.now = now }
}

// WithoutBackgroundJobs leaves the jobs a request starts for RunJobs
// instead of running them in a goroutine (tests).
func WithoutBackgroundJobs() Option {
	return func(u *UseCases) { u.background = false }
}

// WithSyncJobs runs the jobs a request starts to the end before the request
// answers (tests).
func WithSyncJobs() Option {
	return func(u *UseCases) { u.sync, u.background = true, false }
}

func New(repo interfaces.Repository, workspaces interfaces.Workspaces, store interfaces.AttachmentStore, cfg Config, opts ...Option) (*UseCases, error) {
	if repo == nil || workspaces == nil || store == nil {
		return nil, goerr.New("repository, workspaces and attachment store are required")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	u := &UseCases{
		repo: repo, workspaces: workspaces, store: store, cfg: cfg,
		now: func() time.Time { return time.Now().UTC() }, runner: string(model.NewJobID()), background: true,
	}
	for _, opt := range opts {
		opt(u)
	}
	return u, nil
}

// Init prepares the stored data for the configured workspaces: it creates
// the home paper of every workspace that has none yet, and warns about
// stored workspaces that are no longer configured (their data is kept but
// cannot be reached, D13).
func (u *UseCases) Init(ctx context.Context) error {
	now := u.now()
	system := model.Actor{Kind: model.ActorSystem, Name: "papers"}
	for _, ws := range u.workspaces.List() {
		home := &model.Paper{
			Kind: model.PaperKindBlocks,
			ID:   model.NewPaperID(), WorkspaceID: ws.ID, Title: ws.Name, Visibility: model.VisibilityPublic,
			CreatedBy: system, CreatedAt: now, UpdatedBy: system, UpdatedAt: now,
		}
		if _, err := u.repo.EnsureHome(ctx, ws.ID, home); err != nil {
			return goerr.Wrap(err, "failed to create home paper", goerr.V("workspace_id", ws.ID))
		}
	}
	records, err := u.repo.ListWorkspaceRecords(ctx)
	if err != nil {
		return goerr.Wrap(err, "failed to list stored workspaces")
	}
	for _, r := range records {
		if _, ok := u.workspaces.Get(r.ID); !ok {
			logging.From(ctx).Warn("stored workspace is not in the configuration; its papers are kept but hidden", slog.String("workspace", string(r.ID)))
		}
	}
	return nil
}

// --- access

func requirePrincipal(p *model.Principal) error {
	if p == nil || p.User == nil {
		return goerr.Wrap(model.ErrUnauthenticated, "principal is required")
	}
	return nil
}

func notFound(kind string, id any) error {
	return goerr.Wrap(model.ErrNotFound, kind+" not found", goerr.V("id", id))
}

func busy(paper *model.Paper) error {
	return goerr.Wrap(model.ErrBusy, "the paper is being moved to another workspace; try again when the move has finished", goerr.V("paper_id", paper.ID), goerr.V("job_id", paper.JobID))
}

func readOnly() error {
	return goerr.Wrap(model.ErrPermissionDenied, "this token can only read")
}

// workspaceFor returns the workspace if the principal may use it.
func (u *UseCases) workspaceFor(ctx context.Context, p *model.Principal, id model.WorkspaceID) (*model.Workspace, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	ws, ok := u.workspaces.Get(id)
	if !ok || !p.TokenAllows(id) {
		return nil, notFound("workspace", id)
	}
	allow, err := u.workspaces.Allow(ctx, p.User, id)
	if err != nil {
		// The decision could not be made: deny, and keep the cause.
		logging.From(ctx).Error("failed to evaluate workspace rule", slog.String("workspace", string(id)), logging.ErrAttr(err))
		return nil, notFound("workspace", id)
	}
	if !allow {
		return nil, notFound("workspace", id)
	}
	return ws, nil
}

// writableWorkspace is workspaceFor plus the token's write scope.
func (u *UseCases) writableWorkspace(ctx context.Context, p *model.Principal, id model.WorkspaceID) (*model.Workspace, error) {
	ws, err := u.workspaceFor(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if !p.CanWrite() {
		return nil, readOnly()
	}
	return ws, nil
}

// MovedError reports that a paper moved to another workspace. It matches
// model.ErrNotFound, so callers that do not follow forwards treat it as gone.
type MovedError struct {
	To *model.Forward
}

func (e *MovedError) Error() string   { return "paper moved to another workspace" }
func (e *MovedError) Is(t error) bool { return t == model.ErrNotFound }
func (e *MovedError) Unwrap() []error { return nil }
func asMoved(err error) (*MovedError, bool) {
	var m *MovedError
	ok := errors.As(err, &m)
	return m, ok
}

// IsMoved reports whether err says the paper moved, and where.
func IsMoved(err error) (*model.Forward, bool) {
	m, ok := asMoved(err)
	if !ok {
		return nil, false
	}
	return m.To, true
}

// paperFor returns the paper if the principal may use its workspace and see
// it. A paper moved to another workspace yields a MovedError.
func (u *UseCases) paperFor(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, id model.PaperID) (*model.Workspace, *model.Paper, error) {
	ws, err := u.workspaceFor(ctx, p, wsID)
	if err != nil {
		return nil, nil, err
	}
	if !model.ValidID(string(id)) {
		return nil, nil, notFound("paper", id)
	}
	paper, err := u.repo.GetPaper(ctx, wsID, id)
	if errors.Is(err, model.ErrNotFound) {
		if f, ferr := u.repo.GetForward(ctx, wsID, id); ferr == nil {
			return nil, nil, &MovedError{To: f}
		}
		return nil, nil, notFound("paper", id)
	}
	if err != nil {
		return nil, nil, goerr.Wrap(err, "failed to get paper", goerr.V("paper_id", id))
	}
	if paper.IsTrashed() {
		// the original of a finished move points everyone to the copy
		if f, ferr := u.repo.GetForward(ctx, wsID, id); ferr == nil {
			return nil, nil, &MovedError{To: f}
		}
	}
	if !p.SeesPaper(paper) {
		return nil, nil, notFound("paper", id)
	}
	return ws, paper, nil
}

// writablePaper is paperFor plus the token's write scope, and refuses
// trashed papers (they must be restored first).
func (u *UseCases) writablePaper(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, id model.PaperID) (*model.Workspace, *model.Paper, error) {
	ws, paper, err := u.paperFor(ctx, p, wsID, id)
	if err != nil {
		return nil, nil, err
	}
	if !p.CanWrite() {
		return nil, nil, readOnly()
	}
	if paper.IsTrashed() {
		return nil, nil, goerr.Wrap(model.ErrInvalidArgument, "the paper is in the trash; restore it first", goerr.V("paper_id", id))
	}
	if paper.IsBusy() {
		return nil, nil, busy(paper)
	}
	return ws, paper, nil
}

func (u *UseCases) homeID(ctx context.Context, ws model.WorkspaceID) (model.PaperID, error) {
	rec, err := u.repo.GetWorkspaceRecord(ctx, ws)
	if err != nil {
		return "", goerr.Wrap(err, "failed to get workspace record", goerr.V("workspace_id", ws))
	}
	return rec.HomePaperID, nil
}
