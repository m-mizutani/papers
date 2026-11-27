package usecase

import (
	"context"
	"errors"
	"slices"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

// A move to another workspace runs as a job:
//
//	collect   mark every original (Paper.JobID) so nobody else writes it
//	copy      copy each paper: the paper, its attachments, its blocks in
//	          steps, the latest version's snapshots, then the version
//	attach    clear the mark on the copies, then put the copy of the root
//	          into the new parent's body (from here on there is no way back)
//	retire    trash the originals, leave forwards, clear their marks
//	rollback  if the move fails before attach: delete the copies and clear
//	          the marks on the originals
//
// The IDs of copies derive from the job and the original's ID, so a step
// that is repeated writes the same documents again. That is why the copy
// in the target workspace and the progress in the source workspace can be
// separate transactions.

func copyBlockID(j *model.Job, src model.BlockID) model.BlockID {
	return model.BlockID(model.DeriveID(string(j.ID), "block", string(src)))
}

func copyAttachmentID(j *model.Job, src model.AttachmentID) model.AttachmentID {
	return model.AttachmentID(model.DeriveID(string(j.ID), "attachment", string(src)))
}

func (u *UseCases) advanceMove(ctx context.Context, j *model.Job) error {
	switch j.Phase {
	case model.PhaseCollect:
		return u.collect(ctx, j)
	case model.PhaseCopy:
		return u.copyStep(ctx, j)
	case model.PhaseAttach:
		return u.attachStep(ctx, j)
	case model.PhaseRetire:
		return u.retireStep(ctx, j)
	case model.PhaseRollback:
		return u.rollbackStep(ctx, j)
	}
	return goerr.New("unknown move phase", goerr.V("phase", j.Phase))
}

// moveSource is what the copy of one paper needs from the original.
type moveSource struct {
	paper  *model.Paper
	blocks []*model.Block
	latest *model.Version
	// snaps are the latest version's blocks that are copied, with their
	// snapshots.
	vblocks []model.VersionBlock
	snaps   map[model.SnapshotRef]*model.BlockSnapshot
	// children are the child papers that come along.
	children map[model.PaperID]bool
	// databases are the databases named by the paper's HTML (props.databases)
	// that come along; connections to the others are dropped.
	databases map[model.PaperID]bool
	// items are a database's items that come along, in order, and values
	// an item's property values.
	items  []model.PaperID
	values []*model.PropertyValue
}

func (u *UseCases) readMoveSource(ctx context.Context, j *model.Job, it *model.JobItem) (*moveSource, error) {
	ws := j.WorkspaceID
	src := &moveSource{snaps: map[model.SnapshotRef]*model.BlockSnapshot{}, children: map[model.PaperID]bool{}, databases: map[model.PaperID]bool{}}
	var err error
	if src.paper, err = u.repo.GetPaper(ctx, ws, it.PaperID); err != nil {
		return nil, goerr.Wrap(err, "failed to read original", goerr.V("paper_id", it.PaperID))
	}
	if src.blocks, err = u.repo.ListBlocks(ctx, ws, it.PaperID); err != nil {
		return nil, goerr.Wrap(err, "failed to read blocks", goerr.V("paper_id", it.PaperID))
	}
	slices.SortFunc(src.blocks, func(a, b *model.Block) int { return compareIDs(a.ID, b.ID) })
	var snaps []*model.BlockSnapshot
	if src.paper.VersionNumber > 0 {
		if src.latest, err = u.repo.GetVersion(ctx, ws, it.PaperID, src.paper.VersionNumber); err != nil {
			return nil, goerr.Wrap(err, "failed to read latest version", goerr.V("paper_id", it.PaperID))
		}
		if snaps, err = u.repo.GetSnapshots(ctx, ws, it.PaperID, refsOf(src.latest.Blocks)); err != nil {
			return nil, goerr.Wrap(err, "failed to read snapshots", goerr.V("paper_id", it.PaperID))
		}
	}
	for _, s := range snaps {
		src.snaps[model.SnapshotRef{BlockID: s.BlockID, Rev: s.Rev}] = s
	}
	var kids []model.PaperID
	for _, b := range src.blocks {
		if b.Type == model.BlockPaper {
			kids = append(kids, b.Props.PaperID)
		}
	}
	for _, s := range snaps {
		if s.Type == model.BlockPaper {
			kids = append(kids, s.Props.PaperID)
		}
	}
	items, err := u.repo.GetJobItems(ctx, ws, j.ID, kids)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read job items")
	}
	for _, k := range items {
		src.children[k.PaperID] = true
	}
	var linked []model.PaperID
	for _, b := range src.blocks {
		for _, l := range b.Props.Databases {
			linked = append(linked, l.ID)
		}
	}
	for _, s := range snaps {
		for _, l := range s.Props.Databases {
			linked = append(linked, l.ID)
		}
	}
	slices.Sort(linked)
	linked = slices.Compact(linked)
	if len(linked) > 0 {
		along, err := u.repo.GetJobItems(ctx, ws, j.ID, linked)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to read job items")
		}
		for _, k := range along {
			src.databases[k.PaperID] = true
		}
	}
	if src.paper.IsDatabase() && len(src.paper.Items) > 0 {
		along, err := u.repo.GetJobItems(ctx, ws, j.ID, src.paper.Items)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to read job items")
		}
		comes := map[model.PaperID]bool{}
		for _, k := range along {
			comes[k.PaperID] = true
		}
		for _, id := range src.paper.Items {
			if comes[id] {
				src.items = append(src.items, id)
			}
		}
	}
	if src.paper.IsItem() {
		err := u.repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			var err error
			src.values, err = tx.ListItemPropertyValues(it.PaperID)
			return err
		})
		if err != nil {
			return nil, goerr.Wrap(err, "failed to read property values", goerr.V("paper_id", it.PaperID))
		}
	}
	if src.latest != nil {
		for _, vb := range src.latest.Blocks {
			s, ok := src.snaps[vb.Ref()]
			if !ok || (s.Type == model.BlockPaper && !src.children[s.Props.PaperID]) {
				continue
			}
			src.vblocks = append(src.vblocks, vb)
		}
	}
	return src, nil
}

