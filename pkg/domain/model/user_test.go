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

func TestSessionExpired(t *testing.T) {
	now := time.Now()
	s := &model.Session{ExpiresAt: now}
	gt.True(t, s.Expired(now))
	gt.False(t, s.Expired(now.Add(-time.Second)))
}
