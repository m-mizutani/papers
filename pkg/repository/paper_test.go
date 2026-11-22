package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

func newWorkspaceID() model.WorkspaceID { return model.WorkspaceID(uniq("ws")) }

func paperIn(ws model.WorkspaceID, parent model.PaperID, title string) *model.Paper {
	now := time.Now().UTC()
	actor := humanActor(model.NewUserID())
	return &model.Paper{
		ID: model.NewPaperID(), WorkspaceID: ws, Title: title, ParentID: parent,
		Visibility: model.VisibilityPublic,
		CreatedBy:  actor, CreatedAt: now, UpdatedBy: actor, UpdatedAt: now,
	}
}

func blockIn(p *model.Paper, parent model.BlockID, order, content string) *model.Block {
	now := time.Now().UTC()
	return &model.Block{
		ID: model.NewBlockID(), WorkspaceID: p.WorkspaceID, PaperID: p.ID, ParentID: parent,
		Order: order, Type: model.BlockParagraph, Content: content, Rev: 1,
		CreatedBy: p.CreatedBy, CreatedAt: now, UpdatedBy: p.CreatedBy, UpdatedAt: now,
	}
}

// homeAndChild creates a workspace with a home and one public child paper.
func homeAndChild(t *testing.T, repo interfaces.Repository) (home, child *model.Paper) {
	t.Helper()
	ctx := context.Background()
	ws := newWorkspaceID()
	home = paperIn(ws, "", "Home")
	gt.R1(repo.EnsureHome(ctx, ws, home)).NoError(t)
	child = paperIn(ws, home.ID, "Design")
	gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
		if err := tx.PutPaper(child); err != nil {
			return err
		}
		return tx.PutBlock(paperLink(home, child, "a1"))
	}))
	return home, child
}

func paperLink(parent, child *model.Paper, order string) *model.Block {
	b := blockIn(parent, "", order, "")
	b.Type = model.BlockPaper
	b.Props = model.BlockProps{PaperID: child.ID}
	return b
}

func TestWorkspaceRecords(t *testing.T) {
	runRepositoryTest(t, "ensure home is idempotent", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		ws := newWorkspaceID()
		home := paperIn(ws, "", "Home")
		rec := gt.R1(repo.EnsureHome(ctx, ws, home)).NoError(t)
		gt.Equal(t, rec.HomePaperID, home.ID)

		again := gt.R1(repo.EnsureHome(ctx, ws, paperIn(ws, "", "Other"))).NoError(t)
		gt.Equal(t, again.HomePaperID, home.ID)

		got := gt.R1(repo.GetWorkspaceRecord(ctx, ws)).NoError(t)
		gt.Equal(t, got.HomePaperID, home.ID)
		gt.R1(repo.GetPaper(ctx, ws, home.ID)).NoError(t)

		list := gt.R1(repo.ListWorkspaceRecords(ctx)).NoError(t)
		found := false
		for _, r := range list {
			found = found || r.ID == ws
		}
		gt.True(t, found)

		_, err := repo.GetWorkspaceRecord(ctx, newWorkspaceID())
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})
}

