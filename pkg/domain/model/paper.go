package model

import (
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// Visibility decides who can see a paper.
type Visibility string

const (
	// VisibilityPublic papers are readable and writable by everyone who can
	// use the workspace. They live in the workspace's tree.
	VisibilityPublic Visibility = "public"
	// VisibilityPrivate papers are seen only by their creator. They stay out
	// of the tree, in the creator's flat list.
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
// other than the home sits in exactly one parent's body as a paper block.
type Paper struct {
	ID          PaperID     `json:"id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	Title       string      `json:"title"`
	// ParentID is the paper whose body holds this paper's paper block. It
	// is empty for the home and for private papers.
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

// Owner is the user the paper belongs to: the creator, or the user an agent
// acted for.
func (p *Paper) Owner() UserID { return p.CreatedBy.UserID }
