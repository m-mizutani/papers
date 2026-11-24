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
		for _, ev := range paperUpdatedEvents(q) {
			w.event(ev)
		}
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

// canHaveChildren reports whether paper may be a parent: the home, a
// filed paper (public with a parent) or an item. Unfiled, private and HTML
// papers and databases have no children.
func (u *UseCases) canHaveChildren(ctx context.Context, paper *model.Paper) (bool, error) {
	if paper.IsPrivate() || paper.IsHTML() || paper.IsDatabase() {
		// a database has no body; it holds items, not child papers
		return false, nil
	}
	if paper.ParentID != "" || paper.IsItem() {
		return true, nil
	}
	home, err := u.homeID(ctx, paper.WorkspaceID)
	if err != nil {
		return false, err
	}
	return paper.ID == home, nil
}

// hasLiveChildren reports whether a paper has children outside the trash.
// A database's items are not its children (they have no ParentID).
func (u *UseCases) hasLiveChildren(ctx context.Context, ws model.WorkspaceID, id model.PaperID) (bool, error) {
	kids, err := u.repo.ListChildren(ctx, ws, id)
	if err != nil {
		return false, goerr.Wrap(err, "failed to list children")
	}
	return slices.ContainsFunc(kids, func(k *model.Paper) bool { return !k.IsTrashed() }), nil
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

// parentFor resolves the parent of creating, moving or making a paper
// public. The parent must be the home or a filed paper, not in the trash.
func (u *UseCases) parentFor(ctx context.Context, p *model.Principal, ws model.WorkspaceID, ref string) (*model.Paper, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "a parent is required")
	}
	parent, err := u.ResolvePaper(ctx, p, ws, ref)
	if err != nil {
		return nil, err
	}
	if parent.IsPrivate() || parent.IsTrashed() {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "the new parent must be a public paper that is not in the trash")
	}
	ok, err := u.canHaveChildren(ctx, parent)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "an unfiled paper has no child papers; move it under a parent first", goerr.V("paper_id", parent.ID))
	}
	if parent.IsBusy() {
		return nil, busy(parent)
	}
	return parent, nil
}

// MoveInput moves a paper under a new parent, possibly in another
// workspace, or with Unfiled out of the tree (only without Parent and
// Workspace, and only for a paper without children).
type MoveInput struct {
	Parent    string
	After     *string
	Workspace model.WorkspaceID
	Unfiled   bool
}

