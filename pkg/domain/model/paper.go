package model

import (
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// PaperAccess controls who can reach a paper beyond the workspace roles.
//
// When Restricted is false the paper inherits workspace roles and Bindings
// can only add (raise) roles. When Restricted is true workspace roles are
// ignored except for workspace owners, and only Bindings apply.
type PaperAccess struct {
	Restricted bool          `json:"restricted"`
	Bindings   []RoleBinding `json:"bindings"`
}

// Paper is a canvas made of blocks. Its content is the result of applying
// every commit in order; Revision is the sequence number of the latest one.
type Paper struct {
	ID          PaperID     `json:"id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	Title       string      `json:"title"`
	Revision    int64       `json:"revision"`
	Access      PaperAccess `json:"access"`
	CreatedBy   Actor       `json:"created_by"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedBy   Actor       `json:"updated_by"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

const MaxTitleLength = 256

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
	if p.ID == "" || p.WorkspaceID == "" {
		return goerr.Wrap(ErrInvalidArgument, "paper id and workspace id are required")
	}
	if err := ValidateTitle(p.Title); err != nil {
		return err
	}
	if err := p.CreatedBy.Validate(); err != nil {
		return goerr.Wrap(err, "invalid paper creator")
	}
	if err := p.UpdatedBy.Validate(); err != nil {
		return goerr.Wrap(err, "invalid paper updater")
	}
	if p.Revision < 0 {
		return goerr.Wrap(ErrInvalidArgument, "revision must not be negative")
	}
	return validateBindings(p.Access.Bindings)
}
