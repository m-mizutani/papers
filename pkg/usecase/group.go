package usecase

import (
	"context"
	"slices"
	"strings"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func requireAdmin(p *model.Principal) error {
	if err := requirePrincipal(p); err != nil {
		return err
	}
	if !p.SystemAdmin || p.Token != nil {
		return goerr.Wrap(model.ErrPermissionDenied, "system administrator session required")
	}
	return nil
}

// ListGroups is open to every signed-in user so they can pick groups when
// sharing.
func (u *UseCases) ListGroups(ctx context.Context, p *model.Principal) ([]*model.Group, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	gs, err := u.repo.ListGroups(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list groups")
	}
	return gs, nil
}

type GroupInput struct {
	Name        string
	Description string
	Members     []model.UserID
}

func (u *UseCases) normalizeMembers(ctx context.Context, members []model.UserID) ([]model.UserID, error) {
	out := make([]model.UserID, 0, len(members))
	for _, m := range members {
		if slices.Contains(out, m) {
			continue
		}
		if _, err := u.repo.GetUser(ctx, m); err != nil {
			return nil, unknownSubject(err, "user", string(m))
		}
		out = append(out, m)
	}
	return out, nil
}

func (u *UseCases) CreateGroup(ctx context.Context, p *model.Principal, in GroupInput) (*model.Group, error) {
	if err := requireAdmin(p); err != nil {
		return nil, err
	}
	members, err := u.normalizeMembers(ctx, in.Members)
	if err != nil {
		return nil, err
	}
	now := u.now()
	g := &model.Group{ID: model.NewGroupID(), Name: strings.TrimSpace(in.Name), Description: strings.TrimSpace(in.Description), Members: members, CreatedAt: now, UpdatedAt: now}
	if err := u.repo.PutGroup(ctx, g); err != nil {
		return nil, goerr.Wrap(err, "failed to create group")
	}
	return g, nil
}

func (u *UseCases) UpdateGroup(ctx context.Context, p *model.Principal, id model.GroupID, in GroupInput) (*model.Group, error) {
	if err := requireAdmin(p); err != nil {
		return nil, err
	}
	g, err := u.repo.GetGroup(ctx, id)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get group", goerr.V("group_id", id))
	}
	members, err := u.normalizeMembers(ctx, in.Members)
	if err != nil {
		return nil, err
	}
	g.Name = strings.TrimSpace(in.Name)
	g.Description = strings.TrimSpace(in.Description)
	g.Members = members
	g.UpdatedAt = u.now()
	if err := u.repo.PutGroup(ctx, g); err != nil {
		return nil, goerr.Wrap(err, "failed to update group")
	}
	return g, nil
}

func (u *UseCases) DeleteGroup(ctx context.Context, p *model.Principal, id model.GroupID) error {
	if err := requireAdmin(p); err != nil {
		return err
	}
	if _, err := u.repo.GetGroup(ctx, id); err != nil {
		return goerr.Wrap(err, "failed to get group", goerr.V("group_id", id))
	}
	if err := u.repo.DeleteGroup(ctx, id); err != nil {
		return goerr.Wrap(err, "failed to delete group")
	}
	return nil
}

// ListUsers returns every user, for member pickers.
func (u *UseCases) ListUsers(ctx context.Context, p *model.Principal) ([]*model.User, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	us, err := u.repo.ListUsers(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list users")
	}
	return us, nil
}
