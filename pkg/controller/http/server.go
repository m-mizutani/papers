// Package http is the HTTP transport: the REST API, the realtime WebSocket,
// the MCP endpoint and the embedded Web UI.
package http

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/frontend"
	"github.com/m-mizutani/papers/pkg/usecase"
	"github.com/m-mizutani/papers/pkg/utils/errutil"
	"github.com/m-mizutani/papers/pkg/utils/logging"
	"github.com/m-mizutani/papers/pkg/utils/safe"
)

type Server struct {
	router       *chi.Mux
	uc           *usecase.UseCases
	auth         *usecase.Auth
	secureCookie bool
	wsOrigins    []string
	enableMCP    bool
	staticFS     fs.FS
}

type Option func(*Server)

// WithSecureCookie marks cookies Secure. Enable whenever the public URL is
// https (Cloud Run terminates TLS at the load balancer, so the request
// itself never looks like TLS).
func WithSecureCookie(v bool) Option { return func(s *Server) { s.secureCookie = v } }

// WithWebSocketOrigins allows extra Origin host patterns for WebSocket
// upgrades (the request's own host is always allowed), e.g. the Vite dev
// server.
func WithWebSocketOrigins(patterns []string) Option {
	return func(s *Server) { s.wsOrigins = patterns }
}

func WithMCP(enabled bool) Option { return func(s *Server) { s.enableMCP = enabled } }

// WithStaticFS replaces the embedded UI (tests).
func WithStaticFS(f fs.FS) Option { return func(s *Server) { s.staticFS = f } }

func New(uc *usecase.UseCases, auth *usecase.Auth, opts ...Option) (*Server, error) {
	if uc == nil || auth == nil {
		return nil, goerr.New("use cases and auth are required")
	}
	s := &Server{router: chi.NewRouter(), uc: uc, auth: auth}
	for _, opt := range opts {
		opt(s)
	}
	if s.staticFS == nil {
		sub, err := fs.Sub(frontend.StaticFiles, "dist")
		if err != nil {
			return nil, goerr.Wrap(err, "failed to open embedded UI")
		}
		s.staticFS = sub
	}

	r := s.router
	r.Use(middleware.RequestID)
	r.Use(errutil.SentryHTTPMiddleware)
	r.Use(accessLogger)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(r.Context(), w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/api/auth", func(r chi.Router) {
		r.Get("/login", s.handleLogin)
		r.Get("/callback", s.handleCallback)
		r.Post("/logout", s.handleLogout)
		r.With(s.authMiddleware).Get("/me", s.handleMe)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.authMiddleware)
		s.routeAPI(r)
	})

	if s.enableMCP {
		r.With(s.mcpAuthMiddleware).Handle("/mcp", s.mcpHandler())
	}

	r.Get("/*", s.spaHandler())
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

func accessLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		defer func() {
			logging.From(r.Context()).Info("access",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration", time.Since(start),
				"remote", r.RemoteAddr,
				"user_agent", r.UserAgent(),
			)
		}()
		next.ServeHTTP(ww, r)
	})
}

// spaHandler serves built assets and falls back to index.html for client
// side routes.
func (s *Server) spaHandler() http.HandlerFunc {
	fileServer := http.FileServer(http.FS(s.staticFS))
	return func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if strings.HasPrefix(p, "api/") {
			http.NotFound(w, r)
			return
		}
		if p == "" {
			p = "index.html"
		}
		if f, err := s.staticFS.Open(p); err == nil {
			safe.Close(r.Context(), f)
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		index, err := s.staticFS.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer safe.Close(r.Context(), index)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		safe.Copy(r.Context(), w, index)
	}
}
