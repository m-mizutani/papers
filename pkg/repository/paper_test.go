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

		// a private paper has no parent, so it is in no list of children
		none := gt.R1(repo.ListChildren(ctx, ws, private.ID)).NoError(t)
		gt.A(t, none).Length(0)

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

func TestPaperIcon(t *testing.T) {
	runRepositoryTest(t, "icon round trip", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		home, child := homeAndChild(t, repo)
		ws := home.WorkspaceID

		// a paper written without an icon reads back without one
		got := gt.R1(repo.GetPaper(ctx, ws, child.ID)).NoError(t)
		gt.Equal(t, got.Icon, "")

		child.Icon = "👩‍💻"
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutPaper(child) }))
		got = gt.R1(repo.GetPaper(ctx, ws, child.ID)).NoError(t)
		gt.Equal(t, got.Icon, "👩‍💻")
		list := gt.R1(repo.GetPapers(ctx, ws, []model.PaperID{child.ID})).NoError(t)
		gt.Equal(t, list[0].Icon, "👩‍💻")

		// an invalid icon is never written
		child.Icon = "🚀🔥"
		err := repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutPaper(child) })
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
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

func TestFavoritesAndRecents(t *testing.T) {
	runRepositoryTest(t, "favorites round trip", func(t *testing.T, repo interfaces.Repository) {
		// T41
		ctx := context.Background()
		home, child := homeAndChild(t, repo)
		ws, user := home.WorkspaceID, model.NewUserID()
		second := paperIn(ws, home.ID, "Second")
		gt.NoError(t, repo.PutFavorite(ctx, &model.Favorite{UserID: user, WorkspaceID: ws, PaperID: child.ID, Order: "a2"}))
		gt.NoError(t, repo.PutFavorite(ctx, &model.Favorite{UserID: user, WorkspaceID: ws, PaperID: second.ID, Order: "a1"}))
		// another user's and another workspace's favorites stay apart
		gt.NoError(t, repo.PutFavorite(ctx, &model.Favorite{UserID: model.NewUserID(), WorkspaceID: ws, PaperID: child.ID, Order: "a1"}))
		gt.NoError(t, repo.PutFavorite(ctx, &model.Favorite{UserID: user, WorkspaceID: newWorkspaceID(), PaperID: child.ID, Order: "a1"}))

		favorites := gt.R1(repo.ListFavorites(ctx, user, ws)).NoError(t)
		gt.A(t, favorites).Length(2)
		gt.Equal(t, *favorites[0], model.Favorite{UserID: user, WorkspaceID: ws, PaperID: second.ID, Order: "a1"})
		gt.Equal(t, *favorites[1], model.Favorite{UserID: user, WorkspaceID: ws, PaperID: child.ID, Order: "a2"})

		// putting again moves it
		gt.NoError(t, repo.PutFavorite(ctx, &model.Favorite{UserID: user, WorkspaceID: ws, PaperID: child.ID, Order: "Zz"}))
		favorites = gt.R1(repo.ListFavorites(ctx, user, ws)).NoError(t)
		gt.Equal(t, favorites[0].PaperID, child.ID)

		gt.NoError(t, repo.DeleteFavorite(ctx, user, ws, child.ID))
		favorites = gt.R1(repo.ListFavorites(ctx, user, ws)).NoError(t)
		gt.A(t, favorites).Length(1)
		gt.Equal(t, favorites[0].PaperID, second.ID)
		gt.NoError(t, repo.DeleteFavorite(ctx, user, ws, child.ID)) // gone already

		gt.Error(t, repo.PutFavorite(ctx, &model.Favorite{UserID: user, WorkspaceID: ws, PaperID: child.ID, Order: ""}))
		gt.Error(t, repo.PutFavorite(ctx, &model.Favorite{UserID: user, WorkspaceID: ws, Order: "a1"}))
	})

	runRepositoryTest(t, "recents", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		home, child := homeAndChild(t, repo)
		ws, user := home.WorkspaceID, model.NewUserID()
		empty := gt.R1(repo.GetRecent(ctx, user, ws)).NoError(t)
		gt.A(t, empty.PaperIDs).Length(0)
		rec := &model.Recent{UserID: user, WorkspaceID: ws}
		rec.Touch(child.ID, time.Now().UTC())
		gt.NoError(t, repo.PutRecent(ctx, rec))
		got := gt.R1(repo.GetRecent(ctx, user, ws)).NoError(t)
		gt.Equal(t, got.PaperIDs, []model.PaperID{child.ID})
	})

	runRepositoryTest(t, "batch reads across workspaces", func(t *testing.T, repo interfaces.Repository) {
		// T55: GetRecents and GetPapersIn return what was written and leave
		// out what is not there
		ctx := context.Background()
		homeA, childA := homeAndChild(t, repo)
		homeB, childB := homeAndChild(t, repo)
		wsA, wsB, wsC := homeA.WorkspaceID, homeB.WorkspaceID, newWorkspaceID()
		user := model.NewUserID()
		at := time.Now().UTC()
		recA := &model.Recent{UserID: user, WorkspaceID: wsA}
		recA.Touch(childA.ID, at)
		recB := &model.Recent{UserID: user, WorkspaceID: wsB}
		recB.Touch(homeB.ID, at)
		recB.Touch(childB.ID, at.Add(time.Second))
		gt.NoError(t, repo.PutRecent(ctx, recA))
		gt.NoError(t, repo.PutRecent(ctx, recB))
		// someone else's list is not read
		other := &model.Recent{UserID: model.NewUserID(), WorkspaceID: wsC}
		other.Touch(childA.ID, at)
		gt.NoError(t, repo.PutRecent(ctx, other))

		recents := gt.R1(repo.GetRecents(ctx, user, []model.WorkspaceID{wsA, wsB, wsC})).NoError(t)
		gt.A(t, recents).Length(2)
		byWS := map[model.WorkspaceID]*model.Recent{}
		for _, r := range recents {
			gt.Equal(t, r.UserID, user)
			byWS[r.WorkspaceID] = r
		}
		gt.Equal(t, byWS[wsA].PaperIDs, []model.PaperID{childA.ID})
		gt.True(t, timeEq(byWS[wsA].UpdatedAt, at))
		gt.Equal(t, byWS[wsB].PaperIDs, []model.PaperID{childB.ID, homeB.ID})
		gt.True(t, timeEq(byWS[wsB].UpdatedAt, at.Add(time.Second)))
		gt.A(t, gt.R1(repo.GetRecents(ctx, user, nil)).NoError(t)).Length(0)

		papers := gt.R1(repo.GetPapersIn(ctx, []model.PaperKey{
			{WorkspaceID: wsA, PaperID: childA.ID},
			{WorkspaceID: wsB, PaperID: childB.ID},
			{WorkspaceID: wsB, PaperID: childA.ID}, // in another workspace
			{WorkspaceID: wsC, PaperID: model.NewPaperID()},
		})).NoError(t)
		gt.A(t, papers).Length(2)
		byID := map[model.PaperID]*model.Paper{}
		for _, p := range papers {
			byID[p.ID] = p
		}
		gt.Equal(t, byID[childA.ID].WorkspaceID, wsA)
		gt.Equal(t, byID[childA.ID].Title, childA.Title)
		gt.Equal(t, byID[childB.ID].WorkspaceID, wsB)
		gt.A(t, gt.R1(repo.GetPapersIn(ctx, nil)).NoError(t)).Length(0)
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

func TestListPapers(t *testing.T) {
	// papers of one workspace: the home, two unfiled papers of alice, one of
	// bob, a private one of alice and a filed one of bob, with known times
	setup := func(t *testing.T, repo interfaces.Repository) (model.WorkspaceID, model.UserID, map[string]*model.Paper) {
		t.Helper()
		ctx := context.Background()
		ws := newWorkspaceID()
		base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		alice, bob := model.NewUserID(), model.NewUserID()
		mk := func(title string, owner model.UserID, parent model.PaperID, created, updated int) *model.Paper {
			p := paperIn(ws, parent, title)
			p.CreatedBy, p.UpdatedBy = humanActor(owner), humanActor(owner)
			p.CreatedAt, p.UpdatedAt = base.Add(time.Duration(created)*time.Minute), base.Add(time.Duration(updated)*time.Minute)
			return p
		}
		home := mk("Home", model.NewUserID(), "", 0, 0)
		gt.R1(repo.EnsureHome(ctx, ws, home)).NoError(t)
		ps := map[string]*model.Paper{
			"home":  home,
			"a1":    mk("a1", alice, "", 1, 9),
			"a2":    mk("a2", alice, "", 2, 5),
			"b1":    mk("b1", bob, "", 3, 7),
			"memo":  mk("memo", alice, "", 4, 6),
			"filed": mk("filed", bob, home.ID, 5, 8),
		}
		ps["memo"].Visibility = model.VisibilityPrivate
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			for k, p := range ps {
				if k == "home" {
					continue
				}
				if err := tx.PutPaper(p); err != nil {
					return err
				}
			}
			return tx.PutBlock(paperLink(home, ps["filed"], "a1"))
		}))
		return ws, alice, ps
	}
	titles := func(papers []*model.Paper) []string {
		out := []string{}
		for _, p := range papers {
			out = append(out, p.Title)
		}
		return out
	}

	runRepositoryTest(t, "conditions and order", func(t *testing.T, repo interfaces.Repository) {
		// T30
		ctx := context.Background()
		ws, alice, ps := setup(t, repo)
		list := func(q model.PaperQuery) []string {
			t.Helper()
			q.Limit = 100
			got, next := gt.R2(repo.ListPapers(ctx, ws, q)).NoError(t)
			gt.Equal(t, next, "")
			return titles(got)
		}
		gt.Equal(t, list(model.PaperQuery{Sort: model.SortUpdated}), []string{"a1", "filed", "b1", "memo", "a2", "Home"})
		gt.Equal(t, list(model.PaperQuery{Sort: model.SortCreated}), []string{"filed", "memo", "b1", "a2", "a1", "Home"})
		gt.Equal(t, list(model.PaperQuery{Sort: model.SortUpdated, Unfiled: true}), []string{"a1", "b1", "memo", "a2", "Home"})
		gt.Equal(t, list(model.PaperQuery{Sort: model.SortUpdated, Creator: alice}), []string{"a1", "memo", "a2"})
		gt.Equal(t, list(model.PaperQuery{Sort: model.SortCreated, Unfiled: true, Creator: alice}), []string{"memo", "a2", "a1"})
		gt.Equal(t, list(model.PaperQuery{Sort: model.SortCreated, Creator: model.NewUserID()}), []string{})

		// ties are broken by ID, descending
		same := ps["a2"].Clone()
		same.ID = model.NewPaperID()
		same.Title = "a2 twin"
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutPaper(same) }))
		got := list(model.PaperQuery{Sort: model.SortUpdated, Creator: alice})
		want := []string{"a1", "memo", "a2", "a2 twin"}
		if same.ID > ps["a2"].ID {
			want = []string{"a1", "memo", "a2 twin", "a2"}
		}
		gt.Equal(t, got, want)

		// the paper is read back as written
		full, _ := gt.R2(repo.ListPapers(ctx, ws, model.PaperQuery{Sort: model.SortUpdated, Creator: alice, Limit: 1})).NoError(t)
		gt.Equal(t, full[0].ID, ps["a1"].ID)
		gt.Equal(t, full[0].CreatedBy, ps["a1"].CreatedBy)
		gt.True(t, timeEq(full[0].UpdatedAt, ps["a1"].UpdatedAt))
		gt.True(t, timeEq(full[0].CreatedAt, ps["a1"].CreatedAt))

		// other workspaces are not listed; bad queries are refused
		empty, _ := gt.R2(repo.ListPapers(ctx, newWorkspaceID(), model.PaperQuery{Sort: model.SortUpdated, Limit: 10})).NoError(t)
		gt.A(t, empty).Length(0)
		for _, bad := range []model.PaperQuery{
			{Sort: "title", Limit: 10}, {Sort: model.SortUpdated}, {Sort: model.SortUpdated, Limit: 101}, {Sort: model.SortUpdated, Limit: 1, Cursor: "x"},
		} {
			_, _, err := repo.ListPapers(ctx, ws, bad)
			gt.True(t, errors.Is(err, model.ErrInvalidArgument))
		}
	})

	runRepositoryTest(t, "cursor paging", func(t *testing.T, repo interfaces.Repository) {
		// T31: limit 2 reads six papers in three pages without duplicates
		// or gaps
		ctx := context.Background()
		ws, _, _ := setup(t, repo)
		for _, sort := range []model.PaperSort{model.SortUpdated, model.SortCreated} {
			all, _ := gt.R2(repo.ListPapers(ctx, ws, model.PaperQuery{Sort: sort, Limit: 100})).NoError(t)
			gt.A(t, all).Length(6)
			var paged []*model.Paper
			cursor, pages := "", 0
			for {
				got, next := gt.R2(repo.ListPapers(ctx, ws, model.PaperQuery{Sort: sort, Limit: 2, Cursor: cursor})).NoError(t)
				gt.A(t, got).Length(2)
				paged = append(paged, got...)
				pages++
				if next == "" {
					break
				}
				gt.True(t, pages < 3)
				cursor = next
			}
			gt.V(t, pages).Equal(3)
			gt.Equal(t, titles(paged), titles(all))
		}
		// a filter keeps working across pages
		first, next := gt.R2(repo.ListPapers(ctx, ws, model.PaperQuery{Sort: model.SortUpdated, Unfiled: true, Limit: 3})).NoError(t)
		gt.Equal(t, titles(first), []string{"a1", "b1", "memo"})
		rest, end := gt.R2(repo.ListPapers(ctx, ws, model.PaperQuery{Sort: model.SortUpdated, Unfiled: true, Limit: 3, Cursor: next})).NoError(t)
		gt.Equal(t, titles(rest), []string{"a2", "Home"})
		gt.Equal(t, end, "")
	})
}

