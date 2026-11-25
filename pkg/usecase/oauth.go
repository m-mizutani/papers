package usecase

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// OAuth issues API tokens to MCP clients through the OAuth 2.1
// authorization code flow with PKCE. A token it issues is an ordinary
// APIToken of kind agent; OAuth only adds another way to obtain one.
//
// Client registrations are not stored: the client ID is the registration
// (name and redirect URIs) encoded, so unauthenticated callers cannot add
// writes. The authorization code is kept, hashed, on the pending token
// itself; the code names the token so the exchange is a single read.
type OAuth struct {
	auth *Auth
	// resources are the MCP endpoint URLs of this server ({base}/mcp and
	// {mcp-base-url}/mcp). A resource indicator must be one of them.
	resources []string
}

const (
	// OAuthClientIDPrefix marks client IDs this server made.
	OAuthClientIDPrefix = "pcl_"
	// OAuthCodePrefix marks authorization codes.
	OAuthCodePrefix = "pac_"

	// OAuthCodeTTL is how long an authorization code may be exchanged.
	OAuthCodeTTL = 5 * time.Minute
	// OAuthTokenTTL is the lifetime of a token issued through OAuth. There
	// are no refresh tokens: when it expires the client authorizes again.
	OAuthTokenTTL = 90 * 24 * time.Hour

	// DefaultOAuthClientName names a client that registered without a name.
	DefaultOAuthClientName = "MCP client"

	maxOAuthClientIDLen    = 1024
	maxOAuthRedirectURIs   = 5
	maxOAuthRedirectURILen = 512
	maxOAuthClientNameLen  = 64
)

// NewOAuth builds the OAuth use case on top of the authenticator. resources
// are the URLs of this server's MCP endpoint.
func NewOAuth(auth *Auth, resources []string) *OAuth {
	return &OAuth{auth: auth, resources: slices.Compact(slices.Clone(resources))}
}

// OAuthClient is what a client ID carries.
type OAuthClient struct {
	Name         string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

func oauthErr(code, description string) error {
	return model.NewOAuthError(code, description)
}

// validRedirectURI accepts https URLs and http URLs on the loopback host
// (native clients such as Claude Code listen on localhost), without a
// fragment or user info.
func validRedirectURI(raw string) bool {
	if raw == "" || len(raw) > maxOAuthRedirectURILen {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Fragment != "" || strings.Contains(raw, "#") || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return true
		}
	}
	return false
}

func normalizeClient(name string, redirectURIs []string) (*OAuthClient, error) {
	if len(redirectURIs) == 0 || len(redirectURIs) > maxOAuthRedirectURIs {
		return nil, oauthErr(model.OAuthInvalidRedirectURI, "between 1 and 5 redirect_uris are required")
	}
	for _, r := range redirectURIs {
		if !validRedirectURI(r) {
			return nil, oauthErr(model.OAuthInvalidRedirectURI, "redirect_uris must be https URLs or http URLs on localhost, without a fragment, of at most 512 bytes")
		}
	}
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxOAuthClientNameLen || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return nil, oauthErr(model.OAuthInvalidClientMetadata, "client_name must be at most 64 characters without control characters")
	}
	if name == "" {
		name = DefaultOAuthClientName
	}
	return &OAuthClient{Name: name, RedirectURIs: redirectURIs}, nil
}

// RegisterClient validates a dynamic client registration (RFC 7591) and
// returns the client ID that carries it. Nothing is stored.
func (o *OAuth) RegisterClient(name string, redirectURIs []string) (string, *OAuthClient, error) {
	c, err := normalizeClient(name, redirectURIs)
	if err != nil {
		return "", nil, err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", nil, goerr.Wrap(err, "failed to encode client")
	}
	id := OAuthClientIDPrefix + base64.RawURLEncoding.EncodeToString(raw)
	if len(id) > maxOAuthClientIDLen {
		return "", nil, oauthErr(model.OAuthInvalidClientMetadata, "the registration is too long")
	}
	return id, c, nil
}

