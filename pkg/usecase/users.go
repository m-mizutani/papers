package usecase

import (
	"context"
	"slices"
	"strings"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/utils/errutil"
)

// MaxUserResults bounds one answer of FindUsers, and the IDs one call may
// look up.
const MaxUserResults = 20

// UserSummary is what other people may see of a user: never the email.
type UserSummary struct {
	ID        model.UserID `json:"id"`
	Name      string       `json:"name"`
	AvatarURL string       `json:"avatar_url,omitempty"`
}

func summaryOf(u *model.User) *UserSummary {
	return &UserSummary{ID: u.ID, Name: u.Name, AvatarURL: u.AvatarURL}
}

// FindUsersInput asks for the people of a workspace whose name or email
// contains Query (case-insensitive; empty means everyone), or for the users
// IDs names. The two are exclusive.
type FindUsersInput struct {
	Query string
	IDs   []model.UserID
}

// FindUsers lists the people one may mention in the workspace. With a
// query, it reads every user once and keeps those the workspace's rule
// allows and whose name or email matches, by name, at most MaxUserResults.
// With IDs, it reads those users in one batch read, in the order given,
// without the rule: the IDs are already visible where they are written.
func (u *UseCases) FindUsers(ctx context.Context, p *model.Principal, ws model.WorkspaceID, in FindUsersInput) ([]*UserSummary, error) {
	if _, err := u.workspaceFor(ctx, p, ws); err != nil {
		return nil, err
	}
	if len(in.IDs) > 0 {
		return u.usersByID(ctx, in)
	}
	users, err := u.repo.ListUsers(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list users")
	}
	q := strings.ToLower(strings.TrimSpace(in.Query))
	var keep []*model.User
	for _, x := range users {
		if q != "" && !strings.Contains(strings.ToLower(x.Name), q) && !strings.Contains(strings.ToLower(x.Email), q) {
			continue
		}
		allow, err := u.workspaces.Allow(ctx, x, ws)
		if err != nil {
			errutil.Handle(ctx, goerr.Wrap(err, "failed to evaluate workspace rule for a user", goerr.V("workspace_id", ws), goerr.V("user_id", x.ID)), "failed to evaluate workspace rule for a user")
			continue
		}
		if allow {
			keep = append(keep, x)
		}
	}
	slices.SortFunc(keep, func(a, b *model.User) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	out := []*UserSummary{}
	for _, x := range keep[:min(len(keep), MaxUserResults)] {
		out = append(out, summaryOf(x))
	}
	return out, nil
}

func (u *UseCases) usersByID(ctx context.Context, in FindUsersInput) ([]*UserSummary, error) {
	if in.Query != "" {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "give either q or ids, not both")
	}
	if len(in.IDs) > MaxUserResults {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "ids names at most 20 users", goerr.V("count", len(in.IDs)))
	}
	for _, id := range in.IDs {
		if !model.ValidID(string(id)) {
			return nil, goerr.Wrap(model.ErrInvalidArgument, "invalid user id", goerr.V("id", id))
		}
	}
	users, err := u.repo.GetUsers(ctx, in.IDs)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read users")
	}
	byID := map[model.UserID]*model.User{}
	for _, x := range users {
		byID[x.ID] = x
	}
	out := []*UserSummary{}
	seen := map[model.UserID]bool{}
	for _, id := range in.IDs {
		if x := byID[id]; x != nil && !seen[id] {
			seen[id] = true
			out = append(out, summaryOf(x))
		}
	}
	return out, nil
}
