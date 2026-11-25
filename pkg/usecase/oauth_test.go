package usecase_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/repository/memory"
	"github.com/m-mizutani/papers/pkg/usecase"
)

const (
	testBase      = "https://papers.example.com"
	testMCPBase   = "https://papers-direct.example.com"
	testRedirect  = "http://localhost:51234/callback"
	testVerifier  = "verifier-0123456789-0123456789-0123456789-abcdef"
	otherVerifier = "verifier-9999999999-9999999999-9999999999-abcdef"
)

func challengeOf(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// writeCounter counts token writes, to show that registration stores
// nothing.
type writeCounter struct {
	interfaces.Repository
	mu     sync.Mutex
	writes int
}

func (w *writeCounter) count() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
}

func (w *writeCounter) PutToken(ctx context.Context, t *model.APIToken) error {
	w.count()
	return w.Repository.PutToken(ctx, t)
}

func (w *writeCounter) UpdateToken(ctx context.Context, id model.TokenID, fn func(*model.APIToken) error) (*model.APIToken, error) {
	w.count()
	return w.Repository.UpdateToken(ctx, id, fn)
}

type oauthFixture struct {
	auth  *usecase.Auth
	oauth *usecase.OAuth
	repo  *writeCounter
	clock *clock
	alice *model.Principal
}

