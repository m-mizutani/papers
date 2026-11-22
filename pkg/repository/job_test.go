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

func jobIn(ws model.WorkspaceID, paper model.PaperID, lease time.Time) *model.Job {
	now := time.Now().UTC().Truncate(time.Millisecond)
	after := "a1"
	return &model.Job{
		ID: model.NewJobID(), WorkspaceID: ws, Kind: model.JobMove, PaperID: paper, BlockID: model.NewBlockID(),
		RootDone: true, With: model.NewPaperID(),
		Target: &model.JobTarget{WorkspaceID: "other", ParentID: model.NewPaperID(), After: &after},
		State:  model.JobRunning, Phase: model.PhaseCopy, Done: 3, Total: 9, Foreign: true,
		Result: &model.JobResult{Trashed: true, Skipped: []model.PaperID{model.NewPaperID()}, CopyID: model.NewPaperID()},
		Error:  "boom", ErrorCode: "internal", Step: 7, Attempts: 2, RetryAt: now.Add(time.Second),
		Runner: "runner-1", LeaseUntil: lease,
		CreatedBy: humanActor(model.NewUserID()), CreatedAt: now, UpdatedAt: now, ExpireAt: now.Add(model.JobMaxAge),
	}
}

func TestJobs(t *testing.T) {
	runRepositoryTest(t, "round trip and runnable", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		home, child := homeAndChild(t, repo)
		ws := home.WorkspaceID
		now := time.Now().UTC()

		stale := jobIn(ws, child.ID, now.Add(-time.Minute))
		leased := jobIn(ws, child.ID, now.Add(time.Minute))
		done := jobIn(ws, child.ID, now.Add(-time.Minute))
		done.State = model.JobDone
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			for _, j := range []*model.Job{stale, leased, done} {
				if err := tx.PutJob(j); err != nil {
					return err
				}
			}
			// the transaction reads its own write
			got, err := tx.GetJob(stale.ID)
			if err != nil {
				return err
			}
			gt.Equal(t, got.Step, stale.Step)
			return nil
		}))

		got := gt.R1(repo.GetJob(ctx, ws, stale.ID)).NoError(t)
		gt.Equal(t, got.Kind, stale.Kind)
		gt.Equal(t, got.PaperID, stale.PaperID)
		gt.Equal(t, got.BlockID, stale.BlockID)
		gt.True(t, got.RootDone)
		gt.Equal(t, got.With, stale.With)
		gt.Equal(t, *got.Target.After, "a1")
		gt.Equal(t, got.Target.ParentID, stale.Target.ParentID)
		gt.Equal(t, got.State, model.JobRunning)
		gt.Equal(t, got.Phase, model.PhaseCopy)
		gt.Equal(t, got.Done, 3)
		gt.Equal(t, got.Total, 9)
		gt.True(t, got.Foreign)
		gt.Equal(t, got.Result.Skipped, stale.Result.Skipped)
		gt.Equal(t, got.Result.CopyID, stale.Result.CopyID)
		gt.Equal(t, got.Error, "boom")
		gt.Equal(t, got.ErrorCode, "internal")
		gt.Equal(t, got.Step, int64(7))
		gt.Equal(t, got.Attempts, 2)
		gt.True(t, timeEq(got.RetryAt, stale.RetryAt))
		gt.Equal(t, got.Runner, "runner-1")
		gt.True(t, timeEq(got.LeaseUntil, stale.LeaseUntil))
		gt.True(t, timeEq(got.ExpireAt, stale.ExpireAt))
		gt.Equal(t, got.CreatedBy, stale.CreatedBy)

		runnable := gt.R1(repo.ListRunnableJobs(ctx, ws, now, 10)).NoError(t)
		gt.A(t, runnable).Length(1)
		gt.Equal(t, runnable[0].ID, stale.ID)

		_, err := repo.GetJob(ctx, ws, model.NewJobID())
		gt.True(t, errors.Is(err, model.ErrNotFound))
		_, err = repo.GetJob(ctx, newWorkspaceID(), stale.ID)
		gt.True(t, errors.Is(err, model.ErrNotFound))
		// an invalid job is refused
		bad := jobIn(ws, child.ID, now)
		bad.Target = nil
		gt.Error(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutJob(bad) }))
	})

	runRepositoryTest(t, "items", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		home, child := homeAndChild(t, repo)
		ws := home.WorkspaceID
		j := jobIn(ws, child.ID, time.Now().UTC())
		now := time.Now().UTC().Truncate(time.Millisecond)
		root := &model.JobItem{JobID: j.ID, WorkspaceID: ws, PaperID: child.ID, ParentID: home.ID, Expanded: true, Stage: model.StageCopied, CopyID: model.NewPaperID(), Cursor: 4, ExpireAt: now}
		a := &model.JobItem{JobID: j.ID, WorkspaceID: ws, PaperID: model.NewPaperID(), ParentID: child.ID, Depth: 1, ExpireAt: now}
		b := &model.JobItem{JobID: j.ID, WorkspaceID: ws, PaperID: model.NewPaperID(), ParentID: a.PaperID, Depth: 2, ExpireAt: now}
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			if err := tx.PutJob(j); err != nil {
				return err
			}
			for _, it := range []*model.JobItem{b, root, a} {
				if err := tx.PutJobItem(it); err != nil {
					return err
				}
			}
			got, err := tx.GetJobItems(j.ID, []model.PaperID{a.PaperID})
			if err != nil {
				return err
			}
			gt.A(t, got).Length(1)
			return nil
		}))

		got := gt.R1(repo.GetJobItems(ctx, ws, j.ID, []model.PaperID{root.PaperID, model.NewPaperID()})).NoError(t)
		gt.A(t, got).Length(1)
		gt.Equal(t, got[0].ParentID, home.ID)
		gt.True(t, got[0].Expanded)
		gt.Equal(t, got[0].Stage, model.StageCopied)
		gt.Equal(t, got[0].CopyID, root.CopyID)
		gt.Equal(t, got[0].Cursor, 4)
		gt.True(t, timeEq(got[0].ExpireAt, now))

		open := gt.R1(repo.ListUnexpandedJobItems(ctx, ws, j.ID, 10)).NoError(t)
		gt.A(t, open).Length(2)
		gt.Equal(t, open[0].PaperID, a.PaperID) // shallowest first
		gt.Equal(t, open[1].PaperID, b.PaperID)
		first := gt.R1(repo.ListUnexpandedJobItems(ctx, ws, j.ID, 1)).NoError(t)
		gt.A(t, first).Length(1)

		at := gt.R1(repo.ListJobItemsAt(ctx, ws, j.ID, model.StageCollected, 10)).NoError(t)
		gt.A(t, at).Length(2)
		copied := gt.R1(repo.ListJobItemsAt(ctx, ws, j.ID, model.StageCopied, 10)).NoError(t)
		gt.A(t, copied).Length(1)
		gt.Equal(t, copied[0].PaperID, child.ID)
		// items of another job are separate
		none := gt.R1(repo.ListJobItemsAt(ctx, ws, model.NewJobID(), model.StageCollected, 10)).NoError(t)
		gt.A(t, none).Length(0)
	})

	runRepositoryTest(t, "children of many parents", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		home, child := homeAndChild(t, repo)
		ws := home.WorkspaceID
		g1 := paperIn(ws, child.ID, "g1")
		g2 := paperIn(ws, child.ID, "g2")
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			if err := tx.PutPaper(g1); err != nil {
				return err
			}
			return tx.PutPaper(g2)
		}))
		got := gt.R1(repo.ListChildrenOf(ctx, ws, []model.PaperID{home.ID, child.ID, model.NewPaperID()})).NoError(t)
		gt.A(t, got).Length(3)
		none := gt.R1(repo.ListChildrenOf(ctx, ws, nil)).NoError(t)
		gt.A(t, none).Length(0)
	})

	runRepositoryTest(t, "paper job mark round trips", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		home, child := homeAndChild(t, repo)
		child.JobID = model.NewJobID()
		gt.NoError(t, repo.RunTx(ctx, home.WorkspaceID, func(tx interfaces.Tx) error { return tx.PutPaper(child) }))
		got := gt.R1(repo.GetPaper(ctx, home.WorkspaceID, child.ID)).NoError(t)
		gt.Equal(t, got.JobID, child.JobID)
		gt.True(t, got.IsBusy())
	})
}