func TestBlockFields(t *testing.T) {
	runRepositoryTest(t, "fields round trip", func(t *testing.T, repo interfaces.Repository) {
		// T13
		ctx := context.Background()
		_, p := homeAndChild(t, repo)
		ws := p.WorkspaceID
		b := blockIn(p, "", "a1", `<p>RTO <span data-field="rto">15m</span></p>`)
		b.Type = model.BlockHTML
		b.Props = model.BlockProps{Height: 240, Fields: map[string]string{"rto": "15m", "owner": "SRE チーム"}}
		plain := blockIn(p, "", "a2", "no fields")
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			if err := tx.PutBlock(b); err != nil {
				return err
			}
			got, err := tx.GetBlock(p.ID, b.ID)
			if err != nil || !got.Props.Equal(b.Props) {
				return errors.New("staged fields not visible")
			}
			return tx.PutBlock(plain)
		}))
		got := gt.R1(repo.GetBlocks(ctx, ws, p.ID, []model.BlockID{b.ID, plain.ID})).NoError(t)
		gt.A(t, got).Length(2)
		byID := map[model.BlockID]*model.Block{}
		for _, x := range got {
			byID[x.ID] = x
		}
		gt.Equal(t, byID[b.ID].Props.Fields, b.Props.Fields)
		gt.V(t, byID[b.ID].Props.Height).Equal(240)
		gt.True(t, model.SameContents(byID[b.ID], b))
		gt.Nil(t, byID[plain.ID].Props.Fields)

		// a version snapshot keeps them too
		snap := model.SnapshotOf(b)
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutSnapshot(p.ID, snap) }))
		snaps := gt.R1(repo.GetSnapshots(ctx, ws, p.ID, []model.SnapshotRef{{BlockID: b.ID, Rev: b.Rev}})).NoError(t)
		gt.Equal(t, snaps[0].Props.Fields, b.Props.Fields)

		// the values are searchable
		if canSearch(repo) {
			hits := gt.R1(repo.Search(ctx, ws, mustQuery(t, "15m SRE"), 10)).NoError(t)
			gt.A(t, hits).Length(1)
			gt.Equal(t, hits[0].BlockID, b.ID)
		}

		// changing one value is a change of contents
		b.Props.Fields = map[string]string{"rto": "10m", "owner": "SRE チーム"}
		b.Rev++
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutBlock(b) }))
		again := gt.R1(repo.GetBlocks(ctx, ws, p.ID, []model.BlockID{b.ID})).NoError(t)
		gt.Equal(t, again[0].Props.Fields["rto"], "10m")
	})
}

