package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/repository/memory"
	"github.com/m-mizutani/papers/pkg/usecase"
)

type fakeProvider struct {
	claims map[string]*model.ExternalClaims // by code
}

func (f *fakeProvider) Name() model.IdentityProvider { return model.ProviderGoogle }
func (f *fakeProvider) AuthURL(state, nonce string) string {
	return "https://idp.example/auth?state=" + state + "&nonce=" + nonce
}
func (f *fakeProvider) Exchange(_ context.Context, code, nonce string) (*model.ExternalClaims, error) {
	c, ok := f.claims[code]
	if !ok || nonce != "n" {
		return nil, goerr.New("bad code")
	}
	return c, nil
}

func newAuth(t *testing.T) (*usecase.Auth, *memory.Repository, *fakeProvider, *clock) {
	t.Helper()
	repo := memory.New()
	fp := &fakeProvider{claims: map[string]*model.ExternalClaims{}}
	c := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	a, err := usecase.NewAuth(repo, fp, usecase.AuthConfig{
		AllowedDomains: []string{"Example.com"},
		AllowedEmails:  []string{"guest@other.example"},
		SessionTTL:     time.Hour,
	}, usecase.WithAuthClock(c.Now))
	gt.NoError(t, err)
	return a, repo, fp, c
}

func claims(sub, email string) *model.ExternalClaims {
	return &model.ExternalClaims{Provider: model.ProviderGoogle, Subject: sub, Email: email, EmailVerified: true, Name: "N " + sub, Picture: "https://example.com/" + sub}
}

func TestNewAuthRequiresAllowRule(t *testing.T) {
	_, err := usecase.NewAuth(memory.New(), &fakeProvider{}, usecase.AuthConfig{SessionTTL: time.Hour})
	gt.Error(t, err)
	_, err = usecase.NewAuth(memory.New(), nil, usecase.AuthConfig{SessionTTL: 0})
	gt.Error(t, err)
}

func TestLoginFlow(t *testing.T) {
	ctx := context.Background()
	a, repo, fp, c := newAuth(t)
	fp.claims["ok"] = claims("s1", "alice@example.com")

	url := gt.R1(a.LoginURL("st", "n")).NoError(t)
	gt.True(t, strings.Contains(url, "state=st"))

	cred := gt.R1(a.HandleCallback(ctx, "ok", "n")).NoError(t)
	p := gt.R1(a.AuthenticateSession(ctx, cred.ID, cred.Secret)).NoError(t)
	gt.Equal(t, p.User.Email, "alice@example.com")
	gt.Equal(t, p.User.Name, "N s1")

	// a second login with the same subject keeps the same user
	cred2 := gt.R1(a.HandleCallback(ctx, "ok", "n")).NoError(t)
	p2 := gt.R1(a.AuthenticateSession(ctx, cred2.ID, cred2.Secret)).NoError(t)
	gt.Equal(t, p2.User.ID, p.User.ID)

	// wrong secret, expiry and logout
	_, err := a.AuthenticateSession(ctx, cred.ID, "wrong")
	gt.True(t, errors.Is(err, model.ErrUnauthenticated))
	_, err = a.AuthenticateSession(ctx, "nope", cred.Secret)
	gt.True(t, errors.Is(err, model.ErrUnauthenticated))
	gt.NoError(t, a.Logout(ctx, cred.ID))
	_, err = a.AuthenticateSession(ctx, cred.ID, cred.Secret)
	gt.True(t, errors.Is(err, model.ErrUnauthenticated))
	c.Advance(2 * time.Hour)
	_, err = a.AuthenticateSession(ctx, cred2.ID, cred2.Secret)
	gt.True(t, errors.Is(err, model.ErrUnauthenticated))

	// stored secret is hashed
	s := gt.R1(repo.GetSession(ctx, cred2.ID)).NoError(t)
	gt.NotEqual(t, s.SecretHash, cred2.Secret)
}

func TestLoginAdmission(t *testing.T) {
	ctx := context.Background()
	a, repo, fp, _ := newAuth(t)
	fp.claims["outsider"] = claims("s2", "eve@evil.example")
	fp.claims["guest"] = claims("s3", "guest@other.example")
	unverified := claims("s4", "bob@example.com")
	unverified.EmailVerified = false
	fp.claims["unverified"] = unverified
	fp.claims["admin"] = claims("s5", "root@example.com")

	_, err := a.HandleCallback(ctx, "outsider", "n")
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))
	_, err = a.HandleCallback(ctx, "unverified", "n")
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))
	_, err = a.HandleCallback(ctx, "bad", "n")
	gt.True(t, errors.Is(err, model.ErrUnauthenticated))
	_, err = a.HandleCallback(ctx, "guest", "x")
	gt.True(t, errors.Is(err, model.ErrUnauthenticated))

	gt.R1(a.HandleCallback(ctx, "guest", "n")).NoError(t)

	gt.R1(a.HandleCallback(ctx, "admin", "n")).NoError(t)

	users := gt.R1(repo.ListUsers(ctx)).NoError(t)
	gt.A(t, users).Length(2)
}

