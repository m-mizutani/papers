package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
)

// tree builds root with n children, each with one grandchild, all made by p.
func tree(t *testing.T, e *env, p *model.Principal, ws model.WorkspaceID, n int) (*model.Paper, []*model.Paper) {
	t.Helper()
	root := e.child(t, p, ws, "", "root")
	var all []*model.Paper
	for i := range n {
		c := e.child(t, p, ws, string(root.ID), fmt.Sprintf("c%d", i))
		g := e.child(t, p, ws, string(c.ID), fmt.Sprintf("g%d", i))
		all = append(all, c, g)
	}
	return root, all
}

// manual makes an env whose jobs wait for a test to run them, and whose
// steps are small.
func manual(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, func(c *usecase.Config) { c.JobStep = 2 }, usecase.WithoutBackgroundJobs())
}

// started returns the job the last request started in the workspace (the
// only unfinished one besides those given).
func started(t *testing.T, e *env, ws model.WorkspaceID, not ...*model.Job) *model.Job {
	t.Helper()
	jobs := gt.R1(e.repo.ListRunnableJobs(context.Background(), ws, e.clock.Now().Add(time.Hour), 100)).NoError(t)
	var out []*model.Job
	for _, j := range jobs {
		skip := false
		for _, n := range not {
			skip = skip || n.ID == j.ID
		}
		if !skip {
			out = append(out, j)
		}
	}
	gt.A(t, out).Length(1).Required()
	return out[0]
}

func job(t *testing.T, e *env, ws model.WorkspaceID, id model.JobID) *model.Job {
	t.Helper()
	return gt.R1(e.repo.GetJob(context.Background(), ws, id)).NoError(t)
}

