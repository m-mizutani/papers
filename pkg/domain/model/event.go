package model

// EventType is the kind of a realtime event delivered to paper watchers.
// Events describe resulting state (a block's new contents), not the
// requests that produced it, so they can be read straight from a storage
// change feed.
type EventType string

const (
	EventBlockPut     EventType = "block_put"
	EventBlockDeleted EventType = "block_deleted"
	EventPaperUpdated EventType = "paper_updated"
	EventPaperDeleted EventType = "paper_deleted"
)

// Event is delivered to every watcher of a paper, across instances.
type Event struct {
	Type        EventType   `json:"type"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	PaperID     PaperID     `json:"paper_id"`
	Block       *Block      `json:"block,omitempty"`
	Paper       *Paper      `json:"paper,omitempty"`
	// BlockID identifies a deleted block.
	BlockID BlockID `json:"block_id,omitempty"`
}
