package http

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/markup"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
	"github.com/m-mizutani/papers/pkg/utils/safe"
)

func (s *Server) routeAPI(r chi.Router) {
	r.Get("/block-types", s.handleBlockTypes)

	r.Route("/tokens", func(r chi.Router) {
		r.Use(sessionOnly)
		r.Get("/", s.handleListTokens)
		r.Post("/", s.handleCreateToken)
		r.Delete("/{token}", s.handleRevokeToken)
	})

	r.Get("/workspaces", s.handleListWorkspaces)
	r.Route("/workspaces/{ws}", func(r chi.Router) {
		r.Get("/", s.handleGetWorkspace)
		r.Get("/sidebar", s.handleSidebar)
		r.Get("/search", s.handleSearch)
		r.Get("/trash", s.handleListTrash)
		r.Get("/resolve", s.handleResolve)
		r.Get("/papers", s.handleListPapers)
		r.Post("/papers", s.handleCreatePaper)
		r.Route("/papers/{paper}", func(r chi.Router) {
			r.Get("/", s.handleGetPaper)
			r.Get("/blocks", s.handleListBlocks)
			r.Post("/ops", s.handleOps)
			r.Get("/children", s.handleChildren)
			r.Get("/status", s.handleStatus)
			r.Get("/versions", s.handleListVersions)
			r.Post("/versions", s.handleSaveVersion)
			r.Get("/versions/{n}", s.handleGetVersion)
			r.Post("/versions/{n}/restore", s.handleRestoreVersion)
			r.Get("/diff", s.handleDiff)
			r.Post("/move", s.handleMove)
			r.Put("/visibility", s.handleVisibility)
			r.Post("/archive", s.handleArchive(true))
			r.Post("/unarchive", s.handleArchive(false))
			r.Post("/trash", s.handleTrash)
			r.Post("/restore", s.handleRestoreTrash)
			r.Put("/favorite", s.handleFavorite)
			r.Delete("/favorite", s.handleUnfavorite)
			r.Post("/presence", s.handlePresence)
			r.Get("/markdown", s.handleMarkdown)
			r.Post("/attachments", s.handleUpload)
			r.Get("/attachments/{att}", s.handleAttachment)
			r.Get("/html", s.handleReadHTML)
			r.Get("/html/{att}", s.handleHTML)
			r.Get("/live", s.handleLive)
		})
	})
}

func wsParam(r *http.Request) model.WorkspaceID { return model.WorkspaceID(chi.URLParam(r, "ws")) }
func paperParam(r *http.Request) model.PaperID  { return model.PaperID(chi.URLParam(r, "paper")) }

// respond writes v, or the error.
func respond(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	writeJSON(r.Context(), w, status, v)
}

func queryInt(r *http.Request, name string) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, goerr.Wrap(model.ErrInvalidArgument, "invalid "+name, goerr.V(name, v))
	}
	return n, nil
}

// --- meta

type blockTypeInfo struct {
	Type          model.BlockType `json:"type"`
	HumanEditable bool            `json:"human_editable"`
	Container     bool            `json:"container"`
	Parent        model.BlockType `json:"parent,omitempty"`
	Markup        bool            `json:"markup"`
}

func blockTypeInfos() []blockTypeInfo {
	var out []blockTypeInfo
	for _, t := range model.BlockTypes() {
		out = append(out, blockTypeInfo{Type: t, HumanEditable: t.HumanEditable(), Container: t.IsContainer(), Parent: t.RequiredParent(), Markup: t.UsesMarkup()})
	}
	return out
}

func (s *Server) handleBlockTypes(w http.ResponseWriter, r *http.Request) {
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"block_types": blockTypeInfos(), "palette": markup.Palette})
}

// --- tokens

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
	out := make([]tokenJSON, 0, len(toks))
	for _, t := range toks {
		out = append(out, toTokenJSON(t))
	}
	respond(w, r, http.StatusOK, map[string]any{"tokens": out}, err)
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

// --- workspaces

func (s *Server) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	list, groups, err := s.uc.ListWorkspaces(r.Context(), principalFrom(r.Context()))
	if list == nil {
		list = []*usecase.WorkspaceView{}
	}
	if groups == nil {
		groups = []model.WorkspaceGroup{}
	}
	respond(w, r, http.StatusOK, map[string]any{"workspaces": list, "groups": groups}, err)
}

