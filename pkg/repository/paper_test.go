package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

func newPaper(t *testing.T, repo interfaces.Repository) *model.Paper {
	t.Helper()
	now := time.Now().UTC()
	actor := humanActor(model.NewUserID())
	p := &model.Paper{
		ID: model.NewPaperID(), WorkspaceID: model.NewWorkspaceID(), Title: "Design",
		Access:    model.PaperAccess{Restricted: true, Bindings: []model.RoleBinding{{Subject: model.Subject{Kind: model.SubjectGroup, ID: "g"}, Role: model.RoleEditor}}},
		CreatedBy: actor, CreatedAt: now, UpdatedBy: actor, UpdatedAt: now,
	}
	gt.NoError(t, repo.CreatePaper(context.Background(), p))
	return p
}

func newBlock(p *model.Paper, order string, content string) *model.Block {
	now := time.Now().UTC()
	return &model.Block{
		ID: model.NewBlockID(), WorkspaceID: p.WorkspaceID, PaperID: p.ID,
		Order: order, Type: model.BlockParagraph, Content: content, Version: 1,
		CreatedBy: p.CreatedBy, CreatedAt: now, UpdatedBy: p.CreatedBy, UpdatedAt: now,
	}
}

func commitFor(p *model.Paper, at time.Time, changes ...model.Change) *model.Commit {
	return &model.Commit{
		ID: model.NewCommitID(), WorkspaceID: p.WorkspaceID, PaperID: p.ID,
		Actor: p.CreatedBy, Message: "m", Changes: changes, CreatedAt: at,
	}
}