func newOAuth(t *testing.T) *oauthFixture {
	t.Helper()
	repo := &writeCounter{Repository: memory.New()}
	c := &clock{now: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	a, err := usecase.NewAuth(repo, nil, usecase.AuthConfig{SessionTTL: time.Hour}, usecase.WithAuthClock(c.Now))
	gt.NoError(t, err).Required()
	gt.NoError(t, a.EnableDevUser(context.Background(), "alice@example.com", "Alice")).Required()
	alice := gt.R1(a.DevPrincipal(context.Background())).NoError(t)
	o := usecase.NewOAuth(a, []string{testBase + "/mcp", testMCPBase + "/mcp"})
	return &oauthFixture{auth: a, oauth: o, repo: repo, clock: c, alice: alice}
}

func (f *oauthFixture) request(t *testing.T) usecase.AuthorizeRequest {
	t.Helper()
	id, _, err := f.oauth.RegisterClient("Claude Code (papers)", []string{testRedirect})
	gt.NoError(t, err).Required()
	return usecase.AuthorizeRequest{
		ResponseType: "code", ClientID: id, RedirectURI: testRedirect,
		CodeChallenge: challengeOf(testVerifier), CodeChallengeMethod: "S256",
		State: "st", Resource: testMCPBase + "/mcp",
	}
}

// allow approves req and returns the code from the redirect.
func (f *oauthFixture) allow(t *testing.T, req usecase.AuthorizeRequest, scope model.TokenScope) string {
	t.Helper()
	to, err := f.oauth.Decide(context.Background(), f.alice, req, scope, true)
	gt.NoError(t, err).Required()
	u := gt.R1(url.Parse(to)).NoError(t)
	gt.Equal(t, u.Query().Get("state"), "st")
	code := u.Query().Get("code")
	gt.True(t, strings.HasPrefix(code, usecase.OAuthCodePrefix))
	return code
}

func (f *oauthFixture) exchange(req usecase.AuthorizeRequest, code, verifier string) (*usecase.ExchangeResult, error) {
	return f.oauth.Exchange(context.Background(), usecase.ExchangeRequest{
		GrantType: "authorization_code", Code: code, RedirectURI: req.RedirectURI,
		ClientID: req.ClientID, CodeVerifier: verifier, Resource: req.Resource,
	})
}

func oauthCode(err error) string {
	var oe *model.OAuthError
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

// T1 / T3: allow, exchange, and the token authenticates as Alice's agent.
func TestOAuthFlow(t *testing.T) {
	ctx := context.Background()
	f := newOAuth(t)
	req := f.request(t)

	info := gt.R1(f.oauth.CheckAuthorize(req)).NoError(t)
	gt.Equal(t, info.ClientName, "Claude Code (papers)")
	gt.Equal(t, info.RedirectHost, "localhost:51234")
	gt.Equal(t, info.Scope, model.ScopeWrite)

	code := f.allow(t, req, model.ScopeRead)
	res := gt.R1(f.exchange(req, code, testVerifier)).NoError(t)
	gt.True(t, strings.HasPrefix(res.AccessToken, usecase.TokenPrefix))
	gt.Equal(t, res.Scope, model.ScopeRead)
	gt.Equal(t, res.ExpiresIn, usecase.OAuthTokenTTL)

	p := gt.R1(f.auth.AuthenticateToken(ctx, res.AccessToken)).NoError(t)
	gt.Equal(t, p.User.ID, f.alice.User.ID)
	gt.Equal(t, p.Token.Kind, model.TokenAgent)
	gt.Equal(t, p.Token.Scope, model.ScopeRead)
	gt.Equal(t, p.Actor().Kind, model.ActorAgent)
	gt.Equal(t, p.Actor().AgentName, "Claude Code (papers)")

	list := gt.R1(f.auth.ListTokens(ctx, f.alice)).NoError(t)
	gt.A(t, list).Length(1)
	gt.Equal(t, list[0].Name, "Claude Code (papers)")
	gt.NotNil(t, list[0].OAuth)
	gt.Equal(t, list[0].OAuth.RedirectURI, testRedirect)
	gt.False(t, list[0].Pending())
	gt.True(t, list[0].ExpiresAt.Equal(f.clock.Now().Add(usecase.OAuthTokenTTL)))

	// The token is revoked like any other.
	gt.NoError(t, f.auth.RevokeToken(ctx, f.alice, list[0].ID))
	_, err := f.auth.AuthenticateToken(ctx, res.AccessToken)
	gt.True(t, errors.Is(err, model.ErrUnauthenticated))
}

// T2: requests the consent screen refuses, and nothing is stored.
func TestOAuthCheckAuthorizeRejects(t *testing.T) {
	f := newOAuth(t)
	base := f.request(t)
	long := usecase.OAuthClientIDPrefix + strings.Repeat("A", 1100)
	cases := []struct {
		name string
		mod  func(r *usecase.AuthorizeRequest)
		code string
	}{
		{"no prefix", func(r *usecase.AuthorizeRequest) {
			r.ClientID = strings.TrimPrefix(r.ClientID, usecase.OAuthClientIDPrefix)
		}, model.OAuthInvalidClient},
		{"not base64", func(r *usecase.AuthorizeRequest) { r.ClientID = usecase.OAuthClientIDPrefix + "!!!" }, model.OAuthInvalidClient},
		{"too long", func(r *usecase.AuthorizeRequest) { r.ClientID = long }, model.OAuthInvalidClient},
		{"unregistered redirect", func(r *usecase.AuthorizeRequest) { r.RedirectURI = "http://localhost:1/other" }, model.OAuthInvalidRedirectURI},
		{"token response type", func(r *usecase.AuthorizeRequest) { r.ResponseType = "token" }, model.OAuthUnsupportedResponseType},
		{"plain challenge", func(r *usecase.AuthorizeRequest) { r.CodeChallengeMethod = "plain" }, model.OAuthInvalidCodeChallenge},
		{"no challenge", func(r *usecase.AuthorizeRequest) { r.CodeChallenge = "" }, model.OAuthInvalidCodeChallenge},
		{"other resource", func(r *usecase.AuthorizeRequest) { r.Resource = "https://evil.example/mcp" }, model.OAuthInvalidTarget},
		{"relative resource", func(r *usecase.AuthorizeRequest) { r.Resource = "/mcp" }, model.OAuthInvalidTarget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.mod(&r)
			_, err := f.oauth.CheckAuthorize(r)
			gt.Equal(t, oauthCode(err), tc.code)
			gt.True(t, errors.Is(err, model.ErrInvalidArgument))
			_, err = f.oauth.Decide(context.Background(), f.alice, r, model.ScopeWrite, true)
			gt.Equal(t, oauthCode(err), tc.code)
		})
	}
	gt.Equal(t, f.repo.writes, 0)

	// Both of this server's MCP URLs, and none, are accepted.
	for _, res := range []string{testBase + "/mcp", testMCPBase + "/mcp", ""} {
		r := base
		r.Resource = res
		_, err := f.oauth.CheckAuthorize(r)
		gt.NoError(t, err)
	}
}

func TestOAuthScope(t *testing.T) {
	f := newOAuth(t)
	r := f.request(t)
	for scope, want := range map[string]model.TokenScope{"": model.ScopeWrite, "read": model.ScopeRead, "read write": model.ScopeWrite, "write": model.ScopeWrite, "other": model.ScopeWrite} {
		r.Scope = scope
		gt.Equal(t, gt.R1(f.oauth.CheckAuthorize(r)).NoError(t).Scope, want)
	}
	_, err := f.oauth.Decide(context.Background(), f.alice, r, "admin", true)
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestOAuthDeny(t *testing.T) {
	f := newOAuth(t)
	req := f.request(t)
	to := gt.R1(f.oauth.Decide(context.Background(), f.alice, req, model.ScopeWrite, false)).NoError(t)
	u := gt.R1(url.Parse(to)).NoError(t)
	gt.Equal(t, u.Query().Get("error"), "access_denied")
	gt.Equal(t, u.Query().Get("state"), "st")
	gt.Equal(t, u.Query().Get("code"), "")
	gt.Equal(t, f.repo.writes, 0)
}

func TestOAuthDecideNeedsSession(t *testing.T) {
	f := newOAuth(t)
	req := f.request(t)
	_, err := f.oauth.Decide(context.Background(), nil, req, model.ScopeWrite, true)
	gt.True(t, errors.Is(err, model.ErrUnauthenticated))
	withToken := *f.alice
	withToken.Token = &model.APIToken{ID: "t"}
	_, err = f.oauth.Decide(context.Background(), &withToken, req, model.ScopeWrite, true)
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))
}