func (s *Server) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	v, err := s.uc.GetWorkspace(r.Context(), principalFrom(r.Context()), wsParam(r))
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleSidebar(w http.ResponseWriter, r *http.Request) {
	v, err := s.uc.Sidebar(r.Context(), principalFrom(r.Context()), wsParam(r))
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit")
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	q := r.URL.Query()
	hits, err := s.uc.Search(r.Context(), principalFrom(r.Context()), wsParam(r), usecase.SearchInput{
		Query: q.Get("q"), Under: model.PaperID(q.Get("under")), Limit: limit, IncludeArchived: q.Get("archived") == "true",
	})
	if hits == nil {
		hits = []*usecase.SearchHit{}
	}
	respond(w, r, http.StatusOK, map[string]any{"hits": hits}, err)
}

func (s *Server) handleListTrash(w http.ResponseWriter, r *http.Request) {
	list, err := s.uc.ListTrash(r.Context(), principalFrom(r.Context()), wsParam(r))
	if list == nil {
		list = []*usecase.TrashItem{}
	}
	respond(w, r, http.StatusOK, map[string]any{"papers": list}, err)
}

func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	p, err := s.uc.ResolvePaper(r.Context(), principalFrom(r.Context()), wsParam(r), r.URL.Query().Get("path"))
	respond(w, r, http.StatusOK, p, err)
}

// handleListPapers reads one page of the papers list. Every parameter is
// optional; an unknown value is 400.
func (s *Server) handleListPapers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := usecase.ListPapersInput{
		Place: usecase.PaperPlace(q.Get("place")), Creator: usecase.PaperCreator(q.Get("creator")),
		Visibility: model.Visibility(q.Get("visibility")), Sort: model.PaperSort(q.Get("sort")), Cursor: q.Get("cursor"),
	}
	switch v := q.Get("archived"); v {
	case "", "false":
	case "true":
		in.Archived = true
	default:
		writeError(r.Context(), w, goerr.Wrap(model.ErrInvalidArgument, "archived must be true or false", goerr.V("archived", v)))
		return
	}
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 {
			writeError(r.Context(), w, goerr.Wrap(model.ErrInvalidArgument, "limit must be between 1 and 100", goerr.V("limit", q.Get("limit"))))
			return
		}
		in.Limit = n
	}
	page, err := s.uc.ListPapers(r.Context(), principalFrom(r.Context()), wsParam(r), in)
	respond(w, r, http.StatusOK, page, err)
}

type createPaperRequest struct {
	Title      string           `json:"title"`
	Visibility model.Visibility `json:"visibility"`
	Parent     string           `json:"parent"`
	After      *string          `json:"after"`
	Markdown   string           `json:"markdown"`
	// Kind is blocks (default) or html; an HTML paper needs HTML.
	Kind model.PaperKind `json:"kind"`
	HTML string          `json:"html"`
}

func (s *Server) handleCreatePaper(w http.ResponseWriter, r *http.Request) {
	var req createPaperRequest
	if err := decodeJSONLimit(r, &req, maxHTMLRequestBody); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	p, err := s.uc.CreatePaper(r.Context(), principalFrom(r.Context()), wsParam(r), usecase.CreateInput{
		Title: req.Title, Visibility: req.Visibility, Parent: req.Parent, After: req.After, Markdown: req.Markdown,
		Kind: req.Kind, HTML: req.HTML,
	})
	respond(w, r, http.StatusCreated, p, err)
}

// --- papers

func (s *Server) handleGetPaper(w http.ResponseWriter, r *http.Request) {
	v, err := s.uc.GetPaper(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r))
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleListBlocks(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit")
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	page, err := s.uc.ListBlocks(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), r.URL.Query().Get("after"), limit)
	if page != nil && page.Blocks == nil {
		page.Blocks = []*model.Block{}
	}
	respond(w, r, http.StatusOK, page, err)
}

type opsRequest struct {
	Ops   []usecase.Op `json:"ops"`
	Force bool         `json:"force"`
}