func TestPaperRepository(t *testing.T) {
	runRepositoryTest(t, "create and get", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		got := gt.R1(repo.GetPaper(ctx, p.WorkspaceID, p.ID)).NoError(t)
		gt.Equal(t, got.Title, p.Title)
		gt.Equal(t, got.Access, p.Access)
		gt.Equal(t, got.CreatedBy, p.CreatedBy)
		gt.Equal(t, got.UpdatedBy, p.UpdatedBy)
		gt.True(t, timeEq(got.CreatedAt, p.CreatedAt))
		gt.True(t, timeEq(got.UpdatedAt, p.UpdatedAt))

		err := repo.CreatePaper(ctx, p)
		gt.True(t, errors.Is(err, model.ErrAlreadyExists))

		list := gt.R1(repo.ListPapers(ctx, p.WorkspaceID)).NoError(t)
		gt.A(t, list).Length(1)
		other := gt.R1(repo.ListPapers(ctx, model.NewWorkspaceID())).NoError(t)
		gt.A(t, other).Length(0)
	})

	runRepositoryTest(t, "paper is scoped by workspace", func(t *testing.T, repo interfaces.Repository) {
		p := newPaper(t, repo)
		_, err := repo.GetPaper(context.Background(), model.NewWorkspaceID(), p.ID)
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})

	runRepositoryTest(t, "transaction applies blocks, paper and commit atomically", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		b1 := newBlock(p, "V", "first")
		b2 := newBlock(p, "k", "second")
		b2.Props = model.BlockProps{} // explicit zero props round-trip

		gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error {
			gt.Equal(t, tx.Paper().ID, p.ID)
			if err := tx.PutBlock(b1); err != nil {
				return err
			}
			if err := tx.PutBlock(b2); err != nil {
				return err
			}
			// staged writes are visible inside the transaction
			children, err := tx.Children("")
			if err != nil {
				return err
			}
			gt.A(t, children).Length(2)
			gt.Equal(t, children[0].ID, b1.ID)

			paper := tx.Paper()
			paper.Title = "Renamed"
			if err := tx.PutPaper(paper); err != nil {
				return err
			}
			return tx.AddCommit(commitFor(p, time.Now().UTC(), model.Change{Kind: model.ChangeInsert, BlockID: b1.ID, ToVersion: 1}))
		}))

		got := gt.R1(repo.GetPaper(ctx, p.WorkspaceID, p.ID)).NoError(t)
		gt.Equal(t, got.Title, "Renamed")

		blocks := gt.R1(repo.ListBlocks(ctx, p.WorkspaceID, p.ID)).NoError(t)
		gt.A(t, blocks).Length(2)
		gt.Equal(t, blocks[0].ID, b1.ID) // "V" < "k"
		gt.Equal(t, blocks[0].Content, "first")
		gt.Equal(t, blocks[0].Type, b1.Type)
		gt.Equal(t, blocks[0].Order, b1.Order)
		gt.Equal(t, blocks[0].Version, b1.Version)
		gt.Equal(t, blocks[0].CreatedBy, b1.CreatedBy)
		gt.Equal(t, blocks[0].UpdatedBy, b1.UpdatedBy)
		gt.True(t, timeEq(blocks[0].CreatedAt, b1.CreatedAt))
		gt.True(t, timeEq(blocks[0].UpdatedAt, b1.UpdatedAt))

		single := gt.R1(repo.GetBlock(ctx, p.WorkspaceID, p.ID, b2.ID)).NoError(t)
		gt.Equal(t, single.Content, "second")
		gt.Equal(t, single.Props, b2.Props)
		_, err := repo.GetBlock(ctx, p.WorkspaceID, p.ID, model.NewBlockID())
		gt.True(t, errors.Is(err, model.ErrNotFound))

		commits := gt.R1(repo.ListCommits(ctx, p.WorkspaceID, p.ID, "", 10)).NoError(t)
		gt.A(t, commits).Length(1)
		gt.Equal(t, commits[0].Message, "m")
		gt.Equal(t, commits[0].Actor, p.CreatedBy)
		gt.Equal(t, commits[0].Changes, []model.Change{{Kind: model.ChangeInsert, BlockID: b1.ID, ToVersion: 1}})

		// every put also wrote a version snapshot
		v := gt.R1(repo.GetBlockVersion(ctx, p.WorkspaceID, p.ID, b1.ID, 1)).NoError(t)
		gt.Equal(t, v.Content, "first")
	})

	runRepositoryTest(t, "failed transaction leaves nothing behind", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		boom := goerr.New("boom")
		err := repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error {
			if err := tx.PutBlock(newBlock(p, "V", "x")); err != nil {
				return err
			}
			return boom
		})
		gt.True(t, errors.Is(err, boom))
		blocks := gt.R1(repo.ListBlocks(ctx, p.WorkspaceID, p.ID)).NoError(t)
		gt.A(t, blocks).Length(0)
	})

	runRepositoryTest(t, "block versions are immutable and kept after delete", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		b := newBlock(p, "V", "v1")
		gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error { return tx.PutBlock(b) }))
		b2 := b.Clone()
		b2.Content, b2.Version = "v2", 2
		gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error { return tx.PutBlock(b2) }))

		// writing an existing version again is refused
		err := repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error { return tx.PutBlock(b) })
		gt.True(t, errors.Is(err, model.ErrAlreadyExists))
		cur := gt.R1(repo.GetBlock(ctx, p.WorkspaceID, p.ID, b.ID)).NoError(t)
		gt.Equal(t, cur.Content, "v2")

		gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error { return tx.DeleteBlock(b.ID) }))
		vs := gt.R1(repo.ListBlockVersions(ctx, p.WorkspaceID, p.ID, b.ID, 10)).NoError(t)
		gt.A(t, vs).Length(2)
		gt.Equal(t, vs[0].Content, "v2")
		gt.Equal(t, vs[1].Content, "v1")
		gt.A(t, gt.R1(repo.ListBlockVersions(ctx, p.WorkspaceID, p.ID, b.ID, 1)).NoError(t)).Length(1)

		_, err = repo.GetBlockVersion(ctx, p.WorkspaceID, p.ID, b.ID, 3)
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})

	runRepositoryTest(t, "sibling queries see staged writes", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		a, c, e := newBlock(p, "a", "a"), newBlock(p, "c", "c"), newBlock(p, "e", "e")
		cell := newBlock(p, "V", "cell")
		cell.Type, cell.ParentID = model.BlockTableCell, a.ID
		gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error {
			for _, b := range []*model.Block{a, c, e, cell} {
				if err := tx.PutBlock(b); err != nil {
					return err
				}
			}
			return nil
		}))

		gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error {
			first := gt.R1(tx.FirstAfter("", "")).NoError(t)
			gt.Equal(t, first.ID, a.ID)
			gt.Equal(t, gt.R1(tx.FirstAfter("", "a")).NoError(t).ID, c.ID)
			gt.Equal(t, gt.R1(tx.Last("")).NoError(t).ID, e.ID)
			gt.Nil(t, gt.R1(tx.FirstAfter("", "e")).NoError(t))
			gt.Equal(t, gt.R1(tx.Last(a.ID)).NoError(t).ID, cell.ID)
			gt.Nil(t, gt.R1(tx.Last(c.ID)).NoError(t))

			// stage: delete c, insert d, move e before a
			gt.NoError(t, tx.DeleteBlock(c.ID))
			d := newBlock(p, "d", "d")
			gt.NoError(t, tx.PutBlock(d))
			e2 := e.Clone()
			e2.Order, e2.Version = "0V", 2
			gt.NoError(t, tx.PutBlock(e2))

			gt.Equal(t, gt.R1(tx.FirstAfter("", "a")).NoError(t).ID, d.ID)
			gt.Equal(t, gt.R1(tx.FirstAfter("", "")).NoError(t).ID, e.ID)
			gt.Equal(t, gt.R1(tx.Last("")).NoError(t).ID, d.ID)
			children := gt.R1(tx.Children("")).NoError(t)
			gt.A(t, children).Length(3)
			gt.Equal(t, children[0].ID, e.ID)
			gt.Equal(t, children[2].ID, d.ID)
			return nil
		}))
	})

	runRepositoryTest(t, "delete block in transaction", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		b := newBlock(p, "V", "x")
		gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error {
			return tx.PutBlock(b)
		}))
		gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error {
			if err := tx.DeleteBlock(b.ID); err != nil {
				return err
			}
			_, err := tx.GetBlock(b.ID)
			gt.True(t, errors.Is(err, model.ErrNotFound))
			return nil
		}))
		blocks := gt.R1(repo.ListBlocks(ctx, p.WorkspaceID, p.ID)).NoError(t)
		gt.A(t, blocks).Length(0)
	})

	runRepositoryTest(t, "block of another paper rejected", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		other := newPaper(t, repo)
		err := repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error {
			return tx.PutBlock(newBlock(other, "V", "x"))
		})
		gt.Error(t, err)
	})

	runRepositoryTest(t, "list commits newest first with cursor", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		base := time.Now().UTC()
		var ids []model.CommitID
		for i := 0; i < 5; i++ {
			b := newBlock(p, "V", "x")
			c := commitFor(p, base.Add(time.Duration(i)*time.Second), model.Change{Kind: model.ChangeInsert, BlockID: b.ID, ToVersion: 1})
			ids = append(ids, c.ID)
			gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error { return tx.AddCommit(c) }))
		}
		page := gt.R1(repo.ListCommits(ctx, p.WorkspaceID, p.ID, "", 2)).NoError(t)
		gt.A(t, page).Length(2)
		gt.Equal(t, page[0].ID, ids[4])
		gt.Equal(t, page[1].ID, ids[3])
		next := gt.R1(repo.ListCommits(ctx, p.WorkspaceID, p.ID, page[1].ID, 10)).NoError(t)
		gt.A(t, next).Length(3)
		gt.Equal(t, next[2].ID, ids[0])

		_, err := repo.ListCommits(ctx, p.WorkspaceID, p.ID, model.NewCommitID(), 10)
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})

	runRepositoryTest(t, "update access", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		later := time.Now().UTC().Add(time.Minute)
		by := humanActor(model.NewUserID())
		access := model.PaperAccess{Restricted: false}
		got := gt.R1(repo.UpdatePaperAccess(ctx, p.WorkspaceID, p.ID, access, by, later)).NoError(t)
		gt.Equal(t, got.Access.Restricted, false)
		stored := gt.R1(repo.GetPaper(ctx, p.WorkspaceID, p.ID)).NoError(t)
		gt.Equal(t, stored.Access.Restricted, false)
		gt.Equal(t, stored.UpdatedBy, by)
		gt.True(t, timeEq(stored.UpdatedAt, later))
	})

	runRepositoryTest(t, "delete paper", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		gt.NoError(t, repo.DeletePaper(ctx, p.WorkspaceID, p.ID))
		_, err := repo.GetPaper(ctx, p.WorkspaceID, p.ID)
		gt.True(t, errors.Is(err, model.ErrNotFound))
		gt.True(t, errors.Is(repo.DeletePaper(ctx, p.WorkspaceID, p.ID), model.ErrNotFound))
	})
}

