package model_test

import (
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func TestEmailDomain(t *testing.T) {
	gt.Equal(t, model.EmailDomain("a@Example.COM"), "example.com")
	gt.Equal(t, model.EmailDomain("broken"), "")
}

func TestNewAnonymousUser(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	u := model.NewAnonymousUser(now)
	id := string(u.ID)
	gt.True(t, u.Anonymous)
	gt.Equal(t, u.Email, id+"@anonymous.invalid")
	gt.Equal(t, u.EmailDomain(), model.AnonymousEmailDomain)
	gt.Equal(t, u.Name, "Anonymous "+id[len(id)-4:])
	gt.Equal(t, u.CreatedAt, now)
	gt.NoError(t, u.Validate())
	gt.NotEqual(t, model.NewAnonymousUser(now).ID, u.ID)
}

func TestAPIToken(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	tok := &model.APIToken{ID: "t", SecretHash: "h", UserID: "u", Name: "n", Kind: model.TokenAgent, Scope: model.ScopeWrite}
	gt.NoError(t, tok.Validate())
	gt.False(t, tok.Expired(now))
	tok.ExpiresAt = &past
	gt.True(t, tok.Expired(now))

	gt.True(t, tok.AllowsWorkspace("any"))
	tok.Workspaces = []model.WorkspaceID{"ws1"}
	gt.True(t, tok.AllowsWorkspace("ws1"))
	gt.False(t, tok.AllowsWorkspace("ws2"))

	tok.Scope = "admin"
	gt.Error(t, tok.Validate())
}

func TestAPITokenOAuth(t *testing.T) {
	exp := time.Now().Add(5 * time.Minute)
	base := func() *model.APIToken {
		return &model.APIToken{ID: "t", UserID: "u", Name: "n", Kind: model.TokenAgent, Scope: model.ScopeWrite,
			OAuth: &model.OAuthGrant{ClientID: "pcl_x", ClientName: "n", RedirectURI: "http://localhost:1/cb", CodeHash: "c", CodeChallenge: "x", CodeExpiresAt: &exp}}
	}

	pending := base()
	gt.True(t, pending.Pending())
	gt.NoError(t, pending.Validate())

	noChallenge := base()
	noChallenge.OAuth.CodeChallenge = ""
	gt.Error(t, noChallenge.Validate())

	noExpiry := base()
	noExpiry.OAuth.CodeExpiresAt = nil
	gt.Error(t, noExpiry.Validate())

	// Once exchanged the token needs its secret like any other.
	active := base()
	active.OAuth.CodeHash, active.OAuth.CodeChallenge, active.OAuth.CodeExpiresAt = "", "", nil
	gt.False(t, active.Pending())
	gt.Error(t, active.Validate())
	active.SecretHash = "h"
	gt.NoError(t, active.Validate())

	noClient := base()
	noClient.OAuth.ClientID = ""
	gt.Error(t, noClient.Validate())

	plain := &model.APIToken{ID: "t", UserID: "u", Name: "n", Kind: model.TokenAgent, Scope: model.ScopeWrite}
	gt.False(t, plain.Pending())
	gt.Error(t, plain.Validate())
}

func TestSessionExpired(t *testing.T) {
	now := time.Now()
	s := &model.Session{ExpiresAt: now}
	gt.True(t, s.Expired(now))
	gt.False(t, s.Expired(now.Add(-time.Second)))
}
