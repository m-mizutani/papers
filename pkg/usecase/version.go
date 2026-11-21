package usecase

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

func versionBlocks(blocks []*model.Block) []model.VersionBlock {
	out := make([]model.VersionBlock, len(blocks))
	for i, b := range blocks {
		out[i] = model.VersionBlock{BlockID: b.ID, Rev: b.Rev, ParentID: b.ParentID, Order: b.Order}
	}
	return out
}

// sameState reports whether the current title and blocks equal a version.
func sameState(v *model.Version, title string, blocks []*model.Block) bool {
	return v != nil && v.Title == title && slices.Equal(v.Blocks, versionBlocks(blocks))
}

// SaveVersion saves the paper's current state as its next version. Anyone
// who can write the paper may save, people and agents alike; nothing saves
// automatically. When nothing changed since the latest version, no version
// is made and the result is nil.
func (u *UseCases) SaveVersion(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, note string) (*model.Version, error) {
	if _, _, err := u.writablePaper(ctx, p, ws, id); err != nil {
		return nil, err
	}
	note = strings.TrimSpace(note)
	if len(note) > model.MaxVersionNoteLength {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "version note is too long", goerr.V("max", model.MaxVersionNoteLength))
	}
	now := u.now()
	actor := p.Actor()
	var saved *model.Version
	err := u.repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
		saved = nil
		paper, err := tx.GetPaper(id)
		if err != nil {
			return goerr.Wrap(err, "failed to get paper")
		}
		blocks, err := tx.ListBlocks(id)
		if err != nil {
			return goerr.Wrap(err, "failed to list blocks")
		}
		var prev *model.Version
		if paper.VersionNumber > 0 {
			if prev, err = tx.GetVersion(id, paper.VersionNumber); err != nil {
				return goerr.Wrap(err, "failed to get latest version")
			}
		}
		if sameState(prev, paper.Title, blocks) {
			return nil
		}
		known := map[model.SnapshotRef]bool{}
		if prev != nil {
			for _, vb := range prev.Blocks {
				known[vb.Ref()] = true
			}
		}
		for _, b := range blocks {
			s := model.SnapshotOf(b)
			if known[model.SnapshotRef{BlockID: s.BlockID, Rev: s.Rev}] {
				continue
			}
			if err := tx.PutSnapshot(id, s); err != nil {
				return goerr.Wrap(err, "failed to write snapshot")
			}
		}
		v := &model.Version{
			WorkspaceID: ws, PaperID: id, Number: paper.VersionNumber + 1, Title: paper.Title,
			Blocks: versionBlocks(blocks), Note: note, CreatedBy: actor, CreatedAt: now,
		}
		if err := tx.PutVersion(v); err != nil {
			return goerr.Wrap(err, "failed to write version")
		}
		paper.VersionNumber = v.Number
		if err := tx.PutPaper(paper); err != nil {
			return goerr.Wrap(err, "failed to update paper")
		}
		for _, ev := range []*model.Event{
			{Type: model.EventVersionSaved, Version: v.Number},
			{Type: model.EventPaperUpdated, Paper: paper.Clone()},
		} {
			ev.WorkspaceID, ev.PaperID, ev.At = ws, id, now
			if err := tx.AddEvent(ev); err != nil {
				return goerr.Wrap(err, "failed to add event")
			}
		}
		saved = v
		return nil
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

// VersionSummary is a version without its block list.
type VersionSummary struct {
	Number     int         `json:"number"`
	Title      string      `json:"title"`
	Note       string      `json:"note,omitempty"`
	CreatedBy  model.Actor `json:"created_by"`
	CreatedAt  time.Time   `json:"created_at"`
	BlockCount int         `json:"block_count"`
}

func summarize(v *model.Version) *VersionSummary {
	return &VersionSummary{Number: v.Number, Title: v.Title, Note: v.Note, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt, BlockCount: len(v.Blocks)}
}

// ListVersions lists versions newest first, before the given number (0: from
// the latest).
func (u *UseCases) ListVersions(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, before, limit int) ([]*VersionSummary, error) {
	if _, _, err := u.paperFor(ctx, p, ws, id); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	vs, err := u.repo.ListVersions(ctx, ws, id, before, limit)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list versions")
	}
	out := make([]*VersionSummary, len(vs))
	for i, v := range vs {
		out[i] = summarize(v)
	}
	return out, nil
}

// VersionView is a version with its blocks put together from snapshots.
type VersionView struct {
	Version *VersionSummary `json:"version"`
	Paper   *model.Paper    `json:"paper"`
	Blocks  []*model.Block  `json:"blocks"`
}

