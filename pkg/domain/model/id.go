package model

import (
	"github.com/google/uuid"
)

// ID types are distinct string types so that a paper ID can never be passed
// where a block ID is expected. All generated IDs are UUIDv7: they sort by
// creation time, which keeps listings stable without an extra index.

type (
	UserID      string
	WorkspaceID string
	GroupID     string
	PaperID     string
	BlockID     string
	CommitID    string
	SessionID   string
	TokenID     string
)

func (x UserID) String() string      { return string(x) }
func (x WorkspaceID) String() string { return string(x) }
func (x GroupID) String() string     { return string(x) }
func (x PaperID) String() string     { return string(x) }
func (x BlockID) String() string     { return string(x) }
func (x CommitID) String() string    { return string(x) }
func (x SessionID) String() string   { return string(x) }
func (x TokenID) String() string     { return string(x) }

func newID() string {
	return uuid.Must(uuid.NewV7()).String()
}

func NewUserID() UserID           { return UserID(newID()) }
func NewWorkspaceID() WorkspaceID { return WorkspaceID(newID()) }
func NewGroupID() GroupID         { return GroupID(newID()) }
func NewPaperID() PaperID         { return PaperID(newID()) }
func NewBlockID() BlockID         { return BlockID(newID()) }
func NewCommitID() CommitID       { return CommitID(newID()) }
func NewSessionID() SessionID     { return SessionID(newID()) }
func NewTokenID() TokenID         { return TokenID(newID()) }
