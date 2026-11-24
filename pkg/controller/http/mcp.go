package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/m-mizutani/goerr/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/m-mizutani/papers/pkg/domain/markup"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
	"github.com/m-mizutani/papers/pkg/utils/logging"
)

const (
	mcpServerName    = "papers"
	mcpServerVersion = "0.2.0"
	mcpDefaultAgent  = "MCP client"
)

// mcpInstructions is sent to clients on initialize and returned by
// papers_guide. It is the contract an AI needs to write good papers.
const mcpInstructions = `papers is a shared knowledge canvas edited by humans and AI together.

Model
- A workspace holds papers. Each workspace has a home paper; papers nest: a child paper is a "paper" block in its parent's body. Refer to papers by id or by a path of titles from the home ("Runbooks/DB failover").
- A public paper without a parent (other than the home) is unfiled: it is in no body and shows in the people's papers list until someone moves it under a parent. Unfiled papers cannot have child papers. A paper created without a parent is unfiled, so when you know where a new paper belongs, give its parent.
- A paper has a title and an ordered list of blocks. What you write becomes the current state at once. A version (v1, v2, ...) exists only when someone saves one explicitly (papers_save_version). Save a version when you finish a coherent change and the user would want to point at it; never save on every edit.
- Each workspace has rules (its description). Follow them; they come with papers_guide and every read and write.
- public papers are for everyone who can use the workspace; private papers only for their creator (you act for the user who issued your token).
- There are no locks. update and delete carry base_rev: the block's rev you read. If someone changed that block's contents since, the whole batch fails with a conflict listing each block's current state. Re-read, redo your edit on top, and send again. Send force:true only when the user explicitly wants their version to win. Moves never conflict.
- Block IDs are issued by the server. To point at a block you insert in the same batch, give the insert a ref ("n1") and use after:"ref:n1"; the result maps refs to IDs.

Block types (type: props)
- paragraph, quote, heading (props.level 1-3), bulleted_list_item / numbered_list_item (props.indent 0-6), todo (props.indent, props.checked), callout (props.icon, one emoji), table_cell: content is inline markup: **bold**, *italic*, ~~strike~~, ` + "`code`" + `, [link](url), and colour [text]{.red} / background [text]{.bg-yellow}. Colours: gray brown orange yellow green blue purple pink red.
- code: props.language, content verbatim. divider: no content. image: content = caption, props.url (external) or props.attachment_id (an image attached to this paper).
- paper: a child paper. Insert it with {"op":"insert","type":"paper","title":"..."}; it creates the child. Deleting it sends the child (and everything under it) to the trash if you created them all, otherwise archives the child with everything under it.
- table: props.rows / props.cols are (id, order) lists, props.header_row. Cells are table_cell blocks with props.row_id / col_id. Insert with "cells" (rows of strings); then use insert_row / delete_row / move_row / insert_col / delete_col / move_col (block_id = the table, line_id = the row or column, values = new cell contents). Update a cell by its block id.
- mermaid, svg (a complete <svg> with a viewBox), html (self-contained, rendered in a sandboxed iframe): only AI writes these; props.height sets the frame height.
- HTML papers: a paper whose whole body is one HTML document you write; people only view it (in a sandboxed frame where its scripts run) and cannot edit it. Create one with papers_create_paper kind:"html" and its html (required; the paper and its HTML are written together or not at all), or with {"op":"insert","type":"paper","title":"...","paper_kind":"html","html":"..."} in its parent's batch. Replace the whole document with {"op":"set_html","html":"...","base_rev":N}: N is the rev of its one block (type html_page) from papers_read, which also returns the current html; it can go in the same batch as set_title. No other block op works on an HTML paper, and it cannot have child papers. Its title, place, visibility, versions, archive and trash work as for any paper. The document is at most 5 MiB of UTF-8; make it self-contained or use absolute URLs (relative URLs do not resolve). Its scripts cannot reach papers, its API or the viewer's cookies, and cannot open new windows. Whether it may load anything from other hosts (CDN scripts, images, fetch) is up to the workspace: see sandbox in papers_guide with workspace_id.
- In svg and html, mark each value people may change with data-field="name" on its element and put the value in props.fields ({"rto":"15m"}); people then edit just those values in place. Names: lowercase letter first, then lowercase letters, digits or _ (at most 32 characters); at most 32 fields; each value one line, at most 200 bytes. Field values are searchable.

Ops (papers_write; one batch is atomic, at most 240 blocks / 3 MiB)
- {"op":"insert","type":"paragraph","content":"...","after":"<block id>|ref:n1|''","ref":"n2"}  after omitted = append, "" = top.
- {"op":"update","block_id":"...","base_rev":3,"content":"...","props":{...}}  props replaces all props.
- {"op":"delete","block_id":"...","base_rev":3}   {"op":"move","block_id":"...","after":"..."}
- {"op":"set_title","title":"..."}
- {"op":"insert_markdown","markdown":"## Section\n\ntext\n\n- a\n- b","after":"..."}  the fastest way to write a lot; fenced mermaid/svg/html become rich blocks, GFM tables become tables.
- Paper-level ops, each alone in its batch: move_paper {parent (required), after, workspace} or {unfiled:true} (out of the tree; only for a paper without child papers), set_visibility {visibility, parent} (parent is optional: making a paper public without one leaves it unfiled), save_version {note}, restore_version {version}, archive / unarchive {subtree}.
- Work on everything under a paper (deleting a paper block, archive with subtree, moving to another workspace) runs in the background after the call returns; read the papers again to see the result. Deleting a child whose subtree has someone else's paper archives it instead. While a move runs, its papers cannot be written (error "busy"); afterwards the old paper id answers with where it moved.

Databases (tables of papers, like Notion databases)
- A database is a paper of kind "database" (papers_create_paper with kind:"database", or insert {"type":"paper","paper_kind":"database"}). It has no body and no versions; it holds property definitions (columns) and its items in order. Each item is a paper of kind "item" with a body of its own; it stays in its database.
- Property types: text, date (YYYY-MM-DD), select, multi_select. select and multi_select choose from elements: each element has an id and a value (the text people see). Property values store element ids, never the text: renaming an element changes every item at once.
- papers_read on a database returns its properties (with element ids and values) and a page of items with their property values; pass filter and sort to narrow and order them. papers_read on an item also returns its property values.
- Write a database with papers_write on the database. Ops:
  {"op":"add_property","name":"Status","property_type":"select","ref":"p1"}  update_property {property, name}  move_property {property, after}  delete_property {property}
  {"op":"add_element","property":"<id>|ref:p1","value":"Doing","color":"blue","ref":"e1"}  update_element {property, element, value?, color?}  move_element {property, element, after}  delete_element {property, element}
  {"op":"insert_item","title":"...","set":{"<property id>":"<value>"},"ref":"i1"}  {"op":"set_values","item":"<id>","set":{"<property id>":"ref:e1","<multi id>":["<element id>"],"<date id>":"2026-12-10","<text id>":"...","<id>":null}}  move_item {item, after}  set_title {title}
  Create an element and use it in one batch through its ref. null or "" clears a value. Colors: gray brown orange yellow green blue purple pink red.
- Item bodies, titles and versions are written as for any paper (papers_write / papers_save_version on the item). A version of an item includes its property values.`

