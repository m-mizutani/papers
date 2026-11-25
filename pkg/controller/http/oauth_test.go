package http_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/m-mizutani/gt"

	httpctrl "github.com/m-mizutani/papers/pkg/controller/http"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

const (
	oauthRedirect = "http://localhost:51234/callback"
	oauthVerifier = "verifier-verifier-verifier-verifier-verifier-0123"
)

func oauthChallenge(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// oauthTS is a test server whose --base-url is http://127.0.0.1:port and
// whose --mcp-base-url is http://localhost:port: one server reached by two
// host names, as with IAP and a VPN.
type oauthTS struct {
	*testServer
	base, mcp string
}

func newOAuthServer(t *testing.T, mode authMode, sameHost bool) *oauthTS {
	t.Helper()
	var base, mcpBase string
	ts := newTestServerOpts(t, mode, nil, func(u string) []httpctrl.Option {
		base = u
		mcpBase = strings.Replace(u, "127.0.0.1", "localhost", 1)
		if sameHost {
			mcpBase = ""
			return []httpctrl.Option{httpctrl.WithBaseURL(base + "/")}
		}
		return []httpctrl.Option{httpctrl.WithBaseURL(base), httpctrl.WithMCPBaseURL(mcpBase + "/")}
	})
	if mcpBase == "" {
		mcpBase = base
	}
	return &oauthTS{testServer: ts, base: base, mcp: mcpBase}
}

// hostClient sends every request to the test server whatever the URL's
// host is, so the Host header can name either public URL (or neither).
func (o *oauthTS) hostClient() *http.Client {
	addr := o.srv.Listener.Addr().String()
	return &http.Client{
		Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// get sends GET to path with the given Host.
func (o *oauthTS) get(t *testing.T, host, path string) *http.Response {
	t.Helper()
	req := gt.R1(http.NewRequest("GET", "http://"+host+path, nil)).NoError(t)
	resp := gt.R1(o.hostClient().Do(req)).NoError(t)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (o *oauthTS) post(t *testing.T, host, path, contentType string, body []byte) *http.Response {
	t.Helper()
	req := gt.R1(http.NewRequest("POST", "http://"+host+path, bytes.NewReader(body))).NoError(t)
	req.Header.Set("Content-Type", contentType)
	resp := gt.R1(o.hostClient().Do(req)).NoError(t)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func readJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	data := gt.R1(io.ReadAll(resp.Body)).NoError(t)
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("invalid JSON %s: %v", data, err)
	}
	return v
}

func hostOf(u string) string { return strings.TrimPrefix(u, "http://") }

func (o *oauthTS) register(t *testing.T, host string) string {
	t.Helper()
	body := gt.R1(json.Marshal(map[string]any{"client_name": "Claude Code (papers)", "redirect_uris": []string{oauthRedirect}})).NoError(t)
	resp := o.post(t, host, "/oauth/register", "application/json", body)
	gt.Equal(t, resp.StatusCode, http.StatusCreated)
	return readJSON[map[string]any](t, resp)["client_id"].(string)
}

func authorizeQuery(clientID, resource string) url.Values {
	return url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {oauthRedirect},
		"code_challenge": {oauthChallenge(oauthVerifier)}, "code_challenge_method": {"S256"},
		"state": {"st"}, "scope": {"read write"}, "resource": {resource},
	}
}

// decide answers the consent screen as c and returns the redirect URL.
func decide(t *testing.T, c *client, q url.Values, decision string, status int) string {
	t.Helper()
	body := map[string]any{"decision": decision, "scope": "write"}
	for _, k := range []string{"client_id", "redirect_uri", "response_type", "code_challenge", "code_challenge_method", "state", "resource"} {
		body[k] = q.Get(k)
	}
	var out struct {
		RedirectTo string `json:"redirect_to"`
	}
	c.expect("POST", "/api/v1/oauth/authorize", body, status, &out)
	return out.RedirectTo
}

func (o *oauthTS) exchange(t *testing.T, host string, form url.Values) *http.Response {
	t.Helper()
	return o.post(t, host, "/oauth/token", "application/x-www-form-urlencoded", []byte(form.Encode()))
}

// T11, T27, T28: the 401 and the metadata name the public URL the request
// came in on, and nothing for an unknown host.
func TestOAuthMetadata(t *testing.T) {
	o := newOAuthServer(t, authGoogle, false)
	a, b := hostOf(o.base), hostOf(o.mcp)

	t.Run("T11 401 points at the metadata", func(t *testing.T) {
		for _, tc := range []struct{ host, public string }{{a, o.base}, {b, o.mcp}} {
			resp := o.get(t, tc.host, "/mcp")
			gt.Equal(t, resp.StatusCode, http.StatusUnauthorized)
			gt.Equal(t, resp.Header.Get("WWW-Authenticate"), `Bearer realm="papers", resource_metadata="`+tc.public+`/.well-known/oauth-protected-resource/mcp"`)
			gt.Equal(t, resp.Header.Get("Cache-Control"), "no-store")
		}
	})

	t.Run("T27 metadata follows the host", func(t *testing.T) {
		for _, tc := range []struct{ host, public string }{{a, o.base}, {b, o.mcp}} {
			for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
				resp := o.get(t, tc.host, p)
				gt.Equal(t, resp.StatusCode, http.StatusOK)
				gt.Equal(t, resp.Header.Get("Cache-Control"), "no-store")
				prm := readJSON[map[string]any](t, resp)
				gt.Equal(t, prm["resource"], any(tc.public+"/mcp"))
				gt.Equal(t, prm["authorization_servers"], any([]any{tc.public}))
				gt.Equal(t, prm["bearer_methods_supported"], any([]any{"header"}))
				gt.Equal(t, prm["scopes_supported"], any([]any{"read", "write"}))
				gt.Equal(t, prm["resource_name"], any("papers"))
			}
			resp := o.get(t, tc.host, "/.well-known/oauth-authorization-server")
			gt.Equal(t, resp.StatusCode, http.StatusOK)
			gt.Equal(t, resp.Header.Get("Cache-Control"), "no-store")
			asm := readJSON[map[string]any](t, resp)
			gt.Equal(t, asm["issuer"], any(tc.public))
			// the consent screen is always on --base-url, where the cookie is
			gt.Equal(t, asm["authorization_endpoint"], any(o.base+"/oauth/authorize"))
			gt.Equal(t, asm["token_endpoint"], any(tc.public+"/oauth/token"))
			gt.Equal(t, asm["registration_endpoint"], any(tc.public+"/oauth/register"))
			gt.Equal(t, asm["response_types_supported"], any([]any{"code"}))
			gt.Equal(t, asm["grant_types_supported"], any([]any{"authorization_code"}))
			gt.Equal(t, asm["code_challenge_methods_supported"], any([]any{"S256"}))
			gt.Equal(t, asm["token_endpoint_auth_methods_supported"], any([]any{"none"}))
			gt.Equal(t, asm["scopes_supported"], any([]any{"read", "write"}))
		}
	})

	t.Run("T27 unknown host gets no metadata", func(t *testing.T) {
		for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server"} {
			gt.Equal(t, o.get(t, "evil.example", p).StatusCode, http.StatusNotFound)
		}
		resp := o.get(t, "evil.example", "/mcp")
		gt.Equal(t, resp.StatusCode, http.StatusUnauthorized)
		gt.Equal(t, resp.Header.Get("WWW-Authenticate"), `Bearer realm="papers"`)
		gt.Equal(t, resp.Header.Get("Cache-Control"), "no-store")
	})

	t.Run("T27 unknown resource is refused", func(t *testing.T) {
		alice := o.login("alice@example.com")
		clientID := o.register(t, b)
		evil := authorizeQuery(clientID, "https://evil.example/mcp")
		var e errorBody
		alice.expect("GET", "/api/v1/oauth/authorize?"+evil.Encode(), nil, http.StatusBadRequest, &e)
		gt.Equal(t, e.Error.Message, model.OAuthInvalidTarget)
		decide(t, alice, evil, "allow", http.StatusBadRequest)

		// a code made for a known resource cannot be exchanged for another
		redirect := decide(t, alice, authorizeQuery(clientID, o.mcp+"/mcp"), "allow", http.StatusOK)
		code := gt.R1(url.Parse(redirect)).NoError(t).Query().Get("code")
		resp := o.exchange(t, b, url.Values{
			"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthRedirect},
			"client_id": {clientID}, "code_verifier": {oauthVerifier}, "resource": {"https://evil.example/mcp"},
		})
		gt.Equal(t, resp.StatusCode, http.StatusBadRequest)
		gt.Equal(t, readJSON[map[string]any](t, resp)["error"], any("invalid_target"))
	})
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// T12: requests whose redirect cannot be trusted are answered with an
// error, never a redirect; a denial goes back with access_denied.
func TestOAuthAuthorizeAPI(t *testing.T) {
	o := newOAuthServer(t, authGoogle, false)
	alice := o.login("alice@example.com")
	clientID := o.register(t, hostOf(o.mcp))

	bad := authorizeQuery(clientID, "")
	bad.Set("redirect_uri", "http://localhost:9/other")
	resp := alice.do("GET", "/api/v1/oauth/authorize?"+bad.Encode(), nil)
	gt.Equal(t, resp.StatusCode, http.StatusBadRequest)
	gt.Equal(t, resp.Header.Get("Location"), "")
	e := readJSON[errorBody](t, resp)
	gt.Equal(t, e.Error.Code, "invalid_argument")
	gt.Equal(t, e.Error.Message, model.OAuthInvalidRedirectURI)

	for _, tc := range []struct {
		key, value, code string
	}{
		{"client_id", "nope", model.OAuthInvalidClient},
		{"response_type", "token", model.OAuthUnsupportedResponseType},
		{"code_challenge_method", "plain", model.OAuthInvalidCodeChallenge},
	} {
		q := authorizeQuery(clientID, "")
		q.Set(tc.key, tc.value)
		var e errorBody
		alice.expect("GET", "/api/v1/oauth/authorize?"+q.Encode(), nil, http.StatusBadRequest, &e)
		gt.Equal(t, e.Error.Message, tc.code)
	}

	denied := gt.R1(url.Parse(decide(t, alice, authorizeQuery(clientID, ""), "deny", http.StatusOK))).NoError(t)
	gt.Equal(t, denied.Query().Get("error"), "access_denied")
	gt.Equal(t, denied.Query().Get("state"), "st")
	gt.Equal(t, denied.Query().Get("code"), "")

	// the consent API needs a session: none, or a token, is refused
	anon := o.anonymous()
	anon.expect("GET", "/api/v1/oauth/authorize?"+authorizeQuery(clientID, "").Encode(), nil, http.StatusUnauthorized, nil)
	tok := o.token(alice, model.TokenAgent, model.ScopeWrite)
	decide(t, tok, authorizeQuery(clientID, ""), "allow", http.StatusForbidden)
	decide(t, alice, authorizeQuery(clientID, ""), "maybe", http.StatusBadRequest)
}

// T15: what registration refuses; nothing is stored either way.
func TestOAuthRegister(t *testing.T) {
	o := newOAuthServer(t, authGoogle, true)
	host := hostOf(o.base)
	reg := func(body any) *http.Response {
		return o.post(t, host, "/oauth/register", "application/json", gt.R1(json.Marshal(body)).NoError(t))
	}

	resp := reg(map[string]any{"redirect_uris": []string{oauthRedirect}, "grant_types": []string{"authorization_code"}})
	gt.Equal(t, resp.StatusCode, http.StatusCreated)
	gt.Equal(t, resp.Header.Get("Cache-Control"), "no-store")
	ok := readJSON[map[string]any](t, resp)
	gt.True(t, strings.HasPrefix(ok["client_id"].(string), "pcl_"))
	gt.Equal(t, ok["client_name"], any("MCP client"))
	gt.Equal(t, ok["redirect_uris"], any([]any{oauthRedirect}))
	gt.Equal(t, ok["grant_types"], any([]any{"authorization_code"}))
	gt.Equal(t, ok["response_types"], any([]any{"code"}))
	gt.Equal(t, ok["token_endpoint_auth_method"], any("none"))
	gt.NotNil(t, ok["client_id_issued_at"])

	six := []string{oauthRedirect, oauthRedirect, oauthRedirect, oauthRedirect, oauthRedirect, oauthRedirect}
	for _, tc := range []struct {
		name string
		body any
		code string
	}{
		{"no redirect", map[string]any{"redirect_uris": []string{}}, "invalid_redirect_uri"},
		{"six redirects", map[string]any{"redirect_uris": six}, "invalid_redirect_uri"},
		{"http on another host", map[string]any{"redirect_uris": []string{"http://example.com/cb"}}, "invalid_redirect_uri"},
		{"fragment", map[string]any{"redirect_uris": []string{"https://example.com/cb#x"}}, "invalid_redirect_uri"},
		{"513 bytes", map[string]any{"redirect_uris": []string{"https://example.com/" + strings.Repeat("a", 513-len("https://example.com/"))}}, "invalid_redirect_uri"},
		{"long name", map[string]any{"client_name": strings.Repeat("n", 65), "redirect_uris": []string{oauthRedirect}}, "invalid_client_metadata"},
		{"not JSON", "nope", "invalid_client_metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := reg(tc.body)
			gt.Equal(t, resp.StatusCode, http.StatusBadRequest)
			gt.Equal(t, readJSON[map[string]any](t, resp)["error"], any(tc.code))
		})
	}
}

