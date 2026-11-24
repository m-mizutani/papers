package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
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
	"github.com/m-mizutani/papers/pkg/usecase/sample"
)

// allPapers lists every paper of a workspace, the home included.
func allPapers(t *testing.T, e *env, ws model.WorkspaceID) []*model.Paper {
	t.Helper()
	papers, _, err := e.repo.ListPapers(context.Background(), ws, model.PaperQuery{Sort: model.SortCreated, Limit: model.MaxPaperListLimit})
	gt.NoError(t, err).Required()
	return papers
}

func byTitle(papers []*model.Paper) map[string]*model.Paper {
	out := map[string]*model.Paper{}
	for _, p := range papers {
		out[p.Title] = p
	}
	return out
}

func seed(t *testing.T, e *env) map[model.WorkspaceID]usecase.SeedResult {
	t.Helper()
	res, err := e.uc.SeedSamples(context.Background())
	gt.NoError(t, err).Required()
	out := map[model.WorkspaceID]usecase.SeedResult{}
	for _, r := range res {
		out[r.Workspace] = r
	}
	return out
}

func TestSeedSamplesWritesTheSamplesIntoAnEmptyWorkspace(t *testing.T) {
	e := newEnv(t)
	res := seed(t, e)
	gt.True(t, res[wsA].Seeded)
	gt.Equal(t, res[wsA].Papers, 4)

	ctx := context.Background()
	home := e.home(t, wsA)
	blocks := gt.R1(e.repo.ListBlocks(ctx, wsA, home)).NoError(t)
	gt.A(t, blocks).Length(4)
	gt.Equal(t, blocks[0].Type, model.BlockParagraph)
	gt.True(t, strings.Contains(blocks[0].Content, "papers firestore reset --demo"))

	papers := allPapers(t, e, wsA)
	gt.A(t, papers).Length(5) // the home and four samples
	titles := byTitle(papers)
	var order []string
	for _, b := range blocks[1:] {
		gt.Equal(t, b.Type, model.BlockPaper)
		for title, p := range titles {
			if p.ID == b.Props.PaperID {
				order = append(order, title)
			}
		}
	}
	gt.Equal(t, order, []string{"Welcome to papers", "Diagrams and rich blocks", "Release dashboard"})
	gt.Equal(t, titles["Alpha"].ID, home) // the home keeps its title
}

func TestSeedSamplesSkipsAWorkspaceWithAnotherPaper(t *testing.T) {
	e := newEnv(t)
	alice := e.user(t, "alice")
	gt.R1(e.uc.CreatePaper(context.Background(), alice, wsA, usecase.CreateInput{Title: "Unfiled notes"})).NoError(t)

	res := seed(t, e)
	gt.False(t, res[wsA].Seeded)
	gt.A(t, gt.R1(e.repo.ListBlocks(context.Background(), wsA, e.home(t, wsA))).NoError(t)).Length(0)
	gt.A(t, allPapers(t, e, wsA)).Length(2)
}

func TestSeedSamplesSkipsAWorkspaceWhoseHomeHasBlocks(t *testing.T) {
	e := newEnv(t)
	alice := e.user(t, "alice")
	home := e.home(t, wsA)
	e.apply(t, alice, wsA, home, para("our own words"))

	before := len(e.store.Keys())
	res := seed(t, e)
	gt.False(t, res[wsA].Seeded)
	gt.True(t, len(e.store.Keys()) > before) // beta was seeded
	for _, k := range e.store.Keys() {
		gt.False(t, strings.Contains(k, string(wsA))) // nothing staged for alpha
	}
	blocks := gt.R1(e.repo.ListBlocks(context.Background(), wsA, home)).NoError(t)
	gt.A(t, blocks).Length(1)
	gt.Equal(t, blocks[0].Content, "our own words")
	gt.A(t, allPapers(t, e, wsA)).Length(1)
}

func TestSeedSamplesOnlyWritesTheEmptyWorkspaces(t *testing.T) {
	e := newEnv(t)
	alice := e.user(t, "alice")
	e.child(t, alice, wsB, "", "Beta's own paper")

	res := seed(t, e)
	gt.True(t, res[wsA].Seeded)
	gt.False(t, res[wsB].Seeded)
	gt.A(t, allPapers(t, e, wsA)).Length(5)
	gt.A(t, allPapers(t, e, wsB)).Length(2)
}

