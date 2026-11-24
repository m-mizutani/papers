package usecase

import (
	"context"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// The collect phase, shared by every kind, gathers the job's papers as
// items: the root first, then one level of children at a time with the
// parent index. Each kind decides which children it takes (include).
//
// The apply phase (every kind but move) changes the collected papers in
// depth order, root first.

// copyPaperID is the copy of a paper made by a move job.
func copyPaperID(j *model.Job, src model.PaperID) model.PaperID {
	return model.PaperID(model.DeriveID(string(j.ID), "paper", string(src)))
}

// under lists what sits directly under the papers: their children (by
// ParentID) and, for a database, its items (from its Items, so no query
// is needed). Both are read in batches.
func (u *UseCases) under(ctx context.Context, ws model.WorkspaceID, ids []model.PaperID) ([]*model.Paper, error) {
	kids, err := u.repo.ListChildrenOf(ctx, ws, ids)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list children")
	}
	papers, err := u.repo.GetPapers(ctx, ws, ids)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read papers")
	}
	var itemIDs []model.PaperID
	for _, p := range papers {
		if p.IsDatabase() {
			itemIDs = append(itemIDs, p.Items...)
		}
	}
	if len(itemIDs) == 0 {
		return kids, nil
	}
	items, err := u.repo.GetPapers(ctx, ws, itemIDs)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read items")
	}
	for _, q := range items {
		if q.IsItem() {
			kids = append(kids, q)
		}
	}
	return kids, nil
}

func (u *UseCases) collect(ctx context.Context, j *model.Job) error {
	if j.Total == 0 {
		return u.commitStep(ctx, j, func(c *stepCtx) error {
			root, err := c.tx.GetPaper(c.job.PaperID)
			if err != nil {
				return goerr.Wrap(err, "failed to get the job's paper", goerr.V("paper_id", c.job.PaperID))
			}
			if err := u.includeRoot(c, root); err != nil {
				return err
			}
			it := &model.JobItem{JobID: c.job.ID, WorkspaceID: c.job.WorkspaceID, PaperID: root.ID, ParentID: root.Above(), ExpireAt: c.job.ExpireAt}
			if c.job.Kind == model.JobMove {
				it.CopyID = copyPaperID(c.job, root.ID)
			}
			c.job.Total = 1
			return c.tx.PutJobItem(it)
		})
	}
	items, err := u.repo.ListUnexpandedJobItems(ctx, j.WorkspaceID, j.ID, u.cfg.JobStep)
	if err != nil {
		return goerr.Wrap(err, "failed to list job items")
	}
	if len(items) == 0 {
		return u.commitStep(ctx, j, func(c *stepCtx) error {
			c.job.Done = 0
			if c.job.Kind == model.JobMove {
				c.job.Phase = model.PhaseCopy
			} else {
				c.job.Phase = model.PhaseApply
			}
			return nil
		})
	}
	parents := map[model.PaperID]*model.JobItem{}
	ids := make([]model.PaperID, 0, len(items))
	for _, it := range items {
		parents[it.PaperID] = it
		ids = append(ids, it.PaperID)
	}
	kids, err := u.under(ctx, j.WorkspaceID, ids)
	if err != nil {
		return err
	}
	kidIDs := make([]model.PaperID, len(kids))
	for i, k := range kids {
		kidIDs[i] = k.ID
	}
	return u.commitStep(ctx, j, func(c *stepCtx) error {
		fresh, err := c.tx.GetPapers(kidIDs)
		if err != nil {
			return goerr.Wrap(err, "failed to read children")
		}
		known, err := c.tx.GetJobItems(c.job.ID, kidIDs)
		if err != nil {
			return goerr.Wrap(err, "failed to read job items")
		}
		have := map[model.PaperID]bool{}
		for _, it := range known {
			have[it.PaperID] = true
		}
		for _, k := range fresh {
			parent, ok := parents[k.Above()]
			if !ok || have[k.ID] {
				continue // moved meanwhile, or already collected
			}
			if parent.Depth+1 > model.MaxTreeDepth {
				return goerr.Wrap(model.ErrInvalidArgument, "the tree is deeper than the safety limit", goerr.V("max", model.MaxTreeDepth))
			}
			take, err := u.includeChild(c, k)
			if err != nil {
				return err
			}
			if !take {
				continue
			}
			it := &model.JobItem{JobID: c.job.ID, WorkspaceID: c.job.WorkspaceID, PaperID: k.ID, ParentID: k.Above(), Depth: parent.Depth + 1, ExpireAt: c.job.ExpireAt}
			if c.job.Kind == model.JobMove {
				it.CopyID = copyPaperID(c.job, k.ID)
			}
			if err := c.tx.PutJobItem(it); err != nil {
				return err
			}
			c.job.Total++
		}
		for _, it := range items {
			it.Expanded = true
			if err := c.tx.PutJobItem(it); err != nil {
				return err
			}
			c.job.Done++
		}
		return nil
	})
}

// includeRoot checks the root when the job starts collecting.
func (u *UseCases) includeRoot(c *stepCtx, root *model.Paper) error {
	j, p := c.job, c.w.p
	switch j.Kind {
	case model.JobTrash, model.JobDrop:
		if root.IsTrashed() {
			return goerr.Wrap(model.ErrInvalidArgument, "the paper is already in the trash")
		}
		if !p.Owns(root) {
			j.Foreign = true
		}
	case model.JobRestore:
		if !j.RootDone && (!root.IsTrashed() || !p.Owns(root)) {
			return goerr.Wrap(model.ErrInvalidArgument, "the paper is not in your trash")
		}
	case model.JobMove:
		if root.IsTrashed() || root.IsPrivate() {
			return goerr.Wrap(model.ErrInvalidArgument, "only a public paper that is not in the trash can be moved")
		}
		return markMoving(c, root)
	}
	return nil
}