func TestTrashLargeSubtreeInSteps(t *testing.T) {
	e := manual(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	root, all := tree(t, e, alice, wsA, 5)

	res := gt.R1(e.uc.Trash(ctx, alice, wsA, root.ID)).NoError(t)
	gt.Equal(t, res.Paper.ID, root.ID)
	j := started(t, e, wsA)
	gt.False(t, e.paper(t, wsA, root.ID).IsTrashed()) // nothing yet

	// the job is leased to the instance that made it
	other := e.another(t)
	gt.Equal(t, gt.R1(other.RunJobs(ctx)).NoError(t), 0)
	// once the lease ends, another instance takes it over and finishes it
	e.clock.Advance(2 * time.Minute)
	gt.Equal(t, gt.R1(other.RunJobs(ctx)).NoError(t), 1)

	got := job(t, e, wsA, j.ID)
	gt.Equal(t, got.State, model.JobDone)
	gt.True(t, got.Result.Trashed)
	gt.Equal(t, got.Total, 11)
	gt.True(t, e.paper(t, wsA, root.ID).IsTrashed())
	for _, q := range all {
		got := e.paper(t, wsA, q.ID)
		gt.True(t, got.IsTrashed())
		gt.Equal(t, got.DeletedWith, root.ID)
	}
	// the root left its parent's body
	for _, b := range e.blocks(t, alice, wsA, e.home(t, wsA)) {
		gt.NotEqual(t, b.Props.PaperID, root.ID)
	}

	// restoring brings everything back, also in steps
	gt.R1(e.uc.RestoreFromTrash(ctx, alice, wsA, root.ID)).NoError(t)
	e.clock.Advance(2 * time.Minute)
	gt.Equal(t, gt.R1(other.RunJobs(ctx)).NoError(t), 1)
	gt.False(t, e.paper(t, wsA, root.ID).IsTrashed())
	for _, q := range all {
		gt.False(t, e.paper(t, wsA, q.ID).IsTrashed())
	}
}

func TestTrashArchivesWhenOthersOwnPart(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	root, all := tree(t, e, alice, wsA, 2)
	other := e.child(t, bob, wsA, string(all[0].ID), "bob's")

	gt.R1(e.uc.Trash(ctx, alice, wsA, root.ID)).NoError(t)
	for _, q := range append(all, root, other) {
		got := e.paper(t, wsA, q.ID)
		gt.False(t, got.IsTrashed())
		gt.True(t, got.IsArchived())
	}
	// the root stays in its parent's body
	gt.A(t, paperBlocks(t, e, alice, wsA, e.home(t, wsA))).Length(1)
}

func TestDropJobArchivesWhenOthersOwnPart(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	parent := e.child(t, alice, wsA, "", "parent")
	kid := e.child(t, alice, wsA, string(parent.ID), "kid")
	e.child(t, bob, wsA, string(kid.ID), "bob's")
	var block *model.Block
	for _, b := range e.blocks(t, alice, wsA, parent.ID) {
		if b.Props.PaperID == kid.ID {
			block = b
		}
	}
	res := e.apply(t, alice, wsA, parent.ID, usecase.Op{Kind: usecase.OpDelete, BlockID: block.ID, BaseRev: block.Rev})
	gt.A(t, res.Deleted).Length(0)
	gt.True(t, e.paper(t, wsA, kid.ID).IsArchived())
	gt.A(t, e.blocks(t, alice, wsA, parent.ID)).Length(1)
}

func TestArchiveSubtreeJob(t *testing.T) {
	e := manual(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	root, all := tree(t, e, alice, wsA, 3)
	gt.R1(e.uc.Archive(ctx, alice, wsA, root.ID, true)).NoError(t)
	j := started(t, e, wsA)
	stepUntil(t, e, wsA, j.ID, func(*model.Job) bool { return false })
	for _, q := range append(all, root) {
		gt.True(t, e.paper(t, wsA, q.ID).IsArchived())
	}
	// without the subtree it stays a single write
	gt.R1(e.uc.Unarchive(ctx, alice, wsA, root.ID, false)).NoError(t)
	gt.False(t, e.paper(t, wsA, root.ID).IsArchived())
	gt.True(t, e.paper(t, wsA, all[0].ID).IsArchived())
}

// stepUntil runs steps of the job until cond holds.
func stepUntil(t *testing.T, e *env, ws model.WorkspaceID, id model.JobID, cond func(*model.Job) bool) *model.Job {
	t.Helper()
	ctx := context.Background()
	for range 1000 {
		j := gt.R1(e.repo.GetJob(ctx, ws, id)).NoError(t)
		if cond(j) || j.State.Finished() {
			return j
		}
		gt.NoError(t, usecase.StepJob(ctx, e.uc, ws, id)).Required()
	}
	t.Fatal("the job did not reach the condition")
	return nil
}

func TestMoveAcrossInStepsKeepsOriginalsReadOnly(t *testing.T) {
	e := manual(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	root, all := tree(t, e, alice, wsA, 2)
	var ops []usecase.Op
	for i := range 5 {
		ops = append(ops, para(fmt.Sprintf("line %d", i)))
	}
	e.apply(t, alice, wsA, all[0].ID, ops...)
	gt.R1(e.uc.SaveVersion(ctx, alice, wsA, all[0].ID, "v1")).NoError(t)
	att := gt.R1(e.uc.Upload(ctx, alice, wsA, all[1].ID, usecase.UploadInput{Name: "x.png", ContentType: "image/png", Body: strings.NewReader("png")})).NoError(t)
	e.apply(t, alice, wsA, all[1].ID, usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockImage), Props: &model.BlockProps{AttachmentID: att.ID}})

	gt.R1(e.uc.MovePaper(ctx, alice, wsA, root.ID, usecase.MoveInput{Workspace: wsB, Parent: string(e.home(t, wsB))})).NoError(t)
	j := started(t, e, wsA)

	// once collected, the originals cannot be written and are not found
	j = stepUntil(t, e, wsA, j.ID, func(j *model.Job) bool { return j.Phase == model.PhaseCopy })
	_, err := e.uc.Apply(ctx, alice, wsA, all[0].ID, usecase.BatchInput{Ops: []usecase.Op{para("late")}})
	gt.True(t, errors.Is(err, model.ErrBusy))
	_, err = e.uc.Trash(ctx, alice, wsA, all[1].ID)
	gt.True(t, errors.Is(err, model.ErrBusy))
	_, err = e.uc.MovePaper(ctx, alice, wsA, all[1].ID, usecase.MoveInput{Parent: string(e.home(t, wsA))})
	gt.True(t, errors.Is(err, model.ErrBusy))
	hits := gt.R1(e.uc.Search(ctx, alice, wsA, usecase.SearchInput{Query: "line"})).NoError(t)
	gt.A(t, hits).Length(0)
	// they can still be read
	gt.R1(e.uc.GetPaper(ctx, alice, wsA, all[0].ID)).NoError(t)

	j = stepUntil(t, e, wsA, j.ID, func(*model.Job) bool { return false })
	gt.Equal(t, j.State, model.JobDone)
	cp := j.Result.CopyID
	gt.NotNil(t, j.Result.Forward)
	gt.Equal(t, j.Result.Forward.ToPaperID, cp)

	// the copy is in the target's tree, with everything under it
	gt.Equal(t, e.paper(t, wsB, cp).ParentID, e.home(t, wsB))
	gt.False(t, e.paper(t, wsB, cp).IsBusy())
	kids := gt.R1(e.uc.ListChildren(ctx, alice, wsB, cp, false)).NoError(t)
	gt.A(t, kids).Length(2)
	var c0 model.PaperID
	for _, k := range kids {
		if k.Title == "c0" {
			c0 = k.ID
		}
	}
	blocks := e.blocks(t, alice, wsB, c0)
	gt.A(t, blocks).Length(6) // the paper block of g0 and five lines
	v1 := gt.R1(e.uc.GetVersion(ctx, alice, wsB, c0, 1)).NoError(t)
	gt.A(t, v1.Blocks).Length(6)
	hits = gt.R1(e.uc.Search(ctx, alice, wsB, usecase.SearchInput{Query: "line"})).NoError(t)
	gt.True(t, len(hits) > 0)

	// the originals are in the trash, not busy, and lead to their copies
	_, err = e.uc.GetPaper(ctx, alice, wsA, root.ID)
	f, moved := usecase.IsMoved(err)
	gt.True(t, moved)
	gt.Equal(t, f.ToPaperID, cp)
	for _, q := range append(all, root) {
		got := e.paper(t, wsA, q.ID)
		gt.True(t, got.IsTrashed())
		gt.False(t, got.IsBusy())
		f := gt.R1(e.repo.GetForward(ctx, wsA, q.ID)).NoError(t)
		gt.Equal(t, f.ToWorkspaceID, wsB)
	}
}

func TestMoveAcrossRollsBack(t *testing.T) {
	e := manual(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	root, all := tree(t, e, alice, wsA, 2)
	target := e.child(t, alice, wsB, "", "target")

	gt.R1(e.uc.MovePaper(ctx, alice, wsA, root.ID, usecase.MoveInput{Workspace: wsB, Parent: string(target.ID)})).NoError(t)
	j := started(t, e, wsA)
	j = stepUntil(t, e, wsA, j.ID, func(j *model.Job) bool { return j.Phase == model.PhaseAttach })
	items := gt.R1(e.repo.ListJobItemsAt(ctx, wsA, j.ID, model.StageCopied, 0)).NoError(t)
	gt.A(t, items).Length(5)
	for _, it := range items {
		gt.True(t, e.paper(t, wsB, it.CopyID).IsBusy()) // hidden copies exist
	}

	// the new parent goes away before the copy is attached
	gt.R1(e.uc.Trash(ctx, alice, wsB, target.ID)).NoError(t)
	trash := started(t, e, wsB)
	stepUntil(t, e, wsB, trash.ID, func(*model.Job) bool { return false })
	gt.True(t, e.paper(t, wsB, target.ID).IsTrashed())
	j = stepUntil(t, e, wsA, j.ID, func(*model.Job) bool { return false })
	gt.Equal(t, j.State, model.JobFailed)
	gt.Equal(t, j.ErrorCode, "invalid_argument")
	for _, it := range items {
		_, err := e.repo.GetPaper(ctx, wsB, it.CopyID)
		gt.True(t, errors.Is(err, model.ErrNotFound))
	}
	for _, q := range append(all, root) {
		got := e.paper(t, wsA, q.ID)
		gt.False(t, got.IsTrashed())
		gt.False(t, got.IsBusy())
	}
	// the originals can be written again
	e.apply(t, alice, wsA, all[0].ID, para("after"))
}

func TestMoveAcrossStopsAtAnotherMove(t *testing.T) {
	e := manual(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	root := e.child(t, alice, wsA, "", "root")
	mid := e.child(t, alice, wsA, string(root.ID), "mid")
	other := e.child(t, bob, wsA, string(mid.ID), "other")

	// bob's move of his paper has marked it
	gt.R1(e.uc.MovePaper(ctx, bob, wsA, other.ID, usecase.MoveInput{Workspace: wsB, Parent: string(e.home(t, wsB))})).NoError(t)
	first := started(t, e, wsA)
	stepUntil(t, e, wsA, first.ID, func(j *model.Job) bool { return j.Phase == model.PhaseCopy })

	// alice's move of the tree above it fails and lets go of her papers
	gt.R1(e.uc.MovePaper(ctx, alice, wsA, root.ID, usecase.MoveInput{Workspace: wsB, Parent: string(e.home(t, wsB))})).NoError(t)
	second := started(t, e, wsA, first)
	got := stepUntil(t, e, wsA, second.ID, func(*model.Job) bool { return false })
	gt.Equal(t, got.State, model.JobFailed)
	gt.Equal(t, got.ErrorCode, "busy")
	gt.False(t, e.paper(t, wsA, root.ID).IsBusy())
	gt.False(t, e.paper(t, wsA, mid.ID).IsBusy())
	gt.Equal(t, e.paper(t, wsA, other.ID).JobID, first.ID)

	// bob's move still finishes
	done := stepUntil(t, e, wsA, first.ID, func(*model.Job) bool { return false })
	gt.Equal(t, done.State, model.JobDone)
}

func TestJobRetriesTemporaryFailures(t *testing.T) {
	e := manual(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	root, all := tree(t, e, alice, wsA, 2)
	gt.R1(e.uc.Trash(ctx, alice, wsA, root.ID)).NoError(t)
	j := started(t, e, wsA)
	gt.NoError(t, usecase.StepJob(ctx, e.uc, wsA, j.ID)) // the root item

	e.count.failChildren = 1
	gt.NoError(t, usecase.StepJob(ctx, e.uc, wsA, j.ID))
	got := gt.R1(e.repo.GetJob(ctx, wsA, j.ID)).NoError(t)
	gt.Equal(t, got.State, model.JobRunning)
	gt.Equal(t, got.Attempts, 1)
	gt.True(t, got.RetryAt.After(e.clock.Now()))
	gt.True(t, got.LeaseUntil.After(got.RetryAt))

	// after the retry time and the lease, the job goes on and finishes
	e.clock.Advance(10 * time.Minute)
	gt.Equal(t, gt.R1(e.another(t).RunJobs(ctx)).NoError(t), 1)
	got = gt.R1(e.repo.GetJob(ctx, wsA, j.ID)).NoError(t)
	gt.Equal(t, got.State, model.JobDone)
	gt.Equal(t, got.Attempts, 0)
	for _, q := range all {
		gt.True(t, e.paper(t, wsA, q.ID).IsTrashed())
	}
}

func TestRestoreVersionBringsBackTrashedChildWithItsSubtree(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	parent := e.child(t, alice, wsA, "", "parent")
	kid := e.child(t, alice, wsA, string(parent.ID), "kid")
	grand := e.child(t, alice, wsA, string(kid.ID), "grand")
	gt.R1(e.uc.SaveVersion(ctx, alice, wsA, parent.ID, "with kid")).NoError(t)

	gt.R1(e.uc.Trash(ctx, alice, wsA, kid.ID)).NoError(t)
	gt.True(t, e.paper(t, wsA, grand.ID).IsTrashed())

	gt.R1(e.uc.RestoreVersion(ctx, alice, wsA, parent.ID, 1)).NoError(t)
	gt.False(t, e.paper(t, wsA, kid.ID).IsTrashed())
	gt.False(t, e.paper(t, wsA, grand.ID).IsTrashed())
}
