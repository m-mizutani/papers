package http_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func TestLoginFlowAndSession(t *testing.T) {
	ts := newTestServer(t, false)
	c := ts.login("alice@example.com")

	var me struct {
		User struct {
			Email string `json:"email"`
		} `json:"user"`
		SystemAdmin bool `json:"system_admin"`
		NoAuthn     bool `json:"no_authn"`
	}
	c.expect("GET", "/api/auth/me", nil, http.StatusOK, &me)
	gt.Equal(t, me.User.Email, "alice@example.com")
	gt.False(t, me.SystemAdmin)
	gt.False(t, me.NoAuthn)

	// logout requires the CSRF header
	c.csrf = false
	c.expect("POST", "/api/auth/logout", nil, http.StatusForbidden, nil)
	c.csrf = true
	c.expect("POST", "/api/auth/logout", nil, http.StatusOK, nil)
	c.expect("GET", "/api/auth/me", nil, http.StatusUnauthorized, nil)
}

func TestLoginRejections(t *testing.T) {
	ts := newTestServer(t, false)
	c := ts.anonymous()

	// state mismatch
	c.expect("GET", "/api/auth/callback?state=x&code=y", nil, http.StatusBadRequest, nil)

	// a disallowed domain is redirected to the login page with an error
	c2 := ts.anonymous()
	c2.hc.Jar = &jar{cookies: map[string]*http.Cookie{}}
	ts.idp.claims["evil"] = claimsFor("eve@evil.example")
	resp := c2.do("GET", "/api/auth/login", nil)
	loc := gt.R1(url.Parse(resp.Header.Get("Location"))).NoError(t)
	resp = c2.do("GET", "/api/auth/callback?state="+loc.Query().Get("state")+"&code=evil", nil)
	gt.Equal(t, resp.StatusCode, http.StatusFound)
	gt.Equal(t, resp.Header.Get("Location"), "/login?error=not_allowed")

	// open redirects are refused
	c3 := ts.anonymous()
	c3.hc.Jar = &jar{cookies: map[string]*http.Cookie{}}
	ts.idp.claims["ok"] = claimsFor("bob@example.com")
	resp = c3.do("GET", "/api/auth/login?return_to=//evil.example", nil)
	loc = gt.R1(url.Parse(resp.Header.Get("Location"))).NoError(t)
	resp = c3.do("GET", "/api/auth/callback?state="+loc.Query().Get("state")+"&code=ok", nil)
	gt.Equal(t, resp.Header.Get("Location"), "/")
}

func TestAPIRequiresAuthAndCSRF(t *testing.T) {
	ts := newTestServer(t, false)
	anon := ts.anonymous()
	anon.expect("GET", "/api/v1/workspaces", nil, http.StatusUnauthorized, nil)

	c := ts.login("alice@example.com")
	c.expect("GET", "/api/v1/workspaces", nil, http.StatusOK, nil)
	c.csrf = false
	c.expect("POST", "/api/v1/workspaces/team/papers", map[string]string{"title": "x"}, http.StatusForbidden, nil)

	bad := &client{ts: ts, hc: &http.Client{}, bearer: "pap_nope.nope"}
	resp := bad.do("GET", "/api/v1/workspaces", nil)
	gt.Equal(t, resp.StatusCode, http.StatusUnauthorized)
	gt.Equal(t, resp.Header.Get("WWW-Authenticate"), `Bearer realm="papers"`)
}

func TestDevModeLogin(t *testing.T) {
	ts := newTestServer(t, true)
	c := ts.anonymous()
	resp := c.do("GET", "/api/auth/login?return_to=/w/1", nil)
	gt.Equal(t, resp.StatusCode, http.StatusFound)
	gt.Equal(t, resp.Header.Get("Location"), "/w/1")

	var me struct {
		NoAuthn bool `json:"no_authn"`
	}
	c.expect("GET", "/api/auth/me", nil, http.StatusOK, &me)
	gt.True(t, me.NoAuthn)
}

type anonMe struct {
	User struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		Name      string `json:"name"`
		Anonymous bool   `json:"anonymous"`
	} `json:"user"`
	NoAuthn   bool `json:"no_authn"`
	Anonymous bool `json:"anonymous"`
}

// browser is a client with an empty cookie jar, like a first visit.
func (ts *testServer) browser() *client {
	c := ts.anonymous()
	c.hc.Jar = &jar{cookies: map[string]*http.Cookie{}}
	return c
}

func sessionSetCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == "papers_session" {
			return c
		}
	}
	return nil
}

func userCount(t *testing.T, ts *testServer) int {
	t.Helper()
	return len(gt.R1(ts.repo.ListUsers(context.Background())).NoError(t))
}