// T4: a pending token is not listed and cannot authenticate.
func TestOAuthPendingTokenHidden(t *testing.T) {
	ctx := context.Background()
	f := newOAuth(t)
	code := f.allow(t, f.request(t), model.ScopeWrite)
	gt.A(t, gt.R1(f.auth.ListTokens(ctx, f.alice)).NoError(t)).Length(0)
	// The code itself is no bearer token.
	_, err := f.auth.AuthenticateToken(ctx, usecase.TokenPrefix+strings.TrimPrefix(code, usecase.OAuthCodePrefix))
	gt.True(t, errors.Is(err, model.ErrUnauthenticated))
}

// T5 - T8: what the token endpoint refuses.
func TestOAuthExchangeRejects(t *testing.T) {
	t.Run("T5 wrong verifier keeps the code pending", func(t *testing.T) {
		f := newOAuth(t)
		req := f.request(t)
		code := f.allow(t, req, model.ScopeWrite)
		_, err := f.exchange(req, code, otherVerifier)
		gt.Equal(t, oauthCode(err), model.OAuthInvalidGrant)
		gt.R1(f.exchange(req, code, testVerifier)).NoError(t)
	})
	t.Run("T6 code expiry", func(t *testing.T) {
		f := newOAuth(t)
		req := f.request(t)
		code := f.allow(t, req, model.ScopeWrite)
		f.clock.Advance(usecase.OAuthCodeTTL)
		_, err := f.exchange(req, code, testVerifier)
		gt.Equal(t, oauthCode(err), model.OAuthInvalidGrant)

		code = f.allow(t, req, model.ScopeWrite)
		f.clock.Advance(usecase.OAuthCodeTTL - time.Second)
		gt.R1(f.exchange(req, code, testVerifier)).NoError(t)
	})
	t.Run("T7 second exchange", func(t *testing.T) {
		ctx := context.Background()
		f := newOAuth(t)
		req := f.request(t)
		code := f.allow(t, req, model.ScopeWrite)
		first := gt.R1(f.exchange(req, code, testVerifier)).NoError(t)
		_, err := f.exchange(req, code, testVerifier)
		gt.Equal(t, oauthCode(err), model.OAuthInvalidGrant)
		gt.R1(f.auth.AuthenticateToken(ctx, first.AccessToken)).NoError(t)
	})
	t.Run("T8 redirect uri or client differs", func(t *testing.T) {
		f := newOAuth(t)
		req := f.request(t)
		code := f.allow(t, req, model.ScopeWrite)
		other := req
		other.RedirectURI = "http://localhost:1/other"
		_, err := f.exchange(other, code, testVerifier)
		gt.Equal(t, oauthCode(err), model.OAuthInvalidGrant)

		otherID, _, err := f.oauth.RegisterClient("Other", []string{testRedirect})
		gt.NoError(t, err)
		other = req
		other.ClientID = otherID
		_, err = f.exchange(other, code, testVerifier)
		gt.Equal(t, oauthCode(err), model.OAuthInvalidGrant)

		gt.R1(f.exchange(req, code, testVerifier)).NoError(t)
	})
	t.Run("malformed requests", func(t *testing.T) {
		f := newOAuth(t)
		req := f.request(t)
		code := f.allow(t, req, model.ScopeWrite)
		_, err := f.oauth.Exchange(context.Background(), usecase.ExchangeRequest{GrantType: "refresh_token"})
		gt.Equal(t, oauthCode(err), model.OAuthUnsupportedGrantType)
		_, err = f.oauth.Exchange(context.Background(), usecase.ExchangeRequest{GrantType: "authorization_code", Code: code})
		gt.Equal(t, oauthCode(err), model.OAuthInvalidRequest)
		_, err = f.exchange(req, "pac_nope", testVerifier)
		gt.Equal(t, oauthCode(err), model.OAuthInvalidGrant)
		_, err = f.exchange(req, usecase.OAuthCodePrefix+string(model.NewTokenID())+".x", testVerifier)
		gt.Equal(t, oauthCode(err), model.OAuthInvalidGrant)
		bad := req
		bad.Resource = "https://evil.example/mcp"
		_, err = f.exchange(bad, code, testVerifier)
		gt.Equal(t, oauthCode(err), model.OAuthInvalidTarget)
		bad = req
		bad.ClientID = "nope"
		_, err = f.exchange(bad, code, testVerifier)
		gt.Equal(t, oauthCode(err), model.OAuthInvalidClient)
	})
}

