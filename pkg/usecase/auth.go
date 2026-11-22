package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

// AuthConfig decides who may sign in. Which workspaces a signed-in user may
// use is decided by the workspace configuration, not here.
type AuthConfig struct {
	// AllowedDomains admits any verified email in these domains.
	AllowedDomains []string
	// AllowedEmails admits these exact addresses.
	AllowedEmails []string
	SessionTTL    time.Duration
}

func normalizeList(xs []string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		x = strings.ToLower(strings.TrimSpace(x))
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

// Auth authenticates browsers (sessions), API callers (tokens) and, in
// development, a fixed user.
type Auth struct {
	repo     interfaces.Repository
	provider interfaces.IdentityProvider
	cfg      AuthConfig
	now      func() time.Time
	devUser  *model.User
}

type AuthOption func(*Auth)

func WithAuthClock(now func() time.Time) AuthOption {
	return func(a *Auth) { a.now = now }
}

// NewAuth builds the authenticator. provider may be nil only in no-authn
// mode (see EnableDevUser); with a provider, at least one allow rule is
// required so a misconfiguration cannot admit every Google account.
func NewAuth(repo interfaces.Repository, provider interfaces.IdentityProvider, cfg AuthConfig, opts ...AuthOption) (*Auth, error) {
	cfg.AllowedDomains = normalizeList(cfg.AllowedDomains)
	cfg.AllowedEmails = normalizeList(cfg.AllowedEmails)
	if provider != nil && len(cfg.AllowedDomains) == 0 && len(cfg.AllowedEmails) == 0 {
		return nil, goerr.New("at least one allowed domain or email is required")
	}
	if cfg.SessionTTL <= 0 {
		return nil, goerr.New("session TTL must be positive")
	}
	a := &Auth{repo: repo, provider: provider, cfg: cfg, now: func() time.Time { return time.Now().UTC() }}
	for _, opt := range opts {
		opt(a)
	}
	return a, nil
}

// EnableDevUser switches to no-authn mode: every request is authenticated as
// a fixed user, created on first use. Never use in production.
func (a *Auth) EnableDevUser(ctx context.Context, email, name string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	user, err := a.upsertUser(ctx, &model.ExternalClaims{
		Provider: model.ProviderDev, Subject: email, Email: email, EmailVerified: true, Name: name,
	})
	if err != nil {
		return goerr.Wrap(err, "failed to provision dev user")
	}
	a.devUser = user
	return nil
}

func (a *Auth) IsNoAuthn() bool { return a.devUser != nil }

func (a *Auth) HasProvider() bool { return a.provider != nil }

func (a *Auth) LoginURL(state, nonce string) (string, error) {
	if a.provider == nil {
		return "", goerr.New("no identity provider is configured")
	}
	return a.provider.AuthURL(state, nonce), nil
}

func (a *Auth) allowed(claims *model.ExternalClaims) bool {
	email := strings.ToLower(claims.Email)
	if slices.Contains(a.cfg.AllowedEmails, email) {
		return true
	}
	return slices.Contains(a.cfg.AllowedDomains, model.EmailDomain(email))
}

// SessionCredential is what the browser keeps in cookies.
type SessionCredential struct {
	ID        model.SessionID
	Secret    string
	ExpiresAt time.Time
}

// HandleCallback completes a login: it verifies the code with the
// provider, admits or rejects the account, provisions the user and opens a
// session.
func (a *Auth) HandleCallback(ctx context.Context, code, nonce string) (*SessionCredential, error) {
	if a.provider == nil {
		return nil, goerr.New("no identity provider is configured")
	}
	claims, err := a.provider.Exchange(ctx, code, nonce)
	if err != nil {
		return nil, goerr.Wrap(model.ErrUnauthenticated, "failed to verify login", goerr.V("cause", err.Error()))
	}
	if !claims.EmailVerified || claims.Email == "" {
		return nil, goerr.Wrap(model.ErrPermissionDenied, "email is not verified")
	}
	if !a.allowed(claims) {
		return nil, goerr.Wrap(model.ErrPermissionDenied, "account is not allowed to sign in", goerr.V("email", claims.Email))
	}
	user, err := a.upsertUser(ctx, claims)
	if err != nil {
		return nil, err
	}
	return a.openSession(ctx, user.ID)
}

// upsertUser resolves the identity to a user, linking by verified email
// when the identity is new, and refreshes the profile.
func (a *Auth) upsertUser(ctx context.Context, claims *model.ExternalClaims) (*model.User, error) {
	now := a.now()
	identity, err := a.repo.GetIdentity(ctx, claims.Provider, claims.Subject)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return nil, goerr.Wrap(err, "failed to get identity")
	}

	var user *model.User
	if identity != nil {
		user, err = a.repo.GetUser(ctx, identity.UserID)
		if err != nil {
			return nil, goerr.Wrap(err, "identity points to a missing user", goerr.V("user_id", identity.UserID))
		}
	} else {
		user, err = a.repo.GetUserByEmail(ctx, claims.Email)
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			return nil, goerr.Wrap(err, "failed to get user by email")
		}
		if user == nil {
			user = &model.User{ID: model.NewUserID(), Email: strings.ToLower(claims.Email), CreatedAt: now}
		}
		identity = &model.Identity{Provider: claims.Provider, Subject: claims.Subject, UserID: user.ID, CreatedAt: now}
	}

	if claims.Name != "" {
		user.Name = claims.Name
	}
	if claims.Picture != "" {
		user.AvatarURL = claims.Picture
	}
	user.UpdatedAt = now
	user.LastLoginAt = now
	if err := a.repo.PutUser(ctx, user); err != nil {
		return nil, goerr.Wrap(err, "failed to save user")
	}

	identity.Email = claims.Email
	identity.UpdatedAt = now
	if err := a.repo.PutIdentity(ctx, identity); err != nil {
		return nil, goerr.Wrap(err, "failed to save identity")
	}
	return user, nil
}

func randomSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", goerr.Wrap(err, "failed to generate secret")
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func secretMatches(secret, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(hashSecret(secret)), []byte(hash)) == 1
}

func (a *Auth) openSession(ctx context.Context, userID model.UserID) (*SessionCredential, error) {
	secret, err := randomSecret()
	if err != nil {
		return nil, err
	}
	now := a.now()
	s := &model.Session{ID: model.NewSessionID(), SecretHash: hashSecret(secret), UserID: userID, CreatedAt: now, ExpiresAt: now.Add(a.cfg.SessionTTL)}
	if err := a.repo.PutSession(ctx, s); err != nil {
		return nil, goerr.Wrap(err, "failed to save session")
	}
	return &SessionCredential{ID: s.ID, Secret: secret, ExpiresAt: s.ExpiresAt}, nil
}

func (a *Auth) principal(_ context.Context, user *model.User) (*model.Principal, error) {
	return &model.Principal{User: user}, nil
}

// DevPrincipal returns the principal of no-authn mode.
func (a *Auth) DevPrincipal(ctx context.Context) (*model.Principal, error) {
	if a.devUser == nil {
		return nil, goerr.Wrap(model.ErrUnauthenticated, "no-authn mode is not enabled")
	}
	user, err := a.repo.GetUser(ctx, a.devUser.ID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get dev user")
	}
	return a.principal(ctx, user)
}

// AuthenticateSession validates a session cookie pair.
func (a *Auth) AuthenticateSession(ctx context.Context, id model.SessionID, secret string) (*model.Principal, error) {
	s, err := a.repo.GetSession(ctx, id)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, goerr.Wrap(model.ErrUnauthenticated, "unknown session")
		}
		return nil, goerr.Wrap(err, "failed to get session")
	}
	if !secretMatches(secret, s.SecretHash) {
		return nil, goerr.Wrap(model.ErrUnauthenticated, "invalid session secret")
	}
	if s.Expired(a.now()) {
		return nil, goerr.Wrap(model.ErrUnauthenticated, "session expired")
	}
	user, err := a.repo.GetUser(ctx, s.UserID)
	if err != nil {
		return nil, goerr.Wrap(err, "session points to a missing user")
	}
	return a.principal(ctx, user)
}

func (a *Auth) Logout(ctx context.Context, id model.SessionID) error {
	if err := a.repo.DeleteSession(ctx, id); err != nil {
		return goerr.Wrap(err, "failed to delete session")
	}
	return nil
}

// TokenPrefix marks papers API tokens so secret scanners can find them.
const TokenPrefix = "pap_"

func formatToken(id model.TokenID, secret string) string {
	return TokenPrefix + string(id) + "." + secret
}