func (s *Server) mcpHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: mcpServerName, Version: mcpServerVersion}, &mcp.ServerOptions{Instructions: mcpInstructions})
	s.registerMCPTools(server)
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true,
		Logger:    logging.Default(),
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Writes through MCP are always attributed to an agent, even when the
		// caller authenticated with a session (no-authn dev mode).
		p := principalFrom(r.Context())
		if p != nil && p.Token == nil && p.AgentName == "" {
			cp := *p
			cp.AgentName = mcpDefaultAgent
			r = r.WithContext(withPrincipal(r.Context(), &cp))
		}
		streamable.ServeHTTP(w, r)
	})
}

func addTool[In, Out any](s *mcp.Server, name, description string, run func(ctx context.Context, p *model.Principal, in In) (Out, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: description},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
			var zero Out
			p := principalFrom(ctx)
			if p == nil {
				return nil, zero, goerr.Wrap(model.ErrUnauthenticated, "no principal")
			}
			out, err := run(ctx, p, in)
			if err != nil {
				return nil, zero, mcpError(err)
			}
			return nil, out, nil
		})
}

// mcpError explains errors an agent can act on.
func mcpError(err error) error {
	var conflict *usecase.ConflictError
	if errors.As(err, &conflict) {
		return mcpConflictError(conflict)
	}
	if f, ok := usecase.IsMoved(err); ok {
		return goerr.Wrap(model.ErrNotFound, "this paper moved to workspace "+string(f.ToWorkspaceID)+" as paper "+string(f.ToPaperID))
	}
	var amb *usecase.AmbiguousError
	if errors.As(err, &amb) {
		var names []string
		for _, c := range amb.Candidates {
			names = append(names, string(c.ID)+" ("+c.Title+")")
		}
		return goerr.Wrap(model.ErrInvalidArgument, "the path names several papers; pass one of these ids: "+strings.Join(names, ", "))
	}
	return err
}

