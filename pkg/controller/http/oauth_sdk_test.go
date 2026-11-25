//go:build mcp_go_client_oauth

// The MCP Go SDK builds its OAuth client only with this tag; CI and
// `task test` set it.

package http_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/m-mizutani/gt"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// connectOAuth connects an MCP client to endpoint with the SDK's OAuth
// client; approver answers the consent screen.
func connectOAuth(t *testing.T, o *oauthTS, endpoint string, approver *client) (*mcp.ClientSession, *auth.AuthorizationCodeHandler, error) {
	t.Helper()
	hc := o.hostClient()
	handler := gt.R1(auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName: "Claude Code (papers)", RedirectURIs: []string{oauthRedirect}, TokenEndpointAuthMethod: "none",
		}},
		Client: hc,
		AuthorizationCodeFetcher: func(_ context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			u := gt.R1(url.Parse(args.URL)).NoError(t)
			// the browser opens the consent screen on --base-url
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
			return &auth.AuthorizationResult{Code: back.Query().Get("code"), State: back.Query().Get("state")}, nil
		},
	})).NoError(t)
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: hc, OAuthHandler: handler}, nil)
	if err == nil {
		t.Cleanup(func() { _ = session.Close() })
	}
	return session, handler, err
}

// T10, T27, T3, T13: the whole flow with the SDK's OAuth client, on either
// host; the token shows in the list and stops working once revoked.
func TestOAuthFlowWithSDK(t *testing.T) {
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
			session, handler, err := connectOAuth(t, o, tc.mcpHost(o)+"/mcp", alice)
			gt.NoError(t, err).Required()
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
			ts := gt.R1(handler.TokenSource(context.Background())).NoError(t)
			access := gt.R1(ts.Token()).NoError(t).AccessToken
			mcpCall := func() int {
				req := gt.R1(http.NewRequest("POST", tc.mcpHost(o)+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))).NoError(t)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				req.Header.Set("Authorization", "Bearer "+access)
				resp := gt.R1(o.hostClient().Do(req)).NoError(t)
				_ = resp.Body.Close()
				return resp.StatusCode
			}
			gt.Equal(t, mcpCall(), http.StatusOK)
			alice.expect("DELETE", "/api/v1/tokens/"+tok.ID, nil, http.StatusNoContent, nil)
			gt.Equal(t, mcpCall(), http.StatusUnauthorized)
		})
	}
}