func parseToken(raw string) (model.TokenID, string, bool) {
	if !strings.HasPrefix(raw, TokenPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(raw, TokenPrefix)
	id, secret, ok := strings.Cut(rest, ".")
	if !ok || id == "" || secret == "" {
		return "", "", false
	}
	return model.TokenID(id), secret, true
}

// AuthenticateToken validates a bearer token.
func (a *Auth) AuthenticateToken(ctx context.Context, raw string) (*model.Principal, error) {
	id, secret, ok := parseToken(raw)
	if !ok {
		return nil, goerr.Wrap(model.ErrUnauthenticated, "malformed token")
	}
	tok, err := a.repo.GetToken(ctx, id)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, goerr.Wrap(model.ErrUnauthenticated, "unknown token")
		}
		return nil, goerr.Wrap(err, "failed to get token")
	}
	if !secretMatches(secret, tok.SecretHash) {
		return nil, goerr.Wrap(model.ErrUnauthenticated, "invalid token secret")
	}
	now := a.now()
	if tok.Expired(now) {
		return nil, goerr.Wrap(model.ErrUnauthenticated, "token expired")
	}
	user, err := a.repo.GetUser(ctx, tok.UserID)
	if err != nil {
		return nil, goerr.Wrap(err, "token points to a missing user")
	}
	p, err := a.principal(ctx, user)
	if err != nil {
		return nil, err
	}
	p.Token = tok

	// Last-used is informative only; record it at most once a minute to
	// keep a busy agent from turning every read into a write.
	if tok.LastUsedAt == nil || now.Sub(*tok.LastUsedAt) > time.Minute {
		tok.LastUsedAt = &now
		if err := a.repo.PutToken(ctx, tok); err != nil {
			return nil, goerr.Wrap(err, "failed to record token use")
		}
	}
	return p, nil
}

// CreateTokenInput describes a new API token.
type CreateTokenInput struct {
	Name       string
	Kind       model.TokenKind
	AgentName  string
	Scope      model.TokenScope
	Workspaces []model.WorkspaceID
	TTL        time.Duration // 0 = never expires
}

// CreateToken issues a token for the principal. Only browser sessions may
// mint tokens, so a leaked token cannot be used to mint more.
func (a *Auth) CreateToken(ctx context.Context, p *model.Principal, in CreateTokenInput) (*model.APIToken, string, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, "", err
	}
	if p.Token != nil {
		return nil, "", goerr.Wrap(model.ErrPermissionDenied, "tokens cannot create tokens")
	}
	secret, err := randomSecret()
	if err != nil {
		return nil, "", err
	}
	now := a.now()
	tok := &model.APIToken{
		ID: model.NewTokenID(), SecretHash: hashSecret(secret), UserID: p.User.ID,
		Name: strings.TrimSpace(in.Name), Kind: in.Kind, AgentName: strings.TrimSpace(in.AgentName),
		Scope: in.Scope, Workspaces: in.Workspaces, CreatedAt: now,
	}
	if tok.Kind == model.TokenAgent && tok.AgentName == "" {
		tok.AgentName = tok.Name
	}
	if in.TTL < 0 {
		return nil, "", goerr.Wrap(model.ErrInvalidArgument, "ttl must not be negative")
	}
	if in.TTL > 0 {
		exp := now.Add(in.TTL)
		tok.ExpiresAt = &exp
	}
	if err := a.repo.PutToken(ctx, tok); err != nil {
		return nil, "", goerr.Wrap(err, "failed to save token")
	}
	return tok, formatToken(tok.ID, secret), nil
}

func (a *Auth) ListTokens(ctx context.Context, p *model.Principal) ([]*model.APIToken, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	toks, err := a.repo.ListTokens(ctx, p.User.ID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list tokens")
	}
	return toks, nil
}

func (a *Auth) RevokeToken(ctx context.Context, p *model.Principal, id model.TokenID) error {
	if err := requirePrincipal(p); err != nil {
		return err
	}
	tok, err := a.repo.GetToken(ctx, id)
	if err != nil {
		return goerr.Wrap(err, "failed to get token", goerr.V("token_id", id))
	}
	if tok.UserID != p.User.ID {
		return goerr.Wrap(model.ErrNotFound, "token not found", goerr.V("token_id", id))
	}
	if err := a.repo.DeleteToken(ctx, id); err != nil {
		return goerr.Wrap(err, "failed to delete token")
	}
	return nil
}
