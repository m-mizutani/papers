package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	httpctrl "github.com/m-mizutani/papers/pkg/controller/http"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/repository/memory"
	"github.com/m-mizutani/papers/pkg/service/attachment"
	"github.com/m-mizutani/papers/pkg/service/workspace"
	"github.com/m-mizutani/papers/pkg/usecase"
)

type fakeIdP struct {
	claims map[string]*model.ExternalClaims
}

func (f *fakeIdP) Name() model.IdentityProvider { return model.ProviderGoogle }
func (f *fakeIdP) AuthURL(state, nonce string) string {
	return "https://idp.example/auth?state=" + url.QueryEscape(state) + "&nonce=" + url.QueryEscape(nonce)
}
func (f *fakeIdP) Exchange(_ context.Context, code, _ string) (*model.ExternalClaims, error) {
	c, ok := f.claims[code]
	if !ok {
		return nil, goerr.New("bad code")
	}
	return c, nil
}

type testServer struct {
	t    *testing.T
	srv  *httptest.Server
	repo *memory.Repository
	uc   *usecase.UseCases
	auth *usecase.Auth
	idp  *fakeIdP
}

var testUI = fstest.MapFS{
	"index.html":    {Data: []byte("<html>papers</html>")},
	"assets/app.js": {Data: []byte("console.log(1)")},
}

type authMode int

const (
	authGoogle authMode = iota
	authDev
	authAnonymous
)

func newTestServer(t *testing.T, devMode bool) *testServer {
	t.Helper()
	return newTestServerWith(t, devMode, nil)
}

// newTestServerWith lets a test change the use case config. Jobs run to the
// end before the request answers.
func newTestServerWith(t *testing.T, devMode bool, tune func(*usecase.Config)) *testServer {
	t.Helper()
	mode := authGoogle
	if devMode {
		mode = authDev
	}
	return newTestServerMode(t, mode, tune)
}

