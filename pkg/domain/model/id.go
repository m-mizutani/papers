package model

import (
	"math/big"
	"regexp"

	"github.com/google/uuid"
)

// ID types. Every ID that papers issues is a random UUID v4 written as 22
// base62 characters (0-9, A-Z, a-z), left-padded with "0". Workspace IDs are
// the exception: they are names written in the configuration files.
type (
	UserID       string
	WorkspaceID  string
	PaperID      string
	BlockID      string
	SessionID    string
	TokenID      string
	AttachmentID string
	// LineID identifies a row or a column of a table block.
	LineID string
)

func (x UserID) String() string       { return string(x) }
func (x WorkspaceID) String() string  { return string(x) }
func (x PaperID) String() string      { return string(x) }
func (x BlockID) String() string      { return string(x) }
func (x SessionID) String() string    { return string(x) }
func (x TokenID) String() string      { return string(x) }
func (x AttachmentID) String() string { return string(x) }
func (x LineID) String() string       { return string(x) }

// IDLength is the length of every generated ID.
const IDLength = 22

const base62Digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var base62 = big.NewInt(62)

func encodeBase62(b []byte) string {
	n := new(big.Int).SetBytes(b)
	out := make([]byte, IDLength)
	for i := range out {
		out[i] = '0'
	}
	mod := new(big.Int)
	for i := IDLength - 1; i >= 0 && n.Sign() > 0; i-- {
		n.DivMod(n, base62, mod)
		out[i] = base62Digits[mod.Int64()]
	}
	return string(out)
}

func decodeBase62(s string) ([]byte, bool) {
	if len(s) != IDLength {
		return nil, false
	}
	n := new(big.Int)
	for i := 0; i < len(s); i++ {
		d := indexBase62(s[i])
		if d < 0 {
			return nil, false
		}
		n.Mul(n, base62)
		n.Add(n, big.NewInt(int64(d)))
	}
	if n.BitLen() > 128 {
		return nil, false
	}
	b := n.Bytes()
	out := make([]byte, 16)
	copy(out[16-len(b):], b)
	return out, true
}

func indexBase62(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 36
	}
	return -1
}

func newID() string {
	u := uuid.New() // random, version 4
	return encodeBase62(u[:])
}

// ValidID reports whether s is a 22-character base62 encoding of a UUID v4.
func ValidID(s string) bool {
	b, ok := decodeBase62(s)
	if !ok {
		return false
	}
	return b[6]>>4 == 4 && b[8]&0xc0 == 0x80
}

func NewUserID() UserID             { return UserID(newID()) }
func NewPaperID() PaperID           { return PaperID(newID()) }
func NewBlockID() BlockID           { return BlockID(newID()) }
func NewSessionID() SessionID       { return SessionID(newID()) }
func NewTokenID() TokenID           { return TokenID(newID()) }
func NewAttachmentID() AttachmentID { return AttachmentID(newID()) }
func NewLineID() LineID             { return LineID(newID()) }

var workspaceIDPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// MaxWorkspaceIDLength is the longest workspace ID a configuration may use.
const MaxWorkspaceIDLength = 63

// ValidWorkspaceID reports whether id is a valid workspace name.
func ValidWorkspaceID(id WorkspaceID) bool {
	return len(id) <= MaxWorkspaceIDLength && workspaceIDPattern.MatchString(string(id))
}
