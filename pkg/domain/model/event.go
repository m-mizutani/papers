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
	// EventItemPut goes to a database's feed when one of its items' paper
	// record changes (title, state); Paper is the item.
	EventItemPut EventType = "item_put"
	// EventItemDeleted goes to a database's feed when an item is purged.
	EventItemDeleted EventType = "item_deleted"
	// EventPropertyValuePut and EventPropertyValueDeleted go to both the
	// database's and the item's feed.
	EventPropertyValuePut     EventType = "property_value_put"
	EventPropertyValueDeleted EventType = "property_value_deleted"
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

	PropertyValue *PropertyValue `json:"property_value,omitempty"`
	// ItemID and PropertyID name what an item_deleted or a
	// property_value_deleted event removed.
	ItemID     PaperID    `json:"item_id,omitempty"`
	PropertyID PropertyID `json:"property_id,omitempty"`
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
