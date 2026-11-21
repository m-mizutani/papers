package usecase

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

// subtree returns root and every paper under it, read in tx. Children are
// found with the parent index and re-read in the transaction; a paper whose
// ParentID changed meanwhile is not followed.
func (u *UseCases) subtree(ctx context.Context, tx interfaces.Tx, root *model.Paper) ([]*model.Paper, error) {
	out := []*model.Paper{root.Clone()}
	level := []*model.Paper{root}
	for depth := 0; len(level) > 0; depth++ {
		if depth >= model.MaxTreeDepth {
			return nil, goerr.Wrap(model.ErrInvalidArgument, "the tree is deeper than the safety limit", goerr.V("max", model.MaxTreeDepth))
		}
		var next []*model.Paper
		for _, parent := range level {
			kids, err := u.repo.ListChildren(ctx, root.WorkspaceID, parent.ID)
			if err != nil {
				return nil, goerr.Wrap(err, "failed to list children", goerr.V("paper_id", parent.ID))
			}
			if len(kids) == 0 {
				continue
			}
			ids := make([]model.PaperID, len(kids))
			for i, k := range kids {
				ids[i] = k.ID
			}
			fresh, err := tx.GetPapers(ids)
			if err != nil {
				return nil, goerr.Wrap(err, "failed to read children")
			}
			for _, k := range fresh {
				if k.ParentID == parent.ID {
					next = append(next, k)
				}
			}
		}
		out = append(out, next...)
		if len(out) > MaxSubtree {
			return nil, goerr.Wrap(model.ErrInvalidArgument, "too many papers under this one for one operation; work on smaller parts", goerr.V("max", MaxSubtree))
		}
		level = next
	}
	return out, nil
}

// paperBlockOf finds the paper block of child in parent's body.
func paperBlockOf(tx interfaces.Tx, parent, child model.PaperID) (*model.Block, error) {
	blocks, err := tx.Children(parent, "")
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read parent body", goerr.V("paper_id", parent))
	}
	for _, b := range blocks {
		if b.Type == model.BlockPaper && b.Props.PaperID == child {
			return b, nil
		}
	}
	return nil, nil
}

type treeWriter struct {
	u      *UseCases
	ctx    context.Context
	tx     interfaces.Tx
	p      *model.Principal
	actor  model.Actor
	now    time.Time
	events []*model.Event
	papers map[model.PaperID]*model.Paper
	result *BatchResult
}

func (u *UseCases) newTreeWriter(ctx context.Context, tx interfaces.Tx, p *model.Principal, now time.Time) *treeWriter {
	return &treeWriter{u: u, ctx: ctx, tx: tx, p: p, actor: p.Actor(), now: now, papers: map[model.PaperID]*model.Paper{}, result: &BatchResult{Refs: map[string]string{}}}
}

func (w *treeWriter) putPaper(q *model.Paper) error {
	if err := w.tx.PutPaper(q); err != nil {
		return goerr.Wrap(err, "failed to write paper", goerr.V("paper_id", q.ID))
	}
	w.papers[q.ID] = q
	return nil
}

func (w *treeWriter) event(ev *model.Event) {
	ev.At = w.now
	w.events = append(w.events, ev)
}

// removeBlockOf deletes child's paper block from parent's body.
func (w *treeWriter) removeBlockOf(ws model.WorkspaceID, parent, child model.PaperID) error {
	if parent == "" {
		return nil
	}
	b, err := paperBlockOf(w.tx, parent, child)
	if err != nil || b == nil {
		return err
	}
	if err := w.tx.DeleteBlock(parent, b.ID); err != nil {
		return goerr.Wrap(err, "failed to delete paper block")
	}
	w.event(&model.Event{Type: model.EventBlockDeleted, WorkspaceID: ws, PaperID: parent, BlockID: b.ID})
	return nil
}

// addBlockOf puts a paper block for child into parent's body per after.
func (w *treeWriter) addBlockOf(parent *model.Paper, child model.PaperID, after *string) (*model.Block, error) {
	a := &applier{u: w.u, ctx: w.ctx, tx: w.tx, p: w.p, paper: parent, actor: w.actor, now: w.now, refs: map[string]string{}}
	order, err := a.orderAfter(after, "")
	if err != nil {
		return nil, err
	}
	b := a.newBlock(model.BlockPaper, "", model.BlockProps{PaperID: child}, "", order)
	if err := w.tx.PutBlock(b); err != nil {
		return nil, goerr.Wrap(err, "failed to add paper block")
	}
	w.event(&model.Event{Type: model.EventBlockPut, WorkspaceID: parent.WorkspaceID, PaperID: parent.ID, Block: b})
	return b, nil
}

