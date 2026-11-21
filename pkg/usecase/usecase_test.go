package usecase_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/repository/memory"
	"github.com/m-mizutani/papers/pkg/service/attachment"
	"github.com/m-mizutani/papers/pkg/service/workspace"
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

// rules lets a test decide who may use which workspace.
type rules struct {
	*workspace.Registry
	deny map[model.WorkspaceID]map[model.UserID]bool
}

func (r *rules) Allow(ctx context.Context, u *model.User, ws model.WorkspaceID) (bool, error) {
	if r.deny[ws][u.ID] {
		return false, nil
	}
	return r.Registry.Allow(ctx, u, ws)
}

type env struct {
	repo  *memory.Repository
	count *counting
	store *attachment.Memory
	rules *rules
	uc    *usecase.UseCases
	clock *clock
	cfg   usecase.Config
}

// another is a second instance on the same storage.
func (e *env) another(t *testing.T) *usecase.UseCases {
	t.Helper()
	uc, err := usecase.New(e.count, e.rules, e.store, e.cfg, usecase.WithClock(e.clock.Now), usecase.WithoutBackgroundJobs())
	gt.NoError(t, err).Required()
	return uc
}

const (
	wsA model.WorkspaceID = "alpha"
	wsB model.WorkspaceID = "beta"
)

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, nil)
}

// newEnvWith builds an env whose config tune may change, with extra
// options for the use cases.
func newEnvWith(t *testing.T, tune func(*usecase.Config), opts ...usecase.Option) *env {
	t.Helper()
	repo := memory.New(memory.WithEventBuffer(256))
	count := &counting{Repository: repo}
	c := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	r := &rules{
		Registry: workspace.Static(&model.Workspace{ID: wsA, Name: "Alpha", Description: "write runbooks"}, &model.Workspace{ID: wsB, Name: "Beta"}),
		deny:     map[model.WorkspaceID]map[model.UserID]bool{},
	}
	store := attachment.NewMemory()
	cfg := usecase.Config{
		MaxOpsPerBatch: 300, PaperTouchInterval: time.Minute, TrashRetention: 30 * 24 * time.Hour, JobInline: time.Minute, JobLease: time.Minute, JobStep: 200,
	}
	if tune != nil {
		tune(&cfg)
	}
	uc, err := usecase.New(count, r, store, cfg, append([]usecase.Option{usecase.WithClock(c.Now)}, opts...)...)
	gt.NoError(t, err).Required()
	gt.NoError(t, uc.Init(context.Background())).Required()
	return &env{repo: repo, count: count, store: store, rules: r, uc: uc, clock: c, cfg: cfg}
}

func (e *env) user(t *testing.T, name string) *model.Principal {
	t.Helper()
	u := &model.User{ID: model.NewUserID(), Email: name + "@example.com", Name: name}
	gt.NoError(t, e.repo.PutUser(context.Background(), u))
	return &model.Principal{User: u}
}

func (e *env) deny(ws model.WorkspaceID, p *model.Principal) {
	if e.rules.deny[ws] == nil {
		e.rules.deny[ws] = map[model.UserID]bool{}
	}
	e.rules.deny[ws][p.User.ID] = true
}

func (e *env) home(t *testing.T, ws model.WorkspaceID) model.PaperID {
	t.Helper()
	rec := gt.R1(e.repo.GetWorkspaceRecord(context.Background(), ws)).NoError(t)
	return rec.HomePaperID
}

// child creates a public paper under parent (the home when empty).
func (e *env) child(t *testing.T, p *model.Principal, ws model.WorkspaceID, parent, title string) *model.Paper {
	t.Helper()
	paper := gt.R1(e.uc.CreatePaper(context.Background(), p, ws, usecase.CreateInput{Title: title, Parent: parent})).NoError(t)
	return paper
}

func (e *env) apply(t *testing.T, p *model.Principal, ws model.WorkspaceID, id model.PaperID, ops ...usecase.Op) *usecase.BatchResult {
	t.Helper()
	res, err := e.uc.Apply(context.Background(), p, ws, id, usecase.BatchInput{Ops: ops})
	gt.NoError(t, err).Required()
	return res
}

