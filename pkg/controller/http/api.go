package http

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
)

func (s *Server) routeAPI(r chi.Router) {
	r.Get("/block-types", s.handleBlockTypes)
	r.Get("/users", s.handleListUsers)

	r.Get("/groups", s.handleListGroups)
	r.With(sessionOnly).Post("/groups", s.handleCreateGroup)
	r.With(sessionOnly).Put("/groups/{group}", s.handleUpdateGroup)
	r.With(sessionOnly).Delete("/groups/{group}", s.handleDeleteGroup)

	r.Route("/tokens", func(r chi.Router) {
		r.Use(sessionOnly)
		r.Get("/", s.handleListTokens)
		r.Post("/", s.handleCreateToken)
		r.Delete("/{token}", s.handleRevokeToken)
	})

	r.Get("/workspaces", s.handleListWorkspaces)
	r.Post("/workspaces", s.handleCreateWorkspace)
	r.Route("/workspaces/{ws}", func(r chi.Router) {
		r.Get("/", s.handleGetWorkspace)
		r.Patch("/", s.handleUpdateWorkspace)
		r.Get("/search", s.handleSearch)
		r.Get("/papers", s.handleListPapers)
		r.Post("/papers", s.handleCreatePaper)
		r.Route("/papers/{paper}", func(r chi.Router) {
			r.Get("/", s.handleGetPaper)
			r.Delete("/", s.handleDeletePaper)
			r.Put("/access", s.handleUpdateAccess)
			r.Get("/commits", s.handleListCommits)
			r.Post("/commits", s.handleCommit)
			r.Get("/markdown", s.handleMarkdown)
			r.Put("/blocks/{block}/lock", s.handleLock)
			r.Delete("/blocks/{block}/lock", s.handleUnlock)
			r.Get("/live", s.handleLive)
		})
	})
}

func wsParam(r *http.Request) model.WorkspaceID { return model.WorkspaceID(chi.URLParam(r, "ws")) }
func paperParam(r *http.Request) model.PaperID  { return model.PaperID(chi.URLParam(r, "paper")) }

// --- meta ---------------------------------------------------------------

type blockTypeInfo struct {
	Type          model.BlockType `json:"type"`
	HumanEditable bool            `json:"human_editable"`
	Container     bool            `json:"container"`
	Parent        model.BlockType `json:"parent,omitempty"`
}

func blockTypeInfos() []blockTypeInfo {
	var out []blockTypeInfo
	for _, t := range model.BlockTypes() {
		out = append(out, blockTypeInfo{Type: t, HumanEditable: t.HumanEditable(), Container: t.IsContainer(), Parent: t.RequiredParent()})
	}
	return out
}

func (s *Server) handleBlockTypes(w http.ResponseWriter, r *http.Request) {
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"block_types": blockTypeInfos()})
}

