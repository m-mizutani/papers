package model

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/m-mizutani/goerr/v2"
	"github.com/rivo/uniseg"
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

// PaperKind is how a paper's body is held. It is set when the paper is
// created and never changes. Whether a paper is an item of a database is a
// separate matter (Paper.DatabaseID): an item may have any kind.
type PaperKind string

const (
	// PaperKindBlocks papers have a body of blocks people and AI edit.
	PaperKindBlocks PaperKind = "blocks"
	// PaperKindHTML papers have one html_page block pointing to an HTML
	// document written through the API; the Web UI only shows it.
	PaperKindHTML PaperKind = "html"
	// PaperKindDatabase papers are tables: the body is the database's items,
	// with the property definitions and the order of the items on the
	// paper itself. A database has no blocks and no versions.
	PaperKindDatabase PaperKind = "database"
)

// Validate accepts the kinds, and empty (a record written before kinds
// existed, read as blocks).
func (k PaperKind) Validate() error {
	switch k {
	case "", PaperKindBlocks, PaperKindHTML, PaperKindDatabase:
		return nil
	}
	return goerr.Wrap(ErrInvalidArgument, "kind must be blocks, html or database", goerr.V("kind", k))
}

// Or returns the kind, reading empty as blocks.
func (k PaperKind) Or() PaperKind {
	if k == "" {
		return PaperKindBlocks
	}
	return k
}

// MarshalJSON always names the kind, so that readers never see an empty
// one from an older record.
func (k PaperKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(k.Or()))
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
// block (filed), belongs to a database (an item), or has neither (unfiled);
// unfiled and private papers have no children.
type Paper struct {
	ID          PaperID     `json:"id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	// Kind is blocks, html or database; empty in records written before
	// kinds existed, which are blocks (see IsHTML).
	Kind  PaperKind `json:"kind"`
	Title string    `json:"title"`
	// Icon is the emoji people or AI set for the paper; empty means none
	// is set and screens show a default chosen by kind. It is not part of
	// versions.
	Icon string `json:"icon,omitempty"`
	// ParentID is the paper whose body holds this paper's paper block. It
	// is empty for the home, for unfiled papers, for private papers and
	// for items.
	ParentID PaperID `json:"parent_id,omitempty"`
	// DatabaseID is the database an item belongs to; empty for any other
	// paper. It is set at creation and never changes.
	DatabaseID PaperID    `json:"database_id,omitempty"`
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

	// Properties are a database's property definitions, in column order.
	Properties []Property `json:"properties,omitempty"`
	// Items are a database's items in display order. Trashed and archived
	// items stay listed (restoring keeps their place); purging removes them.
	Items []PaperID `json:"items,omitempty"`

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

// MaxIconLength is the longest icon in bytes.
const MaxIconLength = 32

// NormalizeIcon trims the spaces around an icon.
func NormalizeIcon(icon string) string {
	return strings.TrimSpace(icon)
}

// ValidateIcon accepts an empty icon (none set) or one grapheme cluster (one
// character on screen, such as one emoji with its modifiers) of at most
// MaxIconLength bytes, without spaces or control characters. Callers trim the
// value first (NormalizeIcon).
func ValidateIcon(icon string) error {
	if icon == "" {
		return nil
	}
	if len(icon) > MaxIconLength {
		return goerr.Wrap(ErrInvalidArgument, "icon is too long", goerr.V("length", len(icon)))
	}
	if uniseg.GraphemeClusterCount(icon) != 1 {
		return goerr.Wrap(ErrInvalidArgument, "icon must be a single emoji", goerr.V("icon", icon))
	}
	for _, r := range icon {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return goerr.Wrap(ErrInvalidArgument, "icon must not contain spaces or control characters", goerr.V("icon", icon))
		}
	}
	// a cluster of only format characters and combining marks shows nothing
	visible := false
	for _, r := range icon {
		if !unicode.In(r, unicode.Cf, unicode.Mn, unicode.Me) {
			visible = true
			break
		}
	}
	if !visible {
		return goerr.Wrap(ErrInvalidArgument, "icon must be visible", goerr.V("icon", icon))
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
	if err := ValidateIcon(p.Icon); err != nil {
		return err
	}
	if err := p.Kind.Validate(); err != nil {
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
	if err := p.validateDatabase(); err != nil {
		return err
	}
	if err := p.CreatedBy.Validate(); err != nil {
		return goerr.Wrap(err, "invalid paper creator")
	}
	if err := p.UpdatedBy.Validate(); err != nil {
		return goerr.Wrap(err, "invalid paper updater")
	}
	return nil
}

func (p *Paper) validateDatabase() error {
	if p.Kind != PaperKindDatabase && (len(p.Properties) > 0 || len(p.Items) > 0) {
		return goerr.Wrap(ErrInvalidArgument, "only a database has properties and items", goerr.V("id", p.ID))
	}
	switch p.Kind {
	case PaperKindDatabase:
		if p.Visibility != VisibilityPublic {
			return goerr.Wrap(ErrInvalidArgument, "a database is always public", goerr.V("id", p.ID))
		}
		if p.VersionNumber != 0 {
			return goerr.Wrap(ErrInvalidArgument, "a database has no versions", goerr.V("id", p.ID))
		}
		if err := ValidateProperties(p.Properties); err != nil {
			return err
		}
		if len(p.Items) > MaxDatabaseItems {
			return goerr.Wrap(ErrInvalidArgument, "the database has too many items", goerr.V("count", len(p.Items)), goerr.V("max", MaxDatabaseItems))
		}
		seen := make(map[PaperID]bool, len(p.Items))
		for _, id := range p.Items {
			if !ValidID(string(id)) || seen[id] {
				return goerr.Wrap(ErrInvalidArgument, "invalid or duplicate item id", goerr.V("item_id", id))
			}
			seen[id] = true
		}
	}
	if p.DatabaseID != "" {
		if !ValidID(string(p.DatabaseID)) || p.DatabaseID == p.ID {
			return goerr.Wrap(ErrInvalidArgument, "invalid database id", goerr.V("id", p.ID), goerr.V("database_id", p.DatabaseID))
		}
		if p.Visibility != VisibilityPublic || p.ParentID != "" {
			return goerr.Wrap(ErrInvalidArgument, "an item is public and has no parent but its database", goerr.V("id", p.ID))
		}
	}
	return nil
}

func (p *Paper) Clone() *Paper {
	c := *p
	c.Properties = CloneProperties(p.Properties)
	c.Items = slices.Clone(p.Items)
	return &c
}

func (p *Paper) IsDatabase() bool { return p.Kind == PaperKindDatabase }
func (p *Paper) IsItem() bool     { return p.DatabaseID != "" }

// Above is the paper above this one in the tree: the parent, or for an item
// its database. Breadcrumbs, cycle checks and "search under here" walk up
// through it.
func (p *Paper) Above() PaperID {
	if p.DatabaseID != "" {
		return p.DatabaseID
	}
	return p.ParentID
}

// IsUnfiled tells a public paper outside the tree: no parent and no
// database (the home is the caller's to tell apart).
func (p *Paper) IsUnfiled() bool {
	return p.Visibility == VisibilityPublic && p.ParentID == "" && p.DatabaseID == ""
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
func (p *Paper) IsHTML() bool     { return p.Kind == PaperKindHTML }

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
	// Unfiled keeps only papers without a parent and outside any database
	// (the home, unfiled and
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
