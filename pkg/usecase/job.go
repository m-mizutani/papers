package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/utils/async"
	"github.com/m-mizutani/papers/pkg/utils/errutil"
	"github.com/m-mizutani/papers/pkg/utils/logging"
)

// Background jobs (docs/model.html "裏の処理", docs/design.html "#jobs").
// Work proportional to the size of a subtree runs as a job: a sequence of
// small transactions whose progress is stored with each step, so that any
// instance can continue it. The request that makes a job runs it for a
// short while itself; most jobs finish there and the caller sees a normal
// result.

var (
	// errStaleStep: the job moved on (another runner, or a step of the same
	// runner) since this step read it. The step's own writes are discarded;
	// the runner reads the job again.
	errStaleStep = goerr.New("the job changed while the step ran")
)

const (
	// jobMaxAttempts bounds retries of a step that keeps failing with an
	// error that may be temporary.
	jobMaxAttempts = 20
	jobFirstBackoff = 2 * time.Second
	jobMaxBackoff   = 5 * time.Minute
)

// newJob makes a queued job leased to this instance.
func (u *UseCases) newJob(p *model.Principal, ws model.WorkspaceID, kind model.JobKind, paper model.PaperID, now time.Time) *model.Job {
	return &model.Job{
		ID: model.NewJobID(), WorkspaceID: ws, Kind: kind, PaperID: paper,
		State: model.JobQueued, Phase: model.PhaseCollect,
		Runner: u.runner, LeaseUntil: now.Add(u.cfg.JobLease),
		CreatedBy: p.Actor(), CreatedAt: now, UpdatedAt: now, ExpireAt: now.Add(model.JobMaxAge),
	}
}

// startJob stores a new job and runs it inline.
func (u *UseCases) startJob(ctx context.Context, j *model.Job) (*model.Job, error) {
	if err := u.repo.RunTx(ctx, j.WorkspaceID, func(tx interfaces.Tx) error { return tx.PutJob(j) }); err != nil {
		return nil, goerr.Wrap(err, "failed to store job", goerr.V("kind", j.Kind))
	}
	out := u.runInline(ctx, []*model.Job{j})
	return out[0], nil
}

// runInline runs stored jobs for up to the inline budget. Jobs that do not
// finish continue in the background on this instance (unless background
// running is off, in which case the sweeper picks them up once the lease
// ends).
func (u *UseCases) runInline(ctx context.Context, jobs []*model.Job) []*model.Job {
	deadline := u.now().Add(u.cfg.JobInline)
	out := make([]*model.Job, len(jobs))
	for i, j := range jobs {
		got, err := u.run(ctx, j.WorkspaceID, j.ID, deadline)
		if err != nil {
			errutil.Handle(ctx, err, "failed to run job")
			got = j
		}
		out[i] = got
		if !got.State.Finished() && u.background {
			ws, id := got.WorkspaceID, got.ID
			async.Dispatch(ctx, func(ctx context.Context) error {
				_, err := u.run(ctx, ws, id, time.Time{})
				return err
			})
		}
	}
	return out
}

// RunJobs takes over unfinished jobs whose lease has ended (their instance
// stopped) and runs them. The server calls it periodically. It returns how
// many jobs it took.
func (u *UseCases) RunJobs(ctx context.Context) (int, error) {
	n := 0
	for _, ws := range u.workspaces.List() {
		jobs, err := u.repo.ListRunnableJobs(ctx, ws.ID, u.now(), 20)
		if err != nil {
			return n, goerr.Wrap(err, "failed to list runnable jobs", goerr.V("workspace_id", ws.ID))
		}
		for _, j := range jobs {
			ok, err := u.claim(ctx, j)
			if err != nil {
				return n, err
			}
			if !ok {
				continue
			}
			n++
			wsID, id := j.WorkspaceID, j.ID
			if u.background {
				async.Dispatch(ctx, func(ctx context.Context) error {
					_, err := u.run(ctx, wsID, id, time.Time{})
					return err
				})
			} else if _, err := u.run(ctx, wsID, id, time.Time{}); err != nil {
				return n, err
			}
		}
	}
	return n, nil
}

