package model

import "time"

// EventType names what an event reports.
type EventType string

const (
	EventBlockPut     EventType = "block_put"
	EventBlockDeleted EventType = "block_deleted"
	EventPaperUpdated EventType = "paper_updated"
	EventPaperDeleted EventType = "paper_deleted"
	EventVersionSaved EventType = "version_saved"
	EventPresence     EventType = "presence"
)

// Event is one entry of a paper's change feed. The feed exists only to
// deliver changes to open screens; entries expire after EventTTL and are not
// history.
type Event struct {
	Type        EventType   `json:"type"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	PaperID     PaperID     `json:"paper_id"`
	At          time.Time   `json:"at"`

	Block    *Block    `json:"block,omitempty"`
	BlockID  BlockID   `json:"block_id,omitempty"`
	Paper    *Paper    `json:"paper,omitempty"`
	Version  int       `json:"version,omitempty"`
	Presence *Presence `json:"presence,omitempty"`
}

// EventTTL is how long the store keeps feed entries.
const EventTTL = 24 * time.Hour

// Presence says that someone is editing a block right now.
type Presence struct {
	BlockID BlockID   `json:"block_id"`
	Actor   Actor     `json:"actor"`
	Until   time.Time `json:"until"`
	// Leaving marks the end of editing before Until.
	Leaving bool `json:"leaving,omitempty"`
}

// PresenceTTL is how long a presence report lasts without a refresh.
const PresenceTTL = 45 * time.Second