func TestAnonymousFirstVisit(t *testing.T) {
	ts := newTestServerMode(t, authAnonymous, nil)
	c := ts.browser()

	resp := c.do("GET", "/api/auth/me", nil)
	gt.Equal(t, resp.StatusCode, http.StatusOK)
	ck := sessionSetCookie(resp)
	gt.NotNil(t, ck)
	gt.True(t, ck.HttpOnly)
	gt.Equal(t, ck.SameSite, http.SameSiteLaxMode)
	gt.Equal(t, ck.Path, "/")
	gt.N(t, ck.MaxAge).Greater(3500).Less(3601)
	var me anonMe
	gt.NoError(t, decodeBody(resp, &me))
	gt.True(t, me.Anonymous)
	gt.False(t, me.NoAuthn)
	gt.True(t, me.User.Anonymous)
	gt.True(t, strings.HasSuffix(me.User.Email, "@anonymous.invalid"))
	gt.True(t, strings.HasPrefix(me.User.Name, "Anonymous "))

	// the cookie is reused: same user, no new cookie, no new user
	resp = c.do("GET", "/api/v1/workspaces", nil)
	gt.Equal(t, resp.StatusCode, http.StatusOK)
	gt.Nil(t, sessionSetCookie(resp))
	resp = c.do("GET", "/api/auth/me", nil)
	gt.Nil(t, sessionSetCookie(resp))
	var again anonMe
	gt.NoError(t, decodeBody(resp, &again))
	gt.Equal(t, again.User.ID, me.User.ID)
	gt.Equal(t, userCount(t, ts), 1)

	// another browser is another person, and does not see the private paper
	var p struct {
		ID string `json:"id"`
	}
	c.expect("POST", "/api/v1/workspaces/team/papers", map[string]string{"title": "mine", "visibility": "private"}, http.StatusCreated, &p)
	other := ts.browser()
	var otherMe anonMe
	other.expect("GET", "/api/auth/me", nil, http.StatusOK, &otherMe)
	gt.NotEqual(t, otherMe.User.ID, me.User.ID)
	other.expect("GET", "/api/v1/workspaces/team/papers/"+p.ID, nil, http.StatusNotFound, nil)
	c.expect("GET", "/api/v1/workspaces/team/papers/"+p.ID, nil, http.StatusOK, nil)
}

func TestAnonymousInvalidCookie(t *testing.T) {
	ts := newTestServerMode(t, authAnonymous, nil)
	ctx := context.Background()

	// an expired session of an existing anonymous user
	cred, _, err := ts.auth.OpenAnonymousSession(ctx)
	gt.NoError(t, err)
	s := gt.R1(ts.repo.GetSession(ctx, cred.ID)).NoError(t)
	s.ExpiresAt = s.CreatedAt
	gt.NoError(t, ts.repo.PutSession(ctx, s))

	for name, value := range map[string]string{
		"malformed": "abc",
		"unknown":   string(model.NewSessionID()) + ".secret",
		"expired":   string(cred.ID) + "." + cred.Secret,
	} {
		t.Run(name, func(t *testing.T) {
			before := userCount(t, ts)
			c := ts.anonymous()
			req := gt.R1(http.NewRequest("GET", ts.srv.URL+"/api/auth/me", nil)).NoError(t)
			req.AddCookie(&http.Cookie{Name: "papers_session", Value: value})
			resp := gt.R1(c.hc.Do(req)).NoError(t)
			t.Cleanup(func() { _ = resp.Body.Close() })
			gt.Equal(t, resp.StatusCode, http.StatusOK)
			gt.NotNil(t, sessionSetCookie(resp))
			gt.Equal(t, userCount(t, ts), before+1)
		})
	}
}

func TestAnonymousDoesNotWeakenChecks(t *testing.T) {
	ts := newTestServerMode(t, authAnonymous, nil)

	// CSRF comes first: a forged write is refused and creates nobody
	c := ts.browser()
	c.csrf = false
	resp := c.do("POST", "/api/v1/workspaces/team/papers", map[string]string{"title": "x"})
	gt.Equal(t, resp.StatusCode, http.StatusForbidden)
	gt.Nil(t, sessionSetCookie(resp))
	gt.Equal(t, userCount(t, ts), 0)

	// MCP takes bearer tokens only
	resp = ts.browser().do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	gt.Equal(t, resp.StatusCode, http.StatusUnauthorized)
	gt.Equal(t, resp.Header.Get("WWW-Authenticate"), `Bearer realm="papers"`)
	// static files and the health check do not create users either
	ts.browser().expect("GET", "/", nil, http.StatusOK, nil)
	ts.browser().expect("GET", "/healthz", nil, http.StatusOK, nil)
	gt.Equal(t, userCount(t, ts), 0)

	// a bad bearer token is still refused rather than becoming a guest
	bad := &client{ts: ts, hc: &http.Client{}, bearer: "pap_nope.nope"}
	gt.Equal(t, bad.do("GET", "/api/v1/workspaces", nil).StatusCode, http.StatusUnauthorized)
	gt.Equal(t, userCount(t, ts), 0)
}

func TestAnonymousToken(t *testing.T) {
	ts := newTestServerMode(t, authAnonymous, nil)
	c := ts.browser()
	var me anonMe
	c.expect("GET", "/api/auth/me", nil, http.StatusOK, &me)
	tok := ts.token(c, model.TokenPersonal, model.ScopeWrite)
	tok.expect("GET", "/api/v1/workspaces", nil, http.StatusOK, nil)
	var viaToken anonMe
	tok.expect("GET", "/api/auth/me", nil, http.StatusOK, &viaToken)
	gt.Equal(t, viaToken.User.ID, me.User.ID)
	gt.Equal(t, userCount(t, ts), 1)
}

func TestAnonymousLoginRedirects(t *testing.T) {
	ts := newTestServerMode(t, authAnonymous, nil)
	resp := ts.anonymous().do("GET", "/api/auth/login?return_to=/ws/team", nil)
	gt.Equal(t, resp.StatusCode, http.StatusFound)
	gt.Equal(t, resp.Header.Get("Location"), "/ws/team")
}

func TestGoogleModeStillRequiresSession(t *testing.T) {
	ts := newTestServer(t, false)
	resp := ts.browser().do("GET", "/api/v1/workspaces", nil)
	gt.Equal(t, resp.StatusCode, http.StatusUnauthorized)
	gt.Nil(t, sessionSetCookie(resp))
	gt.Equal(t, userCount(t, ts), 0)
}