// denyAll is a workspace rule that lets nobody in.
type denyAll struct{ *workspace.Registry }

func (denyAll) Allow(context.Context, *model.User, model.WorkspaceID) (bool, error) {
	return false, nil
}

func TestSeedSamplesIgnoresTheWorkspaceRule(t *testing.T) {
	repo := memory.New()
	rules := denyAll{workspace.Static(&model.Workspace{ID: wsA, Name: "Alpha"})}
	cfg := usecase.Config{MaxOpsPerBatch: 300, PaperTouchInterval: time.Minute, TrashRetention: time.Hour, JobLease: time.Minute, JobStep: 200}
	uc := gt.R1(usecase.New(repo, rules, attachment.NewMemory(), cfg, usecase.WithSyncJobs())).NoError(t)
	ctx := context.Background()
	gt.NoError(t, uc.Init(ctx)).Required()

	res := gt.R1(uc.SeedSamples(ctx)).NoError(t)
	gt.A(t, res).Length(1)
	gt.True(t, res[0].Seeded)
}

func TestSeedSamplesWritesOnce(t *testing.T) {
	e := newEnv(t)
	seed(t, e)
	before := len(allPapers(t, e, wsA))
	homeBlocks := len(gt.R1(e.repo.ListBlocks(context.Background(), wsA, e.home(t, wsA))).NoError(t))

	res := seed(t, e)
	gt.False(t, res[wsA].Seeded)
	gt.Equal(t, len(allPapers(t, e, wsA)), before)
	gt.Equal(t, len(gt.R1(e.repo.ListBlocks(context.Background(), wsA, e.home(t, wsA))).NoError(t)), homeBlocks)
}

func TestSeedSamplesTwiceAtOnceWritesOneSet(t *testing.T) {
	e := newEnv(t)
	other := e.another(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, uc := range []*usecase.UseCases{e.uc, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = uc.SeedSamples(ctx)
		}()
	}
	wg.Wait()
	gt.NoError(t, errors.Join(errs...))

	gt.A(t, allPapers(t, e, wsA)).Length(5)
	gt.A(t, gt.R1(e.repo.ListBlocks(ctx, wsA, e.home(t, wsA))).NoError(t)).Length(4)
}

// TestSamplesCoverBlockTypes keeps the sample papers showing every block type and
// paper kind. When it fails after adding a block type, add that block to a
// sample in pkg/usecase/sample (see "Demo samples" in CLAUDE.md).
func TestSamplesCoverBlockTypes(t *testing.T) {
	e := newEnv(t)
	seed(t, e)
	ctx := context.Background()
	types := map[model.BlockType]bool{}
	kinds := map[model.PaperKind]bool{}
	for _, p := range allPapers(t, e, wsA) {
		kinds[p.Kind.Or()] = true
		for _, b := range gt.R1(e.repo.ListBlocks(ctx, wsA, p.ID)).NoError(t) {
			types[b.Type] = true
		}
	}
	for _, bt := range model.BlockTypes() {
		if !types[bt] {
			t.Errorf("no sample paper has a %q block; add one to pkg/usecase/sample", bt)
		}
	}
	for _, k := range []model.PaperKind{model.PaperKindBlocks, model.PaperKindHTML} {
		if !kinds[k] {
			t.Errorf("no sample paper is of kind %q; add one to pkg/usecase/sample", k)
		}
	}
}

func TestSeedSamplesNestsAndStoresTheHTMLDocument(t *testing.T) {
	e := newEnv(t)
	seed(t, e)
	titles := byTitle(allPapers(t, e, wsA))
	gt.Equal(t, titles["Working with AI agents"].ParentID, titles["Welcome to papers"].ID)
	gt.Equal(t, titles["Diagrams and rich blocks"].ParentID, e.home(t, wsA))

	dash := titles["Release dashboard"]
	gt.Equal(t, dash.Kind, model.PaperKindHTML)
	alice := e.user(t, "alice")
	view := gt.R1(e.uc.ReadHTML(context.Background(), alice, wsA, dash.ID, "")).NoError(t)
	want := gt.R1(sample.Read("dashboard.html")).NoError(t)
	gt.Equal(t, view.HTML, string(want))
}

