package http_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/m-mizutani/gt"
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
	c.expect("POST", "/api/v1/workspaces", map[string]string{"name": "x"}, http.StatusForbidden, nil)

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
		NoAuthn     bool `json:"no_authn"`
		SystemAdmin bool `json:"system_admin"`
	}
	c.expect("GET", "/api/auth/me", nil, http.StatusOK, &me)
	gt.True(t, me.NoAuthn)
	gt.True(t, me.SystemAdmin)
}