func (w *treeWriter) flush() error {
	for _, q := range w.papers {
		w.event(&model.Event{Type: model.EventPaperUpdated, WorkspaceID: q.WorkspaceID, PaperID: q.ID, Paper: q.Clone()})
	}
	for _, ev := range w.events {
		if err := w.tx.AddEvent(ev); err != nil {
			return goerr.Wrap(err, "failed to add event")
		}
	}
	return nil
}

// checkNotHome refuses the operation on the workspace's home.
func (u *UseCases) checkNotHome(ctx context.Context, paper *model.Paper, what string) error {
	home, err := u.homeID(ctx, paper.WorkspaceID)
	if err != nil {
		return err
	}
	if paper.ID == home {
		return goerr.Wrap(model.ErrInvalidArgument, "the home paper cannot be "+what)
	}
	return nil
}

// AmbiguousError reports a path that names more than one paper.
type AmbiguousError struct {
	Path       string      `json:"path"`
	Candidates []*PaperRef `json:"candidates"`
}

func (e *AmbiguousError) Error() string   { return "the path names more than one paper" }
func (e *AmbiguousError) Is(t error) bool { return t == model.ErrInvalidArgument }

// ResolvePaper turns a paper ID or a path of titles from the home
// ("Runbooks/DB") into a visible paper of the workspace.
func (u *UseCases) ResolvePaper(ctx context.Context, p *model.Principal, ws model.WorkspaceID, ref string) (*model.Paper, error) {
	ref = strings.TrimSpace(ref)
	if model.ValidID(ref) {
		_, paper, err := u.paperFor(ctx, p, ws, model.PaperID(ref))
		return paper, err
	}
	if _, err := u.workspaceFor(ctx, p, ws); err != nil {
		return nil, err
	}
	homeID, err := u.homeID(ctx, ws)
	if err != nil {
		return nil, err
	}
	cur, err := u.repo.GetPaper(ctx, ws, homeID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get home paper")
	}
	for _, seg := range strings.Split(strings.Trim(ref, "/"), "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		kids, err := u.repo.ListChildren(ctx, ws, cur.ID)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list children")
		}
		var hits []*model.Paper
		for _, k := range kids {
			if k.Title == seg && !k.IsTrashed() && p.SeesPaper(k) {
				hits = append(hits, k)
			}
		}
		switch len(hits) {
		case 0:
			return nil, goerr.Wrap(model.ErrInvalidArgument, "no paper at this path", goerr.V("path", ref), goerr.V("missing", seg))
		case 1:
			cur = hits[0]
		default:
			e := &AmbiguousError{Path: ref}
			for _, h := range hits {
				e.Candidates = append(e.Candidates, refOf(h))
			}
			return nil, e
		}
	}
	return cur, nil
}

// parentFor resolves the parent of a move or of making a paper public. An
// empty ref means the home.
func (u *UseCases) parentFor(ctx context.Context, p *model.Principal, ws model.WorkspaceID, ref string) (*model.Paper, error) {
	if strings.TrimSpace(ref) == "" {
		home, err := u.homeID(ctx, ws)
		if err != nil {
			return nil, err
		}
		ref = string(home)
	}
	parent, err := u.ResolvePaper(ctx, p, ws, ref)
	if err != nil {
		return nil, err
	}
	if parent.IsPrivate() || parent.IsTrashed() {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "the new parent must be a public paper that is not in the trash")
	}
	return parent, nil
}

// MoveInput moves a paper under a new parent, possibly in another
// workspace.
type MoveInput struct {
	Parent    string
	After     *string
	Workspace model.WorkspaceID
}

