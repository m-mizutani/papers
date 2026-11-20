package model

import (
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// Workspace is the top-level container of papers and the unit users switch
// between in the UI. Access is controlled by Bindings.
type Workspace struct {
	ID          WorkspaceID   `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Emoji       string        `json:"emoji,omitempty"`
	Bindings    []RoleBinding `json:"bindings"`
	CreatedBy   UserID        `json:"created_by"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

func (w *Workspace) Validate() error {
	if w.ID == "" {
		return goerr.Wrap(ErrInvalidArgument, "workspace id is required")
	}
	if strings.TrimSpace(w.Name) == "" {
		return goerr.Wrap(ErrInvalidArgument, "workspace name is required")
	}
	if w.CreatedBy == "" {
		return goerr.Wrap(ErrInvalidArgument, "workspace creator is required")
	}
	if err := validateBindings(w.Bindings); err != nil {
		return err
	}
	hasOwner := false
	for _, b := range w.Bindings {
		if b.Role == RoleOwner {
			hasOwner = true
			break
		}
	}
	if !hasOwner {
		// A workspace without an owner can only be recovered by a system
		// administrator, so refuse to persist one.
		return goerr.Wrap(ErrInvalidArgument, "workspace must have at least one owner binding")
	}
	return nil
}