type mcpEmpty struct{}

type mcpGuideIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id,omitempty" jsonschema:"optional: also return this workspace's rules and its home paper"`
}

type mcpGuideOut struct {
	Guide          string          `json:"guide"`
	BlockTypes     []blockTypeInfo `json:"block_types"`
	Palette        []string        `json:"palette"`
	WorkspaceRules string          `json:"workspace_rules,omitempty"`
	Language       string          `json:"language,omitempty"`
	// Home is the workspace's home paper as Markdown: its summary and
	// entry points, written by people and agents.
	Home string `json:"home,omitempty"`
	// Sandbox says what HTML (HTML papers, html and svg blocks) may load
	// in the workspace.
	Sandbox string `json:"sandbox,omitempty"`
}

const (
	sandboxOpen   = "HTML in this workspace may load scripts, styles, images, fonts and connections from other hosts."
	sandboxClosed = "HTML in this workspace cannot load anything from other hosts (no CDN scripts, external images, fonts or fetch): embed every script, style and image (data: URLs) in the HTML."
)

type mcpWorkspace struct {
	ID          model.WorkspaceID `json:"id"`
	Name        string            `json:"name"`
	Rules       string            `json:"rules,omitempty"`
	Language    string            `json:"language,omitempty"`
	HomePaperID model.PaperID     `json:"home_paper_id"`
}

type mcpWorkspacesOut struct {
	Workspaces []mcpWorkspace `json:"workspaces"`
}

type mcpSearchIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id" jsonschema:"workspace id"`
	Query       string            `json:"query" jsonschema:"words to find in titles and blocks; quote a phrase to keep it together"`
	Under       string            `json:"under,omitempty" jsonschema:"only under this paper (id or path)"`
	Limit       int               `json:"limit,omitempty" jsonschema:"max hits (default 20)"`
}

type mcpPaperRef struct {
	ID      model.PaperID     `json:"id"`
	Title   string            `json:"title"`
	Status  model.PaperStatus `json:"status"`
	Version int               `json:"version"`
	Private bool              `json:"private,omitempty"`
}

func toMCPRef(r *usecase.PaperRef) mcpPaperRef {
	return mcpPaperRef{ID: r.ID, Title: r.Title, Status: r.Status, Version: r.VersionNumber, Private: r.Visibility == model.VisibilityPrivate}
}

type mcpSearchHit struct {
	Paper   mcpPaperRef   `json:"paper"`
	BlockID model.BlockID `json:"block_id,omitempty"`
	Text    string        `json:"text"`
}

type mcpSearchOut struct {
	Hits []mcpSearchHit `json:"hits"`
}

type mcpReadIn struct {
	WorkspaceID model.WorkspaceID         `json:"workspace_id" jsonschema:"workspace id"`
	Paper       string                    `json:"paper" jsonschema:"paper id or path of titles from the home"`
	At          string                    `json:"at,omitempty" jsonschema:"omit for the current state; latest or a version number for a saved version"`
	After       string                    `json:"after,omitempty" jsonschema:"cursor from a previous read (next) to continue a long paper"`
	Limit       int                       `json:"limit,omitempty" jsonschema:"max top-level blocks (default 200); 0 with at reads the whole version"`
	TOC         bool                      `json:"toc,omitempty" jsonschema:"only headings and child papers, to find your way in a long paper"`
	Filter      []usecase.FilterCondition `json:"filter,omitempty" jsonschema:"database only: conditions all items must meet, e.g. [{\"property\":\"<id>\",\"op\":\"is\",\"elements\":[\"<element id>\"]}]; ops: is (select), contains (multi_select), on/before/after (date, with date), between (date, with from and to), starts_with (text, with text), is_empty, is_not_empty"`
	Sort        string                    `json:"sort,omitempty" jsonschema:"database only: <property id>|title|created|updated followed by :asc or :desc"`
}

