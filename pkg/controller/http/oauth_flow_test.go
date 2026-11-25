package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/m-mizutani/gt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

var resourceMetadataRe = regexp.MustCompile(`resource_metadata="([^"]+)"`)

// oauthLogin does what an MCP client does to get a token for endpoint:
// read the 401, the protected resource metadata and the authorization server
// metadata, register itself, open the consent screen (answered by approver),
// and exchange the code with PKCE. It returns the access token.
func oauthLogin(t *testing.T, o *oauthTS, endpoint string, approver *client) string {
	t.Helper()
	hc := o.hostClient()
	getJSON := func(u string, v any) {
		t.Helper()
		resp := gt.R1(hc.Get(u)).NoError(t)
		defer func() { _ = resp.Body.Close() }()
		gt.Equal(t, resp.StatusCode, http.StatusOK)
		gt.NoError(t, json.NewDecoder(resp.Body).Decode(v))
	}

	// 1. /mcp without a token names its protected resource metadata
	resp := gt.R1(hc.Post(endpoint, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))).NoError(t)
	_ = resp.Body.Close()
	gt.Equal(t, resp.StatusCode, http.StatusUnauthorized)
	m := resourceMetadataRe.FindStringSubmatch(resp.Header.Get("WWW-Authenticate"))
	if m == nil {
		t.Fatalf("no resource_metadata in %q", resp.Header.Get("WWW-Authenticate"))
	}

	// 2. the protected resource metadata names the resource and its issuer
	var prm struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	getJSON(m[1], &prm)
	gt.Equal(t, prm.Resource, endpoint)
	gt.A(t, prm.AuthorizationServers).Length(1)

	// 3. the authorization server metadata
	var asm struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		RegistrationEndpoint  string `json:"registration_endpoint"`
	}
	issuer := prm.AuthorizationServers[0]
	getJSON(strings.TrimSuffix(issuer, "/")+"/.well-known/oauth-authorization-server", &asm)
	gt.Equal(t, asm.Issuer, issuer)

	// 4. dynamic client registration
	reg := gt.R1(json.Marshal(map[string]any{
		"client_name": "Claude Code (papers)", "redirect_uris": []string{oauthRedirect}, "token_endpoint_auth_method": "none",
	})).NoError(t)
	resp = gt.R1(hc.Post(asm.RegistrationEndpoint, "application/json", strings.NewReader(string(reg)))).NoError(t)
	gt.Equal(t, resp.StatusCode, http.StatusCreated)
	clientID := readJSON[map[string]any](t, resp)["client_id"].(string)
	_ = resp.Body.Close()

	// 5. the consent screen opens on --base-url
	cfg := oauth2.Config{
		ClientID:    clientID,
		Endpoint:    oauth2.Endpoint{AuthURL: asm.AuthorizationEndpoint, TokenURL: asm.TokenEndpoint, AuthStyle: oauth2.AuthStyleInParams},
		RedirectURL: oauthRedirect,
		Scopes:      []string{"read", "write"},
	}
	verifier := oauth2.GenerateVerifier()
	resource := oauth2.SetAuthURLParam("resource", prm.Resource)
	u := gt.R1(url.Parse(cfg.AuthCodeURL("st", oauth2.S256ChallengeOption(verifier), resource))).NoError(t)
	gt.Equal(t, u.Scheme+"://"+u.Host+u.Path, o.base+"/oauth/authorize")
	var info struct {
		ClientName   string `json:"client_name"`
		RedirectHost string `json:"redirect_host"`
		Scope        string `json:"scope"`
	}
	approver.expect("GET", "/api/v1/oauth/authorize?"+u.RawQuery, nil, http.StatusOK, &info)
	gt.Equal(t, info.ClientName, "Claude Code (papers)")
	gt.Equal(t, info.RedirectHost, "localhost:51234")
	gt.Equal(t, info.Scope, "write")
	back := gt.R1(url.Parse(decide(t, approver, u.Query(), "allow", http.StatusOK))).NoError(t)
	gt.Equal(t, back.Query().Get("state"), "st")

	// 6. exchange the code with the verifier
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, hc)
	tok := gt.R1(cfg.Exchange(ctx, back.Query().Get("code"), oauth2.VerifierOption(verifier), resource)).NoError(t)
	gt.Equal(t, tok.TokenType, "Bearer")
	return tok.AccessToken
}

// T10, T27, T3, T13: the whole flow an MCP client follows, on either host;
// the token shows in the list and stops working once revoked.
func TestOAuthFlow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sameHost bool
		mcpHost  func(o *oauthTS) string
	}{
		{"T10 one host", true, func(o *oauthTS) string { return o.base }},
		{"T27 through --mcp-base-url", false, func(o *oauthTS) string { return o.mcp }},
		{"T27 through --base-url", false, func(o *oauthTS) string { return o.base }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := newOAuthServer(t, authGoogle, tc.sameHost)
			alice := o.login("alice@example.com")
			endpoint := tc.mcpHost(o) + "/mcp"
			access := oauthLogin(t, o, endpoint, alice)

			c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			hc := &http.Client{Transport: &bearerTransport{token: access, base: o.hostClient().Transport}}
			session, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: hc}, nil)
			gt.NoError(t, err).Required()
			t.Cleanup(func() { _ = session.Close() })
			_, err = session.ListTools(context.Background(), nil)
			gt.NoError(t, err)

			var list struct {
				Tokens []struct {
					ID    string           `json:"id"`
					Name  string           `json:"name"`
					Kind  model.TokenKind  `json:"kind"`
					Scope model.TokenScope `json:"scope"`
					OAuth *struct {
						ClientName  string `json:"client_name"`
						RedirectURI string `json:"redirect_uri"`
					} `json:"oauth"`
				} `json:"tokens"`
			}
			alice.expect("GET", "/api/v1/tokens", nil, http.StatusOK, &list)
			gt.A(t, list.Tokens).Length(1)
			tok := list.Tokens[0]
			gt.Equal(t, tok.Name, "Claude Code (papers)")
			gt.Equal(t, tok.Kind, model.TokenAgent)
			gt.Equal(t, tok.Scope, model.ScopeWrite)
			if tok.OAuth == nil {
				t.Fatal("the token has no oauth field")
			}
			gt.Equal(t, tok.OAuth.ClientName, "Claude Code (papers)")
			gt.Equal(t, tok.OAuth.RedirectURI, oauthRedirect)

			// T13: once revoked, /mcp refuses the token
			mcpCall := func() int {
				req := gt.R1(http.NewRequest("POST", endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))).NoError(t)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				resp := gt.R1(hc.Do(req)).NoError(t)
				_ = resp.Body.Close()
				return resp.StatusCode
			}
			gt.Equal(t, mcpCall(), http.StatusOK)
			alice.expect("DELETE", "/api/v1/tokens/"+tok.ID, nil, http.StatusNoContent, nil)
			gt.Equal(t, mcpCall(), http.StatusUnauthorized)
		})
	}
}