// T9: concurrent exchanges of one code: exactly one wins.
func TestOAuthConcurrentExchange(t *testing.T) {
	f := newOAuth(t)
	req := f.request(t)
	code := f.allow(t, req, model.ScopeWrite)
	const n = 8
	var wg sync.WaitGroup
	results := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.exchange(req, code, testVerifier)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	ok := 0
	for err := range results {
		if err == nil {
			ok++
		} else {
			gt.Equal(t, oauthCode(err), model.OAuthInvalidGrant)
		}
	}
	gt.Equal(t, ok, 1)
}

// Registration validates and stores nothing (T15 at the use case level, T19).
func TestOAuthRegisterClient(t *testing.T) {
	f := newOAuth(t)
	id, c, err := f.oauth.RegisterClient("  ", []string{testRedirect, "https://other.example.com/cb"})
	gt.NoError(t, err)
	gt.Equal(t, c.Name, usecase.DefaultOAuthClientName)
	parsed := gt.R1(usecase.ParseClientID(id)).NoError(t)
	gt.Equal(t, parsed.RedirectURIs, c.RedirectURIs)

	six := []string{}
	for range 6 {
		six = append(six, testRedirect)
	}
	for name, tc := range map[string]struct {
		name string
		uris []string
		code string
	}{
		"none":         {"x", nil, model.OAuthInvalidRedirectURI},
		"six":          {"x", six, model.OAuthInvalidRedirectURI},
		"plain http":   {"x", []string{"http://example.com/cb"}, model.OAuthInvalidRedirectURI},
		"fragment":     {"x", []string{"https://example.com/cb#x"}, model.OAuthInvalidRedirectURI},
		"user info":    {"x", []string{"https://a@example.com/cb"}, model.OAuthInvalidRedirectURI},
		"other scheme": {"x", []string{"myapp://cb"}, model.OAuthInvalidRedirectURI},
		"513 bytes":    {"x", []string{"https://example.com/" + strings.Repeat("a", 493)}, model.OAuthInvalidRedirectURI},
		"long name":    {strings.Repeat("n", 65), []string{testRedirect}, model.OAuthInvalidClientMetadata},
		"control":      {"a\nb", []string{testRedirect}, model.OAuthInvalidClientMetadata},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := f.oauth.RegisterClient(tc.name, tc.uris)
			gt.Equal(t, oauthCode(err), tc.code)
		})
	}
	// 512 bytes and 64 characters are fine.
	_, _, err = f.oauth.RegisterClient(strings.Repeat("名", 64), []string{"https://example.com/" + strings.Repeat("a", 492)})
	gt.NoError(t, err)
	for _, u := range []string{"http://127.0.0.1:8/cb", "http://[::1]:8/cb"} {
		_, _, err = f.oauth.RegisterClient("x", []string{u})
		gt.NoError(t, err)
	}
	gt.Equal(t, f.repo.writes, 0)
}
