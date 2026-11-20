package model

import (
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// ChangeKind is the kind of one recorded change in a commit.
type ChangeKind string

const (
	ChangeInsert   ChangeKind = "insert"
	ChangeUpdate   ChangeKind = "update"
	ChangeDelete   ChangeKind = "delete"
	ChangeMove     ChangeKind = "move"
	ChangeSetTitle ChangeKind = "set_title"
)

// Change records the effect of one operation. Before / After are full block
// snapshots so the history can be replayed or reverted without the original
// request.
type Change struct {
	Kind    ChangeKind `json:"kind"`
	BlockID BlockID    `json:"block_id,omitempty"`
	Before  *Block     `json:"before,omitempty"`
	After   *Block     `json:"after,omitempty"`
	// OverwroteVersion is set when the writer based its edit on a version
	// older than the one it replaced (last-writer-wins overwrite).
	OverwroteVersion int64  `json:"overwrote_version,omitempty"`
	TitleBefore      string `json:"title_before,omitempty"`
	TitleAfter       string `json:"title_after,omitempty"`
}

// Commit is one atomic set of changes to a paper. The sequence of commits of
// a paper is its full history; Seq equals the paper revision it produced.
type Commit struct {
	ID          CommitID    `json:"id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	PaperID     PaperID     `json:"paper_id"`
	Seq         int64       `json:"seq"`
	Actor       Actor       `json:"actor"`
	Message     string      `json:"message,omitempty"`
	Changes     []Change    `json:"changes"`
	CreatedAt   time.Time   `json:"created_at"`
}

const MaxCommitMessageLength = 1024

func (c *Commit) Validate() error {
	if c.ID == "" || c.WorkspaceID == "" || c.PaperID == "" {
		return goerr.Wrap(ErrInvalidArgument, "commit id, workspace id and paper id are required")
	}
	if c.Seq < 1 {
		return goerr.Wrap(ErrInvalidArgument, "commit seq must be positive")
	}
	if err := c.Actor.Validate(); err != nil {
		return goerr.Wrap(err, "invalid commit actor")
	}
	if len(c.Changes) == 0 {
		return goerr.Wrap(ErrInvalidArgument, "commit has no changes")
	}
	if len(c.Message) > MaxCommitMessageLength {
		return goerr.Wrap(ErrInvalidArgument, "commit message is too long")
	}
	return nil
}
