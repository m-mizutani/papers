package model

import (
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// User is a papers account. It is created by papers itself and is never the
// identity provider's subject: an Identity links an external subject (Google
// "sub" today, other providers later) to a User, so one user may sign in
// through several providers.
type User struct {
	ID          UserID    `json:"id"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	LastLoginAt time.Time `json:"last_login_at"`
	// Anonymous marks a user created by --anonymous mode for a browser
	// without a session. It has no Identity; only its session leads to it.
	Anonymous bool `json:"anonymous,omitempty"`
}

// AnonymousEmailDomain is the domain of the synthesized email of anonymous
// users. ".invalid" is reserved (RFC 2606), so it never matches a real
// address, and workspace rules written with real domains keep them out.
const AnonymousEmailDomain = "anonymous.invalid"

// NewAnonymousUser builds an anonymous user with a synthesized email and
// display name.
func NewAnonymousUser(now time.Time) *User {
	id := NewUserID()
	s := string(id)
	return &User{
		ID: id, Email: s + "@" + AnonymousEmailDomain, Name: "Anonymous " + s[len(s)-4:],
		CreatedAt: now, UpdatedAt: now, LastLoginAt: now, Anonymous: true,
	}
}

func (u *User) Validate() error {
	if u.ID == "" {
		return goerr.Wrap(ErrInvalidArgument, "user id is required")
	}
	if u.Email == "" {
		return goerr.Wrap(ErrInvalidArgument, "user email is required", goerr.V("user_id", u.ID))
	}
	return nil
}

// EmailDomain returns the lower-cased domain part of the email address.
func (u *User) EmailDomain() string {
	return EmailDomain(u.Email)
}

func EmailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

// IdentityProvider names an external authentication provider.
type IdentityProvider string

const (
	ProviderGoogle IdentityProvider = "google"
	// ProviderDev is used only by the --no-authn development mode.
	ProviderDev IdentityProvider = "dev"
)

// Identity links an external subject to a User.
type Identity struct {
	Provider  IdentityProvider `json:"provider"`
	Subject   string           `json:"subject"`
	UserID    UserID           `json:"user_id"`
	Email     string           `json:"email"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

func (i *Identity) Validate() error {
	if i.Provider == "" || i.Subject == "" {
		return goerr.Wrap(ErrInvalidArgument, "identity provider and subject are required")
	}
	if i.UserID == "" {
		return goerr.Wrap(ErrInvalidArgument, "identity user id is required")
	}
	return nil
}

// ExternalClaims is what an identity provider asserts about a signed-in
// subject after its token has been verified.
type ExternalClaims struct {
	Provider      IdentityProvider
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
	HostedDomain  string
}

// Session is a browser login. Only the hash of the secret is stored; the
// plaintext secret lives in the user's cookie.
type Session struct {
	ID         SessionID `json:"id"`
	SecretHash string    `json:"secret_hash"`
	UserID     UserID    `json:"user_id"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (s *Session) Validate() error {
	if s.ID == "" || s.SecretHash == "" || s.UserID == "" {
		return goerr.Wrap(ErrInvalidArgument, "session id, secret hash and user id are required")
	}
	return nil
}

func (s *Session) Expired(now time.Time) bool { return !now.Before(s.ExpiresAt) }

// TokenKind distinguishes a personal token (scripts acting as the user) from
// an agent token (an AI acting on behalf of the user). Writes through an
// agent token are recorded with ActorAgent.
type TokenKind string

const (
	TokenPersonal TokenKind = "personal"
	TokenAgent    TokenKind = "agent"
)

// TokenScope caps what a token can do regardless of the owner's role.
type TokenScope string

const (
	ScopeRead  TokenScope = "read"
	ScopeWrite TokenScope = "write"
)

// APIToken authenticates API and MCP callers. Like Session, only the hash of
// the secret is stored.
type APIToken struct {
	ID         TokenID       `json:"id"`
	SecretHash string        `json:"secret_hash"`
	UserID     UserID        `json:"user_id"`
	Name       string        `json:"name"`
	Kind       TokenKind     `json:"kind"`
	AgentName  string        `json:"agent_name,omitempty"`
	Scope      TokenScope    `json:"scope"`
	Workspaces []WorkspaceID `json:"workspaces,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
	ExpiresAt  *time.Time    `json:"expires_at,omitempty"`
	LastUsedAt *time.Time    `json:"last_used_at,omitempty"`
	// OAuth is set on tokens issued through the OAuth authorization flow.
	OAuth *OAuthGrant `json:"oauth,omitempty"`
}

// OAuthGrant records how an OAuth client obtained a token. Until the client
// exchanges its authorization code the token is pending: it has no secret
// (so no bearer token matches it), and the code's hash and PKCE challenge
// are kept here. The exchange sets the secret and clears the code fields.
type OAuthGrant struct {
	ClientID      string     `json:"client_id"`
	ClientName    string     `json:"client_name"`
	RedirectURI   string     `json:"redirect_uri"`
	CodeHash      string     `json:"code_hash,omitempty"`
	CodeChallenge string     `json:"code_challenge,omitempty"`
	CodeExpiresAt *time.Time `json:"code_expires_at,omitempty"`
	// UsedCodeHash is the hash of the code once it was exchanged. Presenting
	// that code again revokes the token (RFC 6749 section 4.1.2).
	UsedCodeHash string `json:"used_code_hash,omitempty"`
}

// Pending reports whether the token still waits for its authorization code
// to be exchanged.
func (t *APIToken) Pending() bool {
	return t.OAuth != nil && t.OAuth.CodeHash != ""
}

func (t *APIToken) Validate() error {
	if t.ID == "" || t.UserID == "" {
		return goerr.Wrap(ErrInvalidArgument, "token id and user id are required")
	}
	if t.SecretHash == "" && !t.Pending() {
		return goerr.Wrap(ErrInvalidArgument, "token secret hash is required")
	}
	if t.OAuth != nil {
		if t.OAuth.ClientID == "" || t.OAuth.RedirectURI == "" {
			return goerr.Wrap(ErrInvalidArgument, "oauth client id and redirect uri are required")
		}
		if t.Pending() && (t.OAuth.CodeChallenge == "" || t.OAuth.CodeExpiresAt == nil) {
			return goerr.Wrap(ErrInvalidArgument, "a pending oauth token needs its code challenge and expiry")
		}
	}
	if t.Name == "" {
		return goerr.Wrap(ErrInvalidArgument, "token name is required")
	}
	switch t.Kind {
	case TokenPersonal, TokenAgent:
	default:
		return goerr.Wrap(ErrInvalidArgument, "invalid token kind", goerr.V("kind", t.Kind))
	}
	switch t.Scope {
	case ScopeRead, ScopeWrite:
	default:
		return goerr.Wrap(ErrInvalidArgument, "invalid token scope", goerr.V("scope", t.Scope))
	}
	return nil
}

func (t *APIToken) Expired(now time.Time) bool {
	return t.ExpiresAt != nil && !now.Before(*t.ExpiresAt)
}

// AllowsWorkspace reports whether the token is usable in ws. An empty
// workspace list means every workspace the owner can reach.
func (t *APIToken) AllowsWorkspace(ws WorkspaceID) bool {
	if len(t.Workspaces) == 0 {
		return true
	}
	for _, w := range t.Workspaces {
		if w == ws {
			return true
		}
	}
	return false
}
