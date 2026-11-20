package http

import (
	"context"
	"net/http"

	"github.com/m-mizutani/goerr/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
	"github.com/m-mizutani/papers/pkg/utils/logging"
)

const (
	mcpServerName    = "papers"
	mcpServerVersion = "0.1.0"
	mcpDefaultAgent  = "MCP client"
)

// mcpInstructions is sent to clients on initialize and returned by
// papers_guide. It is the contract an AI needs to write good papers.
const mcpInstructions = `papers is a shared knowledge canvas edited by humans and AI together.

Model
- A workspace contains papers. A paper has a title and an ordered list of blocks.
- Every change is a commit (an atomic list of ops). Read a paper before editing it and reference blocks by id.
- Humans soft-lock the block they are editing. Writing to a locked block fails with "block is being edited"; wait, edit something else, or ask the user. Use force only when the user explicitly asks you to override.
- Concurrent writes are last-writer-wins per block. Pass base_version (the version you read) on update so overwrites are recorded.
- Every block keeps its own version history (papers_block_history); a paper's history is its list of commits (papers_history).

Block types (type: props)
- paragraph, quote: inline Markdown in content (**bold**, *italic*, ` + "`code`" + `, [link](url)).
- heading: props.level 1-3.
- bulleted_list_item, numbered_list_item: props.indent 0-6. todo: props.indent, props.checked.
- callout: props.icon (an emoji). code: props.language. divider: no content. image: props.url, content = caption.
- table: props.rows, props.cols, props.header_row. Cells are child blocks (table_cell, props.row/col) created automatically; pass "cells" (rows of strings) on insert, update a cell by its block id, resize via the table's props.
- Rich blocks only AI writes (humans can view, not edit):
  - mermaid: Mermaid source (flowchart, sequence, gantt, ...).
  - svg: a complete <svg> element with a viewBox. Rendered in an isolated frame.
  - html: a self-contained HTML document or fragment, rendered in a sandboxed iframe (scripts allowed, no same-origin access, no cookies). Inline all CSS/JS; external network requests may be blocked. props.height sets the frame height in px.

Ops (papers_commit)
- {"op":"insert","type":"paragraph","content":"...","after":"<block id>"}  after omitted = append; after "" = top.
- {"op":"update","block_id":"...","content":"...","props":{...},"type":"...","base_version":3}  props replaces all props.
- {"op":"delete","block_id":"..."}  {"op":"move","block_id":"...","after":"<block id>"}
- {"op":"set_title","title":"..."}
- {"op":"insert_markdown","markdown":"## Section\n\ntext\n\n- a\n- b","after":"<block id>"}  the fastest way to write a lot; fenced mermaid/svg/html become rich blocks, GFM tables become table blocks.
Keep one commit per logical change and write a short commit message.`

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
			cp.HolderKey = "mcp:" + string(p.User.ID)
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
				return nil, zero, err
			}
			return nil, out, nil
		})
}

type mcpEmpty struct{}

type mcpGuideOut struct {
	Guide      string          `json:"guide"`
	BlockTypes []blockTypeInfo `json:"block_types"`
}

type mcpWorkspace struct {
	ID          model.WorkspaceID `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Role        model.Role        `json:"role"`
}

type mcpWorkspacesOut struct {
	Workspaces []mcpWorkspace `json:"workspaces"`
}

type mcpWorkspaceIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id" jsonschema:"workspace id from papers_list_workspaces"`
}

type mcpPaperSummary struct {
	ID        model.PaperID `json:"id"`
	Title     string        `json:"title"`
	Role      model.Role    `json:"role"`
	UpdatedAt string        `json:"updated_at"`
	UpdatedBy string        `json:"updated_by"`
}

type mcpPapersOut struct {
	Papers []mcpPaperSummary `json:"papers"`
}

type mcpSearchIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id" jsonschema:"workspace id"`
	Query       string            `json:"query" jsonschema:"case-insensitive text to find in titles and blocks"`
}

