package http

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

const (
	sessionCookie  = "papers_session"
	stateCookie    = "papers_oauth_state"
	nonceCookie    = "papers_oauth_nonce"
	returnToCookie = "papers_return_to"
	// csrfHeader must accompany every cookie-authenticated mutating request.
	// Browsers cannot attach custom headers cross-origin without a CORS
	// preflight, which this server never grants.
	csrfHeader = "X-Requested-With"
	csrfValue  = "papers"

	maxReturnToLength = 2048
	oauthCookieTTL    = 10 * time.Minute
)

// validateReturnTo accepts only same-origin relative paths as post-login
// redirect targets.
func validateReturnTo(p string) bool {
	if p == "" || len(p) > maxReturnToLength || !strings.HasPrefix(p, "/") {
		return false
	}
	if strings.HasPrefix(p, "//") || strings.HasPrefix(p, `/\`) {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(p)
	return err == nil && u.Scheme == "" && u.Host == ""
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", goerr.Wrap(err, "failed to generate random value")
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) cookie(name, value string, maxAge time.Duration) *http.Cookie {
	c := &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: true,
		Secure: s.secureCookie, SameSite: http.SameSiteLaxMode,
	}
	if maxAge < 0 {
		c.MaxAge = -1
	} else {
		c.MaxAge = int(maxAge.Seconds())
	}
	return c
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	returnTo := r.URL.Query().Get("return_to")
	if !validateReturnTo(returnTo) {
		returnTo = "/"
	}
	if s.auth.IsNoAuthn() {
		http.Redirect(w, r, returnTo, http.StatusFound)
		return
	}
	state, err := randomHex(16)
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	nonce, err := randomHex(16)
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	authURL, err := s.auth.LoginURL(state, nonce)
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	http.SetCookie(w, s.cookie(stateCookie, state, oauthCookieTTL))
	http.SetCookie(w, s.cookie(nonceCookie, nonce, oauthCookieTTL))
	http.SetCookie(w, s.cookie(returnToCookie, returnTo, oauthCookieTTL))
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	stateC, err1 := r.Cookie(stateCookie)
	nonceC, err2 := r.Cookie(nonceCookie)
	for _, name := range []string{stateCookie, nonceCookie, returnToCookie} {
		http.SetCookie(w, s.cookie(name, "", -1))
	}
	if err1 != nil || err2 != nil || stateC.Value == "" || r.URL.Query().Get("state") != stateC.Value {
		writeError(ctx, w, goerr.Wrap(model.ErrInvalidArgument, "invalid OAuth state"))
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		writeError(ctx, w, goerr.Wrap(model.ErrUnauthenticated, "login was not completed", goerr.V("error", e)))
		return
	}
	cred, err := s.auth.HandleCallback(ctx, r.URL.Query().Get("code"), nonceC.Value)
	if err != nil {
		if errors.Is(err, model.ErrPermissionDenied) {
			http.Redirect(w, r, "/login?error=not_allowed", http.StatusFound)
			return
		}
		writeError(ctx, w, err)
		return
	}
	http.SetCookie(w, s.cookie(sessionCookie, string(cred.ID)+"."+cred.Secret, time.Until(cred.ExpiresAt)))

	target := "/"
	if c, err := r.Cookie(returnToCookie); err == nil && validateReturnTo(c.Value) {
		target = c.Value
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Header.Get(csrfHeader) != csrfValue {
		writeError(ctx, w, goerr.Wrap(model.ErrPermissionDenied, "missing CSRF header"))
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		if id, _, ok := strings.Cut(c.Value, "."); ok {
			if err := s.auth.Logout(ctx, model.SessionID(id)); err != nil {
				writeError(ctx, w, err)
				return
			}
		}
	}
	http.SetCookie(w, s.cookie(sessionCookie, "", -1))
	writeJSON(ctx, w, http.StatusOK, map[string]bool{"ok": true})
}

type meResponse struct {
	User    *model.User `json:"user"`
	NoAuthn bool        `json:"no_authn"`
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	writeJSON(r.Context(), w, http.StatusOK, meResponse{User: p.User, NoAuthn: s.auth.IsNoAuthn()})
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// authenticate resolves the principal from, in order: a bearer token, the
// no-authn dev user, or the session cookie.
func (s *Server) authenticate(r *http.Request) (*model.Principal, error) {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); h != "" {
		raw, ok := strings.CutPrefix(h, "Bearer ")
		if !ok {
			return nil, goerr.Wrap(model.ErrUnauthenticated, "unsupported authorization scheme")
		}
		return s.auth.AuthenticateToken(ctx, strings.TrimSpace(raw))
	}

	if !isSafeMethod(r.Method) && r.Header.Get(csrfHeader) != csrfValue {
		return nil, goerr.Wrap(model.ErrPermissionDenied, "missing CSRF header")
	}
	if s.auth.IsNoAuthn() {
		return s.auth.DevPrincipal(ctx)
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, goerr.Wrap(model.ErrUnauthenticated, "no session")
	}
	id, secret, ok := strings.Cut(c.Value, ".")
	if !ok {
		return nil, goerr.Wrap(model.ErrUnauthenticated, "malformed session cookie")
	}
	return s.auth.AuthenticateSession(ctx, model.SessionID(id), secret)
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.authenticate(r)
		if err != nil {
			if r.Header.Get("Authorization") != "" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="papers"`)
			}
			writeError(r.Context(), w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
	})
}

// sessionOnly rejects token-authenticated requests (token management must
// happen in a browser session).
func sessionOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := principalFrom(r.Context()); p != nil && p.Token != nil {
			writeError(r.Context(), w, goerr.Wrap(model.ErrPermissionDenied, "this endpoint requires a browser session"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// mcpAuthMiddleware authenticates MCP requests by bearer token only. Browser
// cookies are not accepted, so a web page cannot drive MCP tools with the
// user's session. In no-authn mode requests without a token act as the dev
// user, which lets a local MCP client connect without setup.
func (s *Server) mcpAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var (
			p   *model.Principal
			err error
		)
		switch {
		case r.Header.Get("Authorization") != "":
			p, err = s.authenticate(r)
		case s.auth.IsNoAuthn():
			p, err = s.auth.DevPrincipal(r.Context())
		default:
			err = goerr.Wrap(model.ErrUnauthenticated, "MCP requires a bearer token")
		}
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="papers"`)
			writeError(r.Context(), w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
	})
}
