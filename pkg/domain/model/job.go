package model

import (
	"crypto/sha256"
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// JobID identifies a background job.
type JobID string

func (x JobID) String() string { return string(x) }

func NewJobID() JobID { return JobID(newID()) }

// DeriveID makes a UUID v4 shaped ID from the parts. The same parts always
// give the same ID, so a job step that writes copies can be repeated without
// creating duplicates.
func DeriveID(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	b := h.Sum(nil)[:16]
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return encodeBase62(b)
}

// JobKind is what a background job does.
type JobKind string

const (
	// JobTrash sends a paper and everything under it to the trash. It fails
	// without changing anything when a paper under it belongs to someone
	// else.
	JobTrash JobKind = "trash"
	// JobDrop handles a paper block deleted from its parent's body: like
	// trash when everything under it is the caller's, otherwise the child
	// is archived with everything under it and the block stays.
	JobDrop JobKind = "drop"
	// JobRestore brings a paper back from the trash with the papers trashed
	// together with it.
	JobRestore JobKind = "restore"
	// JobArchive and JobUnarchive set or clear the archive mark on a paper
	// and everything under it.
	JobArchive   JobKind = "archive"
	JobUnarchive JobKind = "unarchive"
	// JobMove copies a paper and everything under it into another
	// workspace, then trashes the originals and leaves forwards.
	JobMove JobKind = "move"
)

func (k JobKind) Validate() error {
	switch k {
	case JobTrash, JobDrop, JobRestore, JobArchive, JobUnarchive, JobMove:
		return nil
	}
	return goerr.Wrap(ErrInvalidArgument, "unknown job kind", goerr.V("kind", k))
}

// JobState is where a job is in its life.
type JobState string

const (
	JobQueued  JobState = "queued"
	JobRunning JobState = "running"
	JobDone    JobState = "done"
	JobFailed  JobState = "failed"
)

// Finished reports whether the job will not run again.
func (s JobState) Finished() bool { return s == JobDone || s == JobFailed }

// JobPhase is the step a job is in. Every kind starts by collecting the
// papers it works on.
type JobPhase string

const (
	PhaseCollect JobPhase = "collect"
	PhaseApply   JobPhase = "apply"
	// PhaseCopy, PhaseAttach and PhaseRetire are the steps of a move after
	// collecting; PhaseRollback undoes a move that failed before attach.
	PhaseCopy     JobPhase = "copy"
	PhaseAttach   JobPhase = "attach"
	PhaseRetire   JobPhase = "retire"
	PhaseRollback JobPhase = "rollback"
)

// JobTarget is where a move goes.
type JobTarget struct {
	WorkspaceID WorkspaceID `json:"workspace_id"`
	ParentID    PaperID     `json:"parent_id"`
	After       *string     `json:"after,omitempty"`
}

// JobResult is what a finished job did.
type JobResult struct {
	// Trashed or Archived tells which way a trash or drop job went.
	Trashed  bool `json:"trashed,omitempty"`
	Archived bool `json:"archived,omitempty"`
	// Skipped lists papers the job left alone (someone else's paper found
	// under a paper being trashed after the check, for example).
	Skipped []PaperID `json:"skipped,omitempty"`
	// CopyID and Forward are set by a move.
	CopyID  PaperID  `json:"copy_id,omitempty"`
	Forward *Forward `json:"forward,omitempty"`
}

// Job is a background job: work proportional to the size of a subtree,
// split into small transactions and resumable on any instance.
type Job struct {
	ID          JobID       `json:"id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	Kind        JobKind     `json:"kind"`
	PaperID     PaperID     `json:"paper_id"`
	// BlockID is the paper block a drop job removes.
	BlockID BlockID `json:"block_id,omitempty"`
	// RootDone means the root paper was already handled by the request that
	// made the job (restoring a version brings the child back itself).
	RootDone bool `json:"root_done,omitempty"`
	// With is the DeletedWith of the papers a restore brings back.
	With   PaperID    `json:"-"`
	Target *JobTarget `json:"target,omitempty"`

	State JobState `json:"state"`
	Phase JobPhase `json:"phase"`
	Done  int      `json:"done"`
	Total int      `json:"total"`
	// Foreign is set once collecting found a paper that is not the
	// starter's (trash and drop).
	Foreign bool       `json:"-"`
	Result  *JobResult `json:"result,omitempty"`
	Error   string     `json:"error,omitempty"`
	// ErrorCode classifies Error like the API's error codes.
	ErrorCode string `json:"error_code,omitempty"`

	// Step counts the steps stored; a step commits only if the job is still
	// at the step it read.
	Step int64 `json:"-"`
	// Attempts counts failed tries of the current step; RetryAt is when the
	// next try may start.
	Attempts int       `json:"-"`
	RetryAt  time.Time `json:"-"`
	// Runner and LeaseUntil say which instance runs the job and until when.
	Runner     string    `json:"-"`
	LeaseUntil time.Time `json:"-"`

	CreatedBy  Actor      `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// ExpireAt is when storage drops the job (a TTL field).
	ExpireAt time.Time `json:"-"`
}

// JobRetention is how long a finished job is kept.
const JobRetention = 7 * 24 * time.Hour

// JobMaxAge bounds how long an unfinished job and its items are kept.
const JobMaxAge = 30 * 24 * time.Hour

func (j *Job) Validate() error {
	if !ValidID(string(j.ID)) || j.WorkspaceID == "" || !ValidID(string(j.PaperID)) {
		return goerr.Wrap(ErrInvalidArgument, "job id, workspace and paper are required", goerr.V("id", j.ID))
	}
	if err := j.Kind.Validate(); err != nil {
		return err
	}
	switch j.State {
	case JobQueued, JobRunning, JobDone, JobFailed:
	default:
		return goerr.Wrap(ErrInvalidArgument, "unknown job state", goerr.V("state", j.State))
	}
	if j.Kind == JobMove && (j.Target == nil || j.Target.WorkspaceID == "" || j.Target.ParentID == "") {
		return goerr.Wrap(ErrInvalidArgument, "a move needs a target", goerr.V("id", j.ID))
	}
	if j.Kind == JobDrop && j.BlockID == "" {
		return goerr.Wrap(ErrInvalidArgument, "a drop needs the paper block", goerr.V("id", j.ID))
	}
	if err := j.CreatedBy.Validate(); err != nil {
		return goerr.Wrap(err, "invalid job creator")
	}
	return nil
}

func (j *Job) Clone() *Job {
	c := *j
	if j.Target != nil {
		t := *j.Target
		c.Target = &t
	}
	if j.Result != nil {
		r := *j.Result
		r.Skipped = append([]PaperID(nil), j.Result.Skipped...)
		c.Result = &r
	}
	return &c
}

// JobItem is one paper a job works on.
type JobItem struct {
	JobID       JobID       `json:"job_id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	PaperID     PaperID     `json:"paper_id"`
	ParentID    PaperID     `json:"parent_id,omitempty"`
	Depth       int         `json:"depth"`
	// Expanded: the item's children have been collected.
	Expanded bool `json:"expanded"`
	// Stage counts the steps the job has applied to the paper (the
	// ItemStage constants of its kind).
	Stage int `json:"stage"`
	// CopyID is the paper's copy in a move; Cursor counts the blocks
	// already copied.
	CopyID   PaperID   `json:"copy_id,omitempty"`
	Cursor   int       `json:"cursor,omitempty"`
	ExpireAt time.Time `json:"-"`
}

// Item stages. Every kind except move needs one step per paper.
const (
	StageCollected  = 0
	StageApplied    = 1
	StageCopied     = 1
	StageAttached   = 2
	StageRetired    = 3
	StageRolledBack = 9
)

func (i *JobItem) Validate() error {
	if !ValidID(string(i.JobID)) || i.WorkspaceID == "" || !ValidID(string(i.PaperID)) {
		return goerr.Wrap(ErrInvalidArgument, "job item needs job, workspace and paper")
	}
	if i.Depth < 0 || i.Cursor < 0 || i.Stage < 0 {
		return goerr.Wrap(ErrInvalidArgument, "job item depth and cursor must not be negative")
	}
	return nil
}

func (i *JobItem) Clone() *JobItem {
	c := *i
	return &c
}