type mcpSearchHit struct {
	PaperID model.PaperID `json:"paper_id"`
	Title   string        `json:"title"`
	BlockID model.BlockID `json:"block_id,omitempty"`
	Snippet string        `json:"snippet"`
}

type mcpSearchOut struct {
	Hits []mcpSearchHit `json:"hits"`
}

type mcpPaperIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id" jsonschema:"workspace id"`
	PaperID     model.PaperID     `json:"paper_id" jsonschema:"paper id"`
}

type mcpBlock struct {
	ID       model.BlockID    `json:"id"`
	ParentID model.BlockID    `json:"parent_id,omitempty"`
	Type     model.BlockType  `json:"type"`
	Content  string           `json:"content,omitempty"`
	Props    model.BlockProps `json:"props"`
	Version  int64            `json:"version"`
	// EditedBy names who holds the soft lock, if anyone is editing it now.
	EditedBy string `json:"edited_by,omitempty"`
	By       string `json:"last_updated_by"`
}

type mcpPaperOut struct {
	ID       model.PaperID `json:"id"`
	Title    string        `json:"title"`
	Role     model.Role    `json:"role"`
	Blocks   []mcpBlock    `json:"blocks"`
	Markdown string        `json:"markdown"`
}

type mcpCreateIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id" jsonschema:"workspace id"`
	Title       string            `json:"title" jsonschema:"paper title (single line)"`
	Markdown    string            `json:"markdown,omitempty" jsonschema:"optional initial content as Markdown"`
	Restricted  bool              `json:"restricted,omitempty" jsonschema:"true to hide the paper from workspace members except owners"`
}

type mcpCommitIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id" jsonschema:"workspace id"`
	PaperID     model.PaperID     `json:"paper_id" jsonschema:"paper id"`
	Message     string            `json:"message" jsonschema:"short description of the change"`
	Ops         []usecase.Op      `json:"ops" jsonschema:"operations applied atomically, see papers_guide"`
	Force       bool              `json:"force,omitempty" jsonschema:"overwrite blocks humans are editing; only when the user asks"`
}

type mcpCommitOut struct {
	CommitID model.CommitID `json:"commit_id,omitempty"`
	// Versions maps each changed block to its new version; pass it as
	// base_version on the next update of that block.
	Versions map[model.BlockID]int64 `json:"versions,omitempty"`
	// Inserted lists the ids of blocks created by the commit, in order.
	Inserted []model.BlockID `json:"inserted,omitempty"`
	// Overwritten lists blocks whose newer version this commit replaced.
	Overwritten []model.BlockID `json:"overwritten,omitempty"`
}

type mcpHistoryIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id" jsonschema:"workspace id"`
	PaperID     model.PaperID     `json:"paper_id" jsonschema:"paper id"`
	Before      model.CommitID    `json:"before,omitempty" jsonschema:"commit id from the previous page; omit for the newest commits"`
	Limit       int               `json:"limit,omitempty" jsonschema:"max commits (default 50)"`
}

type mcpCommitSummary struct {
	Seq     int64           `json:"seq"`
	ID      model.CommitID  `json:"id"`
	Actor   string          `json:"actor"`
	Kind    model.ActorKind `json:"actor_kind"`
	Message string          `json:"message,omitempty"`
	Changes int             `json:"changes"`
	At      string          `json:"at"`
}

type mcpBlockHistoryIn struct {
	WorkspaceID model.WorkspaceID `json:"workspace_id" jsonschema:"workspace id"`
	PaperID     model.PaperID     `json:"paper_id" jsonschema:"paper id"`
	BlockID     model.BlockID     `json:"block_id" jsonschema:"block id"`
	Limit       int               `json:"limit,omitempty" jsonschema:"max versions (default 200)"`
}

type mcpBlockVersion struct {
	Version int64            `json:"version"`
	Type    model.BlockType  `json:"type"`
	Content string           `json:"content,omitempty"`
	Props   model.BlockProps `json:"props"`
	By      string           `json:"by"`
	At      string           `json:"at"`
}

