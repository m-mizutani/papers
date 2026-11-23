package model

import (
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// Version is a copy of a whole paper that a writer saved on purpose. Papers
// keep only their current state and these versions; nothing records the
// changes in between.
type Version struct {
	WorkspaceID WorkspaceID    `json:"workspace_id"`
	PaperID     PaperID        `json:"paper_id"`
	Number      int            `json:"number"`
	Title       string         `json:"title"`
	Blocks      []VersionBlock `json:"blocks"`
	// Note says what changed in this version. Agents write one through
	// save_version; people's saves leave it empty for now. It is kept so
	// that people can be given a way to write one later.
	Note      string    `json:"note,omitempty"`
	CreatedBy Actor     `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// VersionBlock places one block in a version. Its contents are the
// BlockSnapshot with the same block ID and rev.
type VersionBlock struct {
	BlockID  BlockID `json:"block_id"`
	Rev      int64   `json:"rev"`
	ParentID BlockID `json:"parent_id,omitempty"`
	Order    string  `json:"order"`
}

// SnapshotRef names a BlockSnapshot.
type SnapshotRef struct {
	BlockID BlockID `json:"block_id"`
	Rev     int64   `json:"rev"`
}

func (v VersionBlock) Ref() SnapshotRef { return SnapshotRef{BlockID: v.BlockID, Rev: v.Rev} }

// BlockSnapshot is the immutable contents of a block at a rev. Saving a
// version copies only the blocks whose rev changed since the previous one.
type BlockSnapshot struct {
	BlockID BlockID    `json:"block_id"`
	Rev     int64      `json:"rev"`
	Type    BlockType  `json:"type"`
	Content string     `json:"content"`
	Props   BlockProps `json:"props"`
}

// SnapshotOf copies a block's contents.
func SnapshotOf(b *Block) *BlockSnapshot {
	c := b.Clone()
	return &BlockSnapshot{BlockID: b.ID, Rev: b.Rev, Type: c.Type, Content: c.Content, Props: c.Props}
}

// MaxVersionNoteLength caps the note of a version.
const MaxVersionNoteLength = 2000

func (v *Version) Validate() error {
	if v.PaperID == "" || v.WorkspaceID == "" {
		return goerr.Wrap(ErrInvalidArgument, "version needs workspace and paper")
	}
	if v.Number < 1 {
		return goerr.Wrap(ErrInvalidArgument, "version number must be positive", goerr.V("number", v.Number))
	}
	if len(v.Note) > MaxVersionNoteLength {
		return goerr.Wrap(ErrInvalidArgument, "version note is too long")
	}
	return v.CreatedBy.Validate()
}
