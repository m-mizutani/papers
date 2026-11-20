package usecase

import (
	"context"
	"strings"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// WorkspaceView is a workspace together with the caller's role in it.
type WorkspaceView struct {
	Workspace *model.Workspace
	Role      model.Role
}

func (u *UseCases) ListWorkspaces(ctx context.Context, p *model.Principal) ([]*WorkspaceView, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	all, err := u.repo.ListWorkspaces(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list workspaces")
	}
	var out []*WorkspaceView
	for _, ws := range all {
		if role := p.WorkspaceRole(ws); role.AtLeast(model.RoleViewer) {
			out = append(out, &WorkspaceView{Workspace: ws, Role: role})
		}
	}
	return out, nil
}

func (u *UseCases) GetWorkspace(ctx context.Context, p *model.Principal, id model.WorkspaceID) (*WorkspaceView, error) {
	ws, role, err := u.workspaceFor(ctx, p, id, model.RoleViewer)
	if err != nil {
		return nil, err
	}
	return &WorkspaceView{Workspace: ws, Role: role}, nil
}

type CreateWorkspaceInput struct {
	Name        string
	Description string
	Emoji       string
}

func (u *UseCases) CreateWorkspace(ctx context.Context, p *model.Principal, in CreateWorkspaceInput) (*WorkspaceView, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	if p.Token != nil {
		return nil, goerr.Wrap(model.ErrPermissionDenied, "tokens cannot create workspaces")
	}
	if u.cfg.WorkspaceCreation == WorkspaceCreationAdmin && !p.SystemAdmin {
		return nil, goerr.Wrap(model.ErrPermissionDenied, "only system administrators can create workspaces")
	}
	now := u.now()
	ws := &model.Workspace{
		ID:          model.NewWorkspaceID(),
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
		Emoji:       strings.TrimSpace(in.Emoji),
		Bindings: []model.RoleBinding{
			{Subject: model.Subject{Kind: model.SubjectUser, ID: string(p.User.ID)}, Role: model.RoleOwner},
		},
		CreatedBy: p.User.ID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := u.repo.PutWorkspace(ctx, ws); err != nil {
		return nil, goerr.Wrap(err, "failed to create workspace")
	}
	return &WorkspaceView{Workspace: ws, Role: p.WorkspaceRole(ws)}, nil
}

// UpdateWorkspaceInput replaces the non-nil fields.
type UpdateWorkspaceInput struct {
	Name        *string
	Description *string
	Emoji       *string
	Bindings    *[]model.RoleBinding
}

func (u *UseCases) UpdateWorkspace(ctx context.Context, p *model.Principal, id model.WorkspaceID, in UpdateWorkspaceInput) (*WorkspaceView, error) {
	ws, _, err := u.workspaceFor(ctx, p, id, model.RoleOwner)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		ws.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		ws.Description = strings.TrimSpace(*in.Description)
	}
	if in.Emoji != nil {
		ws.Emoji = strings.TrimSpace(*in.Emoji)
	}
	if in.Bindings != nil {
		if err := u.validateBindingSubjects(ctx, *in.Bindings); err != nil {
			return nil, err
		}
		ws.Bindings = *in.Bindings
	}
	ws.UpdatedAt = u.now()
	if err := u.repo.PutWorkspace(ctx, ws); err != nil {
		return nil, goerr.Wrap(err, "failed to update workspace")
	}
	role := p.WorkspaceRole(ws)
	return &WorkspaceView{Workspace: ws, Role: role}, nil
}
