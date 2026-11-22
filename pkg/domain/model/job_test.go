package model_test

import (
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func TestDeriveID(t *testing.T) {
	a := model.DeriveID("job", "paper", "x")
	gt.True(t, model.ValidID(a))
	gt.Equal(t, model.DeriveID("job", "paper", "x"), a)
	gt.NotEqual(t, model.DeriveID("job", "paper", "y"), a)
	// parts are separated, so shifting text between them changes the ID
	gt.NotEqual(t, model.DeriveID("jobp", "aper", "x"), a)
	gt.True(t, model.ValidID(string(model.NewJobID())))
}

func TestJobValidate(t *testing.T) {
	now := time.Now()
	actor := model.Actor{Kind: model.ActorHuman, UserID: model.NewUserID()}
	good := func() *model.Job {
		return &model.Job{ID: model.NewJobID(), WorkspaceID: "ws", Kind: model.JobTrash, PaperID: model.NewPaperID(), State: model.JobQueued, CreatedBy: actor, CreatedAt: now}
	}
	gt.NoError(t, good().Validate())

	for name, change := range map[string]func(*model.Job){
		"no id":       func(j *model.Job) { j.ID = "" },
		"bad kind":    func(j *model.Job) { j.Kind = "nope" },
		"bad state":   func(j *model.Job) { j.State = "nope" },
		"no paper":    func(j *model.Job) { j.PaperID = "" },
		"move target": func(j *model.Job) { j.Kind = model.JobMove },
		"drop block":  func(j *model.Job) { j.Kind = model.JobDrop },
		"no creator":  func(j *model.Job) { j.CreatedBy = model.Actor{} },
	} {
		t.Run(name, func(t *testing.T) {
			j := good()
			change(j)
			gt.Error(t, j.Validate())
		})
	}

	c := good()
	c.Target = &model.JobTarget{WorkspaceID: "b", ParentID: model.NewPaperID()}
	c.Result = &model.JobResult{Skipped: []model.PaperID{model.NewPaperID()}}
	cl := c.Clone()
	cl.Target.WorkspaceID = "changed"
	cl.Result.Skipped[0] = ""
	gt.Equal(t, c.Target.WorkspaceID, "b")
	gt.NotEqual(t, c.Result.Skipped[0], "")

	gt.True(t, model.JobDone.Finished())
	gt.True(t, model.JobFailed.Finished())
	gt.False(t, model.JobRunning.Finished())

	it := &model.JobItem{JobID: model.NewJobID(), WorkspaceID: "ws", PaperID: model.NewPaperID()}
	gt.NoError(t, it.Validate())
	it.Depth = -1
	gt.Error(t, it.Validate())
}