func compareIDs[T ~string](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// revs maps revisions of the copy: they start over. A block that the
// latest version has at another revision than now gets 1 in the version
// and 2 now.
func (s *moveSource) revs() func(id model.BlockID, rev int64, inVersion bool) int64 {
	verRev := map[model.BlockID]int64{}
	if s.latest != nil {
		for _, vb := range s.latest.Blocks {
			verRev[vb.BlockID] = vb.Rev
		}
	}
	return func(id model.BlockID, rev int64, inVersion bool) int64 {
		vr, ok := verRev[id]
		if !ok || inVersion || vr == rev {
			return 1
		}
		return 2
	}
}

func (s *moveSource) mapProps(j *model.Job, props model.BlockProps) model.BlockProps {
	if props.PaperID != "" {
		props.PaperID = copyPaperID(j, props.PaperID)
	}
	if props.AttachmentID != "" {
		props.AttachmentID = copyAttachmentID(j, props.AttachmentID)
	}
	if len(props.Databases) > 0 {
		var links []model.DatabaseLink
		for _, l := range props.Databases {
			if s.databases[l.ID] {
				links = append(links, model.DatabaseLink{ID: copyPaperID(j, l.ID), Write: l.Write})
			}
		}
		props.Databases = links
	}
	return props
}

// copyStep copies the next part of the next paper. An item's Cursor is 0
// before its paper is written, then 1 + the number of blocks and snapshots
// already copied.
func (u *UseCases) copyStep(ctx context.Context, j *model.Job) error {
	items, err := u.repo.ListJobItemsAt(ctx, j.WorkspaceID, j.ID, model.StageCollected, 1)
	if err != nil {
		return goerr.Wrap(err, "failed to list job items")
	}
	if len(items) == 0 {
		return u.commitStep(ctx, j, func(c *stepCtx) error {
			c.job.Phase, c.job.Done = model.PhaseAttach, 0
			return nil
		})
	}
	it := items[0]
	src, err := u.readMoveSource(ctx, j, it)
	if err != nil {
		return err
	}
	dst := j.Target.WorkspaceID
	now := u.now()
	advance := func(n int, copied bool) error {
		return u.commitStep(ctx, j, func(c *stepCtx) error {
			cur, err := c.tx.GetJobItems(c.job.ID, []model.PaperID{it.PaperID})
			if err != nil {
				return err
			}
			if len(cur) == 0 || cur[0].Cursor != it.Cursor || cur[0].Stage != it.Stage {
				return errStaleStep
			}
			next := cur[0]
			next.Cursor += n
			if copied {
				next.Stage = model.StageCopied
				c.job.Done++
			}
			return c.tx.PutJobItem(next)
		})
	}

	if it.Cursor == 0 {
		d := src.paper.Clone()
		d.ID, d.WorkspaceID, d.JobID = it.CopyID, dst, j.ID
		switch {
		case it.PaperID == j.PaperID:
			d.ParentID = j.Target.ParentID
		case src.paper.IsItem():
			d.DatabaseID = copyPaperID(j, src.paper.DatabaseID)
		default:
			d.ParentID = copyPaperID(j, src.paper.ParentID)
		}
		d.VersionNumber = 0
		if src.latest != nil {
			d.VersionNumber = 1
		}
		d.DeletedAt, d.DeletedWith = nil, ""
		d.UpdatedBy, d.UpdatedAt = j.CreatedBy, now
		if d.IsDatabase() {
			d.Items = nil
			for _, id := range src.items {
				d.Items = append(d.Items, copyPaperID(j, id))
			}
		}
		err := u.repo.RunTx(ctx, dst, func(tx interfaces.Tx) error {
			if err := tx.PutPaper(d); err != nil {
				return err
			}
			for _, v := range src.values {
				cv := v.Clone()
				cv.WorkspaceID, cv.DatabaseID, cv.PaperID = dst, d.DatabaseID, d.ID
				if err := tx.PutPropertyValue(cv); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return goerr.Wrap(err, "failed to write copied paper", goerr.V("paper_id", d.ID))
		}
		if err := u.copyAttachments(ctx, j, src, d); err != nil {
			return err
		}
		return advance(1, false)
	}

	revOf := src.revs()
	offset, step := it.Cursor-1, u.cfg.JobStep
	nb, ns := len(src.blocks), len(src.vblocks)
	switch {
	case offset < nb:
		part := src.blocks[offset:min(offset+step, nb)]
		err := u.repo.RunTx(ctx, dst, func(tx interfaces.Tx) error {
			for _, b := range part {
				if b.Type == model.BlockPaper && !src.children[b.Props.PaperID] {
					continue // a child that does not come along
				}
				cb := b.Clone()
				cb.ID, cb.WorkspaceID, cb.PaperID = copyBlockID(j, b.ID), dst, it.CopyID
				if b.ParentID != "" {
					cb.ParentID = copyBlockID(j, b.ParentID)
				}
				cb.Props = src.mapProps(j, b.Props)
				cb.Rev = revOf(b.ID, b.Rev, false)
				if err := tx.PutBlock(cb); err != nil {
					return goerr.Wrap(err, "failed to write copied block")
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		return advance(len(part), false)
	case offset < nb+ns:
		part := src.vblocks[offset-nb : min(offset-nb+step, ns)]
		err := u.repo.RunTx(ctx, dst, func(tx interfaces.Tx) error {
			for _, vb := range part {
				s := src.snaps[vb.Ref()]
				cs := &model.BlockSnapshot{BlockID: copyBlockID(j, vb.BlockID), Rev: revOf(vb.BlockID, vb.Rev, true), Type: s.Type, Content: s.Content, Props: src.mapProps(j, s.Props)}
				if err := tx.PutSnapshot(it.CopyID, cs); err != nil {
					return goerr.Wrap(err, "failed to write copied snapshot")
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		return advance(len(part), false)
	}
	if src.latest != nil {
		err := u.repo.RunTx(ctx, dst, func(tx interfaces.Tx) error {
			if _, err := tx.GetVersion(it.CopyID, 1); err == nil {
				return nil // written by an earlier try
			} else if !errors.Is(err, model.ErrNotFound) {
				return goerr.Wrap(err, "failed to read copied version")
			}
			v := &model.Version{
				WorkspaceID: dst, PaperID: it.CopyID, Number: 1, Title: src.latest.Title,
				Note: src.latest.Note, CreatedBy: src.latest.CreatedBy, CreatedAt: src.latest.CreatedAt,
				Values: src.latest.Values,
			}
			for _, vb := range src.vblocks {
				nvb := model.VersionBlock{BlockID: copyBlockID(j, vb.BlockID), Rev: revOf(vb.BlockID, vb.Rev, true), Order: vb.Order}
				if vb.ParentID != "" {
					nvb.ParentID = copyBlockID(j, vb.ParentID)
				}
				v.Blocks = append(v.Blocks, nvb)
			}
			return tx.PutVersion(v)
		})
		if err != nil {
			return goerr.Wrap(err, "failed to write copied version")
		}
	}
	return advance(0, true)
}

// copyAttachments copies the attachments the blocks and the latest version
// use to the copy. Writing the same key again is harmless.
func (u *UseCases) copyAttachments(ctx context.Context, j *model.Job, src *moveSource, d *model.Paper) error {
	ids := map[model.AttachmentID]bool{}
	for _, b := range src.blocks {
		if b.Props.AttachmentID != "" {
			ids[b.Props.AttachmentID] = true
		}
	}
	for _, s := range src.snaps {
		if s.Props.AttachmentID != "" {
			ids[s.Props.AttachmentID] = true
		}
	}
	for id := range ids {
		a, err := u.repo.GetAttachment(ctx, src.paper.WorkspaceID, src.paper.ID, id)
		if errors.Is(err, model.ErrNotFound) {
			continue
		}
		if err != nil {
			return goerr.Wrap(err, "failed to get attachment")
		}
		body, err := u.store.Get(ctx, a.StoreKey())
		if err != nil {
			return goerr.Wrap(err, "failed to read attachment")
		}
		na := *a
		na.ID, na.WorkspaceID, na.PaperID = copyAttachmentID(j, id), d.WorkspaceID, d.ID
		err = u.store.Put(ctx, na.StoreKey(), na.ContentType, body)
		_ = body.Close()
		if err != nil {
			return goerr.Wrap(err, "failed to copy attachment")
		}
		if err := u.repo.PutAttachment(ctx, &na); err != nil {
			return goerr.Wrap(err, "failed to save copied attachment")
		}
	}
	return nil
}

// attachStep clears the mark on copies, the root's last; the root's copy
// goes into the new parent's body.
func (u *UseCases) attachStep(ctx context.Context, j *model.Job) error {
	items, err := u.repo.ListJobItemsAt(ctx, j.WorkspaceID, j.ID, model.StageCopied, u.cfg.JobStep+1)
	if err != nil {
		return goerr.Wrap(err, "failed to list job items")
	}
	var root *model.JobItem
	rest := items[:0:0]
	for _, it := range items {
		if it.PaperID == j.PaperID {
			root = it
		} else if len(rest) < u.cfg.JobStep {
			rest = append(rest, it)
		}
	}
	dst := j.Target.WorkspaceID
	now := u.now()
	setStage := func(done []*model.JobItem, last bool) error {
		return u.commitStep(ctx, j, func(c *stepCtx) error {
			for _, it := range done {
				it.Stage = model.StageAttached
				if err := c.tx.PutJobItem(it); err != nil {
					return err
				}
				c.job.Done++
			}
			if last {
				c.job.Phase, c.job.Done = model.PhaseRetire, 0
			}
			return nil
		})
	}
	if len(rest) > 0 {
		ids := make([]model.PaperID, len(rest))
		for i, it := range rest {
			ids[i] = it.CopyID
		}
		err := u.repo.RunTx(ctx, dst, func(tx interfaces.Tx) error {
			copies, err := tx.GetPapers(ids)
			if err != nil {
				return goerr.Wrap(err, "failed to read copies")
			}
			w := u.newTreeWriter(ctx, tx, jobPrincipal(j), now)
			w.actor = j.CreatedBy
			for _, q := range copies {
				if q.JobID == j.ID {
					q.JobID = ""
					if err := w.putPaper(q); err != nil {
						return err
					}
				}
			}
			return w.flush()
		})
		if err != nil {
			return err
		}
		return setStage(rest, false)
	}
	if root == nil {
		return setStage(nil, true)
	}
	err = u.repo.RunTx(ctx, dst, func(tx interfaces.Tx) error {
		w := u.newTreeWriter(ctx, tx, jobPrincipal(j), now)
		w.actor = j.CreatedBy
		np, err := tx.GetPaper(j.Target.ParentID)
		if err != nil {
			return goerr.Wrap(err, "the new parent is gone", goerr.V("paper_id", j.Target.ParentID))
		}
		if ok, err := u.canHaveChildren(ctx, np); err != nil {
			return err
		} else if !ok || np.IsTrashed() {
			return goerr.Wrap(model.ErrInvalidArgument, "the new parent must be the home or a filed paper that is not in the trash")
		}
		if np.IsBusy() {
			return busy(np)
		}
		cp, err := tx.GetPaper(root.CopyID)
		if err != nil {
			return goerr.Wrap(err, "failed to read the copy")
		}
		if cp.JobID == j.ID {
			cp.JobID = ""
			if err := w.putPaper(cp); err != nil {
				return err
			}
		}
		if b, err := paperBlockOf(tx, np.ID, cp.ID); err != nil {
			return err
		} else if b == nil {
			if _, err := w.addBlockOf(np, cp.ID, j.Target.After); err != nil {
				return err
			}
		}
		return w.flush()
	})
	if err != nil {
		return err
	}
	return setStage([]*model.JobItem{root}, true)
}

// retireStep trashes originals and leaves forwards.
func (u *UseCases) retireStep(ctx context.Context, j *model.Job) error {
	items, err := u.repo.ListJobItemsAt(ctx, j.WorkspaceID, j.ID, model.StageAttached, u.cfg.JobStep)
	if err != nil {
		return goerr.Wrap(err, "failed to list job items")
	}
	if len(items) == 0 {
		return u.commitStep(ctx, j, func(c *stepCtx) error {
			finishJob(c.job, c.now)
			c.job.Result.CopyID = copyPaperID(c.job, c.job.PaperID)
			f, err := u.repo.GetForward(ctx, c.job.WorkspaceID, c.job.PaperID)
			if err == nil {
				c.job.Result.Forward = f
			}
			return nil
		})
	}
	ids := make([]model.PaperID, len(items))
	for i, it := range items {
		ids[i] = it.PaperID
	}
	return u.commitStep(ctx, j, func(c *stepCtx) error {
		papers, err := c.tx.GetPapers(ids)
		if err != nil {
			return goerr.Wrap(err, "failed to read originals")
		}
		byID := map[model.PaperID]*model.Paper{}
		for _, q := range papers {
			byID[q.ID] = q
		}
		for _, it := range items {
			if q := byID[it.PaperID]; q != nil {
				if !q.IsTrashed() {
					q.DeletedAt, q.DeletedWith = ptr(c.now), c.job.PaperID
				}
				if q.JobID == c.job.ID {
					q.JobID = ""
				}
				if err := c.w.putPaper(q); err != nil {
					return err
				}
				f := &model.Forward{WorkspaceID: c.job.WorkspaceID, PaperID: q.ID, ToWorkspaceID: c.job.Target.WorkspaceID, ToPaperID: it.CopyID, MovedAt: c.now}
				if err := c.tx.PutForward(f); err != nil {
					return goerr.Wrap(err, "failed to write forward")
				}
				if q.ID == c.job.PaperID {
					if err := c.w.removeBlockOf(q.WorkspaceID, q.ParentID, q.ID); err != nil {
						return err
					}
				}
			}
			it.Stage = model.StageRetired
			if err := c.tx.PutJobItem(it); err != nil {
				return err
			}
			c.job.Done++
		}
		return nil
	})
}

// rollbackStep undoes a move that failed before the copy was attached:
// it deletes the copies and clears the marks on the originals.
func (u *UseCases) rollbackStep(ctx context.Context, j *model.Job) error {
	var items []*model.JobItem
	for _, stage := range []int{model.StageAttached, model.StageCopied, model.StageCollected} {
		got, err := u.repo.ListJobItemsAt(ctx, j.WorkspaceID, j.ID, stage, u.cfg.JobStep)
		if err != nil {
			return goerr.Wrap(err, "failed to list job items")
		}
		if len(got) > 0 {
			items = got
			break
		}
	}
	if len(items) == 0 {
		return u.commitStep(ctx, j, func(c *stepCtx) error {
			finishJob(c.job, c.now)
			c.job.State = model.JobFailed
			return nil
		})
	}
	dst := j.Target.WorkspaceID
	for _, it := range items {
		if it.Stage == model.StageCollected && it.Cursor == 0 {
			continue // nothing was copied
		}
		if it.PaperID == j.PaperID {
			err := u.repo.RunTx(ctx, dst, func(tx interfaces.Tx) error {
				w := u.newTreeWriter(ctx, tx, jobPrincipal(j), u.now())
				if err := w.removeBlockOf(dst, j.Target.ParentID, it.CopyID); err != nil && !errors.Is(err, model.ErrNotFound) {
					return err
				}
				return w.flush()
			})
			if err != nil {
				return goerr.Wrap(err, "failed to detach the copy")
			}
		}
		prefix := (&model.Attachment{WorkspaceID: dst, PaperID: it.CopyID}).StorePrefix()
		if err := u.store.DeletePrefix(ctx, prefix); err != nil {
			return goerr.Wrap(err, "failed to delete copied attachments")
		}
		if err := u.repo.PurgePaper(ctx, dst, it.CopyID); err != nil && !errors.Is(err, model.ErrNotFound) {
			return goerr.Wrap(err, "failed to delete copy", goerr.V("paper_id", it.CopyID))
		}
	}
	ids := make([]model.PaperID, len(items))
	for i, it := range items {
		ids[i] = it.PaperID
	}
	return u.commitStep(ctx, j, func(c *stepCtx) error {
		papers, err := c.tx.GetPapers(ids)
		if err != nil {
			return goerr.Wrap(err, "failed to read originals")
		}
		for _, q := range papers {
			if q.JobID == c.job.ID {
				q.JobID = ""
				if err := c.w.putPaper(q); err != nil {
					return err
				}
			}
		}
		for _, it := range items {
			it.Stage = model.StageRolledBack
			if err := c.tx.PutJobItem(it); err != nil {
				return err
			}
		}
		return nil
	})
}

// moveAcross starts the job that moves a paper and everything under it to
// another workspace.
func (u *UseCases) moveAcross(ctx context.Context, p *model.Principal, paper *model.Paper, in MoveInput) (*BatchResult, error) {
	if _, err := u.writableWorkspace(ctx, p, in.Workspace); err != nil {
		return nil, err
	}
	parent, err := u.parentFor(ctx, p, in.Workspace, in.Parent)
	if err != nil {
		return nil, err
	}
	j := u.newJob(p.Actor(), paper.WorkspaceID, model.JobMove, paper.ID, u.now())
	j.Target = &model.JobTarget{WorkspaceID: in.Workspace, ParentID: parent.ID, After: in.After}
	return u.treeJob(ctx, j)
}