func TestPaperTree(t *testing.T) {
	runRepositoryTest(t, "children, private and batch reads", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		home, child := homeAndChild(t, repo)
		ws := home.WorkspaceID

		private := paperIn(ws, "", "Memo")
		private.Visibility = model.VisibilityPrivate
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutPaper(private) }))

		children := gt.R1(repo.ListChildren(ctx, ws, home.ID)).NoError(t)
		gt.A(t, children).Length(1)
		gt.Equal(t, children[0].ID, child.ID)

		mine := gt.R1(repo.ListPrivatePapers(ctx, ws, private.Owner())).NoError(t)
		gt.A(t, mine).Length(1)
		gt.Equal(t, mine[0].ID, private.ID)
		others := gt.R1(repo.ListPrivatePapers(ctx, ws, child.Owner())).NoError(t)
		gt.A(t, others).Length(0)

		got := gt.R1(repo.GetPapers(ctx, ws, []model.PaperID{child.ID, model.NewPaperID(), home.ID})).NoError(t)
		gt.A(t, got).Length(2)

		_, err := repo.GetPaper(ctx, ws, model.NewPaperID())
		gt.True(t, errors.Is(err, model.ErrNotFound))
		// another workspace does not see the paper
		_, err = repo.GetPaper(ctx, newWorkspaceID(), child.ID)
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})

	runRepositoryTest(t, "trash and purge", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		home, child := homeAndChild(t, repo)
		ws := home.WorkspaceID
		b := blockIn(child, "", "a1", "hello")
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutBlock(b) }))

		past := time.Now().UTC().Add(-31 * 24 * time.Hour)
		child.DeletedAt = &past
		child.DeletedWith = child.ID
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutPaper(child) }))

		trash := gt.R1(repo.ListTrash(ctx, ws, child.Owner())).NoError(t)
		gt.A(t, trash).Length(1)
		old := gt.R1(repo.ListTrashedBefore(ctx, ws, time.Now().Add(-30*24*time.Hour))).NoError(t)
		gt.A(t, old).Length(1)

		gt.NoError(t, repo.PurgePaper(ctx, ws, child.ID))
		_, err := repo.GetPaper(ctx, ws, child.ID)
		gt.True(t, errors.Is(err, model.ErrNotFound))
		_, err = repo.ListBlocks(ctx, ws, child.ID)
		gt.True(t, errors.Is(err, model.ErrNotFound))
		if canSearch(repo) {
			hits := gt.R1(repo.Search(ctx, ws, mustQuery(t, "hello"), 10)).NoError(t)
			gt.A(t, hits).Length(0)
		}
	})
}

func TestBlocks(t *testing.T) {
	runRepositoryTest(t, "transaction reads its own writes", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		_, p := homeAndChild(t, repo)
		ws := p.WorkspaceID
		a := blockIn(p, "", "a1", "A")
		c := blockIn(p, "", "a3", "C")
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			for _, b := range []*model.Block{a, c} {
				if err := tx.PutBlock(b); err != nil {
					return err
				}
			}
			next, err := tx.FirstAfter(p.ID, "", "a1")
			if err != nil {
				return err
			}
			if next == nil || next.ID != c.ID {
				return errors.New("FirstAfter did not see the staged block")
			}
			last, err := tx.Last(p.ID, "")
			if err != nil || last == nil || last.ID != c.ID {
				return errors.New("Last did not see the staged block")
			}
			n, err := tx.CountBlocks(p.ID)
			if err != nil || n != 2 {
				return errors.New("CountBlocks did not see the staged blocks")
			}
			return nil
		}))

		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			if err := tx.DeleteBlock(p.ID, a.ID); err != nil {
				return err
			}
			if _, err := tx.GetBlock(p.ID, a.ID); !errors.Is(err, model.ErrNotFound) {
				return errors.New("deleted block is still visible")
			}
			children, err := tx.Children(p.ID, "")
			if err != nil || len(children) != 1 {
				return errors.New("Children did not drop the deleted block")
			}
			return nil
		}))
		list := gt.R1(repo.ListBlocks(ctx, ws, p.ID)).NoError(t)
		gt.A(t, list).Length(1)
		gt.Equal(t, list[0].ID, c.ID)
		got := gt.R1(repo.GetBlocks(ctx, ws, p.ID, []model.BlockID{c.ID, a.ID})).NoError(t)
		gt.A(t, got).Length(1)
	})

	runRepositoryTest(t, "failed transaction writes nothing", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		_, p := homeAndChild(t, repo)
		boom := errors.New("boom")
		err := repo.RunTx(ctx, p.WorkspaceID, func(tx interfaces.Tx) error {
			if err := tx.PutBlock(blockIn(p, "", "a1", "x")); err != nil {
				return err
			}
			return boom
		})
		gt.True(t, errors.Is(err, boom))
		list := gt.R1(repo.ListBlocks(ctx, p.WorkspaceID, p.ID)).NoError(t)
		gt.A(t, list).Length(0)
	})

	runRepositoryTest(t, "block of an unknown paper rejected", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		_, p := homeAndChild(t, repo)
		other := paperIn(p.WorkspaceID, "", "ghost")
		err := repo.RunTx(ctx, p.WorkspaceID, func(tx interfaces.Tx) error {
			return tx.PutBlock(blockIn(other, "", "a1", "x"))
		})
		gt.Error(t, err)
	})

	runRepositoryTest(t, "range reads include table cells", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		_, p := homeAndChild(t, repo)
		ws := p.WorkspaceID
		row, col := model.TableLine{ID: model.NewLineID(), Order: "a1"}, model.TableLine{ID: model.NewLineID(), Order: "a1"}
		table := blockIn(p, "", "a2", "")
		table.Type = model.BlockTable
		table.Props = model.BlockProps{Rows: []model.TableLine{row}, Cols: []model.TableLine{col}}
		cell := blockIn(p, table.ID, "a1", "cell")
		cell.Type = model.BlockTableCell
		cell.Props = model.BlockProps{RowID: row.ID, ColID: col.ID}
		blocks := []*model.Block{blockIn(p, "", "a1", "one"), table, cell, blockIn(p, "", "a3", "three")}
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			for _, b := range blocks {
				if err := tx.PutBlock(b); err != nil {
					return err
				}
			}
			return nil
		}))

		first, next := gt.R2(repo.ListBlocksRange(ctx, ws, p.ID, "", 2)).NoError(t)
		gt.A(t, first).Length(3) // one, table and its cell
		gt.Equal(t, next, "a2")
		rest, end := gt.R2(repo.ListBlocksRange(ctx, ws, p.ID, next, 2)).NoError(t)
		gt.A(t, rest).Length(1)
		gt.Equal(t, rest[0].Content, "three")
		gt.Equal(t, end, "")
	})
}