type mcpBlockHistoryOut struct {
	Versions []mcpBlockVersion `json:"versions"`
}

type mcpHistoryOut struct {
	Commits []mcpCommitSummary `json:"commits"`
}

func actorLabel(a model.Actor) string {
	if a.Kind == model.ActorAgent {
		if a.AgentName != "" {
			return a.AgentName + " (for " + a.Name + ")"
		}
		return "agent (for " + a.Name + ")"
	}
	return a.Name
}

const timeLayout = "2006-01-02T15:04:05Z07:00"

func (s *Server) registerMCPTools(server *mcp.Server) {
	addTool(server, "papers_guide", "Explain block types, ops and editing etiquette. Call once before writing.",
		func(_ context.Context, _ *model.Principal, _ mcpEmpty) (mcpGuideOut, error) {
			return mcpGuideOut{Guide: mcpInstructions, BlockTypes: blockTypeInfos()}, nil
		})

	addTool(server, "papers_list_workspaces", "List workspaces you can access.",
		func(ctx context.Context, p *model.Principal, _ mcpEmpty) (mcpWorkspacesOut, error) {
			views, err := s.uc.ListWorkspaces(ctx, p)
			if err != nil {
				return mcpWorkspacesOut{}, err
			}
			out := mcpWorkspacesOut{Workspaces: []mcpWorkspace{}}
			for _, v := range views {
				out.Workspaces = append(out.Workspaces, mcpWorkspace{ID: v.Workspace.ID, Name: v.Workspace.Name, Description: v.Workspace.Description, Role: v.Role})
			}
			return out, nil
		})

	addTool(server, "papers_list_papers", "List papers in a workspace, most recently updated first.",
		func(ctx context.Context, p *model.Principal, in mcpWorkspaceIn) (mcpPapersOut, error) {
			list, err := s.uc.ListPapers(ctx, p, in.WorkspaceID)
			if err != nil {
				return mcpPapersOut{}, err
			}
			out := mcpPapersOut{Papers: []mcpPaperSummary{}}
			for _, ps := range list {
				out.Papers = append(out.Papers, mcpPaperSummary{
					ID: ps.Paper.ID, Title: ps.Paper.Title, Role: ps.Role,
					UpdatedAt: ps.Paper.UpdatedAt.Format(timeLayout), UpdatedBy: actorLabel(ps.Paper.UpdatedBy),
				})
			}
			return out, nil
		})

	addTool(server, "papers_search", "Find papers whose title or blocks contain the query.",
		func(ctx context.Context, p *model.Principal, in mcpSearchIn) (mcpSearchOut, error) {
			hits, err := s.uc.SearchPapers(ctx, p, in.WorkspaceID, in.Query, 50)
			if err != nil {
				return mcpSearchOut{}, err
			}
			out := mcpSearchOut{Hits: []mcpSearchHit{}}
			for _, h := range hits {
				out.Hits = append(out.Hits, mcpSearchHit{PaperID: h.Paper.ID, Title: h.Paper.Title, BlockID: h.BlockID, Snippet: h.Snippet})
			}
			return out, nil
		})

	addTool(server, "papers_read_paper", "Read a paper: blocks with ids, versions and who is editing them now, plus a Markdown rendering.",
		func(ctx context.Context, p *model.Principal, in mcpPaperIn) (mcpPaperOut, error) {
			v, err := s.uc.GetPaper(ctx, p, in.WorkspaceID, in.PaperID)
			if err != nil {
				return mcpPaperOut{}, err
			}
			md, err := s.uc.ExportMarkdown(ctx, p, in.WorkspaceID, in.PaperID)
			if err != nil {
				return mcpPaperOut{}, err
			}
			editing := map[model.BlockID]string{}
			for _, l := range v.Locks {
				editing[l.BlockID] = actorLabel(l.Holder)
			}
			out := mcpPaperOut{ID: v.Paper.ID, Title: v.Paper.Title, Role: v.Role, Blocks: []mcpBlock{}, Markdown: md}
			for _, b := range v.Blocks {
				out.Blocks = append(out.Blocks, mcpBlock{
					ID: b.ID, ParentID: b.ParentID, Type: b.Type, Content: b.Content, Props: b.Props,
					Version: b.Version, EditedBy: editing[b.ID], By: actorLabel(b.UpdatedBy),
				})
			}
			return out, nil
		})

	addTool(server, "papers_create_paper", "Create a paper, optionally with initial Markdown content.",
		func(ctx context.Context, p *model.Principal, in mcpCreateIn) (mcpPaperSummary, error) {
			v, err := s.uc.CreatePaper(ctx, p, in.WorkspaceID, usecase.CreatePaperInput{Title: in.Title, Markdown: in.Markdown, Restricted: in.Restricted})
			if err != nil {
				return mcpPaperSummary{}, err
			}
			return mcpPaperSummary{ID: v.Paper.ID, Title: v.Paper.Title, Role: v.Role,
				UpdatedAt: v.Paper.UpdatedAt.Format(timeLayout), UpdatedBy: actorLabel(v.Paper.UpdatedBy)}, nil
		})

	addTool(server, "papers_commit", "Apply ops (insert, update, delete, move, set_title, insert_markdown) to a paper atomically.",
		func(ctx context.Context, p *model.Principal, in mcpCommitIn) (mcpCommitOut, error) {
			res, err := s.uc.Commit(ctx, p, in.WorkspaceID, in.PaperID, usecase.CommitInput{Ops: in.Ops, Message: in.Message, Force: in.Force})
			if err != nil {
				return mcpCommitOut{}, err
			}
			out := mcpCommitOut{}
			if res.Commit == nil {
				return out, nil
			}
			out.CommitID = res.Commit.ID
			out.Versions = map[model.BlockID]int64{}
			topLevel := map[model.BlockID]bool{}
			for _, b := range res.Blocks {
				out.Versions[b.ID] = b.Version
				topLevel[b.ID] = b.ParentID == ""
			}
			for _, ch := range res.Commit.Changes {
				if ch.Kind == model.ChangeInsert && topLevel[ch.BlockID] {
					out.Inserted = append(out.Inserted, ch.BlockID)
				}
				if ch.OverwroteVersion > 0 {
					out.Overwritten = append(out.Overwritten, ch.BlockID)
				}
			}
			return out, nil
		})

	addTool(server, "papers_block_history", "List the versions of one block, newest first (also works for deleted blocks).",
		func(ctx context.Context, p *model.Principal, in mcpBlockHistoryIn) (mcpBlockHistoryOut, error) {
			vs, err := s.uc.ListBlockVersions(ctx, p, in.WorkspaceID, in.PaperID, in.BlockID, in.Limit)
			if err != nil {
				return mcpBlockHistoryOut{}, err
			}
			out := mcpBlockHistoryOut{Versions: []mcpBlockVersion{}}
			for _, b := range vs {
				out.Versions = append(out.Versions, mcpBlockVersion{Version: b.Version, Type: b.Type, Content: b.Content, Props: b.Props, By: actorLabel(b.UpdatedBy), At: b.UpdatedAt.Format(timeLayout)})
			}
			return out, nil
		})

	addTool(server, "papers_history", "List a paper's commits newest first (who changed what, when).",
		func(ctx context.Context, p *model.Principal, in mcpHistoryIn) (mcpHistoryOut, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = 50
			}
			commits, err := s.uc.ListCommits(ctx, p, in.WorkspaceID, in.PaperID, in.Before, limit)
			if err != nil {
				return mcpHistoryOut{}, err
			}
			out := mcpHistoryOut{Commits: []mcpCommitSummary{}}
			for _, c := range commits {
				out.Commits = append(out.Commits, mcpCommitSummary{ID: c.ID, Actor: actorLabel(c.Actor), Kind: c.Actor.Kind, Message: c.Message, Changes: len(c.Changes), At: c.CreatedAt.Format(timeLayout)})
			}
			return out, nil
		})
}
