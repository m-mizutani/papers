package model

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// Visibility decides who can see a paper.
type Visibility string

const (
	// VisibilityPublic papers are readable and writable by everyone who can
	// use the workspace. They live in the workspace's tree, or are unfiled
	// (no parent) until someone places them.
	VisibilityPublic Visibility = "public"
	// VisibilityPrivate papers are seen only by their creator. They stay out
	// of the tree and show in the creator's papers list.
	VisibilityPrivate Visibility = "private"
)

func (v Visibility) Validate() error {
	switch v {
	case VisibilityPublic, VisibilityPrivate:
		return nil
	}
	return goerr.Wrap(ErrInvalidArgument, "visibility must be public or private", goerr.V("visibility", v))
}

// PaperStatus is derived from VersionNumber, ArchivedAt and DeletedAt.
type PaperStatus string

const (
	StatusDraft     PaperStatus = "draft"
	StatusCanonical PaperStatus = "canonical"
	StatusArchived  PaperStatus = "archived"
	StatusTrashed   PaperStatus = "trashed"
)

// Paper is a document: a title and an ordered list of blocks. A public paper
// other than the home either sits in exactly one parent's body as a paper
// block (filed), or has no parent (unfiled); unfiled and private papers
// have no children.
type Paper struct {
	ID          PaperID     `json:"id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	Title       string      `json:"title"`
	// ParentID is the paper whose body holds this paper's paper block. It
	// is empty for the home, for unfiled papers and for private papers.
	ParentID   PaperID    `json:"parent_id,omitempty"`
	Visibility Visibility `json:"visibility"`
	// VersionNumber is the latest saved version; 0 means a draft.
	VersionNumber int `json:"version_number"`
	// ArchivedAt hides the paper from search and listings.
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	// DeletedAt puts the paper in the trash; DeletedWith is the paper whose
	// deletion took this one along (itself when deleted directly).
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
	DeletedWith PaperID    `json:"deleted_with,omitempty"`
	// JobID is the move job working on the paper. While it is set only that
	// job writes the paper and its blocks, and search leaves the paper out.
	JobID JobID `json:"job_id,omitempty"`

	CreatedBy Actor     `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is approximate: it lags the last write by up to the touch
	// interval to keep writes to the paper document rare.
	UpdatedBy Actor     `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MaxTitleLength is the longest title in bytes.
const MaxTitleLength = 256

// MaxBlocksPerPaper caps the number of blocks in one paper.
const MaxBlocksPerPaper = 5000

// MaxTreeDepth is the safety valve for walking parents. It is not a limit of
// the specification.
const MaxTreeDepth = 100

func ValidateTitle(title string) error {
	if len(title) > MaxTitleLength {
		return goerr.Wrap(ErrInvalidArgument, "title is too long", goerr.V("length", len(title)))
	}
	if strings.ContainsAny(title, "\n\r") {
		return goerr.Wrap(ErrInvalidArgument, "title must be a single line")
	}
	return nil
}

func (p *Paper) Validate() error {
	if !ValidID(string(p.ID)) || p.WorkspaceID == "" {
		return goerr.Wrap(ErrInvalidArgument, "paper id and workspace id are required", goerr.V("id", p.ID))
	}
	if err := ValidateTitle(p.Title); err != nil {
		return err
	}
	if err := p.Visibility.Validate(); err != nil {
		return err
	}
	if p.Visibility == VisibilityPrivate && p.ParentID != "" {
		return goerr.Wrap(ErrInvalidArgument, "private papers have no parent", goerr.V("id", p.ID))
	}
	if p.ParentID == p.ID && p.ID != "" {
		return goerr.Wrap(ErrInvalidArgument, "a paper cannot be its own parent", goerr.V("id", p.ID))
	}
	if p.VersionNumber < 0 {
		return goerr.Wrap(ErrInvalidArgument, "version number must not be negative")
	}
	if (p.DeletedAt == nil) != (p.DeletedWith == "") {
		return goerr.Wrap(ErrInvalidArgument, "deleted_at and deleted_with go together", goerr.V("id", p.ID))
	}
	if err := p.CreatedBy.Validate(); err != nil {
		return goerr.Wrap(err, "invalid paper creator")
	}
	if err := p.UpdatedBy.Validate(); err != nil {
		return goerr.Wrap(err, "invalid paper updater")
	}
	return nil
}

func (p *Paper) Clone() *Paper {
	c := *p
	return &c
}

// Status derives the paper's state.
func (p *Paper) Status() PaperStatus {
	switch {
	case p.DeletedAt != nil:
		return StatusTrashed
	case p.ArchivedAt != nil:
		return StatusArchived
	case p.VersionNumber > 0:
		return StatusCanonical
	}
	return StatusDraft
}

func (p *Paper) IsTrashed() bool  { return p.DeletedAt != nil }
func (p *Paper) IsArchived() bool { return p.ArchivedAt != nil }
func (p *Paper) IsPrivate() bool  { return p.Visibility == VisibilityPrivate }
func (p *Paper) IsBusy() bool     { return p.JobID != "" }

// Owner is the user the paper belongs to: the creator, or the user an agent
// acted for.
func (p *Paper) Owner() UserID { return p.CreatedBy.UserID }

// PaperSort is the order of a papers list, newest first.
type PaperSort string

const (
	SortUpdated PaperSort = "updated"
	SortCreated PaperSort = "created"
)

func (s PaperSort) Validate() error {
	switch s {
	case SortUpdated, SortCreated:
		return nil
	}
	return goerr.Wrap(ErrInvalidArgument, "sort must be updated or created", goerr.V("sort", s))
}

// TimeOf is the value of p the sort orders by.
func (s PaperSort) TimeOf(p *Paper) time.Time {
	if s == SortCreated {
		return p.CreatedAt
	}
	return p.UpdatedAt
}

// MaxPaperListLimit bounds one read of a papers list.
const MaxPaperListLimit = 100

// PaperQuery is one read of the papers of a workspace, ordered by Sort
// descending with ties broken by ID descending. Storage filters only on
// what has an index; everything else is left to the caller.
type PaperQuery struct {
	// Unfiled keeps only papers without a parent (the home, unfiled and
	// private papers).
	Unfiled bool
	// Creator keeps only papers this user created (or an agent created
	// for them), when set.
	Creator UserID
	Sort    PaperSort
	Limit   int
	// Cursor continues after the paper a previous read ended with.
	Cursor string
}

func (q PaperQuery) Validate() error {
	if err := q.Sort.Validate(); err != nil {
		return err
	}
	if q.Limit < 1 || q.Limit > MaxPaperListLimit {
		return goerr.Wrap(ErrInvalidArgument, "limit out of range", goerr.V("limit", q.Limit), goerr.V("max", MaxPaperListLimit))
	}
	if q.Cursor != "" {
		if _, err := ParsePaperCursor(q.Cursor); err != nil {
			return err
		}
	}
	return nil
}

// PaperCursor is a position in a papers list: the sort value and the ID of
// the last paper read.
type PaperCursor struct {
	At time.Time
	ID PaperID
}

// CursorOf is the cursor that continues a list sorted by s after p.
func CursorOf(s PaperSort, p *Paper) string {
	raw := s.TimeOf(p).UTC().Format(time.RFC3339Nano) + " " + string(p.ID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// ParsePaperCursor reads a cursor made by CursorOf.
func ParsePaperCursor(cursor string) (PaperCursor, error) {
	bad := goerr.Wrap(ErrInvalidArgument, "invalid cursor", goerr.V("cursor", cursor))
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return PaperCursor{}, bad
	}
	at, id, ok := strings.Cut(string(raw), " ")
	if !ok || !ValidID(id) {
		return PaperCursor{}, bad
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return PaperCursor{}, bad
	}
	return PaperCursor{At: t, ID: PaperID(id)}, nil
}

// After reports whether p comes after the cursor in a list sorted by s
// (newer first, then larger IDs first).
func (c PaperCursor) After(s PaperSort, p *Paper) bool {
	t := s.TimeOf(p)
	return t.Before(c.At) || (t.Equal(c.At) && p.ID < c.ID)
}

// PaperKey names a paper across workspaces.
type PaperKey struct {
	WorkspaceID WorkspaceID
	PaperID     PaperID
}