// MovePaper moves a public paper under another parent. Within a workspace it
// writes at most three documents (the old parent's body, none for an
// unfiled paper; the new parent's body; and the paper); everything under the
// paper follows. Across workspaces it copies the paper and everything under
// it with new IDs, sends the originals to the trash and leaves forwards
// behind. With Unfiled it takes the paper out of the tree.
func (u *UseCases) MovePaper(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, in MoveInput) (*BatchResult, error) {
	_, paper, err := u.writablePaper(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	if paper.IsPrivate() {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "a private paper is not in the tree; make it public to place it")
	}
	if paper.IsItem() {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "an item stays in its database; reorder it with move_item on the database")
	}
	if err := u.checkNotHome(ctx, paper, "moved"); err != nil {
		return nil, err
	}
	if in.Unfiled {
		if strings.TrimSpace(in.Parent) != "" || in.Workspace != "" {
			return nil, goerr.Wrap(model.ErrInvalidArgument, "unfiled takes neither a parent nor a workspace")
		}
		return u.moveToUnfiled(ctx, p, paper)
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
		for _, q := range []*model.Paper{cur, np} {
			if q.IsBusy() {
				return busy(q)
			}
		}
		if ok, err := u.canHaveChildren(ctx, np); err != nil {
			return err
		} else if !ok || np.IsTrashed() {
			return goerr.Wrap(model.ErrInvalidArgument, "the new parent must be the home or a filed paper that is not in the trash")
		}
		// cycle check: the new parent must not be the paper or under it
		at := np
		for depth := 0; ; depth++ {
			if at.ID == cur.ID {
				return goerr.Wrap(model.ErrInvalidArgument, "cannot move a paper under itself")
			}
			if at.Above() == "" {
				break
			}
			if depth >= model.MaxTreeDepth {
				return goerr.Wrap(model.ErrInvalidArgument, "the tree is deeper than the safety limit")
			}
			if at, err = tx.GetPaper(at.Above()); err != nil {
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

// moveToUnfiled takes a filed paper without children out of the tree: its
// paper block leaves the parent's body and its ParentID is cleared, in one
// transaction.
func (u *UseCases) moveToUnfiled(ctx context.Context, p *model.Principal, paper *model.Paper) (*BatchResult, error) {
	ws := paper.WorkspaceID
	kids, err := u.hasLiveChildren(ctx, ws, paper.ID)
	if err != nil {
		return nil, err
	}
	if kids {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "only a paper without child papers can be unfiled", goerr.V("paper_id", paper.ID))
	}
	var result *BatchResult
	err = u.repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
		w := u.newTreeWriter(ctx, tx, p, u.now())
		cur, err := tx.GetPaper(paper.ID)
		if err != nil {
			return goerr.Wrap(err, "failed to get paper")
		}
		w.result.Paper = cur
		result = w.result
		if cur.IsBusy() {
			return busy(cur)
		}
		if cur.IsPrivate() {
			return goerr.Wrap(model.ErrInvalidArgument, "a private paper is not in the tree")
		}
		if cur.ParentID == "" {
			return nil // already unfiled
		}
		// Checked again in the transaction: a child paper inserted meanwhile
		// adds a paper block to this body, which this read then conflicts with.
		blocks, err := tx.Children(cur.ID, "")
		if err != nil {
			return goerr.Wrap(err, "failed to list blocks", goerr.V("paper_id", cur.ID))
		}
		if slices.ContainsFunc(blocks, func(b *model.Block) bool { return b.Type == model.BlockPaper }) {
			return goerr.Wrap(model.ErrInvalidArgument, "only a paper without child papers can be unfiled", goerr.V("paper_id", paper.ID))
		}
		parent, err := tx.GetPaper(cur.ParentID)
		if err != nil {
			return goerr.Wrap(err, "failed to get parent", goerr.V("parent_id", cur.ParentID))
		}
		if parent.IsBusy() {
			return busy(parent)
		}
		if err := w.removeBlockOf(ws, cur.ParentID, cur.ID); err != nil {
			return err
		}
		cur.ParentID = ""
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

// VisibilityInput changes who can use a paper. Making it public places it
// under Parent, or leaves it unfiled when Parent is empty.
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
	if (paper.IsDatabase() || paper.IsItem()) && in.Visibility == model.VisibilityPrivate {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "databases and their items are always public")
	}
	if err := u.checkNotHome(ctx, paper, "made private"); err != nil {
		return nil, err
	}
	var parent *model.Paper
	if in.Visibility == model.VisibilityPublic && paper.IsPrivate() && strings.TrimSpace(in.Parent) != "" {
		if parent, err = u.parentFor(ctx, p, ws, in.Parent); err != nil {
			return nil, err
		}
	}
	if in.Visibility == model.VisibilityPrivate && !paper.IsPrivate() {
		kids, err := u.hasLiveChildren(ctx, ws, id)
		if err != nil {
			return nil, err
		}
		if kids {
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
		if cur.IsBusy() {
			return busy(cur)
		}
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
			if parent == nil {
				break // unfiled
			}
			np, err := tx.GetPaper(parent.ID)
			if err != nil {
				return goerr.Wrap(err, "failed to get parent")
			}
			if np.IsBusy() {
				return busy(np)
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
	if subtree {
		kind := model.JobArchive
		if !archived {
			kind = model.JobUnarchive
		}
		return u.treeJob(ctx, u.newJob(p.Actor(), ws, kind, id, u.now()))
	}
	var result *BatchResult
	err = u.repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
		w := u.newTreeWriter(ctx, tx, p, u.now())
		cur, err := tx.GetPaper(id)
		if err != nil {
			return goerr.Wrap(err, "failed to get paper")
		}
		if cur.IsBusy() {
			return busy(cur)
		}
		if cur.IsArchived() != archived {
			if archived {
				cur.ArchivedAt = ptr(w.now)
			} else {
				cur.ArchivedAt = nil
			}
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

// treeJob starts a job over a paper's subtree. The result is the paper as
// it is when the job starts; the job's changes arrive through the change
// feed.
func (u *UseCases) treeJob(ctx context.Context, j *model.Job) (*BatchResult, error) {
	if err := u.startJob(ctx, j); err != nil {
		return nil, err
	}
	paper, err := u.repo.GetPaper(ctx, j.WorkspaceID, j.PaperID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read the job's paper", goerr.V("paper_id", j.PaperID))
	}
	return &BatchResult{Paper: paper, Refs: map[string]string{}}, nil
}

// Trash sends a paper and everything under it to the trash. Only the
// creator may; when a paper under it belongs to someone else, the job
// archives the paper and everything under it instead (D18). It runs as a
// background job.
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
	return u.treeJob(ctx, u.newJob(p.Actor(), ws, model.JobTrash, id, u.now()))
}

// RestoreFromTrash brings a paper back with everything that was trashed
// together with it. It goes back under its parent, or under the nearest
// ancestor that is not in the trash (the home if none); a paper that was
// unfiled stays unfiled. It runs as a background job.
func (u *UseCases) RestoreFromTrash(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID) (*BatchResult, error) {
	_, paper, err := u.paperFor(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	if !p.CanWrite() {
		return nil, readOnly()
	}
	if !paper.IsTrashed() || !p.Owns(paper) {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "the paper is not in your trash")
	}
	if paper.IsItem() {
		db, err := u.repo.GetPaper(ctx, ws, paper.DatabaseID)
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			return nil, goerr.Wrap(err, "failed to get the item's database", goerr.V("database_id", paper.DatabaseID))
		}
		if err != nil || db.IsTrashed() {
			return nil, goerr.Wrap(model.ErrInvalidArgument, "the item's database is in the trash; restore the database instead", goerr.V("database_id", paper.DatabaseID))
		}
	}
	j := u.newJob(p.Actor(), ws, model.JobRestore, id, u.now())
	j.With = paper.DeletedWith
	return u.treeJob(ctx, j)
}

// placeRestored puts a restored paper back in the tree: under its parent,
// or the nearest ancestor that can have children and is not in the trash
// (the home if none). A private or unfiled paper has no place in the tree
// and stays as it is.
func (u *UseCases) placeRestored(w *treeWriter, r *model.Paper, home model.PaperID) error {
	if r.IsItem() {
		return u.restoreItemPlace(w, r)
	}
	if r.IsPrivate() || r.ParentID == "" {
		return nil
	}
	target := r.ParentID
	for depth := 0; target != ""; depth++ {
		anc, err := w.tx.GetPaper(target)
		if err != nil || depth >= model.MaxTreeDepth {
			target = ""
			break
		}
		// an unfiled ancestor (public, no parent, not the home) cannot
		// take children; going on from it reaches "" and so the home
		if !anc.IsTrashed() && !anc.IsPrivate() && !anc.IsBusy() && !anc.IsDatabase() && (anc.ParentID != "" || anc.IsItem() || anc.ID == home) {
			break
		}
		target = anc.Above()
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

// restoreItemPlace keeps a restored item in its database: it goes back to
// its place in Items, or at the end if a purge removed it from there.
func (u *UseCases) restoreItemPlace(w *treeWriter, r *model.Paper) error {
	db, err := w.tx.GetPaper(r.DatabaseID)
	if err != nil {
		return goerr.Wrap(err, "failed to get the item's database", goerr.V("database_id", r.DatabaseID))
	}
	if slices.Contains(db.Items, r.ID) {
		return nil
	}
	if len(db.Items) >= model.MaxDatabaseItems {
		return goerr.Wrap(model.ErrInvalidArgument, "the database has too many items", goerr.V("max", model.MaxDatabaseItems))
	}
	db.Items = append(db.Items, r.ID)
	return w.putPaper(db)
}

// TrashItem is a paper the caller sent to the trash: when, when it is
// purged, and how many papers went with it.
type TrashItem struct {
	*PaperRef
	DeletedAt time.Time `json:"deleted_at"`
	PurgeAt   time.Time `json:"purge_at"`
	Below     int       `json:"below"`
}

// ListTrash lists the caller's papers in the trash (the ones deleted
// directly; papers deleted with them come back with them).

func (u *UseCases) ListTrash(ctx context.Context, p *model.Principal, ws model.WorkspaceID) ([]*TrashItem, error) {
	if _, err := u.workspaceFor(ctx, p, ws); err != nil {
		return nil, err
	}
	papers, err := u.repo.ListTrash(ctx, ws, p.User.ID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list trash")
	}
	// What went to the trash with each paper is in the same list.
	below := map[model.PaperID]int{}
	for _, q := range papers {
		if q.DeletedWith != q.ID {
			below[q.DeletedWith]++
		}
	}
	out := []*TrashItem{}
	for _, q := range papers {
		if q.DeletedWith != q.ID || q.DeletedAt == nil {
			continue
		}
		out = append(out, &TrashItem{PaperRef: refOf(q), DeletedAt: *q.DeletedAt, PurgeAt: q.DeletedAt.Add(u.cfg.TrashRetention), Below: below[q.ID]})
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
			if q.IsItem() {
				if err := u.forgetItem(ctx, q); err != nil {
					return n, err
				}
			}
			if err := u.repo.PurgePaper(ctx, ws.ID, q.ID); err != nil && !errors.Is(err, model.ErrNotFound) {
				return n, goerr.Wrap(err, "failed to purge paper", goerr.V("paper_id", q.ID))
			}
			n++
		}
	}
	return n, nil
}

// forgetItem takes an item that is about to be purged out of its
// database's Items, before the purge: an item left out of Items while it
// still exists is invisible (it is in the trash) and goes back at the end
// if restored, while an ID left in Items after a purge would be dangling.
func (u *UseCases) forgetItem(ctx context.Context, q *model.Paper) error {
	now := u.now()
	err := u.repo.RunTx(ctx, q.WorkspaceID, func(tx interfaces.Tx) error {
		db, err := tx.GetPaper(q.DatabaseID)
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		if err != nil {
			return goerr.Wrap(err, "failed to get database", goerr.V("database_id", q.DatabaseID))
		}
		if !slices.Contains(db.Items, q.ID) {
			return nil
		}
		db.Items = slices.DeleteFunc(db.Items, func(id model.PaperID) bool { return id == q.ID })
		if err := tx.PutPaper(db); err != nil {
			return goerr.Wrap(err, "failed to update database")
		}
		evs := append(paperUpdatedEvents(db), &model.Event{Type: model.EventItemDeleted, WorkspaceID: db.WorkspaceID, PaperID: db.ID, ItemID: q.ID})
		for _, ev := range evs {
			ev.At = now
			if err := tx.AddEvent(ev); err != nil {
				return goerr.Wrap(err, "failed to add event")
			}
		}
		return nil
	})
	if err != nil {
		return goerr.Wrap(err, "failed to take the item out of its database", goerr.V("paper_id", q.ID))
	}
	return nil
}
