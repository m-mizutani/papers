// Package google implements interfaces.IdentityProvider with Google's
// OpenID Connect authorization-code flow.
package google

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/utils/safe"
)

// Public Google endpoints, not credentials.
const (
	defaultAuthEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultTokenEndpoint = "https://oauth2.googleapis.com/token" // #nosec G101 -- a public URL
	defaultJWKSEndpoint  = "https://www.googleapis.com/oauth2/v3/certs"
)

var issuers = []string{"https://accounts.google.com", "accounts.google.com"}

// Config holds the OAuth client registered in Google Cloud.
type Config struct {
	ClientID     string
	ClientSecret string `masq:"secret"`
	RedirectURL  string
	// HostedDomain, when set, asks Google to show only accounts of this
	// Workspace domain. It is a UI hint; admission is enforced by papers.
	HostedDomain string
}

type Provider struct {
	cfg           Config
	authEndpoint  string
	tokenEndpoint string
	jwksEndpoint  string
	httpClient    *http.Client
	jwksTTL       time.Duration

	// Google's signing keys are public and rotate slowly, so they are cached
	// per instance; this is a cache of external public data, not shared
	// application state.
	mu        sync.Mutex
	keys      jwk.Set
	fetchedAt time.Time
}

var _ interfaces.IdentityProvider = (*Provider)(nil)

type Option func(*Provider)

// WithEndpoints overrides Google's endpoints (tests).
func WithEndpoints(auth, token, jwks string) Option {
	return func(p *Provider) {
		p.authEndpoint, p.tokenEndpoint, p.jwksEndpoint = auth, token, jwks
	}
}

func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

func New(cfg Config, opts ...Option) (*Provider, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		return nil, goerr.New("google client id, client secret and redirect URL are required")
	}
	if _, err := url.Parse(cfg.RedirectURL); err != nil {
		return nil, goerr.Wrap(err, "invalid redirect URL")
	}
	p := &Provider{
		cfg: cfg, authEndpoint: defaultAuthEndpoint, tokenEndpoint: defaultTokenEndpoint,
		jwksEndpoint: defaultJWKSEndpoint, httpClient: &http.Client{Timeout: 10 * time.Second},
		jwksTTL: time.Hour,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

func (p *Provider) Name() model.IdentityProvider { return model.ProviderGoogle }

func (p *Provider) AuthURL(state, nonce string) string {
	q := url.Values{}
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", p.cfg.RedirectURL)
	q.Set("response_type", "code")
	q.Set("scope", "openid email profile")
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("prompt", "select_account")
	if p.cfg.HostedDomain != "" {
		q.Set("hd", p.cfg.HostedDomain)
	}
	return p.authEndpoint + "?" + q.Encode()
}

func (p *Provider) keySet(ctx context.Context, refresh bool) (jwk.Set, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !refresh && p.keys != nil && time.Since(p.fetchedAt) < p.jwksTTL {
		return p.keys, nil
	}
	set, err := jwk.Fetch(ctx, p.jwksEndpoint, jwk.WithHTTPClient(p.httpClient))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to fetch Google JWKS")
	}
	p.keys, p.fetchedAt = set, time.Now()
	return set, nil
}

type tokenResponse struct {
	IDToken          string `json:"id_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (p *Provider) redeem(ctx context.Context, code string) (string, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", p.cfg.ClientID)
	form.Set("client_secret", p.cfg.ClientSecret)
	form.Set("redirect_uri", p.cfg.RedirectURL)
	form.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", goerr.Wrap(err, "failed to build token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", goerr.Wrap(err, "failed to call token endpoint")
	}
	defer safe.Close(ctx, resp.Body)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", goerr.Wrap(err, "failed to read token response")
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", goerr.Wrap(err, "failed to decode token response", goerr.V("status", resp.StatusCode))
	}
	if resp.StatusCode != http.StatusOK || tr.IDToken == "" {
		return "", goerr.New("token endpoint rejected the code",
			goerr.V("status", resp.StatusCode), goerr.V("error", tr.Error), goerr.V("description", tr.ErrorDescription))
	}
	return tr.IDToken, nil
}

func (p *Provider) verify(ctx context.Context, idToken string, refresh bool) (jwt.Token, error) {
	set, err := p.keySet(ctx, refresh)
	if err != nil {
		return nil, err
	}
	tok, err := jwt.ParseString(idToken,
		jwt.WithKeySet(set),
		jwt.WithValidate(true),
		jwt.WithAudience(p.cfg.ClientID),
		jwt.WithAcceptableSkew(time.Minute),
	)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to verify ID token")
	}
	return tok, nil
}

func claim[T any](tok jwt.Token, name string) T {
	var v T
	if err := tok.Get(name, &v); err != nil {
		var zero T
		return zero
	}
	return v
}

func (p *Provider) Exchange(ctx context.Context, code, nonce string) (*model.ExternalClaims, error) {
	if code == "" || nonce == "" {
		return nil, goerr.New("code and nonce are required")
	}
	raw, err := p.redeem(ctx, code)
	if err != nil {
		return nil, err
	}
	tok, err := p.verify(ctx, raw, false)
	if err != nil {
		// Google may have rotated keys since the cache was filled.
		tok, err = p.verify(ctx, raw, true)
		if err != nil {
			return nil, err
		}
	}

	iss, _ := tok.Issuer()
	validIssuer := false
	for _, want := range issuers {
		if iss == want {
			validIssuer = true
		}
	}
	if !validIssuer {
		return nil, goerr.New("unexpected ID token issuer", goerr.V("iss", iss))
	}
	if got := claim[string](tok, "nonce"); got != nonce {
		return nil, goerr.New("ID token nonce mismatch")
	}
	sub, _ := tok.Subject()
	if sub == "" {
		return nil, goerr.New("ID token has no subject")
	}

	return &model.ExternalClaims{
		Provider:      model.ProviderGoogle,
		Subject:       sub,
		Email:         claim[string](tok, "email"),
		EmailVerified: claim[bool](tok, "email_verified"),
		Name:          claim[string](tok, "name"),
		Picture:       claim[string](tok, "picture"),
		HostedDomain:  claim[string](tok, "hd"),
	}, nil
}
