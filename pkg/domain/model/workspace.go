package model

import (
	"strings"

	"github.com/m-mizutani/goerr/v2"
)

// Workspace is defined by a configuration file, not stored data. It is the
// unit of who can see what: whoever the workspace's rule allows can use
// every public paper in it.
type Workspace struct {
	ID WorkspaceID `json:"id"`
	// Name is the display name. It defaults to the ID.
	Name string `json:"name"`
	// Description tells people and agents what the workspace collects and
	// how to write there. Agents receive it as the workspace's rules.
	Description string `json:"description,omitempty"`
	Emoji       string `json:"emoji,omitempty"`
	Color       string `json:"color,omitempty"`
	// Language is the default language agents should write in.
	Language string `json:"language,omitempty"`
	// Group is the display group from the global configuration.
	Group string `json:"group,omitempty"`
	// Sandbox is what AI-authored HTML drawn in a sandboxed frame may do in
	// this workspace (HTML papers, html and svg blocks).
	Sandbox Sandbox `json:"sandbox"`
}

// Sandbox limits the AI-authored HTML of a workspace.
type Sandbox struct {
	// ExternalResources lets the HTML load scripts, styles, images, fonts
	// and connections from other hosts. Without it only what the HTML
	// embeds runs. The configuration defaults it to false.
	ExternalResources bool `json:"external_resources"`
}

// SandboxCSP is the Content-Security-Policy that keeps HTML from loading
// anything it does not embed, for a workspace without external resources.
const SandboxCSP = "default-src 'none'; script-src 'unsafe-inline' 'unsafe-eval'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; worker-src blob:"

// Validate checks a workspace definition loaded from configuration.
func (w *Workspace) Validate() error {
	if !ValidWorkspaceID(w.ID) {
		return goerr.Wrap(ErrInvalidArgument, "workspace id must be lowercase letters, digits and single hyphens, at most 63 characters", goerr.V("id", w.ID))
	}
	if strings.TrimSpace(w.Name) == "" {
		return goerr.Wrap(ErrInvalidArgument, "workspace name is required", goerr.V("id", w.ID))
	}
	if w.Emoji != "" && w.Color != "" {
		return goerr.Wrap(ErrInvalidArgument, "set either emoji or color, not both", goerr.V("id", w.ID))
	}
	return nil
}

// WorkspaceRecord is the only stored data about a workspace: which paper is
// its home.
type WorkspaceRecord struct {
	ID          WorkspaceID `json:"id"`
	HomePaperID PaperID     `json:"home_paper_id"`
}

// WorkspaceGroup is a display group of workspaces from the global
// configuration. It has no effect on access.
type WorkspaceGroup struct {
	Name       string        `json:"name"`
	Workspaces []WorkspaceID `json:"workspaces"`
}