func newTestServerMode(t *testing.T, mode authMode, tune func(*usecase.Config)) *testServer {
	t.Helper()
	repo := memory.New(memory.WithEventBuffer(64))
	wss := workspace.Static(&model.Workspace{ID: "team", Name: "Team", Description: "Write runbooks as numbered lists.", Sandbox: model.Sandbox{ExternalResources: true}}, &model.Workspace{ID: "other", Name: "Other"})
	cfg := usecase.Config{MaxOpsPerBatch: 300, PaperTouchInterval: time.Minute, TrashRetention: 30 * 24 * time.Hour, JobLease: time.Minute, JobStep: 200}
	if tune != nil {
		tune(&cfg)
	}
	uc := gt.R1(usecase.New(repo, wss, attachment.NewMemory(), cfg, usecase.WithSyncJobs())).NoError(t)
	gt.NoError(t, uc.Init(context.Background())).Required()
	idp := &fakeIdP{claims: map[string]*model.ExternalClaims{}}
	var auth *usecase.Auth
	switch mode {
	case authDev:
		auth = gt.R1(usecase.NewAuth(repo, nil, usecase.AuthConfig{SessionTTL: time.Hour})).NoError(t)
		gt.NoError(t, auth.EnableDevUser(context.Background(), "dev@localhost", "Dev"))
	case authAnonymous:
		auth = gt.R1(usecase.NewAuth(repo, nil, usecase.AuthConfig{SessionTTL: time.Hour})).NoError(t)
		auth.EnableAnonymous()
	default:
		auth = gt.R1(usecase.NewAuth(repo, idp, usecase.AuthConfig{SessionTTL: time.Hour, AllowedDomains: []string{"example.com"}})).NoError(t)
	}
	h := gt.R1(httpctrl.New(uc, auth, httpctrl.WithMCP(true), httpctrl.WithStaticFS(testUI))).NoError(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &testServer{t: t, srv: srv, repo: repo, uc: uc, auth: auth, idp: idp}
}

// client is an HTTP client with a cookie jar or a bearer token.
type client struct {
	ts     *testServer
	hc     *http.Client
	bearer string
	csrf   bool
}

func (ts *testServer) anonymous() *client {
	return &client{ts: ts, hc: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, csrf: true}
}

type jar struct{ cookies map[string]*http.Cookie }

func (j *jar) SetCookies(_ *url.URL, cs []*http.Cookie) {
	for _, c := range cs {
		if c.MaxAge < 0 {
			delete(j.cookies, c.Name)
		} else {
			j.cookies[c.Name] = c
		}
	}
}
func (j *jar) Cookies(_ *url.URL) []*http.Cookie {
	var out []*http.Cookie
	for _, c := range j.cookies {
		out = append(out, c)
	}
	return out
}

// login performs the OAuth flow with the fake IdP and returns a session client.
func (ts *testServer) login(email string) *client {
	ts.t.Helper()
	c := ts.anonymous()
	c.hc.Jar = &jar{cookies: map[string]*http.Cookie{}}
	code := "code-" + email
	ts.idp.claims[code] = &model.ExternalClaims{Provider: model.ProviderGoogle, Subject: "sub-" + email, Email: email, EmailVerified: true, Name: strings.Split(email, "@")[0]}

	resp := c.do("GET", "/api/auth/login?return_to=/w/x", nil)
	gt.Equal(ts.t, resp.StatusCode, http.StatusFound)
	loc := gt.R1(url.Parse(resp.Header.Get("Location"))).NoError(ts.t)
	state := loc.Query().Get("state")

	resp = c.do("GET", "/api/auth/callback?state="+url.QueryEscape(state)+"&code="+code, nil)
	gt.Equal(ts.t, resp.StatusCode, http.StatusFound)
	gt.Equal(ts.t, resp.Header.Get("Location"), "/w/x")
	return c
}

func (ts *testServer) token(owner *client, kind model.TokenKind, scope model.TokenScope) *client {
	ts.t.Helper()
	var out struct {
		Secret string `json:"secret"`
	}
	owner.expect("POST", "/api/v1/tokens", map[string]any{"name": "t", "kind": kind, "scope": scope}, http.StatusCreated, &out)
	return &client{ts: ts, hc: &http.Client{}, bearer: out.Secret}
}

func (c *client) do(method, path string, body any) *http.Response {
	c.ts.t.Helper()
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		gt.NoError(c.ts.t, err)
		r = bytes.NewReader(data)
	}
	req := gt.R1(http.NewRequest(method, c.ts.srv.URL+path, r)).NoError(c.ts.t)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.csrf {
		req.Header.Set("X-Requested-With", "papers")
	}
	resp := gt.R1(c.hc.Do(req)).NoError(c.ts.t)
	c.ts.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// expect sends a request, asserts the status and decodes the JSON body.
func (c *client) expect(method, path string, body any, status int, out any) {
	c.ts.t.Helper()
	resp := c.do(method, path, body)
	data := gt.R1(io.ReadAll(resp.Body)).NoError(c.ts.t)
	if resp.StatusCode != status {
		c.ts.t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, status, data)
	}
	if out != nil {
		gt.NoError(c.ts.t, json.Unmarshal(data, out))
	}
}

func TestHealthzAndSPA(t *testing.T) {
	ts := newTestServer(t, true)
	c := ts.anonymous()

	var health map[string]string
	c.expect("GET", "/healthz", nil, http.StatusOK, &health)
	gt.Equal(t, health["status"], "ok")

	for _, p := range []string{"/", "/ws/team/p/def"} {
		resp := c.do("GET", p, nil)
		gt.Equal(t, resp.StatusCode, http.StatusOK)
		gt.Equal(t, string(gt.R1(io.ReadAll(resp.Body)).NoError(t)), "<html>papers</html>")
	}
	resp := c.do("GET", "/assets/app.js", nil)
	gt.Equal(t, resp.StatusCode, http.StatusOK)
	gt.True(t, strings.Contains(resp.Header.Get("Cache-Control"), "immutable"))
	gt.Equal(t, resp.Header.Get("X-Content-Type-Options"), "nosniff")

	// unknown API paths never fall back to the SPA
	gt.Equal(t, c.do("GET", "/api/nope", nil).StatusCode, http.StatusNotFound)
}

func TestNewRequiresDependencies(t *testing.T) {
	_, err := httpctrl.New(nil, nil)
	gt.Error(t, err)
}

func claimsFor(email string) *model.ExternalClaims {
	return &model.ExternalClaims{Provider: model.ProviderGoogle, Subject: "sub-" + email, Email: email, EmailVerified: true}
}

func decodeBody(resp *http.Response, out any) error {
	return json.NewDecoder(resp.Body).Decode(out)
}