// ParseClientID reads the registration a client ID carries and validates
// it again, since anyone can make one.
func ParseClientID(id string) (*OAuthClient, error) {
	rest, ok := strings.CutPrefix(id, OAuthClientIDPrefix)
	if !ok || len(id) > maxOAuthClientIDLen {
		return nil, oauthErr(model.OAuthInvalidClient, "unknown client_id")
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return nil, oauthErr(model.OAuthInvalidClient, "unknown client_id")
	}
	var c OAuthClient
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, oauthErr(model.OAuthInvalidClient, "unknown client_id")
	}
	nc, err := normalizeClient(c.Name, c.RedirectURIs)
	if err != nil {
		return nil, oauthErr(model.OAuthInvalidClient, "unknown client_id")
	}
	return nc, nil
}

// AuthorizeRequest is an authorization request as the client sent it.
type AuthorizeRequest struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	State               string
	Scope               string
	Resource            string
}

// AuthorizeInfo is what the consent screen shows.
type AuthorizeInfo struct {
	ClientName   string
	RedirectHost string
	// Scope is the access the client asked for; the person may lower it.
	Scope model.TokenScope
}

func (o *OAuth) checkResource(resource string) error {
	if resource == "" || slices.Contains(o.resources, resource) {
		return nil
	}
	return oauthErr(model.OAuthInvalidTarget, "resource is not this server's MCP endpoint")
}

// requestedScope reads the scope parameter: write unless only read was
// asked for. Other values are ignored.
func requestedScope(scope string) model.TokenScope {
	fields := strings.Fields(scope)
	if slices.Contains(fields, string(model.ScopeRead)) && !slices.Contains(fields, string(model.ScopeWrite)) {
		return model.ScopeRead
	}
	return model.ScopeWrite
}

// CheckAuthorize validates an authorization request. Its errors mean the
// redirect URI cannot be trusted or the request is unusable, so the caller
// shows them instead of redirecting.
func (o *OAuth) CheckAuthorize(req AuthorizeRequest) (*AuthorizeInfo, error) {
	client, err := ParseClientID(req.ClientID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(client.RedirectURIs, req.RedirectURI) {
		return nil, oauthErr(model.OAuthInvalidRedirectURI, "redirect_uri is not registered for this client")
	}
	if req.ResponseType != "code" {
		return nil, oauthErr(model.OAuthUnsupportedResponseType, "response_type must be code")
	}
	if req.CodeChallengeMethod != "S256" || !validCodeChallenge(req.CodeChallenge) {
		return nil, oauthErr(model.OAuthInvalidCodeChallenge, "a code_challenge with code_challenge_method S256 is required")
	}
	if err := o.checkResource(req.Resource); err != nil {
		return nil, err
	}
	u, err := url.Parse(req.RedirectURI)
	if err != nil {
		return nil, oauthErr(model.OAuthInvalidRedirectURI, "invalid redirect_uri")
	}
	return &AuthorizeInfo{ClientName: client.Name, RedirectHost: u.Host, Scope: requestedScope(req.Scope)}, nil
}

// validCodeChallenge accepts a base64url SHA-256 digest (43 characters).
func validCodeChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil
}