// mcpValue is a property value with what it reads as.
type mcpValue struct {
	PropertyID model.PropertyID `json:"property_id"`
	Property   string           `json:"property"`
	Value      any              `json:"value"`
	// ElementValues are the values (texts) of the chosen elements of a
	// select or multi_select.
	ElementValues []string `json:"element_values,omitempty"`
}

type mcpItem struct {
	ID     model.PaperID `json:"id"`
	Title  string        `json:"title"`
	Values []mcpValue    `json:"values"`
}

func toMCPValues(props []model.Property, values []*model.PropertyValue) []mcpValue {
	out := []mcpValue{}
	for _, v := range values {
		prop := model.FindProperty(props, v.PropertyID)
		if prop == nil {
			continue
		}
		mv := mcpValue{PropertyID: v.PropertyID, Property: prop.Name, Value: v.Value}
		if prop.Type.HasElements() {
			ids, _ := v.Strings()
			if s, ok := v.Str(); ok {
				ids = []string{s}
			}
			for _, id := range ids {
				if i := prop.ElementIndex(model.ElementID(id)); i >= 0 {
					mv.ElementValues = append(mv.ElementValues, prop.Elements[i].Value)
				}
			}
		}
		out = append(out, mv)
	}
	return out
}

type mcpBlock struct {
	ID       model.BlockID    `json:"id"`
	ParentID model.BlockID    `json:"parent_id,omitempty"`
	Type     model.BlockType  `json:"type"`
	Content  string           `json:"content,omitempty"`
	Props    model.BlockProps `json:"props"`
	Rev      int64            `json:"rev"`
	By       string           `json:"last_updated_by,omitempty"`
}

type mcpReadOut struct {
	ID       model.PaperID     `json:"id"`
	Kind     model.PaperKind   `json:"kind"`
	Title    string            `json:"title"`
	Path     []mcpPaperRef     `json:"path"`
	Status   model.PaperStatus `json:"status"`
	Version  int               `json:"version"`
	At       string            `json:"at"`
	URL      string            `json:"url"`
	Blocks   []mcpBlock        `json:"blocks"`
	Children []mcpPaperRef     `json:"children,omitempty"`
	Next     string            `json:"next,omitempty"`
	// HTML is the document of an HTML paper at the state read.
	HTML           string `json:"html,omitempty"`
	WorkspaceRules string `json:"workspace_rules,omitempty"`
	// DatabaseID is the database an item belongs to.
	DatabaseID model.PaperID `json:"database_id,omitempty"`
	// Properties are a database's (or an item's database's) definitions.
	Properties []model.Property `json:"properties,omitempty"`
	// Items are a page of a database's items; Values an item's values.
	Items  []mcpItem  `json:"items,omitempty"`
	Values []mcpValue `json:"values,omitempty"`
}

type mcpCreateIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id" jsonschema:"workspace id"`
	Title       string            `json:"title" jsonschema:"paper title (single line)"`
	Parent      string            `json:"parent,omitempty" jsonschema:"parent paper id or path; omit to create an unfiled paper"`
	Private     bool              `json:"private,omitempty" jsonschema:"true for a paper only the user can see (outside the tree)"`
	Markdown    string            `json:"markdown,omitempty" jsonschema:"optional initial content as Markdown (not for an HTML paper)"`
	Kind        model.PaperKind   `json:"kind,omitempty" jsonschema:"blocks (default); html: an HTML paper whose whole body is the html you give; database: a table whose rows are items (then use papers_write with database ops)"`
	HTML        string            `json:"html,omitempty" jsonschema:"the document of an HTML paper (required with kind html; UTF-8, at most 5 MiB)"`
}

type mcpWriteIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id" jsonschema:"workspace id"`
	Paper       string            `json:"paper" jsonschema:"paper id or path"`
	Ops         []usecase.Op      `json:"ops" jsonschema:"operations applied atomically, see papers_guide"`
	Force       bool              `json:"force,omitempty" jsonschema:"overwrite newer changes by others instead of failing with a conflict; only when the user asks"`
}