func TestLoginLinksExistingUserByEmail(t *testing.T) {
	ctx := context.Background()
	a, repo, fp, _ := newAuth(t)
	pre := &model.User{ID: model.NewUserID(), Email: "carol@example.com"}
	gt.NoError(t, repo.PutUser(ctx, pre))
	fp.claims["c"] = claims("s6", "Carol@example.com")

	cred := gt.R1(a.HandleCallback(ctx, "c", "n")).NoError(t)
	p := gt.R1(a.AuthenticateSession(ctx, cred.ID, cred.Secret)).NoError(t)
	gt.Equal(t, p.User.ID, pre.ID)
	id := gt.R1(repo.GetIdentity(ctx, model.ProviderGoogle, "s6")).NoError(t)
	gt.Equal(t, id.UserID, pre.ID)
}

func TestTokens(t *testing.T) {
	ctx := context.Background()
	a, repo, fp, c := newAuth(t)
	fp.claims["ok"] = claims("s1", "alice@example.com")
	cred := gt.R1(a.HandleCallback(ctx, "ok", "n")).NoError(t)
	session := gt.R1(a.AuthenticateSession(ctx, cred.ID, cred.Secret)).NoError(t)

	tok, raw, err := a.CreateToken(ctx, session, usecase.CreateTokenInput{Name: "claude", Kind: model.TokenAgent, Scope: model.ScopeWrite, TTL: time.Hour})
	gt.NoError(t, err)
	gt.True(t, strings.HasPrefix(raw, usecase.TokenPrefix))
	gt.Equal(t, tok.AgentName, "claude")

	p := gt.R1(a.AuthenticateToken(ctx, raw)).NoError(t)
	gt.Equal(t, p.User.ID, session.User.ID)
	gt.Equal(t, p.Actor().Kind, model.ActorAgent)
	stored := gt.R1(repo.GetToken(ctx, tok.ID)).NoError(t)
	gt.NotNil(t, stored.LastUsedAt)

	// tokens cannot mint tokens
	_, _, err = a.CreateToken(ctx, p, usecase.CreateTokenInput{Name: "x", Kind: model.TokenPersonal, Scope: model.ScopeRead})
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))

	for _, bad := range []string{"", "pap_", "pap_x", "nope", "pap_" + string(tok.ID) + ".wrong", "pap_missing.secret"} {
		_, err := a.AuthenticateToken(ctx, bad)
		gt.True(t, errors.Is(err, model.ErrUnauthenticated))
	}

	list := gt.R1(a.ListTokens(ctx, session)).NoError(t)
	gt.A(t, list).Length(1)

	// another user cannot revoke it
	other := &model.Principal{User: &model.User{ID: "other", Email: "o@example.com"}}
	gt.True(t, errors.Is(a.RevokeToken(ctx, other, tok.ID), model.ErrNotFound))

	c.Advance(2 * time.Hour)
	_, err = a.AuthenticateToken(ctx, raw)
	gt.True(t, errors.Is(err, model.ErrUnauthenticated))

	gt.NoError(t, a.RevokeToken(ctx, session, tok.ID))
	gt.A(t, gt.R1(a.ListTokens(ctx, session)).NoError(t)).Length(0)

	_, _, err = a.CreateToken(ctx, session, usecase.CreateTokenInput{Name: "x", Kind: model.TokenPersonal, Scope: model.ScopeRead, TTL: -time.Second})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	_, _, err = a.CreateToken(ctx, session, usecase.CreateTokenInput{Name: "", Kind: model.TokenPersonal, Scope: model.ScopeRead})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestDevUser(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	a := gt.R1(usecase.NewAuth(repo, nil, usecase.AuthConfig{SessionTTL: time.Hour})).NoError(t)
	gt.False(t, a.IsNoAuthn())
	gt.False(t, a.HasProvider())
	_, err := a.DevPrincipal(ctx)
	gt.True(t, errors.Is(err, model.ErrUnauthenticated))
	_, err = a.LoginURL("s", "n")
	gt.Error(t, err)

	gt.NoError(t, a.EnableDevUser(ctx, "Dev@localhost", "Dev"))
	gt.True(t, a.IsNoAuthn())
	p := gt.R1(a.DevPrincipal(ctx)).NoError(t)
	gt.Equal(t, p.User.Email, "dev@localhost")

	// idempotent across restarts
	gt.NoError(t, a.EnableDevUser(ctx, "dev@localhost", "Dev"))
	gt.A(t, gt.R1(repo.ListUsers(ctx)).NoError(t)).Length(1)
}