// MovePaper moves a public paper under another parent. Within a workspace it
// writes three documents (the old parent's body, the new parent's body and
// the paper); everything under the paper follows. Across workspaces it
// copies the paper and everything under it with new IDs, sends the
// originals to the trash and leaves forwards behind.
func (u *UseCases) MovePaper(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, in MoveInput) (*BatchResult, error) {
	_, paper, err := u.writablePaper(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	if paper.IsPrivate() {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "a private paper is not in the tree; make it public to place it")
	}
	if err := u.checkNotHome(ctx, paper, "moved"); err != nil {
		return nil, err
	}
	if in.Workspace != "" && in.Workspace != ws {
		return u.moveAcross(ctx, p, paper, in)
	}
	parent, err := u.parentFor(ctx, p, ws, in.Parent)
	if err != nil {
		return nil, err
	}
	var result *BatchResult
	err = u.repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
		w := u.newTreeWriter(ctx, tx, p, u.now())
		cur, err := tx.GetPaper(id)
		if err != nil {
			return goerr.Wrap(err, "failed to get paper")
		}
		np, err := tx.GetPaper(parent.ID)
		if err != nil {
			return goerr.Wrap(err, "failed to get new parent")
		}
		// cycle check: the new parent must not be the paper or under it
		at := np
		for depth := 0; ; depth++ {
			if at.ID == cur.ID {
				return goerr.Wrap(model.ErrInvalidArgument, "cannot move a paper under itself")
			}
			if at.ParentID == "" {
				break
			}
			if depth >= model.MaxTreeDepth {
				return goerr.Wrap(model.ErrInvalidArgument, "the tree is deeper than the safety limit")
			}
			if at, err = tx.GetPaper(at.ParentID); err != nil {
				return goerr.Wrap(err, "failed to walk ancestors")
			}
		}
		if cur.ParentID == np.ID {
			// same parent: only the position changes
			b, err := paperBlockOf(tx, np.ID, cur.ID)
			if err != nil {
				return err
			}
			if b == nil {
				if _, err := w.addBlockOf(np, cur.ID, in.After); err != nil {
					return err
				}
			} else {
				a := &applier{u: u, ctx: ctx, tx: tx, p: p, paper: np, actor: w.actor, now: w.now, refs: map[string]string{}}
				order, err := a.orderAfter(in.After, b.ID)
				if err != nil {
					return err
				}
				if order != b.Order {
					b.Order = order
					if err := tx.PutBlock(b); err != nil {
						return goerr.Wrap(err, "failed to move paper block")
					}
					w.event(&model.Event{Type: model.EventBlockPut, WorkspaceID: ws, PaperID: np.ID, Block: b})
				}
			}
		} else {
			if err := w.removeBlockOf(ws, cur.ParentID, cur.ID); err != nil {
				return err
			}
			if _, err := w.addBlockOf(np, cur.ID, in.After); err != nil {
				return err
			}
			cur.ParentID = np.ID
			cur.UpdatedBy, cur.UpdatedAt = w.actor, w.now
			if err := w.putPaper(cur); err != nil {
				return err
			}
		}
		w.result.Paper = cur
		result = w.result
		return w.flush()
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// VisibilityInput changes who can use a paper. Making it public places it
// under Parent (default: the home).
type VisibilityInput struct {
	Visibility model.Visibility
	Parent     string
	After      *string
}

// SetVisibility switches a paper between public and private. Only its
// creator may do it. A private paper leaves the tree; only a paper without
// children can become private.
func (u *UseCases) SetVisibility(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, in VisibilityInput) (*BatchResult, error) {
	if err := in.Visibility.Validate(); err != nil || in.Visibility == "" {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "visibility must be public or private")
	}
	_, paper, err := u.writablePaper(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	if !p.Owns(paper) {
		return nil, goerr.Wrap(model.ErrPermissionDenied, "only the creator can change who can use the paper")
	}
	if err := u.checkNotHome(ctx, paper, "made private"); err != nil {
		return nil, err
	}
	var parent *model.Paper
	if in.Visibility == model.VisibilityPublic && paper.IsPrivate() {
		if parent, err = u.parentFor(ctx, p, ws, in.Parent); err != nil {
			return nil, err
		}
	}
	if in.Visibility == model.VisibilityPrivate && !paper.IsPrivate() {
		kids, err := u.repo.ListChildren(ctx, ws, id)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list children")
		}
		if slices.ContainsFunc(kids, func(k *model.Paper) bool { return !k.IsTrashed() }) {
			return nil, goerr.Wrap(model.ErrInvalidArgument, "only a paper without child papers can become private")
		}
	}
	var result *BatchResult
	err = u.repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
		w := u.newTreeWriter(ctx, tx, p, u.now())
		cur, err := tx.GetPaper(id)
		if err != nil {
			return goerr.Wrap(err, "failed to get paper")
		}
		w.result.Paper = cur
		result = w.result
		if cur.Visibility == in.Visibility {
			return nil
		}
		switch in.Visibility {
		case model.VisibilityPrivate:
			if err := w.removeBlockOf(ws, cur.ParentID, cur.ID); err != nil {
				return err
			}
			cur.ParentID = ""
		case model.VisibilityPublic:
			np, err := tx.GetPaper(parent.ID)
			if err != nil {
				return goerr.Wrap(err, "failed to get parent")
			}
			if _, err := w.addBlockOf(np, cur.ID, in.After); err != nil {
				return err
			}
			cur.ParentID = np.ID
		}
		cur.Visibility = in.Visibility
		cur.UpdatedBy, cur.UpdatedAt = w.actor, w.now
		if err := w.putPaper(cur); err != nil {
			return err
		}
		return w.flush()
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Archive hides a paper (and with subtree, everything under it) from lists,
// search and the agents' guide. Anyone who can use the paper may do it.
func (u *UseCases) Archive(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, subtree bool) (*BatchResult, error) {
	return u.setArchived(ctx, p, ws, id, subtree, true)
}

func (u *UseCases) Unarchive(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, subtree bool) (*BatchResult, error) {
	return u.setArchived(ctx, p, ws, id, subtree, false)
}

func (u *UseCases) setArchived(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, subtree, archived bool) (*BatchResult, error) {
	_, paper, err := u.writablePaper(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	if err := u.checkNotHome(ctx, paper, "archived"); err != nil {
		return nil, err
	}
	var result *BatchResult
	err = u.repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
		w := u.newTreeWriter(ctx, tx, p, u.now())
		cur, err := tx.GetPaper(id)
		if err != nil {
			return goerr.Wrap(err, "failed to get paper")
		}
		targets := []*model.Paper{cur}
		if subtree {
			if targets, err = u.subtree(ctx, tx, cur); err != nil {
				return err
			}
		}
		for _, q := range targets {
			if q.IsTrashed() || !p.SeesPaper(q) || q.IsArchived() == archived {
				continue
			}
			if archived {
				q.ArchivedAt = ptr(w.now)
			} else {
				q.ArchivedAt = nil
			}
			if err := w.putPaper(q); err != nil {
				return err
			}
		}
		if q, ok := w.papers[id]; ok {
			cur = q
		}
		w.result.Paper = cur
		result = w.result
		return w.flush()
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Trash sends a paper and everything under it to the trash. Only the
// creator may, and only if every paper under it is theirs too (D18).
func (u *UseCases) Trash(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID) (*BatchResult, error) {
	_, paper, err := u.writablePaper(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	if err := u.checkNotHome(ctx, paper, "deleted"); err != nil {
		return nil, err
	}
	if !p.Owns(paper) {
		return nil, goerr.Wrap(model.ErrPermissionDenied, "only the creator can delete the paper; archive it instead")
	}
	var result *BatchResult
	err = u.repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
		w := u.newTreeWriter(ctx, tx, p, u.now())
		cur, err := tx.GetPaper(id)
		if err != nil {
			return goerr.Wrap(err, "failed to get paper")
		}
		tree, err := u.subtree(ctx, tx, cur)
		if err != nil {
			return err
		}
		if !ownsAll(p, tree) {
			return goerr.Wrap(model.ErrPermissionDenied, "some papers under this one belong to others; archive it instead")
		}
		for _, q := range tree {
			if q.IsTrashed() {
				continue
			}
			q.DeletedAt, q.DeletedWith = ptr(w.now), cur.ID
			if err := w.putPaper(q); err != nil {
				return err
			}
		}
		if err := w.removeBlockOf(ws, cur.ParentID, cur.ID); err != nil {
			return err
		}
		w.result.Paper = w.papers[cur.ID]
		w.result.Trashed = []model.PaperID{cur.ID}
		result = w.result
		return w.flush()
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// RestoreFromTrash brings a paper back with everything that was trashed
// together with it. It goes back under its parent, or under the nearest
// ancestor that is not in the trash (the home if none).
func (u *UseCases) RestoreFromTrash(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID) (*BatchResult, error) {
	_, paper, err := u.paperFor(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	if !p.CanWrite() {
		return nil, readOnly()
	}
	if !paper.IsTrashed() {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "the paper is not in the trash")
	}
	home, err := u.homeID(ctx, ws)
	if err != nil {
		return nil, err
	}
	var result *BatchResult
	err = u.repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
		w := u.newTreeWriter(ctx, tx, p, u.now())
		cur, err := tx.GetPaper(id)
		if err != nil {
			return goerr.Wrap(err, "failed to get paper")
		}
		if !cur.IsTrashed() || !p.Owns(cur) {
			return goerr.Wrap(model.ErrInvalidArgument, "the paper is not in your trash")
		}
		if err := u.restoreTrashed(ctx, w, cur, home); err != nil {
			return err
		}
		w.result.Paper = w.papers[cur.ID]
		result = w.result
		return w.flush()
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// restoreTrashed restores root and the papers trashed with it, placing root
// in the tree.
func (u *UseCases) restoreTrashed(ctx context.Context, w *treeWriter, root *model.Paper, home model.PaperID) error {
	tree, err := u.subtree(ctx, w.tx, root)
	if err != nil {
		return err
	}
	with := root.DeletedWith
	for _, q := range tree {
		if q.DeletedWith != with || !w.p.Owns(q) {
			continue
		}
		q.DeletedAt, q.DeletedWith = nil, ""
		if err := w.putPaper(q); err != nil {
			return err
		}
	}
	r := w.papers[root.ID]
	if r.IsPrivate() {
		return nil
	}
	// find the nearest ancestor that is usable
	target := r.ParentID
	for depth := 0; target != ""; depth++ {
		anc, err := w.tx.GetPaper(target)
		if err != nil || depth >= model.MaxTreeDepth {
			target = ""
			break
		}
		if !anc.IsTrashed() && !anc.IsPrivate() {
			break
		}
		target = anc.ParentID
	}
	if target == "" {
		target = home
	}
	if target == r.ParentID {
		if b, err := paperBlockOf(w.tx, target, r.ID); err != nil {
			return err
		} else if b != nil {
			return nil // still in its parent's body
		}
	} else if err := w.removeBlockOf(r.WorkspaceID, r.ParentID, r.ID); err != nil {
		return err
	}
	parent, err := w.tx.GetPaper(target)
	if err != nil {
		return goerr.Wrap(err, "failed to get restore target")
	}
	if _, err := w.addBlockOf(parent, r.ID, nil); err != nil {
		return err
	}
	r.ParentID = target
	return w.putPaper(r)
}

// ListTrash lists the caller's papers in the trash (the ones deleted
// directly; papers deleted with them come back with them).
func (u *UseCases) ListTrash(ctx context.Context, p *model.Principal, ws model.WorkspaceID) ([]*PaperRef, error) {
	if _, err := u.workspaceFor(ctx, p, ws); err != nil {
		return nil, err
	}
	papers, err := u.repo.ListTrash(ctx, ws, p.User.ID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list trash")
	}
	var out []*PaperRef
	for _, q := range papers {
		if q.DeletedWith == q.ID {
			out = append(out, refOf(q))
		}
	}
	return out, nil
}

// PurgeTrash removes papers that have been in the trash longer than the
// retention, with their attachments. It runs in the background.
func (u *UseCases) PurgeTrash(ctx context.Context) (int, error) {
	cutoff := u.now().Add(-u.cfg.TrashRetention)
	n := 0
	for _, ws := range u.workspaces.List() {
		old, err := u.repo.ListTrashedBefore(ctx, ws.ID, cutoff)
		if err != nil {
			return n, goerr.Wrap(err, "failed to list old trash", goerr.V("workspace_id", ws.ID))
		}
		for _, q := range old {
			prefix := (&model.Attachment{WorkspaceID: ws.ID, PaperID: q.ID}).StorePrefix()
			if err := u.store.DeletePrefix(ctx, prefix); err != nil {
				return n, goerr.Wrap(err, "failed to delete attachments", goerr.V("paper_id", q.ID))
			}
			if err := u.repo.PurgePaper(ctx, ws.ID, q.ID); err != nil && !errors.Is(err, model.ErrNotFound) {
				return n, goerr.Wrap(err, "failed to purge paper", goerr.V("paper_id", q.ID))
			}
			n++
		}
	}
	return n, nil
}