func assemble(paper *model.Paper, vbs []model.VersionBlock, snaps []*model.BlockSnapshot) []*model.Block {
	byRef := map[model.SnapshotRef]*model.BlockSnapshot{}
	for _, s := range snaps {
		byRef[model.SnapshotRef{BlockID: s.BlockID, Rev: s.Rev}] = s
	}
	out := make([]*model.Block, 0, len(vbs))
	for _, vb := range vbs {
		s, ok := byRef[vb.Ref()]
		if !ok {
			continue
		}
		out = append(out, &model.Block{
			ID: vb.BlockID, WorkspaceID: paper.WorkspaceID, PaperID: paper.ID, ParentID: vb.ParentID, Order: vb.Order,
			Type: s.Type, Content: s.Content, Props: s.Props, Rev: vb.Rev,
		})
	}
	return out
}

func refsOf(vbs []model.VersionBlock) []model.SnapshotRef {
	refs := make([]model.SnapshotRef, len(vbs))
	for i, vb := range vbs {
		refs[i] = vb.Ref()
	}
	return refs
}

// ParseAt reads an "at" value: "" is the current state (0), "latest" the
// latest version (-1), a number that version.
func ParseAt(at string) (int, error) {
	switch at = strings.TrimPrefix(strings.TrimSpace(at), "v"); at {
	case "", "current":
		return 0, nil
	case "latest":
		return -1, nil
	}
	n, err := strconv.Atoi(at)
	if err != nil || n < 1 {
		return 0, goerr.Wrap(model.ErrInvalidArgument, `at must be "latest" or a version number`, goerr.V("at", at))
	}
	return n, nil
}

// GetVersion reads one version (number -1: the latest).
func (u *UseCases) GetVersion(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, number int) (*VersionView, error) {
	_, paper, err := u.paperFor(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	if number < 0 {
		number = paper.VersionNumber
	}
	if number == 0 {
		return nil, goerr.Wrap(model.ErrNotFound, "the paper has no version yet")
	}
	v, err := u.repo.GetVersion(ctx, ws, id, number)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get version", goerr.V("number", number))
	}
	snaps, err := u.repo.GetSnapshots(ctx, ws, id, refsOf(v.Blocks))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get snapshots")
	}
	view := paper.Clone()
	view.Title = v.Title
	return &VersionView{Version: summarize(v), Paper: view, Blocks: assemble(paper, v.Blocks, snaps)}, nil
}

// Status tells whether the current state differs from the latest version.
type Status struct {
	VersionNumber int  `json:"version_number"`
	Changed       bool `json:"changed"`
}

func (u *UseCases) Status(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID) (*Status, error) {
	_, paper, err := u.paperFor(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	blocks, err := u.repo.ListBlocks(ctx, ws, id)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list blocks")
	}
	st := &Status{VersionNumber: paper.VersionNumber, Changed: true}
	if paper.VersionNumber == 0 {
		st.Changed = len(blocks) > 0 || paper.Title != ""
		return st, nil
	}
	v, err := u.repo.GetVersion(ctx, ws, id, paper.VersionNumber)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get latest version")
	}
	st.Changed = !sameState(v, paper.Title, blocks)
	return st, nil
}

// DiffKind is how a block differs between two states.
type DiffKind string

const (
	DiffAdded   DiffKind = "added"
	DiffRemoved DiffKind = "removed"
	DiffChanged DiffKind = "changed"
	DiffMoved   DiffKind = "moved"
)

type DiffEntry struct {
	BlockID model.BlockID `json:"block_id"`
	Kind    DiffKind      `json:"kind"`
	Before  *model.Block  `json:"before,omitempty"`
	After   *model.Block  `json:"after,omitempty"`
}

type Diff struct {
	From        int          `json:"from"`
	To          int          `json:"to"`
	TitleBefore string       `json:"title_before"`
	TitleAfter  string       `json:"title_after"`
	Entries     []*DiffEntry `json:"entries"`
}

type side struct {
	title  string
	vbs    []model.VersionBlock
	blocks map[model.BlockID]*model.Block // current state only
	number int
}

