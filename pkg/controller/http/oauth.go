package http

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
	"github.com/m-mizutani/papers/pkg/utils/errutil"
)

// OAuth for MCP clients. The browser opens the consent screen on
// --base-url, where the session cookie is. Everything an MCP client calls
// itself (metadata, registration, token, /mcp) answers with the public URL
// the request came in on, but only when its Host is the host of
// --base-url or --mcp-base-url: a relaying proxy on another host gets no
// metadata, so it cannot point a client's token requests at itself.

const (
	protectedResourcePath   = "/.well-known/oauth-protected-resource"
	authorizationServerPath = "/.well-known/oauth-authorization-server"

	maxOAuthRequestBody = 64 << 10
)

var oauthScopes = []string{string(model.ScopeRead), string(model.ScopeWrite)}

// WithBaseURL sets the public URL of the Web UI (--base-url). The OAuth
// consent screen is served there.
func WithBaseURL(u string) Option {
	return func(s *Server) { s.baseURL = strings.TrimRight(u, "/") }
}

// WithMCPBaseURL sets the public URL where MCP clients reach this server
// (--mcp-base-url). It defaults to the base URL.
func WithMCPBaseURL(u string) Option {
	return func(s *Server) { s.mcpBaseURL = strings.TrimRight(u, "/") }
}

func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// publicURL returns the public URL whose host the request was sent to, or
// "" when the Host is not one of this server's public URLs.
func (s *Server) publicURL(r *http.Request) string {
	host := strings.ToLower(r.Host)
	if host == "" {
		return ""
	}
	for _, u := range []string{s.baseURL, s.mcpBaseURL} {
		if u != "" && urlHost(u) == host {
			return u
		}
	}
	return ""
}

func (s *Server) oauthResources() []string {
	var out []string
	for _, u := range []string{s.baseURL, s.mcpBaseURL} {
		if u != "" {
			out = append(out, u+"/mcp")
		}
	}
	return out
}

func (s *Server) routeOAuth(r chi.Router) {
	r.Get(protectedResourcePath, s.handleProtectedResource)
	r.Get(protectedResourcePath+"/mcp", s.handleProtectedResource)
	r.Get(authorizationServerPath, s.handleAuthorizationServer)
	r.Post("/oauth/register", s.handleOAuthRegister)
	r.Post("/oauth/token", s.handleOAuthToken)
}

// mcpChallenge is the WWW-Authenticate value of a 401 from /mcp.
func (s *Server) mcpChallenge(r *http.Request) string {
	if m := s.publicURL(r); m != "" {
		return `Bearer realm="papers", resource_metadata="` + m + protectedResourcePath + `/mcp"`
	}
	return `Bearer realm="papers"`
}

type protectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ScopesSupported        []string `json:"scopes_supported"`
	ResourceName           string   `json:"resource_name"`
}

func (s *Server) handleProtectedResource(w http.ResponseWriter, r *http.Request) {
	m := s.publicURL(r)
	if m == "" {
		http.NotFound(w, r)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, protectedResourceMetadata{
		Resource:               m + "/mcp",
		AuthorizationServers:   []string{m},
		BearerMethodsSupported: []string{"header"},
		ScopesSupported:        oauthScopes,
		ResourceName:           mcpServerName,
	})
}

type authorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported                   []string `json:"scopes_supported"`
}

func (s *Server) handleAuthorizationServer(w http.ResponseWriter, r *http.Request) {
	m := s.publicURL(r)
	if m == "" {
		http.NotFound(w, r)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, authorizationServerMetadata{
		Issuer:                            m,
		AuthorizationEndpoint:             s.baseURL + "/oauth/authorize",
		TokenEndpoint:                     m + "/oauth/token",
		RegistrationEndpoint:              m + "/oauth/register",
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none"},
		ScopesSupported:                   oauthScopes,
	})
}

// oauthErrorBody is the error format of the OAuth endpoints (RFC 6749
// section 5.2, RFC 7591 section 3.2.2).
type oauthErrorBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// writeOAuthError writes an error of a client-facing OAuth endpoint.
// fallback is the code for invalid arguments that carry no OAuth code.
func writeOAuthError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	ctx := r.Context()
	var oe *model.OAuthError
	switch {
	case errors.As(err, &oe):
		errutil.Handle(ctx, goerr.Wrap(err, "oauth request rejected", goerr.T(errutil.TagBenign)), "oauth request rejected")
		writeJSON(ctx, w, http.StatusBadRequest, oauthErrorBody{Error: oe.Code, ErrorDescription: oe.Description})
	case errors.Is(err, model.ErrInvalidArgument):
		errutil.Handle(ctx, goerr.Wrap(err, "oauth request rejected", goerr.T(errutil.TagBenign)), "oauth request rejected")
		writeJSON(ctx, w, http.StatusBadRequest, oauthErrorBody{Error: fallback})
	default:
		errutil.Handle(ctx, err, "oauth request failed")
		writeJSON(ctx, w, http.StatusInternalServerError, oauthErrorBody{Error: "server_error"})
	}
}

