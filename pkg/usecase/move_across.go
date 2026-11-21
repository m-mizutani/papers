package usecase

import (
	"context"
	"errors"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

// copied is one paper of a cross-workspace move, read from the source.
type copied struct {
	src      *model.Paper
	dst      *model.Paper
	blocks   []*model.Block
	latest   *model.Version
	snaps    []*model.BlockSnapshot
	blockIDs map[model.BlockID]model.BlockID
	attIDs   map[model.AttachmentID]model.AttachmentID
	// atts are the copied attachments, saved once the copy exists.
	atts []*model.Attachment
}

// moveAcross copies a paper and everything under it into another workspace
// with new IDs: the current state, and the latest version as the copy's v1
// (older versions are not copied). Then it sends the originals to the trash
// and leaves a forward from each old ID to its copy. The two workspaces are
// written in two transactions, target first: if the second fails, the copy
// exists and the originals are untouched, so nothing is lost.
func (u *UseCases) moveAcross(ctx context.Context, p *model.Principal, paper *model.Paper, in MoveInput) (*BatchResult, error) {
	src, dst := paper.WorkspaceID, in.Workspace
	if _, err := u.writableWorkspace(ctx, p, dst); err != nil {
		return nil, err
	}
	parent, err := u.parentFor(ctx, p, dst, in.Parent)
	if err != nil {
		return nil, err
	}
	now := u.now()
	actor := p.Actor()

	// read the source
	var items []*copied
	err = u.repo.RunTx(ctx, src, func(tx interfaces.Tx) error {
		items = nil
		tree, err := u.subtree(ctx, tx, paper)
		if err != nil {
			return err
		}
		for _, q := range tree {
			if q.IsTrashed() || !p.SeesPaper(q) {
				continue
			}
			c := &copied{src: q, blockIDs: map[model.BlockID]model.BlockID{}, attIDs: map[model.AttachmentID]model.AttachmentID{}}
			if c.blocks, err = tx.ListBlocks(q.ID); err != nil {
				return goerr.Wrap(err, "failed to read blocks", goerr.V("paper_id", q.ID))
			}
			if q.VersionNumber > 0 {
				if c.latest, err = tx.GetVersion(q.ID, q.VersionNumber); err != nil {
					return goerr.Wrap(err, "failed to read latest version", goerr.V("paper_id", q.ID))
				}
				if c.snaps, err = tx.GetSnapshots(q.ID, refsOf(c.latest.Blocks)); err != nil {
					return goerr.Wrap(err, "failed to read snapshots", goerr.V("paper_id", q.ID))
				}
			}
			items = append(items, c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// new identities
	paperIDs := map[model.PaperID]model.PaperID{}
	for _, c := range items {
		paperIDs[c.src.ID] = model.NewPaperID()
	}
	for _, c := range items {
		d := c.src.Clone()
		d.ID, d.WorkspaceID = paperIDs[c.src.ID], dst
		d.ParentID = paperIDs[c.src.ParentID]
		if c.src.ID == paper.ID {
			d.ParentID = parent.ID
		}
		d.VersionNumber = 0
		if c.latest != nil {
			d.VersionNumber = 1
		}
		d.UpdatedBy, d.UpdatedAt = actor, now
		c.dst = d
		for _, b := range c.blocks {
			c.blockIDs[b.ID] = model.NewBlockID()
		}
		if c.latest != nil {
			for _, vb := range c.latest.Blocks {
				if _, ok := c.blockIDs[vb.BlockID]; !ok {
					c.blockIDs[vb.BlockID] = model.NewBlockID()
				}
			}
		}
		if err := u.copyAttachments(ctx, c); err != nil {
			return nil, err
		}
	}

	mapProps := func(c *copied, props model.BlockProps) model.BlockProps {
		if props.PaperID != "" {
			props.PaperID = paperIDs[props.PaperID]
		}
		if props.AttachmentID != "" {
			props.AttachmentID = c.attIDs[props.AttachmentID]
		}
		return props
	}

	// write the copy
	err = u.repo.RunTx(ctx, dst, func(tx interfaces.Tx) error {
		w := u.newTreeWriter(ctx, tx, p, now)
		for _, c := range items {
			if err := tx.PutPaper(c.dst); err != nil {
				return goerr.Wrap(err, "failed to write copied paper")
			}
		}
		for _, c := range items {
			// Revisions start over. A block that the latest version has at
			// another revision than now gets 1 in the version and 2 now.
			verRev := map[model.BlockID]int64{}
			if c.latest != nil {
				for _, vb := range c.latest.Blocks {
					verRev[vb.BlockID] = vb.Rev
				}
			}
			newRev := func(id model.BlockID, rev int64, inVersion bool) int64 {
				vr, ok := verRev[id]
				switch {
				case !ok:
					return 1
				case inVersion || vr == rev:
					return 1
				default:
					return 2
				}
			}
			for _, b := range c.blocks {
				if b.Type == model.BlockPaper && paperIDs[b.Props.PaperID] == "" {
					continue // a child that did not come along
				}
				nb := b.Clone()
				nb.ID, nb.WorkspaceID, nb.PaperID = c.blockIDs[b.ID], dst, c.dst.ID
				if b.ParentID != "" {
					nb.ParentID = c.blockIDs[b.ParentID]
				}
				nb.Props = mapProps(c, b.Props)
				nb.Rev = newRev(b.ID, b.Rev, false)
				if err := tx.PutBlock(nb); err != nil {
					return goerr.Wrap(err, "failed to write copied block")
				}
			}
			if c.latest != nil {
				byRef := map[model.SnapshotRef]*model.BlockSnapshot{}
				for _, s := range c.snaps {
					byRef[model.SnapshotRef{BlockID: s.BlockID, Rev: s.Rev}] = s
				}
				v := &model.Version{
					WorkspaceID: dst, PaperID: c.dst.ID, Number: 1, Title: c.latest.Title,
					Note: c.latest.Note, CreatedBy: c.latest.CreatedBy, CreatedAt: c.latest.CreatedAt,
				}
				for _, vb := range c.latest.Blocks {
					s, ok := byRef[vb.Ref()]
					if !ok {
						continue
					}
					if s.Type == model.BlockPaper && paperIDs[s.Props.PaperID] == "" {
						continue
					}
					nvb := model.VersionBlock{BlockID: c.blockIDs[vb.BlockID], Rev: newRev(vb.BlockID, vb.Rev, true), Order: vb.Order}
					if vb.ParentID != "" {
						nvb.ParentID = c.blockIDs[vb.ParentID]
					}
					ns := &model.BlockSnapshot{BlockID: nvb.BlockID, Rev: nvb.Rev, Type: s.Type, Content: s.Content, Props: mapProps(c, s.Props)}
					if err := tx.PutSnapshot(c.dst.ID, ns); err != nil {
						return goerr.Wrap(err, "failed to write copied snapshot")
					}
					v.Blocks = append(v.Blocks, nvb)
				}
				if err := tx.PutVersion(v); err != nil {
					return goerr.Wrap(err, "failed to write copied version")
				}
			}
		}
		np, err := tx.GetPaper(parent.ID)
		if err != nil {
			return goerr.Wrap(err, "failed to get new parent")
		}
		if _, err := w.addBlockOf(np, paperIDs[paper.ID], in.After); err != nil {
			return err
		}
		return w.flush()
	})
	if err != nil {
		return nil, err
	}

	for _, c := range items {
		for _, a := range c.atts {
			if err := u.repo.PutAttachment(ctx, a); err != nil {
				return nil, goerr.Wrap(err, "failed to save copied attachment")
			}
		}
	}

	// trash the originals and leave forwards
	var result *BatchResult
	err = u.repo.RunTx(ctx, src, func(tx interfaces.Tx) error {
		w := u.newTreeWriter(ctx, tx, p, now)
		for _, c := range items {
			q, err := tx.GetPaper(c.src.ID)
			if errors.Is(err, model.ErrNotFound) {
				continue
			}
			if err != nil {
				return goerr.Wrap(err, "failed to get original")
			}
			if !q.IsTrashed() {
				q.DeletedAt, q.DeletedWith = ptr(now), paper.ID
				if err := w.putPaper(q); err != nil {
					return err
				}
			}
			f := &model.Forward{WorkspaceID: src, PaperID: c.src.ID, ToWorkspaceID: dst, ToPaperID: c.dst.ID, MovedAt: now}
			if err := tx.PutForward(f); err != nil {
				return goerr.Wrap(err, "failed to write forward")
			}
			if c.src.ID == paper.ID {
				w.result.Forward = f
			}
		}
		if err := w.removeBlockOf(src, paper.ParentID, paper.ID); err != nil {
			return err
		}
		w.result.Paper = items[0].dst
		result = w.result
		return w.flush()
	})
	if err != nil {
		return nil, goerr.Wrap(err, "the paper was copied but the originals were not moved to the trash", goerr.V("copy", paperIDs[paper.ID]))
	}
	return result, nil
}

// copyAttachments copies the attachments the blocks and the latest version
// use to the copy's paper.
func (u *UseCases) copyAttachments(ctx context.Context, c *copied) error {
	ids := map[model.AttachmentID]bool{}
	for _, b := range c.blocks {
		if b.Props.AttachmentID != "" {
			ids[b.Props.AttachmentID] = true
		}
	}
	for _, s := range c.snaps {
		if s.Props.AttachmentID != "" {
			ids[s.Props.AttachmentID] = true
		}
	}
	for id := range ids {
		a, err := u.repo.GetAttachment(ctx, c.src.WorkspaceID, c.src.ID, id)
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
		na.ID, na.WorkspaceID, na.PaperID = model.NewAttachmentID(), c.dst.WorkspaceID, c.dst.ID
		err = u.store.Put(ctx, na.StoreKey(), na.ContentType, body)
		_ = body.Close()
		if err != nil {
			return goerr.Wrap(err, "failed to copy attachment")
		}
		c.atts = append(c.atts, &na)
		c.attIDs[id] = na.ID
	}
	return nil
}
