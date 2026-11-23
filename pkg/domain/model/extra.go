package model

import (
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// Attachment is an immutable file that belongs to one paper. Its bytes live
// in an AttachmentStore; this is the metadata.
type Attachment struct {
	ID          AttachmentID `json:"id"`
	WorkspaceID WorkspaceID  `json:"workspace_id"`
	PaperID     PaperID      `json:"paper_id"`
	Name        string       `json:"name"`
	ContentType string       `json:"content_type"`
	Size        int64        `json:"size"`
	SHA256      string       `json:"sha256"`
	CreatedBy   Actor        `json:"created_by"`
	CreatedAt   time.Time    `json:"created_at"`
}

// MaxAttachmentSize caps one attachment.
const MaxAttachmentSize = 20 << 20

// AllowedAttachmentTypes are the content types papers accepts for now.
var AllowedAttachmentTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
}

// StoreKey is where the attachment's bytes live in the store.
func (a *Attachment) StoreKey() string { return a.StorePrefix() + string(a.ID) }

// StorePrefix is the key prefix of every attachment of the paper.
func (a *Attachment) StorePrefix() string {
	return "workspaces/" + string(a.WorkspaceID) + "/papers/" + string(a.PaperID) + "/attachments/"
}

func (a *Attachment) Validate() error {
	if !ValidID(string(a.ID)) || a.PaperID == "" || a.WorkspaceID == "" {
		return goerr.Wrap(ErrInvalidArgument, "attachment id, workspace and paper are required")
	}
	if !AllowedAttachmentTypes[a.ContentType] {
		return goerr.Wrap(ErrInvalidArgument, "only images can be attached for now", goerr.V("content_type", a.ContentType))
	}
	if a.Size <= 0 || a.Size > MaxAttachmentSize {
		return goerr.Wrap(ErrInvalidArgument, "attachment size out of range", goerr.V("size", a.Size))
	}
	return nil
}

// Favorite is a public paper a user keeps in the sidebar of a workspace.
// It is personal: no one else sees it.
type Favorite struct {
	UserID      UserID      `json:"user_id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	PaperID     PaperID     `json:"paper_id"`
	Order       string      `json:"order"`
}

func (f *Favorite) Validate() error {
	if f.UserID == "" || f.WorkspaceID == "" || !ValidID(string(f.PaperID)) {
		return goerr.Wrap(ErrInvalidArgument, "favorite needs a user, a workspace and a paper", goerr.V("paper_id", f.PaperID))
	}
	if !ValidOrderKey(f.Order) {
		return goerr.Wrap(ErrInvalidArgument, "invalid favorite order key", goerr.V("order", f.Order))
	}
	return nil
}

// Recent is the list of papers a user opened recently in a workspace.
type Recent struct {
	UserID      UserID      `json:"user_id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	PaperIDs    []PaperID   `json:"paper_ids"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// MaxRecent is how many recent papers the sidebar keeps.
const MaxRecent = 10

// Touch moves id to the front.
func (r *Recent) Touch(id PaperID, now time.Time) {
	out := []PaperID{id}
	for _, p := range r.PaperIDs {
		if p != id && len(out) < MaxRecent {
			out = append(out, p)
		}
	}
	r.PaperIDs, r.UpdatedAt = out, now
}

// Forward records where a paper went when it moved to another workspace.
type Forward struct {
	WorkspaceID   WorkspaceID `json:"workspace_id"`
	PaperID       PaperID     `json:"paper_id"`
	ToWorkspaceID WorkspaceID `json:"to_workspace_id"`
	ToPaperID     PaperID     `json:"to_paper_id"`
	MovedAt       time.Time   `json:"moved_at"`
}