func (s *Server) handleOps(w http.ResponseWriter, r *http.Request) {
	var req opsRequest
	if err := decodeJSONLimit(r, &req, maxHTMLRequestBody); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	res, err := s.uc.Apply(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), usecase.BatchInput{Ops: req.Ops, Force: req.Force})
	respond(w, r, http.StatusOK, res, err)
}

func (s *Server) handleChildren(w http.ResponseWriter, r *http.Request) {
	list, err := s.uc.ListChildren(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), r.URL.Query().Get("archived") == "true")
	if list == nil {
		list = []*usecase.PaperRef{}
	}
	respond(w, r, http.StatusOK, map[string]any{"papers": list}, err)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.uc.Status(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r))
	respond(w, r, http.StatusOK, st, err)
}

func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	before, err := queryInt(r, "before")
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	limit, err := queryInt(r, "limit")
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	list, err := s.uc.ListVersions(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), before, limit)
	if list == nil {
		list = []*usecase.VersionSummary{}
	}
	respond(w, r, http.StatusOK, map[string]any{"versions": list}, err)
}

type saveVersionRequest struct {
	Note string `json:"note"`
}

func (s *Server) handleSaveVersion(w http.ResponseWriter, r *http.Request) {
	var req saveVersionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	v, err := s.uc.SaveVersion(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), req.Note)
	var out *usecase.VersionSummary
	if v != nil {
		out = &usecase.VersionSummary{Number: v.Number, Title: v.Title, Note: v.Note, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt, BlockCount: len(v.Blocks)}
	}
	// version is null when nothing changed since the latest version
	respond(w, r, http.StatusOK, map[string]any{"version": out}, err)
}

func versionParam(r *http.Request) (int, error) {
	return usecase.ParseAt(chi.URLParam(r, "n"))
}

func (s *Server) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	n, err := versionParam(r)
	if err == nil && n == 0 {
		err = goerr.Wrap(model.ErrInvalidArgument, "a version number or latest is required")
	}
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	v, err := s.uc.GetVersion(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), n)
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleRestoreVersion(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(chi.URLParam(r, "n"))
	if err != nil {
		writeError(r.Context(), w, goerr.Wrap(model.ErrInvalidArgument, "invalid version number"))
		return
	}
	res, err := s.uc.RestoreVersion(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), n)
	respond(w, r, http.StatusOK, res, err)
}

// handleDiff compares ?from= and ?to=: version numbers, "latest", or
// empty / "current" for the current state.
func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	from, err := usecase.ParseAt(r.URL.Query().Get("from"))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	to, err := usecase.ParseAt(r.URL.Query().Get("to"))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	d, err := s.uc.Diff(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), from, to)
	respond(w, r, http.StatusOK, d, err)
}

type moveRequest struct {
	Parent    string            `json:"parent"`
	After     *string           `json:"after"`
	Workspace model.WorkspaceID `json:"workspace"`
	Unfiled   bool              `json:"unfiled"`
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	var req moveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	res, err := s.uc.MovePaper(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), usecase.MoveInput{Parent: req.Parent, After: req.After, Workspace: req.Workspace, Unfiled: req.Unfiled})
	respond(w, r, http.StatusOK, res, err)
}

type visibilityRequest struct {
	Visibility model.Visibility `json:"visibility"`
	Parent     string           `json:"parent"`
	After      *string          `json:"after"`
}

func (s *Server) handleVisibility(w http.ResponseWriter, r *http.Request) {
	var req visibilityRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	res, err := s.uc.SetVisibility(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), usecase.VisibilityInput{Visibility: req.Visibility, Parent: req.Parent, After: req.After})
	respond(w, r, http.StatusOK, res, err)
}

type archiveRequest struct {
	Subtree bool `json:"subtree"`
}

func (s *Server) handleArchive(archived bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req archiveRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(r.Context(), w, err)
			return
		}
		p := principalFrom(r.Context())
		var res *usecase.BatchResult
		var err error
		if archived {
			res, err = s.uc.Archive(r.Context(), p, wsParam(r), paperParam(r), req.Subtree)
		} else {
			res, err = s.uc.Unarchive(r.Context(), p, wsParam(r), paperParam(r), req.Subtree)
		}
		respond(w, r, http.StatusOK, res, err)
	}
}

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	res, err := s.uc.Trash(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r))
	respond(w, r, http.StatusOK, res, err)
}