func TestVersions(t *testing.T) {
	runRepositoryTest(t, "save, list and read snapshots", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		_, p := homeAndChild(t, repo)
		ws := p.WorkspaceID
		b := blockIn(p, "", "a1", "draft")
		snap := model.SnapshotOf(b)
		now := time.Now().UTC()
		v1 := &model.Version{
			WorkspaceID: ws, PaperID: p.ID, Number: 1, Title: p.Title,
			Blocks:    []model.VersionBlock{{BlockID: b.ID, Rev: b.Rev, Order: b.Order}},
			CreatedBy: p.CreatedBy, CreatedAt: now,
		}
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			if err := tx.PutBlock(b); err != nil {
				return err
			}
			if err := tx.PutSnapshot(p.ID, snap); err != nil {
				return err
			}
			got, err := tx.GetSnapshots(p.ID, []model.SnapshotRef{{BlockID: b.ID, Rev: 1}})
			if err != nil || len(got) != 1 {
				return errors.New("staged snapshot not visible")
			}
			return tx.PutVersion(v1)
		}))

		v2 := *v1
		v2.Number, v2.Note, v2.CreatedAt = 2, "second", now.Add(time.Second)
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutVersion(&v2) }))

		err := repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutVersion(v1) })
		gt.True(t, errors.Is(err, model.ErrAlreadyExists))

		list := gt.R1(repo.ListVersions(ctx, ws, p.ID, 0, 10)).NoError(t)
		gt.A(t, list).Length(2)
		gt.Equal(t, list[0].Number, 2)
		older := gt.R1(repo.ListVersions(ctx, ws, p.ID, 2, 10)).NoError(t)
		gt.A(t, older).Length(1)

		got := gt.R1(repo.GetVersion(ctx, ws, p.ID, 2)).NoError(t)
		gt.Equal(t, got.Note, "second")
		gt.Equal(t, got.Blocks, v1.Blocks)
		snaps := gt.R1(repo.GetSnapshots(ctx, ws, p.ID, []model.SnapshotRef{got.Blocks[0].Ref()})).NoError(t)
		gt.A(t, snaps).Length(1)
		gt.Equal(t, snaps[0].Content, "draft")

		_, err = repo.GetVersion(ctx, ws, p.ID, 9)
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})
}

