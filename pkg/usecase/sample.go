package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"path"
	"slices"
	"strings"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase/sample"
	"github.com/m-mizutani/papers/pkg/utils/logging"
)

// papers firestore reset --demo writes the sample papers of package sample
// after deleting every document. SeedSamples still writes only into a
// workspace that is nearly empty (its home has no blocks and it has no
// paper besides the home), so a workspace someone wrote in while the reset
// ran is left alone. Nothing records that a workspace was
// seeded; the blocks the first batch puts into the home are the record, so a
// workspace is seeded at most once and never after people wrote in it.

// errNotEmpty stops the home batch when the home already has blocks.
var errNotEmpty = errors.New("workspace is not empty")

// SeedResult is what SeedSamples did to one workspace.
type SeedResult struct {
	Workspace model.WorkspaceID
	// Seeded is false when the workspace was not empty.
	Seeded bool
	// Papers is the number of sample papers written.
	Papers int
}

// SeedSamples writes the sample papers into each configured workspace that
// is still nearly empty, as papers itself (the system actor; no workspace
// rule applies). Call it after Init.
func (u *UseCases) SeedSamples(ctx context.Context) ([]SeedResult, error) {
	var out []SeedResult
	for _, ws := range u.workspaces.List() {
		r, err := u.seedWorkspace(ctx, ws.ID)
		if err != nil {
			return out, goerr.Wrap(err, "failed to write sample papers", goerr.V("workspace_id", ws.ID))
		}
		if r.Seeded {
			logging.From(ctx).Info("seeded sample papers", slog.String("workspace", string(ws.ID)), slog.Int("papers", r.Papers))
		} else {
			logging.From(ctx).Info("skipped sample papers", slog.String("workspace", string(ws.ID)), slog.String("reason", "not empty"))
		}
		out = append(out, r)
	}
	return out, nil
}