// claim leases an unfinished job whose lease ended to this instance.
func (u *UseCases) claim(ctx context.Context, j *model.Job) (bool, error) {
	claimed := false
	err := u.repo.RunTx(ctx, j.WorkspaceID, func(tx interfaces.Tx) error {
		claimed = false
		cur, err := tx.GetJob(j.ID)
		if err != nil {
			return goerr.Wrap(err, "failed to get job", goerr.V("job_id", j.ID))
		}
		now := u.now()
		if cur.State.Finished() || cur.LeaseUntil.After(now) {
			return nil
		}
		cur.Runner, cur.LeaseUntil, cur.UpdatedAt = u.runner, now.Add(u.cfg.JobLease), now
		cur.Step++
		claimed = true
		return tx.PutJob(cur)
	})
	if err != nil {
		return false, goerr.Wrap(err, "failed to claim job", goerr.V("job_id", j.ID))
	}
	return claimed, nil
}

// run advances the job one step at a time while this instance holds it,
// until it finishes or the deadline passes (zero: no deadline). It returns
// the job as last read.
func (u *UseCases) run(ctx context.Context, ws model.WorkspaceID, id model.JobID, deadline time.Time) (*model.Job, error) {
	for {
		j, err := u.repo.GetJob(ctx, ws, id)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to get job", goerr.V("job_id", id))
		}
		if j.State.Finished() || j.Runner != u.runner {
			return j, nil
		}
		now := u.now()
		if !deadline.IsZero() && !now.Before(deadline) {
			return j, nil
		}
		if j.RetryAt.After(now) {
			if !deadline.IsZero() {
				return j, nil
			}
			t := time.NewTimer(j.RetryAt.Sub(now))
			select {
			case <-ctx.Done():
				t.Stop()
				return j, nil
			case <-t.C:
			}
			continue
		}
		err = u.advance(ctx, j)
		if err == nil || errors.Is(err, errStaleStep) {
			continue
		}
		if ferr := u.recordFailure(ctx, j, err); ferr != nil && !errors.Is(ferr, errStaleStep) {
			return nil, ferr
		}
	}
}

// advance runs the next step of the job.
func (u *UseCases) advance(ctx context.Context, j *model.Job) error {
	if j.Kind == model.JobMove {
		return u.advanceMove(ctx, j)
	}
	switch j.Phase {
	case model.PhaseCollect:
		return u.collect(ctx, j)
	case model.PhaseApply:
		return u.applyTree(ctx, j)
	}
	return goerr.New("unknown job phase", goerr.V("phase", j.Phase), goerr.V("kind", j.Kind))
}

// stepCtx is what a step's transaction works with.
type stepCtx struct {
	tx  interfaces.Tx
	job *model.Job
	w   *treeWriter
	now time.Time
}

// jobPrincipal acts for the job's starter.
func jobPrincipal(j *model.Job) *model.Principal {
	return &model.Principal{User: &model.User{ID: j.CreatedBy.UserID, Name: j.CreatedBy.Name}}
}

// commitStep runs fn in a transaction over the job's workspace. It checks
// that the job is still at the step read by the caller and held by this
// instance, then stores the job with the step counted and the lease
// renewed, and the tree writes fn made.
func (u *UseCases) commitStep(ctx context.Context, j *model.Job, fn func(c *stepCtx) error) error {
	var stored *model.Job
	err := u.repo.RunTx(ctx, j.WorkspaceID, func(tx interfaces.Tx) error {
		cur, err := tx.GetJob(j.ID)
		if err != nil {
			return goerr.Wrap(err, "failed to get job", goerr.V("job_id", j.ID))
		}
		if cur.Runner != u.runner || cur.Step != j.Step || cur.State.Finished() {
			return errStaleStep
		}
		now := u.now()
		c := &stepCtx{tx: tx, job: cur, now: now, w: u.newTreeWriter(ctx, tx, jobPrincipal(cur), now)}
		c.w.actor = cur.CreatedBy
		if err := fn(c); err != nil {
			return err
		}
		if err := c.w.flush(); err != nil {
			return err
		}
		cur.Step++
		cur.Attempts, cur.RetryAt = 0, time.Time{}
		if cur.State == model.JobQueued {
			cur.State = model.JobRunning
		}
		cur.LeaseUntil, cur.UpdatedAt = now.Add(u.cfg.JobLease), now
		if err := tx.PutJob(cur); err != nil {
			return goerr.Wrap(err, "failed to store job")
		}
		stored = cur
		return nil
	})
	if err != nil {
		return err
	}
	*j = *stored
	return nil
}