func (u *UseCases) sideOf(ctx context.Context, paper *model.Paper, number int) (*side, error) {
	if number == 0 {
		blocks, err := u.repo.ListBlocks(ctx, paper.WorkspaceID, paper.ID)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list blocks")
		}
		s := &side{title: paper.Title, vbs: versionBlocks(blocks), blocks: map[model.BlockID]*model.Block{}}
		for _, b := range blocks {
			s.blocks[b.ID] = b
		}
		return s, nil
	}
	if number < 0 {
		number = paper.VersionNumber
	}
	if number == 0 {
		return nil, goerr.Wrap(model.ErrNotFound, "the paper has no version yet")
	}
	v, err := u.repo.GetVersion(ctx, paper.WorkspaceID, paper.ID, number)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get version", goerr.V("number", number))
	}
	return &side{title: v.Title, vbs: v.Blocks, number: number}, nil
}

// contents reads the blocks of refs on a side: from the current state, or
// from snapshots for a version.
func (u *UseCases) contents(ctx context.Context, paper *model.Paper, s *side, want []model.VersionBlock) (map[model.BlockID]*model.Block, error) {
	out := map[model.BlockID]*model.Block{}
	if s.blocks != nil {
		for _, vb := range want {
			out[vb.BlockID] = s.blocks[vb.BlockID]
		}
		return out, nil
	}
	if len(want) == 0 {
		return out, nil
	}
	snaps, err := u.repo.GetSnapshots(ctx, paper.WorkspaceID, paper.ID, refsOf(want))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get snapshots")
	}
	for _, b := range assemble(paper, want, snaps) {
		out[b.ID] = b
	}
	return out, nil
}