func TestLockRepository(t *testing.T) {
	runRepositoryTest(t, "acquire, conflict, renew, release", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		now := time.Now().UTC()
		blockID := model.NewBlockID()
		lock := func(holder string, at time.Time) *model.BlockLock {
			return &model.BlockLock{
				WorkspaceID: p.WorkspaceID, PaperID: p.ID, BlockID: blockID,
				HolderKey: holder, Holder: humanActor(model.UserID(holder)),
				AcquiredAt: at, ExpiresAt: at.Add(30 * time.Second),
			}
		}

		gt.R1(repo.AcquireLock(ctx, lock("alice", now), now)).NoError(t)

		cur, err := repo.AcquireLock(ctx, lock("bob", now), now)
		gt.True(t, errors.Is(err, model.ErrBlockLocked))
		gt.Equal(t, cur.HolderKey, "alice")

		// renewing one's own lock succeeds
		gt.R1(repo.AcquireLock(ctx, lock("alice", now.Add(10*time.Second)), now.Add(10*time.Second))).NoError(t)

		locks := gt.R1(repo.ListLocks(ctx, p.WorkspaceID, p.ID, now)).NoError(t)
		gt.A(t, locks).Length(1)
		gt.Equal(t, locks[0].Holder, humanActor("alice"))

		released := gt.R1(repo.ReleaseLock(ctx, p.WorkspaceID, p.ID, blockID, "bob")).NoError(t)
		gt.False(t, released)
		released = gt.R1(repo.ReleaseLock(ctx, p.WorkspaceID, p.ID, blockID, "alice")).NoError(t)
		gt.True(t, released)

		gt.R1(repo.AcquireLock(ctx, lock("bob", now), now)).NoError(t)
	})

	runRepositoryTest(t, "expired lock can be taken over", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		now := time.Now().UTC()
		blockID := model.NewBlockID()
		old := &model.BlockLock{WorkspaceID: p.WorkspaceID, PaperID: p.ID, BlockID: blockID, HolderKey: "a", Holder: humanActor("a"), AcquiredAt: now, ExpiresAt: now.Add(time.Second)}
		gt.R1(repo.AcquireLock(ctx, old, now)).NoError(t)

		later := now.Add(2 * time.Second)
		gt.A(t, gt.R1(repo.ListLocks(ctx, p.WorkspaceID, p.ID, later)).NoError(t)).Length(0)

		newer := &model.BlockLock{WorkspaceID: p.WorkspaceID, PaperID: p.ID, BlockID: blockID, HolderKey: "b", Holder: humanActor("b"), AcquiredAt: later, ExpiresAt: later.Add(time.Second)}
		gt.R1(repo.AcquireLock(ctx, newer, later)).NoError(t)
	})

	runRepositoryTest(t, "lock on missing paper", func(t *testing.T, repo interfaces.Repository) {
		now := time.Now()
		l := &model.BlockLock{WorkspaceID: "w", PaperID: model.NewPaperID(), BlockID: "b", HolderKey: "a", Holder: humanActor("a"), AcquiredAt: now, ExpiresAt: now.Add(time.Second)}
		_, err := repo.AcquireLock(context.Background(), l, now)
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})
}