type mcpWriteOut struct {
	PaperID model.PaperID `json:"paper_id"`
	// Refs maps each ref to the ID the server issued.
	Refs map[string]string `json:"refs,omitempty"`
	// Revs maps each changed block to its new rev; pass it as base_rev on
	// the next update of that block.
	Revs        map[model.BlockID]int64 `json:"revs,omitempty"`
	Deleted     []model.BlockID         `json:"deleted,omitempty"`
	Editing     []usecase.Editing       `json:"editing,omitempty"`
	Version     *int                    `json:"version,omitempty"`
	NotRestored []usecase.NotRestored   `json:"not_restored,omitempty"`
	// Unsaved reminds that the change is not in a version yet.
	Unsaved        bool   `json:"unsaved"`
	WorkspaceRules string `json:"workspace_rules,omitempty"`
}

type mcpSaveIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id" jsonschema:"workspace id"`
	Paper       string            `json:"paper" jsonschema:"paper id or path"`
	Note        string            `json:"note,omitempty" jsonschema:"what changed in this version"`
}

type mcpSaveOut struct {
	Saved   bool   `json:"saved"`
	Version int    `json:"version"`
	URL     string `json:"url,omitempty"`
}

type mcpVersionsIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id" jsonschema:"workspace id"`
	Paper       string            `json:"paper" jsonschema:"paper id or path"`
	Before      int               `json:"before,omitempty" jsonschema:"only versions before this number"`
	Limit       int               `json:"limit,omitempty" jsonschema:"max versions (default 50)"`
}

type mcpVersion struct {
	Number int    `json:"number"`
	Note   string `json:"note,omitempty"`
	By     string `json:"by"`
	At     string `json:"at"`
}

type mcpVersionsOut struct {
	Versions []mcpVersion `json:"versions"`
}

type mcpDiffIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id" jsonschema:"workspace id"`
	Paper       string            `json:"paper" jsonschema:"paper id or path"`
	From        string            `json:"from,omitempty" jsonschema:"version number or latest (default latest)"`
	To          string            `json:"to,omitempty" jsonschema:"version number, latest, or omit for the current state"`
}

// actorLabel is how a writer is shown: "Claude (for Alice)", or a person,
// marked when they wrote through the API (D7).
func actorLabel(a model.Actor) string {
	switch {
	case a.Kind == model.ActorAgent && a.AgentName != "":
		return a.AgentName + " (for " + a.Name + ")"
	case a.Kind == model.ActorAgent:
		return "agent (for " + a.Name + ")"
	case a.Kind == model.ActorSystem:
		return "papers"
	case a.TokenID != "":
		return a.Name + " (via API)"
	}
	return a.Name
}

const timeLayout = "2006-01-02T15:04:05Z07:00"

func paperURL(ws model.WorkspaceID, id model.PaperID, at string) string {
	u := "/ws/" + string(ws) + "/p/" + string(id)
	switch at {
	case "":
	case "latest":
		u += "/latest"
	default:
		u += "/v/" + at
	}
	return u
}

func (s *Server) rules(ctx context.Context, p *model.Principal, ws model.WorkspaceID) string {
	v, err := s.uc.GetWorkspace(ctx, p, ws)
	if err != nil {
		return ""
	}
	return v.Description
}

func toMCPBlocks(blocks []*model.Block) []mcpBlock {
	out := make([]mcpBlock, 0, len(blocks))
	for _, b := range blocks {
		by := ""
		if b.UpdatedBy.Kind != "" {
			by = actorLabel(b.UpdatedBy)
		}
		out = append(out, mcpBlock{ID: b.ID, ParentID: b.ParentID, Type: b.Type, Content: b.Content, Props: b.Props, Rev: b.Rev, By: by})
	}
	return out
}