// T16: token endpoint errors and headers.
func TestOAuthTokenEndpoint(t *testing.T) {
	o := newOAuthServer(t, authGoogle, true)
	host := hostOf(o.base)
	alice := o.login("alice@example.com")
	clientID := o.register(t, host)

	resp := o.exchange(t, host, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"x"}})
	gt.Equal(t, resp.StatusCode, http.StatusBadRequest)
	gt.Equal(t, resp.Header.Get("Cache-Control"), "no-store")
	gt.Equal(t, readJSON[map[string]any](t, resp)["error"], any("unsupported_grant_type"))

	resp = o.post(t, host, "/oauth/token", "application/json", []byte(`{"grant_type":"authorization_code"}`))
	gt.Equal(t, resp.StatusCode, http.StatusBadRequest)
	gt.Equal(t, readJSON[map[string]any](t, resp)["error"], any("invalid_request"))

	redirect := decide(t, alice, authorizeQuery(clientID, ""), "allow", http.StatusOK)
	code := gt.R1(url.Parse(redirect)).NoError(t).Query().Get("code")
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthRedirect},
		"client_id": {clientID}, "code_verifier": {oauthVerifier},
	}
	resp = o.exchange(t, host, form)
	gt.Equal(t, resp.StatusCode, http.StatusOK)
	gt.Equal(t, resp.Header.Get("Cache-Control"), "no-store")
	tok := readJSON[map[string]any](t, resp)
	gt.True(t, strings.HasPrefix(tok["access_token"].(string), "pap_"))
	gt.Equal(t, tok["token_type"], any("Bearer"))
	gt.Equal(t, tok["expires_in"], any(float64(7776000)))
	gt.Equal(t, tok["scope"], any("write"))

	// the access token is an ordinary bearer token for the REST API too
	bearer := &client{ts: o.testServer, hc: &http.Client{}, bearer: tok["access_token"].(string)}
	bearer.expect("GET", "/api/v1/workspaces", nil, http.StatusOK, nil)

	// the code works once, and presenting it again revokes the token it gave
	resp = o.exchange(t, host, form)
	gt.Equal(t, resp.StatusCode, http.StatusBadRequest)
	gt.Equal(t, readJSON[map[string]any](t, resp)["error"], any("invalid_grant"))
	bearer.expect("GET", "/api/v1/workspaces", nil, http.StatusUnauthorized, nil)
}