var reExternal = regexp.MustCompile(`(?i)(src|href)\s*=\s*["']?\s*(https?:)?//|url\(\s*["']?\s*(https?:)?//|@import`)

func TestSamplesAreSelfContained(t *testing.T) {
	e := newEnv(t)
	seed(t, e)
	ctx := context.Background()
	images := 0
	for _, p := range allPapers(t, e, wsA) {
		for _, b := range gt.R1(e.repo.ListBlocks(ctx, wsA, p.ID)).NoError(t) {
			if b.Type != model.BlockImage {
				continue
			}
			images++
			gt.Equal(t, b.Props.URL, "")
			a := gt.R1(e.repo.GetAttachment(ctx, wsA, p.ID, b.Props.AttachmentID)).NoError(t)
			gt.Equal(t, a.ContentType, "image/png")
		}
	}
	gt.True(t, images > 0)

	files := []string{sample.HomeFile}
	for _, en := range sample.Entries() {
		files = append(files, en.File)
	}
	for _, f := range files {
		body := string(gt.R1(sample.Read(f)).NoError(t))
		if m := reExternal.FindString(body); m != "" {
			t.Errorf("%s loads something from another host: %q", f, m)
		}
		for _, img := range sample.Images(body) {
			gt.R1(sample.Read(img)).NoError(t)
		}
	}
}

func TestSeedSamplesSkipsWithFewReads(t *testing.T) {
	e := newEnv(t)
	alice := e.user(t, "alice")
	for i := range 300 {
		gt.R1(e.uc.CreatePaper(context.Background(), alice, wsA, usecase.CreateInput{Title: fmt.Sprintf("Paper %d", i)})).NoError(t)
	}
	e.child(t, alice, wsB, "", "Beta's own paper")
	e.count.reset()

	res := seed(t, e)
	gt.False(t, res[wsA].Seeded)
	gt.Equal(t, e.count.callsOf("ListPapers"), 2) // one per workspace
	gt.Equal(t, e.count.blocks, 0)
	gt.Equal(t, e.count.papers, 0)
}

func TestSeedSamplesWritesThroughTheNormalPath(t *testing.T) {
	e := newEnv(t)
	seed(t, e)
	ctx := context.Background()
	alice := e.user(t, "alice")
	titles := byTitle(allPapers(t, e, wsA))

	hits := gt.R1(e.uc.Search(ctx, alice, wsA, usecase.SearchInput{Query: "emphasised", Limit: 10})).NoError(t)
	found := slices.ContainsFunc(hits, func(h *usecase.SearchHit) bool { return h.Paper.ID == titles["Welcome to papers"].ID })
	gt.True(t, found)

	for _, p := range titles {
		gt.Equal(t, p.CreatedBy.Kind, model.ActorSystem)
		gt.Equal(t, p.VersionNumber, 0)
		versions := gt.R1(e.uc.ListVersions(ctx, alice, wsA, p.ID, 0, 10)).NoError(t)
		gt.A(t, versions).Length(0)
		for _, b := range gt.R1(e.repo.ListBlocks(ctx, wsA, p.ID)).NoError(t) {
			gt.Equal(t, b.CreatedBy.Kind, model.ActorSystem)
		}
	}
}

// failingStore refuses to store anything.
type failingStore struct{ interfaces.AttachmentStore }

var errStoreDown = errors.New("the store is down")

func (failingStore) Put(context.Context, string, string, io.Reader) error { return errStoreDown }

func TestSeedSamplesReportsAStoreFailure(t *testing.T) {
	repo := memory.New()
	rules := workspace.Static(&model.Workspace{ID: wsA, Name: "Alpha"})
	cfg := usecase.Config{MaxOpsPerBatch: 300, PaperTouchInterval: time.Minute, TrashRetention: time.Hour, JobLease: time.Minute, JobStep: 200}
	uc := gt.R1(usecase.New(repo, rules, failingStore{attachment.NewMemory()}, cfg, usecase.WithSyncJobs())).NoError(t)
	ctx := context.Background()
	gt.NoError(t, uc.Init(ctx)).Required()

	_, err := uc.SeedSamples(ctx)
	gt.Error(t, err)
	gt.True(t, errors.Is(err, errStoreDown))
}