// includeChild decides whether a child found under the job's papers is
// part of the job.
func (u *UseCases) includeChild(c *stepCtx, k *model.Paper) (bool, error) {
	j, p := c.job, c.w.p
	switch j.Kind {
	case model.JobTrash, model.JobDrop:
		if k.IsTrashed() {
			return false, nil // trashed before, on its own
		}
		if !p.Owns(k) {
			j.Foreign = true // others' papers are archived, not deleted (D18)
		}
		return true, nil
	case model.JobRestore:
		return k.DeletedWith == j.With && p.Owns(k), nil
	case model.JobArchive, model.JobUnarchive:
		return !k.IsTrashed(), nil
	case model.JobMove:
		if k.IsTrashed() || !p.SeesPaper(k) {
			return false, nil
		}
		return true, markMoving(c, k)
	}
	return false, nil
}

// markMoving makes a paper read-only for the move.
func markMoving(c *stepCtx, q *model.Paper) error {
	if q.IsBusy() && q.JobID != c.job.ID {
		return busy(q)
	}
	if q.JobID == c.job.ID {
		return nil
	}
	q.JobID = c.job.ID
	return c.w.putPaper(q)
}

// applyTree is the apply phase of trash, drop, restore, archive and
// unarchive.
func (u *UseCases) applyTree(ctx context.Context, j *model.Job) error {
	items, err := u.repo.ListJobItemsAt(ctx, j.WorkspaceID, j.ID, model.StageCollected, u.cfg.JobStep)
	if err != nil {
		return goerr.Wrap(err, "failed to list job items")
	}
	if len(items) == 0 {
		return u.commitStep(ctx, j, func(c *stepCtx) error {
			finishJob(c.job, c.now)
			switch {
			case (c.job.Kind == model.JobTrash || c.job.Kind == model.JobDrop) && c.job.Foreign:
				c.job.Result.Archived = true
			case c.job.Kind == model.JobTrash || c.job.Kind == model.JobDrop:
				c.job.Result.Trashed = true
			}
			return nil
		})
	}
	var home model.PaperID
	if j.Kind == model.JobRestore && !j.RootDone {
		if home, err = u.homeID(ctx, j.WorkspaceID); err != nil {
			return err
		}
	}
	ids := make([]model.PaperID, len(items))
	for i, it := range items {
		ids[i] = it.PaperID
	}
	return u.commitStep(ctx, j, func(c *stepCtx) error {
		papers, err := c.tx.GetPapers(ids)
		if err != nil {
			return goerr.Wrap(err, "failed to read papers")
		}
		byID := map[model.PaperID]*model.Paper{}
		for _, q := range papers {
			byID[q.ID] = q
		}
		for _, it := range items {
			if q := byID[it.PaperID]; q != nil {
				if err := u.applyItem(c, q, home); err != nil {
					return err
				}
			}
			it.Stage = model.StageApplied
			if err := c.tx.PutJobItem(it); err != nil {
				return err
			}
			c.job.Done++
		}
		return nil
	})
}

func (u *UseCases) applyItem(c *stepCtx, q *model.Paper, home model.PaperID) error {
	j, w, p := c.job, c.w, c.w.p
	root := q.ID == j.PaperID
	skip := func() {
		if j.Result == nil {
			j.Result = &model.JobResult{}
		}
		j.Result.Skipped = append(j.Result.Skipped, q.ID)
	}
	switch {
	case (j.Kind == model.JobTrash || j.Kind == model.JobDrop) && !j.Foreign:
		if q.IsTrashed() {
			return nil
		}
		if !p.Owns(q) {
			// someone else added it under the tree after the check
			skip()
			return nil
		}
		q.DeletedAt, q.DeletedWith = ptr(c.now), j.PaperID
		if err := w.putPaper(q); err != nil {
			return err
		}
		if root {
			return w.removeBlockOf(q.WorkspaceID, q.ParentID, q.ID)
		}
	case j.Kind == model.JobTrash, j.Kind == model.JobDrop, j.Kind == model.JobArchive:
		if q.IsTrashed() || q.IsArchived() || !p.SeesPaper(q) {
			return nil
		}
		q.ArchivedAt = ptr(c.now)
		return w.putPaper(q)
	case j.Kind == model.JobUnarchive:
		if q.IsTrashed() || !q.IsArchived() || !p.SeesPaper(q) {
			return nil
		}
		q.ArchivedAt = nil
		return w.putPaper(q)
	case j.Kind == model.JobRestore:
		if root {
			if j.RootDone {
				return nil
			}
			if !q.IsTrashed() {
				return nil
			}
			q.DeletedAt, q.DeletedWith = nil, ""
			if err := w.putPaper(q); err != nil {
				return err
			}
			return u.placeRestored(w, w.papers[q.ID], home)
		}
		if q.DeletedWith != j.With || !p.Owns(q) {
			return nil
		}
		q.DeletedAt, q.DeletedWith = nil, ""
		return w.putPaper(q)
	}
	return nil
}