// finishJob marks the job done (in a step).
func finishJob(j *model.Job, now time.Time) {
	j.State, j.FinishedAt, j.ExpireAt = model.JobDone, &now, now.Add(model.JobRetention)
	if j.Result == nil {
		j.Result = &model.JobResult{}
	}
}

// permanent reports whether err is the job's fault rather than the
// system's: retrying will not help.
func permanent(err error) bool {
	for _, s := range []error{model.ErrInvalidArgument, model.ErrPermissionDenied, model.ErrNotFound, model.ErrBusy, model.ErrConflict} {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

// errorCode names the kind of a job's failure, as the HTTP layer does.
func errorCode(err error) string {
	switch {
	case errors.Is(err, model.ErrPermissionDenied):
		return "permission_denied"
	case errors.Is(err, model.ErrNotFound):
		return "not_found"
	case errors.Is(err, model.ErrBusy):
		return "busy"
	case errors.Is(err, model.ErrConflict):
		return "conflict"
	case errors.Is(err, model.ErrInvalidArgument):
		return "invalid_argument"
	}
	return "internal"
}

// JobError turns a failed job into the error its request would have
// returned.
func JobError(j *model.Job) error {
	sentinel := map[string]error{
		"permission_denied": model.ErrPermissionDenied, "not_found": model.ErrNotFound,
		"busy": model.ErrBusy, "conflict": model.ErrConflict, "invalid_argument": model.ErrInvalidArgument,
	}[j.ErrorCode]
	if sentinel == nil {
		return goerr.New(j.Error, goerr.V("job_id", j.ID))
	}
	return goerr.Wrap(sentinel, j.Error, goerr.V("job_id", j.ID))
}

// recordFailure stores a failed step: a temporary error schedules a retry
// with backoff; a permanent one (or too many retries) fails the job, or
// starts rolling back a move that has not been attached yet.
func (u *UseCases) recordFailure(ctx context.Context, j *model.Job, cause error) error {
	final := permanent(cause) || j.Attempts+1 >= jobMaxAttempts
	if final {
		logging.From(ctx).Info("background job step failed", slog.String("job_id", string(j.ID)), slog.String("kind", string(j.Kind)), logging.ErrAttr(cause))
	} else {
		errutil.Handle(ctx, goerr.Wrap(cause, "background job step failed; retrying", goerr.V("job_id", j.ID), goerr.V("attempts", j.Attempts+1)), "background job step failed")
	}
	return u.repo.RunTx(ctx, j.WorkspaceID, func(tx interfaces.Tx) error {
		cur, err := tx.GetJob(j.ID)
		if err != nil {
			return goerr.Wrap(err, "failed to get job", goerr.V("job_id", j.ID))
		}
		if cur.Runner != u.runner || cur.Step != j.Step || cur.State.Finished() {
			return errStaleStep
		}
		now := u.now()
		cur.UpdatedAt = now
		cur.Step++
		switch {
		case !final:
			cur.Attempts++
			backoff := jobFirstBackoff << min(cur.Attempts-1, 10)
			cur.RetryAt = now.Add(min(backoff, jobMaxBackoff))
			cur.LeaseUntil = cur.RetryAt.Add(u.cfg.JobLease)
		case cur.Kind == model.JobMove && cur.Phase != model.PhaseRetire && cur.Phase != model.PhaseRollback:
			cur.Error, cur.ErrorCode = cause.Error(), errorCode(cause)
			cur.Phase, cur.Attempts, cur.RetryAt = model.PhaseRollback, 0, time.Time{}
			cur.LeaseUntil = now.Add(u.cfg.JobLease)
		default:
			if cur.Error == "" {
				cur.Error, cur.ErrorCode = cause.Error(), errorCode(cause)
			}
			cur.State, cur.FinishedAt, cur.ExpireAt = model.JobFailed, &now, now.Add(model.JobRetention)
		}
		return tx.PutJob(cur)
	})
}

// GetJob returns a job to the user who started it.
func (u *UseCases) GetJob(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.JobID) (*model.Job, error) {
	if _, err := u.workspaceFor(ctx, p, ws); err != nil {
		return nil, err
	}
	if !model.ValidID(string(id)) {
		return nil, notFound("job", id)
	}
	j, err := u.repo.GetJob(ctx, ws, id)
	if errors.Is(err, model.ErrNotFound) {
		return nil, notFound("job", id)
	}
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get job", goerr.V("job_id", id))
	}
	if j.CreatedBy.UserID != p.User.ID {
		return nil, notFound("job", id)
	}
	return j, nil
}