func TestHTMLPapers(t *testing.T) {
	runRepositoryTest(t, "kind, html page block and attachment in one transaction", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		home, _ := homeAndChild(t, repo)
		p := paperIn(home.WorkspaceID, "", "Report")
		p.Kind = model.PaperKindHTML
		a := &model.Attachment{
			ID: model.NewAttachmentID(), WorkspaceID: p.WorkspaceID, PaperID: p.ID,
			ContentType: model.HTMLContentType, Size: 12, SHA256: "html-sum",
			CreatedBy: p.CreatedBy, CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
		}
		page := blockIn(p, "", "a1", "hello")
		page.Type = model.BlockHTMLPage
		page.Props = model.BlockProps{AttachmentID: a.ID}
		gt.NoError(t, repo.RunTx(ctx, p.WorkspaceID, func(tx interfaces.Tx) error {
			if err := tx.PutPaper(p); err != nil {
				return err
			}
			if err := tx.PutAttachment(a); err != nil {
				return err
			}
			return tx.PutBlock(page)
		}))

		got := gt.R1(repo.GetPaper(ctx, p.WorkspaceID, p.ID)).NoError(t)
		gt.Equal(t, got.Kind, model.PaperKindHTML)
		gotA := gt.R1(repo.GetAttachment(ctx, p.WorkspaceID, p.ID, a.ID)).NoError(t)
		gt.Equal(t, gotA.ID, a.ID)
		gt.Equal(t, gotA.WorkspaceID, a.WorkspaceID)
		gt.Equal(t, gotA.PaperID, a.PaperID)
		gt.Equal(t, gotA.ContentType, a.ContentType)
		gt.Equal(t, gotA.Size, a.Size)
		gt.Equal(t, gotA.SHA256, a.SHA256)
		gt.Equal(t, gotA.CreatedBy, a.CreatedBy)
		gt.True(t, gotA.CreatedAt.Equal(a.CreatedAt))
		byHash := gt.R1(repo.FindAttachmentByHash(ctx, p.WorkspaceID, p.ID, "html-sum")).NoError(t)
		gt.Equal(t, byHash.ID, a.ID)
		blocks := gt.R1(repo.ListBlocks(ctx, p.WorkspaceID, p.ID)).NoError(t)
		gt.A(t, blocks).Length(1)
		gt.Equal(t, blocks[0].Type, model.BlockHTMLPage)
		gt.Equal(t, blocks[0].Props.AttachmentID, a.ID)
	})

	runRepositoryTest(t, "attachment of a failed transaction is not stored", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		_, p := homeAndChild(t, repo)
		a := &model.Attachment{
			ID: model.NewAttachmentID(), WorkspaceID: p.WorkspaceID, PaperID: p.ID,
			ContentType: model.HTMLContentType, Size: 3, SHA256: "s",
			CreatedBy: p.CreatedBy, CreatedAt: time.Now().UTC(),
		}
		boom := errors.New("boom")
		err := repo.RunTx(ctx, p.WorkspaceID, func(tx interfaces.Tx) error {
			if err := tx.PutAttachment(a); err != nil {
				return err
			}
			return boom
		})
		gt.True(t, errors.Is(err, boom))
		_, err = repo.GetAttachment(ctx, p.WorkspaceID, p.ID, a.ID)
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})

	runRepositoryTest(t, "a paper without a kind reads as blocks", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		_, p := homeAndChild(t, repo) // written without Kind, as older records are
		got := gt.R1(repo.GetPaper(ctx, p.WorkspaceID, p.ID)).NoError(t)
		gt.Equal(t, got.Kind.Or(), model.PaperKindBlocks)
		gt.False(t, got.IsHTML())
	})
}

func TestTxAttachmentOfUnknownPaper(t *testing.T) {
	runRepositoryTest(t, "attachment of an unknown paper rejected", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		_, p := homeAndChild(t, repo)
		err := repo.RunTx(ctx, p.WorkspaceID, func(tx interfaces.Tx) error {
			return tx.PutAttachment(&model.Attachment{
				ID: model.NewAttachmentID(), WorkspaceID: p.WorkspaceID, PaperID: model.NewPaperID(),
				ContentType: model.HTMLContentType, Size: 3, SHA256: "s", CreatedBy: p.CreatedBy, CreatedAt: time.Now().UTC(),
			})
		})
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	})
}