// T14, T29: existing ways keep working, and /api/auth/me names the URL the
// settings page shows.
func TestOAuthKeepsExistingAuth(t *testing.T) {
	t.Run("no-authn MCP without a token", func(t *testing.T) {
		o := newOAuthServer(t, authDev, true)
		req := gt.R1(http.NewRequest("POST", o.base+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))).NoError(t)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp := gt.R1(http.DefaultClient.Do(req)).NoError(t)
		defer func() { _ = resp.Body.Close() }()
		gt.Equal(t, resp.StatusCode, http.StatusOK)
	})

	t.Run("T29 mcp_base_url", func(t *testing.T) {
		var me struct {
			MCPBaseURL string `json:"mcp_base_url"`
		}
		o := newOAuthServer(t, authGoogle, false)
		o.login("alice@example.com").expect("GET", "/api/auth/me", nil, http.StatusOK, &me)
		gt.Equal(t, me.MCPBaseURL, o.mcp)

		same := newOAuthServer(t, authGoogle, true)
		same.login("alice@example.com").expect("GET", "/api/auth/me", nil, http.StatusOK, &me)
		gt.Equal(t, me.MCPBaseURL, same.base)
	})

	t.Run("no MCP, no OAuth endpoints", func(t *testing.T) {
		ts := newTestServerOpts(t, authGoogle, nil, func(u string) []httpctrl.Option {
			return []httpctrl.Option{httpctrl.WithBaseURL(u), httpctrl.WithMCP(false)}
		})
		c := ts.anonymous()
		gt.Equal(t, c.do("POST", "/oauth/register", map[string]any{"redirect_uris": []string{oauthRedirect}}).StatusCode, http.StatusMethodNotAllowed)
		resp := c.do("GET", "/.well-known/oauth-authorization-server", nil)
		gt.Equal(t, resp.Header.Get("Content-Type"), "text/html; charset=utf-8")
		alice := ts.login("alice@example.com")
		alice.expect("GET", "/api/v1/oauth/authorize", nil, http.StatusNotFound, nil)
	})
}