func (e *env) blocks(t *testing.T, p *model.Principal, ws model.WorkspaceID, id model.PaperID) []*model.Block {
	t.Helper()
	page := gt.R1(e.uc.ListBlocks(context.Background(), p, ws, id, "", 0)).NoError(t)
	return page.Blocks
}

func (e *env) paper(t *testing.T, ws model.WorkspaceID, id model.PaperID) *model.Paper {
	t.Helper()
	return gt.R1(e.repo.GetPaper(context.Background(), ws, id)).NoError(t)
}

func ptr[T any](v T) *T { return &v }

func para(content string) usecase.Op {
	return usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockParagraph), Content: ptr(content)}
}

func top(blocks []*model.Block) []*model.Block {
	var out []*model.Block
	for _, b := range blocks {
		if b.ParentID == "" {
			out = append(out, b)
		}
	}
	return out
}

// counting counts reads of papers and blocks, to pin down that reads are
// batched rather than one per item.
type counting struct {
	interfaces.Repository
	mu     sync.Mutex
	papers int // GetPaper + GetPapers calls
	blocks int
	// failChildren makes the next calls of ListChildrenOf fail.
	failChildren int
}

func (c *counting) ListChildrenOf(ctx context.Context, ws model.WorkspaceID, parents []model.PaperID) ([]*model.Paper, error) {
	c.mu.Lock()
	fail := c.failChildren > 0
	if fail {
		c.failChildren--
	}
	c.mu.Unlock()
	if fail {
		return nil, errors.New("storage is unavailable")
	}
	return c.Repository.ListChildrenOf(ctx, ws, parents)
}

func (c *counting) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.papers, c.blocks = 0, 0
}

func (c *counting) GetPaper(ctx context.Context, ws model.WorkspaceID, id model.PaperID) (*model.Paper, error) {
	c.mu.Lock()
	c.papers++
	c.mu.Unlock()
	return c.Repository.GetPaper(ctx, ws, id)
}

func (c *counting) GetPapers(ctx context.Context, ws model.WorkspaceID, ids []model.PaperID) ([]*model.Paper, error) {
	c.mu.Lock()
	c.papers++
	c.mu.Unlock()
	return c.Repository.GetPapers(ctx, ws, ids)
}

func (c *counting) ListBlocksRange(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, after string, limit int) ([]*model.Block, string, error) {
	c.mu.Lock()
	c.blocks++
	c.mu.Unlock()
	return c.Repository.ListBlocksRange(ctx, ws, paper, after, limit)
}

func (c *counting) ListBlocks(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) ([]*model.Block, error) {
	c.mu.Lock()
	c.blocks++
	c.mu.Unlock()
	return c.Repository.ListBlocks(ctx, ws, paper)
}

func TestNewValidatesConfig(t *testing.T) {
	repo := memory.New()
	ws := workspace.Static(&model.Workspace{ID: "x"})
	store := attachment.NewMemory()
	good := usecase.Config{MaxOpsPerBatch: 1, PaperTouchInterval: 0, TrashRetention: time.Hour, JobInline: 0, JobLease: time.Minute, JobStep: 1}
	gt.R1(usecase.New(repo, ws, store, good)).NoError(t)
	bad := []usecase.Config{
		{MaxOpsPerBatch: 0, TrashRetention: time.Hour},
		{MaxOpsPerBatch: 1, PaperTouchInterval: -1, TrashRetention: time.Hour},
		{MaxOpsPerBatch: 1},
		{MaxOpsPerBatch: 1, TrashRetention: time.Hour, JobLease: time.Minute},
		{MaxOpsPerBatch: 1, TrashRetention: time.Hour, JobStep: 1},
		{MaxOpsPerBatch: 1, TrashRetention: time.Hour, JobInline: -1, JobLease: time.Minute, JobStep: 1},
	}
	for _, c := range bad {
		_, err := usecase.New(repo, ws, store, c)
		gt.Error(t, err)
	}
	_, err := usecase.New(nil, ws, store, good)
	gt.Error(t, err)
}

func TestInitCreatesOneHome(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	home := e.home(t, wsA)
	gt.NoError(t, e.uc.Init(ctx))
	gt.Equal(t, e.home(t, wsA), home)
	h := e.paper(t, wsA, home)
	gt.Equal(t, h.Title, "Alpha")
	gt.Equal(t, h.Visibility, model.VisibilityPublic)
}
