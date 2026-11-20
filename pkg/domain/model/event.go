package model

// EventType is the kind of a realtime event delivered to paper subscribers.
type EventType string

const (
	EventCommit  EventType = "commit"
	EventLock    EventType = "lock"
	EventUnlock  EventType = "unlock"
	EventDeleted EventType = "paper_deleted"
	EventAccess  EventType = "access_changed"
)

// Event is published to every subscriber of a paper, across instances.
type Event struct {
	Type        EventType   `json:"type"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	PaperID     PaperID     `json:"paper_id"`
	Commit      *Commit     `json:"commit,omitempty"`
	Lock        *BlockLock  `json:"lock,omitempty"`
	// Unlock events identify the released lock.
	BlockID   BlockID `json:"block_id,omitempty"`
	HolderKey string  `json:"holder_key,omitempty"`
}