type userJSON struct {
	ID        model.UserID `json:"id"`
	Email     string       `json:"email"`
	Name      string       `json:"name"`
	AvatarURL string       `json:"avatar_url,omitempty"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.uc.ListUsers(r.Context(), principalFrom(r.Context()))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	out := make([]userJSON, 0, len(users))
	for _, u := range users {
		out = append(out, userJSON{ID: u.ID, Email: u.Email, Name: u.Name, AvatarURL: u.AvatarURL})
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"users": out})
}

// --- groups ---------------------------------------------------------------

type groupRequest struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Members     []model.UserID `json:"members"`
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	gs, err := s.uc.ListGroups(r.Context(), principalFrom(r.Context()))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	if gs == nil {
		gs = []*model.Group{}
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"groups": gs})
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req groupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	g, err := s.uc.CreateGroup(r.Context(), principalFrom(r.Context()), usecase.GroupInput(req))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusCreated, g)
}

func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	var req groupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	g, err := s.uc.UpdateGroup(r.Context(), principalFrom(r.Context()), model.GroupID(chi.URLParam(r, "group")), usecase.GroupInput(req))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, g)
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if err := s.uc.DeleteGroup(r.Context(), principalFrom(r.Context()), model.GroupID(chi.URLParam(r, "group"))); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- tokens ---------------------------------------------------------------

// tokenJSON never carries the secret hash.
type tokenJSON struct {
	ID         model.TokenID       `json:"id"`
	Name       string              `json:"name"`
	Kind       model.TokenKind     `json:"kind"`
	AgentName  string              `json:"agent_name,omitempty"`
	Scope      model.TokenScope    `json:"scope"`
	Workspaces []model.WorkspaceID `json:"workspaces,omitempty"`
	CreatedAt  time.Time           `json:"created_at"`
	ExpiresAt  *time.Time          `json:"expires_at,omitempty"`
	LastUsedAt *time.Time          `json:"last_used_at,omitempty"`
}

func toTokenJSON(t *model.APIToken) tokenJSON {
	return tokenJSON{ID: t.ID, Name: t.Name, Kind: t.Kind, AgentName: t.AgentName, Scope: t.Scope, Workspaces: t.Workspaces, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt}
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	toks, err := s.auth.ListTokens(r.Context(), principalFrom(r.Context()))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	out := make([]tokenJSON, 0, len(toks))
	for _, t := range toks {
		out = append(out, toTokenJSON(t))
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"tokens": out})
}

type createTokenRequest struct {
	Name       string              `json:"name"`
	Kind       model.TokenKind     `json:"kind"`
	AgentName  string              `json:"agent_name"`
	Scope      model.TokenScope    `json:"scope"`
	Workspaces []model.WorkspaceID `json:"workspaces"`
	TTLHours   int                 `json:"ttl_hours"`
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	tok, raw, err := s.auth.CreateToken(r.Context(), principalFrom(r.Context()), usecase.CreateTokenInput{
		Name: req.Name, Kind: req.Kind, AgentName: req.AgentName, Scope: req.Scope,
		Workspaces: req.Workspaces, TTL: time.Duration(req.TTLHours) * time.Hour,
	})
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusCreated, map[string]any{"token": toTokenJSON(tok), "secret": raw})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.RevokeToken(r.Context(), principalFrom(r.Context()), model.TokenID(chi.URLParam(r, "token"))); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- workspaces -------------------------------------------------------------

type workspaceJSON struct {
	*model.Workspace
	Role model.Role `json:"role"`
}

func (s *Server) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	views, err := s.uc.ListWorkspaces(r.Context(), principalFrom(r.Context()))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	out := make([]workspaceJSON, 0, len(views))
	for _, v := range views {
		out = append(out, workspaceJSON{Workspace: v.Workspace, Role: v.Role})
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"workspaces": out})
}

type createWorkspaceRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Emoji       string `json:"emoji"`
}

func (s *Server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req createWorkspaceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	v, err := s.uc.CreateWorkspace(r.Context(), principalFrom(r.Context()), usecase.CreateWorkspaceInput(req))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusCreated, workspaceJSON{Workspace: v.Workspace, Role: v.Role})
}

func (s *Server) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	v, err := s.uc.GetWorkspace(r.Context(), principalFrom(r.Context()), wsParam(r))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, workspaceJSON{Workspace: v.Workspace, Role: v.Role})
}

type updateWorkspaceRequest struct {
	Name        *string              `json:"name"`
	Description *string              `json:"description"`
	Emoji       *string              `json:"emoji"`
	Bindings    *[]model.RoleBinding `json:"bindings"`
}

func (s *Server) handleUpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req updateWorkspaceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	v, err := s.uc.UpdateWorkspace(r.Context(), principalFrom(r.Context()), wsParam(r), usecase.UpdateWorkspaceInput(req))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, workspaceJSON{Workspace: v.Workspace, Role: v.Role})
}

// --- papers -----------------------------------------------------------------

type paperJSON struct {
	*model.Paper
	Role model.Role `json:"role"`
}

func (s *Server) handleListPapers(w http.ResponseWriter, r *http.Request) {
	list, err := s.uc.ListPapers(r.Context(), principalFrom(r.Context()), wsParam(r))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	out := make([]paperJSON, 0, len(list))
	for _, p := range list {
		out = append(out, paperJSON{Paper: p.Paper, Role: p.Role})
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"papers": out})
}

type createPaperRequest struct {
	Title        string `json:"title"`
	Markdown     string `json:"markdown"`
	Restricted   bool   `json:"restricted"`
	ConnectionID string `json:"connection_id"`
}

type paperViewJSON struct {
	Paper  *model.Paper       `json:"paper"`
	Blocks []*model.Block     `json:"blocks"`
	Locks  []*model.BlockLock `json:"locks"`
	Role   model.Role         `json:"role"`
	// LockTTLMillis tells clients how often to renew their locks.
	LockTTLMillis int64 `json:"lock_ttl_ms"`
}

func (s *Server) toPaperView(v *usecase.PaperView) paperViewJSON {
	out := paperViewJSON{Paper: v.Paper, Blocks: v.Blocks, Locks: v.Locks, Role: v.Role, LockTTLMillis: s.uc.LockTTL()}
	if out.Blocks == nil {
		out.Blocks = []*model.Block{}
	}
	if out.Locks == nil {
		out.Locks = []*model.BlockLock{}
	}
	return out
}

func (s *Server) handleCreatePaper(w http.ResponseWriter, r *http.Request) {
	var req createPaperRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	v, err := s.uc.CreatePaper(r.Context(), principalFrom(r.Context()), wsParam(r), usecase.CreatePaperInput{
		Title: req.Title, Markdown: req.Markdown, Restricted: req.Restricted, HolderKey: req.ConnectionID,
	})
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusCreated, s.toPaperView(v))
}

func (s *Server) handleGetPaper(w http.ResponseWriter, r *http.Request) {
	v, err := s.uc.GetPaper(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, s.toPaperView(v))
}

func (s *Server) handleDeletePaper(w http.ResponseWriter, r *http.Request) {
	if err := s.uc.DeletePaper(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r)); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateAccess(w http.ResponseWriter, r *http.Request) {
	var req model.PaperAccess
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	p, err := s.uc.UpdatePaperAccess(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), req)
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, p)
}

func (s *Server) handleListCommits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	after, limit := int64(0), 0
	if v := q.Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(r.Context(), w, goerr.Wrap(model.ErrInvalidArgument, "invalid after"))
			return
		}
		after = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(r.Context(), w, goerr.Wrap(model.ErrInvalidArgument, "invalid limit"))
			return
		}
		limit = n
	}
	commits, err := s.uc.ListCommits(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), after, limit)
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	if commits == nil {
		commits = []*model.Commit{}
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"commits": commits})
}

type commitRequest struct {
	Ops          []usecase.Op `json:"ops"`
	Message      string       `json:"message"`
	Force        bool         `json:"force"`
	ConnectionID string       `json:"connection_id"`
}

type commitResponse struct {
	Commit *model.Commit `json:"commit"`
	Paper  *model.Paper  `json:"paper"`
}

func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request) {
	var req commitRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	res, err := s.uc.Commit(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), usecase.CommitInput{
		Ops: req.Ops, Message: req.Message, Force: req.Force, HolderKey: req.ConnectionID,
	})
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, commitResponse{Commit: res.Commit, Paper: res.Paper})
}

func (s *Server) handleMarkdown(w http.ResponseWriter, r *http.Request) {
	md, err := s.uc.ExportMarkdown(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(md)); err != nil {
		writeError(r.Context(), w, goerr.Wrap(err, "failed to write markdown"))
	}
}

type lockRequest struct {
	ConnectionID string `json:"connection_id"`
}

func (s *Server) holderKey(r *http.Request, connectionID string) string {
	if connectionID != "" {
		return connectionID
	}
	return principalFrom(r.Context()).HolderKey
}

func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	var req lockRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(r.Context(), w, err)
			return
		}
	}
	lock, err := s.uc.AcquireLock(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), model.BlockID(chi.URLParam(r, "block")), s.holderKey(r, req.ConnectionID))
	if err != nil {
		if errors.Is(err, model.ErrBlockLocked) {
			writeErrorWithLock(r.Context(), w, err, lock)
			return
		}
		writeError(r.Context(), w, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, lock)
}

func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	var req lockRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(r.Context(), w, err)
			return
		}
	}
	if err := s.uc.ReleaseLock(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), model.BlockID(chi.URLParam(r, "block")), s.holderKey(r, req.ConnectionID)); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type searchHitJSON struct {
	Paper   *model.Paper  `json:"paper"`
	BlockID model.BlockID `json:"block_id,omitempty"`
	Snippet string        `json:"snippet"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	hits, err := s.uc.SearchPapers(r.Context(), principalFrom(r.Context()), wsParam(r), r.URL.Query().Get("q"), 50)
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	out := make([]searchHitJSON, 0, len(hits))
	for _, h := range hits {
		out = append(out, searchHitJSON{Paper: h.Paper, BlockID: h.BlockID, Snippet: h.Snippet})
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"hits": out})
}