// Diff compares two states of a paper: version numbers, 0 for the current
// state and -1 for the latest version. Blocks are matched by ID and
// revision, and only differing ones are read.
func (u *UseCases) Diff(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, from, to int) (*Diff, error) {
	_, paper, err := u.paperFor(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	a, err := u.sideOf(ctx, paper, from)
	if err != nil {
		return nil, err
	}
	b, err := u.sideOf(ctx, paper, to)
	if err != nil {
		return nil, err
	}
	before := map[model.BlockID]model.VersionBlock{}
	for _, vb := range a.vbs {
		before[vb.BlockID] = vb
	}
	after := map[model.BlockID]model.VersionBlock{}
	for _, vb := range b.vbs {
		after[vb.BlockID] = vb
	}
	var needA, needB []model.VersionBlock
	type pending struct {
		id   model.BlockID
		kind DiffKind
	}
	var order []pending
	for _, vb := range a.vbs {
		nb, ok := after[vb.BlockID]
		switch {
		case !ok:
			order = append(order, pending{vb.BlockID, DiffRemoved})
			needA = append(needA, vb)
		case nb.Rev != vb.Rev:
			order = append(order, pending{vb.BlockID, DiffChanged})
			needA, needB = append(needA, vb), append(needB, nb)
		case nb.Order != vb.Order || nb.ParentID != vb.ParentID:
			order = append(order, pending{vb.BlockID, DiffMoved})
			needB = append(needB, nb)
		}
	}
	for _, vb := range b.vbs {
		if _, ok := before[vb.BlockID]; !ok {
			order = append(order, pending{vb.BlockID, DiffAdded})
			needB = append(needB, vb)
		}
	}
	ca, err := u.contents(ctx, paper, a, needA)
	if err != nil {
		return nil, err
	}
	cb, err := u.contents(ctx, paper, b, needB)
	if err != nil {
		return nil, err
	}
	d := &Diff{From: a.number, To: b.number, TitleBefore: a.title, TitleAfter: b.title}
	for _, pe := range order {
		e := &DiffEntry{BlockID: pe.id, Kind: pe.kind, Before: ca[pe.id], After: cb[pe.id]}
		if pe.kind == DiffChanged && e.Before != nil && e.After != nil && model.SameContents(e.Before, e.After) {
			// a revision bump without a visible change (e.g. restored)
			if e.Before.Order == e.After.Order {
				continue
			}
			e.Kind = DiffMoved
		}
		d.Entries = append(d.Entries, e)
	}
	return d, nil
}

// freshRev is the revision of a block brought back by a restore. It jumps
// past every revision the block could have had, so (block, revision) keeps
// naming one content.
func freshRev(old int64, now time.Time) int64 {
	return max(old+1, now.UnixMicro())
}

// RestoreVersion makes the current state equal to a version: blocks deleted
// since come back with their IDs, blocks made since go away. Child papers
// that moved elsewhere are not brought back, and children in the trash come
// back only for their creator; both are listed in the result. Unsaved
// changes of the current state are lost, so screens ask first.
func (u *UseCases) RestoreVersion(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, number int) (*BatchResult, error) {
	if _, _, err := u.writablePaper(ctx, p, ws, id); err != nil {
		return nil, err
	}
	if number < 1 {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "version is required")
	}
	now := u.now()
	var result *BatchResult
	err := u.repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
		paper, err := tx.GetPaper(id)
		if err != nil {
			return goerr.Wrap(err, "failed to get paper")
		}
		v, err := tx.GetVersion(id, number)
		if err != nil {
			return goerr.Wrap(err, "failed to get version", goerr.V("number", number))
		}
		snaps, err := tx.GetSnapshots(id, refsOf(v.Blocks))
		if err != nil {
			return goerr.Wrap(err, "failed to get snapshots")
		}
		target := assemble(paper, v.Blocks, snaps)
		if len(target) != len(v.Blocks) {
			return goerr.New("version refers to missing snapshots", goerr.V("paper_id", id), goerr.V("number", number))
		}
		current, err := tx.ListBlocks(id)
		if err != nil {
			return goerr.Wrap(err, "failed to list blocks")
		}
		a := &applier{
			u: u, ctx: ctx, tx: tx, p: p, paper: paper, actor: p.Actor(), now: now,
			orig: map[model.BlockID]*model.Block{}, created: map[model.BlockID]bool{},
			touched: map[model.BlockID]*model.Block{}, refs: map[string]string{},
			others: map[model.PaperID]*model.Paper{}, bytes: map[model.BlockID]int{},
			result: &BatchResult{}, unbounded: true,
		}
		cur := map[model.BlockID]*model.Block{}
		for _, b := range current {
			cur[b.ID] = b
			a.orig[b.ID] = b.Clone()
		}
		want := map[model.BlockID]bool{}
		for _, b := range target {
			want[b.ID] = true
		}
		for _, b := range current {
			if want[b.ID] {
				continue
			}
			if b.Type == model.BlockPaper {
				gone, err := a.dropChild(b.Props.PaperID)
				if err != nil {
					return err
				}
				if !gone {
					continue
				}
			}
			if err := a.remove(b); err != nil {
				return err
			}
		}
		w := u.newTreeWriter(ctx, tx, p, now)
		for _, t := range target {
			if t.Type == model.BlockPaper {
				ok, err := a.childBack(w, t.Props.PaperID)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
			}
			c := cur[t.ID]
			switch {
			case c == nil:
				nb := t.Clone()
				nb.Rev = freshRev(t.Rev, now)
				nb.CreatedBy, nb.CreatedAt, nb.UpdatedBy, nb.UpdatedAt = a.actor, now, a.actor, now
				if err := a.put(nb); err != nil {
					return err
				}
				a.inserted = true
			case !model.SameContents(c, t):
				nb := c.Clone()
				nb.Type, nb.Content, nb.Props = t.Type, t.Content, t.Props
				nb.ParentID, nb.Order = t.ParentID, t.Order
				a.bump(nb)
				if err := a.put(nb); err != nil {
					return err
				}
			case c.Order != t.Order || c.ParentID != t.ParentID:
				nb := c.Clone()
				nb.ParentID, nb.Order = t.ParentID, t.Order
				if err := a.put(nb); err != nil {
					return err
				}
			}
		}
		if v.Title != paper.Title {
			a.paper.Title = v.Title
			a.titleChanged, a.paperChanged = true, true
		}
		for qid, q := range w.papers {
			a.others[qid] = q
		}
		if err := a.finish(); err != nil {
			return err
		}
		a.result.NotRestored = append(a.result.NotRestored, w.result.NotRestored...)
		result = a.result
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// childBack decides whether a paper block of a version comes back: the
// child must still be this paper's child; a child in the trash comes back
// with what was trashed with it when the caller created it.
func (a *applier) childBack(w *treeWriter, child model.PaperID) (bool, error) {
	q, err := a.tx.GetPaper(child)
	if err != nil {
		w.result.NotRestored = append(w.result.NotRestored, NotRestored{PaperID: child, Reason: "missing"})
		return false, nil
	}
	if q.ParentID != a.paper.ID {
		w.result.NotRestored = append(w.result.NotRestored, NotRestored{PaperID: child, Reason: "moved"})
		return false, nil
	}
	if !q.IsTrashed() {
		return true, nil
	}
	if !a.p.Owns(q) {
		w.result.NotRestored = append(w.result.NotRestored, NotRestored{PaperID: child, Reason: "trashed"})
		return false, nil
	}
	tree, err := a.u.subtree(a.ctx, a.tx, q)
	if err != nil {
		return false, err
	}
	for _, d := range tree {
		if d.DeletedWith == q.DeletedWith && a.p.Owns(d) {
			d.DeletedAt, d.DeletedWith = nil, ""
			if err := w.putPaper(d); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}