func redirectWith(redirectURI string, params map[string]string) (string, error) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return "", goerr.Wrap(err, "failed to parse redirect_uri")
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Decide answers an authorization request on behalf of the signed-in
// person: with allow it makes a pending token and returns the redirect with
// its code, otherwise the redirect with access_denied. Only browser
// sessions may decide, so a token cannot mint tokens.
func (o *OAuth) Decide(ctx context.Context, p *model.Principal, req AuthorizeRequest, scope model.TokenScope, allow bool) (string, error) {
	if err := requirePrincipal(p); err != nil {
		return "", err
	}
	if p.Token != nil {
		return "", goerr.Wrap(model.ErrPermissionDenied, "tokens cannot authorize clients")
	}
	info, err := o.CheckAuthorize(req)
	if err != nil {
		return "", err
	}
	if !allow {
		return redirectWith(req.RedirectURI, map[string]string{"error": "access_denied", "state": req.State})
	}
	switch scope {
	case model.ScopeRead, model.ScopeWrite:
	default:
		return "", goerr.Wrap(model.ErrInvalidArgument, "scope must be read or write", goerr.V("scope", scope))
	}

	secret, err := randomSecret()
	if err != nil {
		return "", err
	}
	now := o.auth.now()
	codeExp := now.Add(OAuthCodeTTL)
	tok := &model.APIToken{
		ID: model.NewTokenID(), UserID: p.User.ID,
		Name: info.ClientName, Kind: model.TokenAgent, AgentName: info.ClientName,
		Scope: scope, CreatedAt: now, ExpiresAt: &codeExp,
		OAuth: &model.OAuthGrant{
			ClientID: req.ClientID, ClientName: info.ClientName, RedirectURI: req.RedirectURI,
			CodeHash: hashSecret(secret), CodeChallenge: req.CodeChallenge, CodeExpiresAt: &codeExp,
		},
	}
	if err := o.auth.repo.PutToken(ctx, tok); err != nil {
		return "", goerr.Wrap(err, "failed to save pending token")
	}
	code := OAuthCodePrefix + string(tok.ID) + "." + secret
	return redirectWith(req.RedirectURI, map[string]string{"code": code, "state": req.State})
}

// ExchangeRequest is a token request (grant_type=authorization_code).
type ExchangeRequest struct {
	GrantType    string
	Code         string
	RedirectURI  string
	ClientID     string
	CodeVerifier string
	Resource     string
}

// ExchangeResult is the issued access token.
type ExchangeResult struct {
	AccessToken string
	ExpiresIn   time.Duration
	Scope       model.TokenScope
}

func pkceMatches(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

// Exchange trades an authorization code for an access token. Checking and
// consuming the code happen in one transaction, so a code works once.
func (o *OAuth) Exchange(ctx context.Context, req ExchangeRequest) (*ExchangeResult, error) {
	if req.GrantType != "authorization_code" {
		return nil, oauthErr(model.OAuthUnsupportedGrantType, "only authorization_code is supported")
	}
	if req.Code == "" || req.CodeVerifier == "" || req.RedirectURI == "" || req.ClientID == "" {
		return nil, oauthErr(model.OAuthInvalidRequest, "code, code_verifier, redirect_uri and client_id are required")
	}
	if _, err := ParseClientID(req.ClientID); err != nil {
		return nil, err
	}
	if err := o.checkResource(req.Resource); err != nil {
		return nil, err
	}
	rest, ok := strings.CutPrefix(req.Code, OAuthCodePrefix)
	id, codeSecret, ok2 := strings.Cut(rest, ".")
	if !ok || !ok2 || id == "" || codeSecret == "" {
		return nil, oauthErr(model.OAuthInvalidGrant, "invalid code")
	}

	secret, err := randomSecret()
	if err != nil {
		return nil, err
	}
	now := o.auth.now()
	invalid := oauthErr(model.OAuthInvalidGrant, "the code is invalid, expired or already used")
	tok, err := o.auth.repo.UpdateToken(ctx, model.TokenID(id), func(t *model.APIToken) error {
		g := t.OAuth
		if !t.Pending() || g.CodeExpiresAt == nil || !now.Before(*g.CodeExpiresAt) {
			return invalid
		}
		if !secretMatches(codeSecret, g.CodeHash) || g.ClientID != req.ClientID || g.RedirectURI != req.RedirectURI {
			return invalid
		}
		if !pkceMatches(req.CodeVerifier, g.CodeChallenge) {
			return invalid
		}
		exp := now.Add(OAuthTokenTTL)
		t.SecretHash = hashSecret(secret)
		t.ExpiresAt = &exp
		g.CodeHash, g.CodeChallenge, g.CodeExpiresAt = "", "", nil
		return nil
	})
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, invalid
		}
		return nil, err
	}
	return &ExchangeResult{AccessToken: formatToken(tok.ID, secret), ExpiresIn: OAuthTokenTTL, Scope: tok.Scope}, nil
}