func TestForwards(t *testing.T) {
	runRepositoryTest(t, "forward record", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		_, p := homeAndChild(t, repo)
		to := newWorkspaceID()
		f := &model.Forward{WorkspaceID: p.WorkspaceID, PaperID: p.ID, ToWorkspaceID: to, ToPaperID: model.NewPaperID(), MovedAt: time.Now().UTC()}
		gt.NoError(t, repo.RunTx(ctx, p.WorkspaceID, func(tx interfaces.Tx) error { return tx.PutForward(f) }))
		got := gt.R1(repo.GetForward(ctx, p.WorkspaceID, p.ID)).NoError(t)
		gt.Equal(t, got.ToWorkspaceID, to)
		gt.Equal(t, got.ToPaperID, f.ToPaperID)
		_, err := repo.GetForward(ctx, p.WorkspaceID, model.NewPaperID())
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})
}

func TestPinsAndRecents(t *testing.T) {
	runRepositoryTest(t, "pins and recents", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		home, child := homeAndChild(t, repo)
		ws, user := home.WorkspaceID, model.NewUserID()
		gt.NoError(t, repo.PutPin(ctx, &model.Pin{UserID: user, WorkspaceID: ws, PaperID: child.ID, Order: "a2"}))
		gt.NoError(t, repo.PutPin(ctx, &model.Pin{UserID: user, WorkspaceID: ws, PaperID: home.ID, Order: "a1"}))
		pins := gt.R1(repo.ListPins(ctx, user, ws)).NoError(t)
		gt.A(t, pins).Length(2)
		gt.Equal(t, pins[0].PaperID, home.ID)
		gt.NoError(t, repo.DeletePin(ctx, user, ws, home.ID))
		pins = gt.R1(repo.ListPins(ctx, user, ws)).NoError(t)
		gt.A(t, pins).Length(1)

		empty := gt.R1(repo.GetRecent(ctx, user, ws)).NoError(t)
		gt.A(t, empty.PaperIDs).Length(0)
		rec := &model.Recent{UserID: user, WorkspaceID: ws}
		rec.Touch(child.ID, time.Now().UTC())
		gt.NoError(t, repo.PutRecent(ctx, rec))
		got := gt.R1(repo.GetRecent(ctx, user, ws)).NoError(t)
		gt.Equal(t, got.PaperIDs, []model.PaperID{child.ID})
	})
}

func TestAttachments(t *testing.T) {
	runRepositoryTest(t, "metadata", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		_, p := homeAndChild(t, repo)
		a := &model.Attachment{
			ID: model.NewAttachmentID(), WorkspaceID: p.WorkspaceID, PaperID: p.ID,
			ContentType: "image/png", Size: 10, SHA256: "abc", Name: "a.png",
			CreatedBy: p.CreatedBy, CreatedAt: time.Now().UTC(),
		}
		gt.NoError(t, repo.PutAttachment(ctx, a))
		got := gt.R1(repo.GetAttachment(ctx, p.WorkspaceID, p.ID, a.ID)).NoError(t)
		gt.Equal(t, got.SHA256, "abc")
		byHash := gt.R1(repo.FindAttachmentByHash(ctx, p.WorkspaceID, p.ID, "abc")).NoError(t)
		gt.Equal(t, byHash.ID, a.ID)
		_, err := repo.FindAttachmentByHash(ctx, p.WorkspaceID, p.ID, "zzz")
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})
}

func TestPaperBlocks(t *testing.T) {
	runRepositoryTest(t, "paper blocks in body order", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		home, child := homeAndChild(t, repo)
		second := paperIn(home.WorkspaceID, home.ID, "Second")
		gt.NoError(t, repo.RunTx(ctx, home.WorkspaceID, func(tx interfaces.Tx) error {
			if err := tx.PutPaper(second); err != nil {
				return err
			}
			if err := tx.PutBlock(blockIn(home, "", "a0V", "text")); err != nil {
				return err
			}
			return tx.PutBlock(paperLink(home, second, "Z"))
		}))
		got := gt.R1(repo.ListPaperBlocks(ctx, home.WorkspaceID, home.ID)).NoError(t)
		gt.A(t, got).Length(2)
		gt.Equal(t, got[0].Props.PaperID, second.ID) // "Z" sorts before "a1"
		gt.Equal(t, got[1].Props.PaperID, child.ID)
	})
}