type registerRequest struct {
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

type registerResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// handleOAuthRegister is dynamic client registration (RFC 7591). Fields
// other than client_name and redirect_uris are ignored; nothing is stored.
func (s *Server) handleOAuthRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	data, err := io.ReadAll(io.LimitReader(r.Body, maxOAuthRequestBody+1))
	if err != nil || len(data) > maxOAuthRequestBody || json.Unmarshal(data, &req) != nil {
		writeOAuthError(w, r, model.NewOAuthError(model.OAuthInvalidClientMetadata, "the body must be a JSON client registration"), model.OAuthInvalidClientMetadata)
		return
	}
	id, c, err := s.oauth.RegisterClient(req.ClientName, req.RedirectURIs)
	if err != nil {
		writeOAuthError(w, r, err, model.OAuthInvalidClientMetadata)
		return
	}
	writeJSON(r.Context(), w, http.StatusCreated, registerResponse{
		ClientID: id, ClientIDIssuedAt: time.Now().Unix(), ClientName: c.Name, RedirectURIs: c.RedirectURIs,
		GrantTypes: []string{"authorization_code"}, ResponseTypes: []string{"code"}, TokenEndpointAuthMethod: "none",
	})
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

// handleOAuthToken exchanges an authorization code (form encoded, RFC 6749
// section 4.1.3) for an access token.
func (s *Server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct != "application/x-www-form-urlencoded" {
		writeOAuthError(w, r, model.NewOAuthError(model.OAuthInvalidRequest, "the body must be application/x-www-form-urlencoded"), model.OAuthInvalidRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxOAuthRequestBody)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, r, model.NewOAuthError(model.OAuthInvalidRequest, "invalid form body"), model.OAuthInvalidRequest)
		return
	}
	f := r.PostForm
	res, err := s.oauth.Exchange(r.Context(), usecase.ExchangeRequest{
		GrantType: f.Get("grant_type"), Code: f.Get("code"), RedirectURI: f.Get("redirect_uri"),
		ClientID: f.Get("client_id"), CodeVerifier: f.Get("code_verifier"), Resource: f.Get("resource"),
	})
	if err != nil {
		writeOAuthError(w, r, err, model.OAuthInvalidRequest)
		return
	}
	w.Header().Set("Pragma", "no-cache")
	writeJSON(r.Context(), w, http.StatusOK, tokenResponse{
		AccessToken: res.AccessToken, TokenType: "Bearer",
		ExpiresIn: int64(res.ExpiresIn / time.Second), Scope: string(res.Scope),
	})
}

func authorizeRequestFromQuery(q url.Values) usecase.AuthorizeRequest {
	return usecase.AuthorizeRequest{
		ResponseType: q.Get("response_type"), ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"),
		CodeChallenge: q.Get("code_challenge"), CodeChallengeMethod: q.Get("code_challenge_method"),
		State: q.Get("state"), Scope: q.Get("scope"), Resource: q.Get("resource"),
	}
}

type authorizeInfoJSON struct {
	ClientName   string           `json:"client_name"`
	RedirectHost string           `json:"redirect_host"`
	Scope        model.TokenScope `json:"scope"`
}

// handleCheckAuthorize validates an authorization request for the consent
// screen. Errors keep the OAuth error code as the message, and the screen
// shows them without redirecting.
func (s *Server) handleCheckAuthorize(w http.ResponseWriter, r *http.Request) {
	info, err := s.oauth.CheckAuthorize(authorizeRequestFromQuery(r.URL.Query()))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, authorizeInfoJSON{ClientName: info.ClientName, RedirectHost: info.RedirectHost, Scope: info.Scope})
}

type decideRequest struct {
	ClientID            string           `json:"client_id"`
	RedirectURI         string           `json:"redirect_uri"`
	ResponseType        string           `json:"response_type"`
	CodeChallenge       string           `json:"code_challenge"`
	CodeChallengeMethod string           `json:"code_challenge_method"`
	State               string           `json:"state"`
	Resource            string           `json:"resource"`
	Scope               model.TokenScope `json:"scope"`
	Decision            string           `json:"decision"`
}

// handleDecideAuthorize records the person's answer on the consent screen
// and returns where the browser goes next.
func (s *Server) handleDecideAuthorize(w http.ResponseWriter, r *http.Request) {
	var req decideRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	var allow bool
	switch req.Decision {
	case "allow":
		allow = true
	case "deny":
	default:
		writeError(r.Context(), w, goerr.Wrap(model.ErrInvalidArgument, "decision must be allow or deny", goerr.V("decision", req.Decision)))
		return
	}
	to, err := s.oauth.Decide(r.Context(), principalFrom(r.Context()), usecase.AuthorizeRequest{
		ResponseType: req.ResponseType, ClientID: req.ClientID, RedirectURI: req.RedirectURI,
		CodeChallenge: req.CodeChallenge, CodeChallengeMethod: req.CodeChallengeMethod,
		State: req.State, Resource: req.Resource,
	}, req.Scope, allow)
	respond(w, r, http.StatusOK, map[string]string{"redirect_to": to}, err)
}