func (s *Server) registerMCPTools(server *mcp.Server) {
	addTool(server, "papers_guide", "Explain papers, block types, ops and the workspace's rules. Call once before writing.",
		func(ctx context.Context, p *model.Principal, in mcpGuideIn) (mcpGuideOut, error) {
			out := mcpGuideOut{Guide: mcpInstructions, BlockTypes: blockTypeInfos(), Palette: markup.Palette}
			if in.WorkspaceID == "" {
				return out, nil
			}
			ws, err := s.uc.GetWorkspace(ctx, p, in.WorkspaceID)
			if err != nil {
				return out, err
			}
			out.WorkspaceRules, out.Language = ws.Description, ws.Language
			out.Sandbox = sandboxOpen
			if !ws.Sandbox.ExternalResources {
				out.Sandbox = sandboxClosed
			}
			md, err := s.uc.Export(ctx, p, in.WorkspaceID, ws.HomePaperID)
			if err != nil {
				return out, err
			}
			out.Home = md
			return out, nil
		})

	addTool(server, "papers_list_workspaces", "List the workspaces you can use, with their rules.",
		func(ctx context.Context, p *model.Principal, _ mcpEmpty) (mcpWorkspacesOut, error) {
			list, _, err := s.uc.ListWorkspaces(ctx, p)
			if err != nil {
				return mcpWorkspacesOut{}, err
			}
			out := mcpWorkspacesOut{Workspaces: []mcpWorkspace{}}
			for _, v := range list {
				out.Workspaces = append(out.Workspaces, mcpWorkspace{ID: v.ID, Name: v.Name, Rules: v.Description, Language: v.Language, HomePaperID: v.HomePaperID})
			}
			return out, nil
		})

	addTool(server, "papers_search", "Find papers and blocks containing the words (current state; archived papers excluded).",
		func(ctx context.Context, p *model.Principal, in mcpSearchIn) (mcpSearchOut, error) {
			var under model.PaperID
			if in.Under != "" {
				q, err := s.uc.ResolvePaper(ctx, p, in.WorkspaceID, in.Under)
				if err != nil {
					return mcpSearchOut{}, err
				}
				under = q.ID
			}
			hits, err := s.uc.Search(ctx, p, in.WorkspaceID, usecase.SearchInput{Query: in.Query, Under: under, Limit: in.Limit})
			if err != nil {
				return mcpSearchOut{}, err
			}
			out := mcpSearchOut{Hits: []mcpSearchHit{}}
			for _, h := range hits {
				out.Hits = append(out.Hits, mcpSearchHit{Paper: toMCPRef(h.Paper), BlockID: h.BlockID, Text: h.Text})
			}
			return out, nil
		})

	addTool(server, "papers_read", "Read a paper: blocks with ids and revs (send rev back as base_rev), its place in the tree, and its child papers; for an HTML paper also its html. Reads the current state unless at is given; long papers come in ranges (pass next as after).",
		func(ctx context.Context, p *model.Principal, in mcpReadIn) (mcpReadOut, error) {
			return s.mcpRead(ctx, p, in)
		})

	addTool(server, "papers_create_paper", "Create a paper under a parent (unfiled without one, or a private one), optionally with initial Markdown, or an HTML paper (kind html) with its HTML.",
		func(ctx context.Context, p *model.Principal, in mcpCreateIn) (mcpPaperRef, error) {
			vis := model.VisibilityPublic
			if in.Private {
				vis = model.VisibilityPrivate
			}
			q, err := s.uc.CreatePaper(ctx, p, in.WorkspaceID, usecase.CreateInput{Title: in.Title, Visibility: vis, Parent: in.Parent, Markdown: in.Markdown, Kind: in.Kind, HTML: in.HTML})
			if err != nil {
				return mcpPaperRef{}, err
			}
			return mcpPaperRef{ID: q.ID, Title: q.Title, Status: q.Status(), Private: q.IsPrivate()}, nil
		})

	addTool(server, "papers_write", "Apply ops to a paper atomically (see papers_guide; an HTML paper takes set_html and set_title). Fails with a conflict when a block changed since your base_rev. Does not save a version.",
		func(ctx context.Context, p *model.Principal, in mcpWriteIn) (mcpWriteOut, error) {
			q, err := s.uc.ResolvePaper(ctx, p, in.WorkspaceID, in.Paper)
			if err != nil {
				return mcpWriteOut{}, err
			}
			res, err := s.uc.Apply(ctx, p, in.WorkspaceID, q.ID, usecase.BatchInput{Ops: in.Ops, Force: in.Force})
			if err != nil {
				return mcpWriteOut{}, err
			}
			out := mcpWriteOut{
				PaperID: q.ID, Refs: res.Refs, Revs: map[model.BlockID]int64{}, Deleted: res.Deleted,
				Editing: res.Editing, NotRestored: res.NotRestored,
				Unsaved: res.Version == nil, WorkspaceRules: s.rules(ctx, p, in.WorkspaceID),
			}
			for _, b := range res.Blocks {
				out.Revs[b.ID] = b.Rev
			}
			if res.Version != nil {
				out.Version = &res.Version.Number
			}
			return out, nil
		})

	addTool(server, "papers_save_version", "Save the paper's current state as its next version (v1 makes a draft canonical). Nothing happens when nothing changed since the latest version.",
		func(ctx context.Context, p *model.Principal, in mcpSaveIn) (mcpSaveOut, error) {
			q, err := s.uc.ResolvePaper(ctx, p, in.WorkspaceID, in.Paper)
			if err != nil {
				return mcpSaveOut{}, err
			}
			v, err := s.uc.SaveVersion(ctx, p, in.WorkspaceID, q.ID, in.Note)
			if err != nil {
				return mcpSaveOut{}, err
			}
			if v == nil {
				return mcpSaveOut{Saved: false, Version: q.VersionNumber}, nil
			}
			n := v.Number
			return mcpSaveOut{Saved: true, Version: n, URL: paperURL(in.WorkspaceID, q.ID, itoa(n))}, nil
		})

	addTool(server, "papers_versions", "List a paper's saved versions, newest first (who saved, when, note).",
		func(ctx context.Context, p *model.Principal, in mcpVersionsIn) (mcpVersionsOut, error) {
			q, err := s.uc.ResolvePaper(ctx, p, in.WorkspaceID, in.Paper)
			if err != nil {
				return mcpVersionsOut{}, err
			}
			vs, err := s.uc.ListVersions(ctx, p, in.WorkspaceID, q.ID, in.Before, in.Limit)
			if err != nil {
				return mcpVersionsOut{}, err
			}
			out := mcpVersionsOut{Versions: []mcpVersion{}}
			for _, v := range vs {
				out.Versions = append(out.Versions, mcpVersion{Number: v.Number, Note: v.Note, By: actorLabel(v.CreatedBy), At: v.CreatedAt.Format(timeLayout)})
			}
			return out, nil
		})

	addTool(server, "papers_diff", "Compare two states of a paper (versions or the current state).",
		func(ctx context.Context, p *model.Principal, in mcpDiffIn) (*usecase.Diff, error) {
			q, err := s.uc.ResolvePaper(ctx, p, in.WorkspaceID, in.Paper)
			if err != nil {
				return nil, err
			}
			fromRaw := in.From
			if fromRaw == "" {
				fromRaw = "latest"
			}
			from, err := usecase.ParseAt(fromRaw)
			if err != nil {
				return nil, err
			}
			to, err := usecase.ParseAt(in.To)
			if err != nil {
				return nil, err
			}
			return s.uc.Diff(ctx, p, in.WorkspaceID, q.ID, from, to)
		})
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func (s *Server) mcpRead(ctx context.Context, p *model.Principal, in mcpReadIn) (mcpReadOut, error) {
	q, err := s.uc.ResolvePaper(ctx, p, in.WorkspaceID, in.Paper)
	if err != nil {
		return mcpReadOut{}, err
	}
	view, err := s.uc.GetPaper(ctx, p, in.WorkspaceID, q.ID)
	if err != nil {
		return mcpReadOut{}, err
	}
	at, err := usecase.ParseAt(in.At)
	if err != nil {
		return mcpReadOut{}, err
	}
	out := mcpReadOut{
		ID: q.ID, Kind: view.Paper.Kind.Or(), Title: view.Paper.Title, Status: view.Paper.Status(), Version: view.Paper.VersionNumber,
		Path: []mcpPaperRef{}, WorkspaceRules: s.rules(ctx, p, in.WorkspaceID),
	}
	for _, a := range view.Path {
		out.Path = append(out.Path, toMCPRef(a))
	}
	out.DatabaseID = view.Paper.DatabaseID
	if view.Paper.IsDatabase() {
		sort, err := usecase.ParseItemSort(in.Sort)
		if err != nil {
			return mcpReadOut{}, err
		}
		page, err := s.uc.ListItems(ctx, p, in.WorkspaceID, q.ID, usecase.ItemsInput{After: in.After, Limit: min(in.Limit, usecase.MaxItemsLimit), Filter: in.Filter, Sort: sort})
		if err != nil {
			return mcpReadOut{}, err
		}
		out.Properties, out.Items, out.Next = view.Paper.Properties, []mcpItem{}, page.NextCursor
		for _, r := range page.Items {
			out.Items = append(out.Items, mcpItem{ID: r.Item.ID, Title: r.Item.Title, Values: toMCPValues(view.Paper.Properties, r.Values)})
		}
		out.URL, out.Blocks = paperURL(in.WorkspaceID, q.ID, ""), []mcpBlock{}
		return out, nil
	}
	if view.Database != nil {
		out.Properties, out.Values = view.Database.Properties, toMCPValues(view.Database.Properties, view.Values)
	}
	var blocks []*model.Block
	refs := map[model.PaperID]*usecase.PaperRef{}
	if at == 0 {
		limit := in.Limit
		if limit <= 0 {
			limit = 200
		}
		page, err := s.uc.ListBlocks(ctx, p, in.WorkspaceID, q.ID, in.After, limit)
		if err != nil {
			return mcpReadOut{}, err
		}
		blocks, refs, out.Next = page.Blocks, page.Papers, page.Next
		out.URL = paperURL(in.WorkspaceID, q.ID, "")
	} else {
		v, err := s.uc.GetVersion(ctx, p, in.WorkspaceID, q.ID, at)
		if err != nil {
			return mcpReadOut{}, err
		}
		blocks, out.Title, out.At = v.Blocks, v.Paper.Title, itoa(v.Version.Number)
		out.URL = paperURL(in.WorkspaceID, q.ID, out.At)
	}
	if in.TOC {
		var keep []*model.Block
		for _, b := range blocks {
			if b.Type == model.BlockHeading || b.Type == model.BlockPaper {
				keep = append(keep, b)
			}
		}
		blocks = keep
	}
	if view.Paper.IsHTML() {
		doc, err := s.uc.ReadHTML(ctx, p, in.WorkspaceID, q.ID, in.At)
		if err != nil {
			return mcpReadOut{}, err
		}
		out.HTML = doc.HTML
	}
	out.Blocks = toMCPBlocks(blocks)
	for _, b := range blocks {
		if r, ok := refs[b.Props.PaperID]; ok && b.Type == model.BlockPaper {
			out.Children = append(out.Children, toMCPRef(r))
		}
	}
	return out, nil
}

// mcpConflictError explains a conflict to an agent with the current state
// of every conflicting block, so it can redo its edit without another read.
func mcpConflictError(c *usecase.ConflictError) error {
	type current struct {
		BlockID model.BlockID     `json:"block_id"`
		BaseRev int64             `json:"your_base_rev"`
		Deleted bool              `json:"deleted,omitempty"`
		Rev     int64             `json:"rev,omitempty"`
		Type    model.BlockType   `json:"type,omitempty"`
		Content string            `json:"content,omitempty"`
		Props   *model.BlockProps `json:"props,omitempty"`
		By      string            `json:"last_updated_by,omitempty"`
	}
	list := make([]current, 0, len(c.Conflicts))
	for _, cf := range c.Conflicts {
		cur := current{BlockID: cf.BlockID, BaseRev: cf.BaseRev, Deleted: cf.Current == nil}
		if b := cf.Current; b != nil {
			props := b.Props
			cur.Rev, cur.Type, cur.Content, cur.Props, cur.By = b.Rev, b.Type, b.Content, &props, actorLabel(b.UpdatedBy)
		}
		list = append(list, cur)
	}
	body, err := json.Marshal(list)
	if err != nil {
		return goerr.Wrap(err, "failed to encode conflicts")
	}
	return goerr.Wrap(model.ErrConflict, "conflict: nothing was written. These blocks changed since your base_rev; "+
		"their current state follows. Redo your edit on top of it with the new rev as base_rev, "+
		"or resend with force:true only if the user wants to overwrite. Deleted blocks must be inserted again. "+
		"For an HTML paper (html_page block), read its current html with papers_read. "+string(body))
}
