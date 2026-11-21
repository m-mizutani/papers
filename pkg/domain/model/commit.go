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
	// ChangeRestore makes an earlier version current again, as a new
	// version (history is never rewritten).
	ChangeRestore ChangeKind = "restore"
)

// Change records the effect of one operation on one block. The block
// contents live in the block's version history: FromVersion is the version
// before the change (0 for an insert) and ToVersion the version after it (0
// for a delete), so a commit stays small however large the blocks are.
type Change struct {
	Kind        ChangeKind `json:"kind"`
	BlockID     BlockID    `json:"block_id,omitempty"`
	FromVersion int64      `json:"from_version,omitempty"`
	ToVersion   int64      `json:"to_version,omitempty"`
	// OverwroteVersion and OverwroteBy are set when the writer forced its
	// edit over contents someone else had changed since the writer's base
	// version: the version that was replaced and who wrote it.
	OverwroteVersion int64  `json:"overwrote_version,omitempty"`
	OverwroteBy      *Actor `json:"overwrote_by,omitempty"`
	// RestoredVersion is the version a restore copied its contents from.
	RestoredVersion int64  `json:"restored_version,omitempty"`
	TitleBefore     string `json:"title_before,omitempty"`
	TitleAfter      string `json:"title_after,omitempty"`
}

// Commit is one atomic set of changes to a paper. Commits of a paper are
// ordered by CreatedAt (ties broken by ID, which is time ordered too).
type Commit struct {
	ID          CommitID    `json:"id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	PaperID     PaperID     `json:"paper_id"`
	Actor       Actor       `json:"actor"`
	Message     string      `json:"message,omitempty"`
	Changes     []Change    `json:"changes"`
	CreatedAt   time.Time   `json:"created_at"`
}

const (
	MaxCommitMessageLength = 1024
	// MaxChangesPerCommit bounds the records one commit writes in a single
	// storage transaction: every block change writes the block, its version
	// snapshot and its search entry.
	MaxChangesPerCommit = 240
	// MaxCommitContentBytes bounds the block content one commit writes. The
	// content is written twice (block and version) next to search entries
	// of up to MaxSearchTextLength each, and the whole transaction must fit
	// in one storage request (10 MiB on Firestore).
	MaxCommitContentBytes = 3 * 1024 * 1024
)

func (c *Commit) Validate() error {
	if c.ID == "" || c.WorkspaceID == "" || c.PaperID == "" {
		return goerr.Wrap(ErrInvalidArgument, "commit id, workspace id and paper id are required")
	}
	if err := c.Actor.Validate(); err != nil {
		return goerr.Wrap(err, "invalid commit actor")
	}
	if len(c.Changes) == 0 {
		return goerr.Wrap(ErrInvalidArgument, "commit has no changes")
	}
	if len(c.Changes) > MaxChangesPerCommit {
		return goerr.Wrap(ErrInvalidArgument, "commit has too many changes", goerr.V("count", len(c.Changes)), goerr.V("max", MaxChangesPerCommit))
	}
	if len(c.Message) > MaxCommitMessageLength {
		return goerr.Wrap(ErrInvalidArgument, "commit message is too long")
	}
	return nil
}