func (u *UseCases) seedWorkspace(ctx context.Context, ws model.WorkspaceID) (SeedResult, error) {
	res := SeedResult{Workspace: ws}
	homeID, err := u.homeID(ctx, ws)
	if err != nil {
		return res, err
	}
	// Any paper besides the home means people already use the workspace.
	// Two papers are enough to tell.
	papers, _, err := u.repo.ListPapers(ctx, ws, model.PaperQuery{Sort: model.SortCreated, Limit: 2})
	if err != nil {
		return res, goerr.Wrap(err, "failed to list papers")
	}
	var home *model.Paper
	for _, p := range papers {
		if p.ID != homeID {
			return res, nil
		}
		home = p
	}
	if home == nil {
		return res, goerr.New("the workspace has no home paper", goerr.V("workspace_id", ws))
	}
	// A home with blocks is skipped before the HTML sample is staged, so
	// that restarts leave no unreferenced document in the attachment store.
	if first, _, err := u.repo.ListBlocksRange(ctx, ws, home.ID, "", 1); err != nil {
		return res, goerr.Wrap(err, "failed to read the home's blocks")
	} else if len(first) > 0 {
		return res, nil
	}

	// The home batch decides, in its transaction, whether the workspace is
	// still empty. It also writes the home record once, so that two
	// instances seeding at the same time conflict on it and the later one
	// sees the blocks of the first.
	gate := func(a *applier) error {
		n, err := a.tx.CountBlocks(a.paper.ID)
		if err != nil {
			return goerr.Wrap(err, "failed to count the home's blocks")
		}
		if n > 0 {
			return errNotEmpty
		}
		a.paperChanged = true
		return nil
	}
	entries := sample.Entries()
	created, err := u.seedPaper(ctx, home, sample.HomeFile, childrenOf(entries, ""), gate)
	if errors.Is(err, errNotEmpty) {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	res.Seeded = true
	res.Papers = len(created)

	// Then each blocks paper gets its body and its children, parents first.
	for _, e := range entries {
		if e.Kind == model.PaperKindHTML {
			continue
		}
		paper, ok := created[e.Key]
		if !ok {
			return res, goerr.New("a sample paper was not created", goerr.V("key", e.Key))
		}
		if e.Kind == model.PaperKindDatabase {
			n, err := u.seedDatabase(ctx, paper, e.File)
			if err != nil {
				return res, err
			}
			res.Papers += n
			continue
		}
		more, err := u.seedPaper(ctx, paper, e.File, childrenOf(entries, e.Key), nil)
		if err != nil {
			return res, err
		}
		for k, p := range more {
			created[k] = p
		}
		res.Papers += len(more)
	}
	return res, nil
}

func childrenOf(entries []sample.Entry, parent string) []sample.Entry {
	var out []sample.Entry
	for _, e := range entries {
		if e.Parent == parent {
			out = append(out, e)
		}
	}
	return out
}

// seedPaper writes one sample paper's body (the Markdown file) followed by
// paper blocks for its children in one batch, and returns the children by
// key. Images the Markdown refers to are stored as attachments of the paper
// first.
func (u *UseCases) seedPaper(ctx context.Context, paper *model.Paper, file string, children []sample.Entry, check func(a *applier) error) (map[string]*model.Paper, error) {
	body, err := sample.Read(file)
	if err != nil {
		return nil, err
	}
	md := string(body)
	for _, name := range sample.Images(md) {
		data, err := sample.Read(name)
		if err != nil {
			return nil, err
		}
		a, err := u.attach(ctx, systemActor, paper, UploadInput{Name: name, ContentType: mime.TypeByExtension(path.Ext(name)), Body: bytes.NewReader(data)})
		if err != nil {
			return nil, goerr.Wrap(err, "failed to store a sample image", goerr.V("name", name))
		}
		md = strings.ReplaceAll(md, "("+name+")", "(attachment:"+string(a.ID)+")")
	}

	ops := []Op{{Kind: OpInsertMarkdown, Markdown: &md}}
	for _, c := range children {
		op := Op{Kind: OpInsert, Ref: c.Key, Type: ptr(model.BlockPaper), Title: ptr(c.Title)}
		if c.Kind == model.PaperKindDatabase {
			op.PaperKind = ptr(model.PaperKindDatabase)
		}
		if c.Kind == model.PaperKindHTML {
			doc, err := sample.Read(c.File)
			if err != nil {
				return nil, err
			}
			op.PaperKind, op.HTML = ptr(model.PaperKindHTML), ptr(string(doc))
		}
		ops = append(ops, op)
	}
	res, err := u.applyBatch(ctx, nil, systemActor, paper, BatchInput{Ops: ops}, check)
	if err != nil {
		return nil, err
	}

	byBlock := map[model.BlockID]model.PaperID{}
	for _, b := range res.Blocks {
		if b.Type == model.BlockPaper {
			byBlock[b.ID] = b.Props.PaperID
		}
	}
	ids := make([]model.PaperID, 0, len(children))
	keys := map[model.PaperID]string{}
	for _, c := range children {
		id, ok := byBlock[model.BlockID(res.Refs[c.Key])]
		if !ok {
			return nil, goerr.New("the batch did not create a sample paper", goerr.V("key", c.Key))
		}
		ids = append(ids, id)
		keys[id] = c.Key
	}
	out := map[string]*model.Paper{}
	if len(ids) == 0 {
		return out, nil
	}
	papers, err := u.repo.GetPapers(ctx, paper.WorkspaceID, ids)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read the new sample papers")
	}
	for _, p := range papers {
		out[keys[p.ID]] = p
	}
	if len(out) != len(ids) {
		return nil, goerr.New("a new sample paper could not be read", goerr.V("paper_id", paper.ID))
	}
	return out, nil
}

// seedDatabase fills a sample database: its definition's ops go to the
// database's batch (properties, elements and items with their values, in
// one transaction, as an agent would send them), then the items that have
// a body get it. It returns the number of items written.
func (u *UseCases) seedDatabase(ctx context.Context, db *model.Paper, file string) (int, error) {
	def, err := sample.ReadDatabase(file)
	if err != nil {
		return 0, err
	}
	ops := make([]Op, 0, len(def.Ops))
	for i, raw := range def.Ops {
		var op Op
		if err := json.Unmarshal(raw, &op); err != nil {
			return 0, goerr.Wrap(err, "failed to read a sample database op", goerr.V("file", file), goerr.V("index", i))
		}
		ops = append(ops, op)
	}
	res, err := u.applyBatch(ctx, nil, systemActor, db, BatchInput{Ops: ops}, nil)
	if err != nil {
		return 0, goerr.Wrap(err, "failed to write a sample database", goerr.V("file", file))
	}
	if len(def.Bodies) == 0 {
		return len(res.Items), nil
	}
	byID := map[model.PaperID]*model.Paper{}
	for _, q := range res.Items {
		byID[q.ID] = q
	}
	refs := make([]string, 0, len(def.Bodies))
	for ref := range def.Bodies {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	for _, ref := range refs {
		item, ok := byID[model.PaperID(res.Refs[ref])]
		if !ok {
			return 0, goerr.New("a sample database body names no item", goerr.V("file", file), goerr.V("ref", ref))
		}
		if _, err := u.seedPaper(ctx, item, def.Bodies[ref], nil, nil); err != nil {
			return 0, err
		}
	}
	return len(res.Items), nil
}