func (s *Server) handleRestoreTrash(w http.ResponseWriter, r *http.Request) {
	res, err := s.uc.RestoreFromTrash(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r))
	respond(w, r, http.StatusOK, res, err)
}

type favoriteRequest struct {
	After *string `json:"after"`
}

func (s *Server) handleFavorite(w http.ResponseWriter, r *http.Request) {
	var req favoriteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	if err := s.uc.Favorite(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), req.After); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnfavorite(w http.ResponseWriter, r *http.Request) {
	if err := s.uc.Unfavorite(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r)); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type presenceRequest struct {
	BlockID model.BlockID `json:"block_id"`
	Leaving bool          `json:"leaving"`
}

func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	var req presenceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	if err := s.uc.ReportPresence(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), req.BlockID, req.Leaving); err != nil {
		writeError(r.Context(), w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMarkdown(w http.ResponseWriter, r *http.Request) {
	md, err := s.uc.Export(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// text/markdown with nosniff (server.go), so browsers never render it as HTML
	_, _ = io.WriteString(w, md) // #nosec G705
}

// handleUpload takes the image as the raw request body; its Content-Type
// is the image type and ?name= the file name.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, model.MaxAttachmentSize+1)
	a, err := s.uc.Upload(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), usecase.UploadInput{
		Name: r.URL.Query().Get("name"), ContentType: r.Header.Get("Content-Type"), Body: body,
	})
	respond(w, r, http.StatusCreated, a, err)
}

func (s *Server) handleReadHTML(w http.ResponseWriter, r *http.Request) {
	v, err := s.uc.ReadHTML(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), r.URL.Query().Get("at"))
	respond(w, r, http.StatusOK, v, err)
}

// htmlFrameCSP is the Content-Security-Policy of an HTML paper's document
// served on its own: it runs as an opaque origin with scripts, whatever
// opens it, and only papers' own pages may frame it.
const htmlFrameCSP = "sandbox allow-scripts; frame-ancestors 'self'"

// handleHTML serves one document of an HTML paper. It always streams the
// bytes through the server, unlike images, which may be redirected to a
// signed URL. Signed URLs may be added later to keep large documents off
// the server; doing so needs all of: deciding it for every attachment, the
// signing permission (iam.serviceAccounts.signBlob) together with a CORS
// rule on the bucket (the UI fetches the document), signing with
// response-content-disposition=attachment so that opening the URL never
// runs the document on the storage origin, and keeping this route for
// "Open HTML in a new tab".
func (s *Server) handleHTML(w http.ResponseWriter, r *http.Request) {
	c, err := s.uc.OpenHTML(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), model.AttachmentID(chi.URLParam(r, "att")))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	csp, mode := htmlFrameCSP, "open"
	if !c.ExternalResources {
		csp, mode = csp+"; "+model.SandboxCSP, "closed"
	}
	h := w.Header()
	// The document never changes, but its policy follows the workspace's
	// sandbox setting, which a deploy may change: browsers revalidate every
	// time, and the ETag (document and setting) answers 304 while both stay.
	etag := `"` + string(c.Attachment.ID) + "." + mode + `"`
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", etag)
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body, err := c.Open(r.Context())
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	defer safe.Close(r.Context(), body)
	h.Set("Content-Type", model.HTMLObjectContentType)
	h.Set("Content-Length", strconv.FormatInt(c.Attachment.Size, 10))
	safe.Copy(r.Context(), w, body)
}

func (s *Server) handleAttachment(w http.ResponseWriter, r *http.Request) {
	c, err := s.uc.OpenAttachment(r.Context(), principalFrom(r.Context()), wsParam(r), paperParam(r), model.AttachmentID(chi.URLParam(r, "att")))
	if err != nil {
		writeError(r.Context(), w, err)
		return
	}
	if c.URL != "" {
		http.Redirect(w, r, c.URL, http.StatusFound) // #nosec G710 -- a signed URL issued by our AttachmentStore
		return
	}
	defer safe.Close(r.Context(), c.Body)
	h := w.Header()
	h.Set("Content-Type", c.Attachment.ContentType)
	h.Set("Content-Length", strconv.FormatInt(c.Attachment.Size, 10))
	// immutable: an attachment never changes
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	safe.Copy(r.Context(), w, c.Body)
}
