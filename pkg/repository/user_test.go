package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

func TestUserRepository(t *testing.T) {
	runRepositoryTest(t, "round trip", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		now := time.Now().UTC()
		u := &model.User{
			ID: model.NewUserID(), Email: uniq("u") + "@example.com", Name: "Alice",
			AvatarURL: "https://example.com/a.png", CreatedAt: now, UpdatedAt: now, LastLoginAt: now,
		}
		gt.NoError(t, repo.PutUser(ctx, u))

		got := gt.R1(repo.GetUser(ctx, u.ID)).NoError(t)
		gt.Equal(t, got.ID, u.ID)
		gt.Equal(t, got.Email, u.Email)
		gt.Equal(t, got.Name, u.Name)
		gt.Equal(t, got.AvatarURL, u.AvatarURL)
		gt.True(t, timeEq(got.CreatedAt, u.CreatedAt))
		gt.True(t, timeEq(got.UpdatedAt, u.UpdatedAt))
		gt.True(t, timeEq(got.LastLoginAt, u.LastLoginAt))

		byEmail := gt.R1(repo.GetUserByEmail(ctx, u.Email)).NoError(t)
		gt.Equal(t, byEmail.ID, u.ID)

		// Mutating the returned value must not change the stored one.
		got.Name = "changed"
		again := gt.R1(repo.GetUser(ctx, u.ID)).NoError(t)
		gt.Equal(t, again.Name, "Alice")

		list := gt.R1(repo.ListUsers(ctx)).NoError(t)
		gt.A(t, list).Longer(0)
	})

	runRepositoryTest(t, "missing user", func(t *testing.T, repo interfaces.Repository) {
		_, err := repo.GetUser(context.Background(), model.NewUserID())
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})

	runRepositoryTest(t, "duplicated email rejected", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		email := uniq("dup") + "@example.com"
		gt.NoError(t, repo.PutUser(ctx, &model.User{ID: model.NewUserID(), Email: email}))
		err := repo.PutUser(ctx, &model.User{ID: model.NewUserID(), Email: email})
		gt.True(t, errors.Is(err, model.ErrAlreadyExists))
	})

	runRepositoryTest(t, "invalid user rejected", func(t *testing.T, repo interfaces.Repository) {
		gt.Error(t, repo.PutUser(context.Background(), &model.User{ID: model.NewUserID()}))
	})

	runRepositoryTest(t, "identity", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		now := time.Now().UTC()
		id := &model.Identity{Provider: model.ProviderGoogle, Subject: uniq("sub"), UserID: model.NewUserID(), Email: "x@example.com", CreatedAt: now, UpdatedAt: now}
		gt.NoError(t, repo.PutIdentity(ctx, id))
		got := gt.R1(repo.GetIdentity(ctx, id.Provider, id.Subject)).NoError(t)
		gt.Equal(t, got.UserID, id.UserID)
		gt.Equal(t, got.Email, id.Email)
		gt.True(t, timeEq(got.CreatedAt, now))
		gt.True(t, timeEq(got.UpdatedAt, now))

		_, err := repo.GetIdentity(ctx, model.ProviderGoogle, uniq("other"))
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})
}

func TestSessionRepository(t *testing.T) {
	runRepositoryTest(t, "lifecycle", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		now := time.Now().UTC()
		s := &model.Session{ID: model.NewSessionID(), SecretHash: "hash", UserID: model.NewUserID(), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
		gt.NoError(t, repo.PutSession(ctx, s))
		got := gt.R1(repo.GetSession(ctx, s.ID)).NoError(t)
		gt.Equal(t, got.SecretHash, s.SecretHash)
		gt.Equal(t, got.UserID, s.UserID)
		gt.True(t, timeEq(got.CreatedAt, s.CreatedAt))
		gt.True(t, timeEq(got.ExpiresAt, s.ExpiresAt))

		gt.NoError(t, repo.DeleteSession(ctx, s.ID))
		_, err := repo.GetSession(ctx, s.ID)
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})
}

func TestTokenRepository(t *testing.T) {
	runRepositoryTest(t, "lifecycle", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		now := time.Now().UTC()
		exp := now.Add(24 * time.Hour)
		owner := model.NewUserID()
		tok := &model.APIToken{
			ID: model.NewTokenID(), SecretHash: "h", UserID: owner, Name: "claude",
			Kind: model.TokenAgent, AgentName: "Claude", Scope: model.ScopeWrite,
			Workspaces: []model.WorkspaceID{"ws1"}, CreatedAt: now, ExpiresAt: &exp,
		}
		gt.NoError(t, repo.PutToken(ctx, tok))
		got := gt.R1(repo.GetToken(ctx, tok.ID)).NoError(t)
		gt.Equal(t, got.SecretHash, tok.SecretHash)
		gt.Equal(t, got.UserID, owner)
		gt.Equal(t, got.Name, tok.Name)
		gt.Equal(t, got.Kind, tok.Kind)
		gt.Equal(t, got.AgentName, tok.AgentName)
		gt.Equal(t, got.Scope, tok.Scope)
		gt.Equal(t, got.Workspaces, tok.Workspaces)
		gt.True(t, timeEq(got.CreatedAt, now))
		gt.True(t, timeEq(*got.ExpiresAt, exp))
		gt.Nil(t, got.LastUsedAt)

		list := gt.R1(repo.ListTokens(ctx, owner)).NoError(t)
		gt.A(t, list).Length(1)

		gt.NoError(t, repo.DeleteToken(ctx, tok.ID))
		_, err := repo.GetToken(ctx, tok.ID)
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})
}
