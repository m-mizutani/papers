package usecase

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase/markdown"
)

// PaperSummary is a paper in a listing, with the caller's role.
type PaperSummary struct {
	Paper *model.Paper
	Role  model.Role
}

func (u *UseCases) ListPapers(ctx context.Context, p *model.Principal, wsID model.WorkspaceID) ([]*PaperSummary, error) {
	ws, err := u.repo.GetWorkspace(ctx, wsID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get workspace", goerr.V("workspace_id", wsID))
	}
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	papers, err := u.repo.ListPapers(ctx, wsID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list papers")
	}
	var out []*PaperSummary
	for _, paper := range papers {
		if role := p.PaperRole(ws, paper); role.AtLeast(model.RoleViewer) {
			out = append(out, &PaperSummary{Paper: paper, Role: role})
		}
	}
	// A user with no workspace role and no visible paper learns nothing.
	if len(out) == 0 && !p.WorkspaceRole(ws).AtLeast(model.RoleViewer) {
		return nil, denied(model.RoleNone, "workspace", wsID)
	}
	slices.SortFunc(out, func(a, b *PaperSummary) int {
		return b.Paper.UpdatedAt.Compare(a.Paper.UpdatedAt)
	})
	return out, nil
}

type CreatePaperInput struct {
	Title      string
	Markdown   string
	Restricted bool
	HolderKey  string
}

// PaperView is everything needed to render a paper.
type PaperView struct {
	Paper  *model.Paper
	Blocks []*model.Block
	Locks  []*model.BlockLock
	Role   model.Role
}

func (u *UseCases) CreatePaper(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, in CreatePaperInput) (*PaperView, error) {
	ws, _, err := u.workspaceFor(ctx, p, wsID, model.RoleEditor)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if err := model.ValidateTitle(title); err != nil {
		return nil, err
	}
	now := u.now()
	actor := p.Actor()
	paper := &model.Paper{
		ID: model.NewPaperID(), WorkspaceID: ws.ID, Title: title,
		Access:    model.PaperAccess{Restricted: in.Restricted},
		CreatedBy: actor, CreatedAt: now, UpdatedBy: actor, UpdatedAt: now,
	}
	if err := u.repo.CreatePaper(ctx, paper); err != nil {
		return nil, goerr.Wrap(err, "failed to create paper")
	}
	if strings.TrimSpace(in.Markdown) != "" {
		md := in.Markdown
		if _, err := u.Commit(ctx, p, ws.ID, paper.ID, CommitInput{
			Ops:       []Op{{Kind: OpInsertMarkdown, Markdown: &md}},
			Message:   "Initial content",
			HolderKey: in.HolderKey,
		}); err != nil {
			return nil, goerr.Wrap(err, "failed to write initial content")
		}
	}
	return u.GetPaper(ctx, p, ws.ID, paper.ID)
}

func (u *UseCases) GetPaper(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, id model.PaperID) (*PaperView, error) {
	_, paper, role, err := u.paperFor(ctx, p, wsID, id, model.RoleViewer)
	if err != nil {
		return nil, err
	}
	blocks, err := u.repo.ListBlocks(ctx, wsID, id)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list blocks")
	}
	locks, err := u.repo.ListLocks(ctx, wsID, id, u.now())
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list locks")
	}
	return &PaperView{Paper: paper, Blocks: blocks, Locks: locks, Role: role}, nil
}

func (u *UseCases) DeletePaper(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, id model.PaperID) error {
	if _, _, _, err := u.paperFor(ctx, p, wsID, id, model.RoleOwner); err != nil {
		return err
	}
	if err := u.repo.DeletePaper(ctx, wsID, id); err != nil {
		return goerr.Wrap(err, "failed to delete paper")
	}
	return nil
}

func (u *UseCases) UpdatePaperAccess(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, id model.PaperID, access model.PaperAccess) (*model.Paper, error) {
	if _, _, _, err := u.paperFor(ctx, p, wsID, id, model.RoleOwner); err != nil {
		return nil, err
	}
	if err := u.validateBindingSubjects(ctx, access.Bindings); err != nil {
		return nil, err
	}
	paper, err := u.repo.UpdatePaperAccess(ctx, wsID, id, access, p.Actor(), u.now())
	if err != nil {
		return nil, goerr.Wrap(err, "failed to update paper access")
	}
	return paper, nil
}

// MaxHistoryPage bounds one page of commits or block versions.
const MaxHistoryPage = 200

func pageSize(limit int) int {
	if limit <= 0 || limit > MaxHistoryPage {
		return MaxHistoryPage
	}
	return limit
}

// ListCommits returns the paper's commits newest first; before is the
// pagination cursor (the last commit ID of the previous page).
func (u *UseCases) ListCommits(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, id model.PaperID, before model.CommitID, limit int) ([]*model.Commit, error) {
	if _, _, _, err := u.paperFor(ctx, p, wsID, id, model.RoleViewer); err != nil {
		return nil, err
	}
	commits, err := u.repo.ListCommits(ctx, wsID, id, before, pageSize(limit))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list commits")
	}
	return commits, nil
}

// ListBlockVersions returns a block's history newest first. It works for
// deleted blocks too, so a deletion can be inspected or undone.
func (u *UseCases) ListBlockVersions(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, id model.PaperID, block model.BlockID, limit int) ([]*model.Block, error) {
	if _, _, _, err := u.paperFor(ctx, p, wsID, id, model.RoleViewer); err != nil {
		return nil, err
	}
	vs, err := u.repo.ListBlockVersions(ctx, wsID, id, block, pageSize(limit))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list block versions")
	}
	return vs, nil
}

func (u *UseCases) ExportMarkdown(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, id model.PaperID) (string, error) {
	view, err := u.GetPaper(ctx, p, wsID, id)
	if err != nil {
		return "", err
	}
	return markdown.Render(view.Paper.Title, view.Blocks), nil
}

// SearchHit is one matching paper.
type SearchHit struct {
	Paper   *model.Paper
	BlockID model.BlockID
	Snippet string
}

const snippetRadius = 60

func snippet(text string, at, length int) string {
	start := max(at-snippetRadius, 0)
	end := min(at+length+snippetRadius, len(text))
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	s := strings.ReplaceAll(text[start:end], "\n", " ")
	if start > 0 {
		s = "…" + s
	}
	if end < len(text) {
		s += "…"
	}
	return s
}

// SearchPapers finds papers of a workspace by title and block text through
// the repository's search index, and returns at most limit hits, one per
// paper, best match first. Only papers the caller can read are returned.
// Hidden (restricted) papers are filtered after the index lookup, so a
// workspace with many restricted matches may return fewer than limit hits.
func (u *UseCases) SearchPapers(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, query string, limit int) ([]*SearchHit, error) {
	q, err := model.ParseSearchQuery(query)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > model.MaxSearchLimit {
		limit = model.MaxSearchLimit
	}
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	ws, err := u.repo.GetWorkspace(ctx, wsID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get workspace", goerr.V("workspace_id", wsID))
	}
	// Fetch more entries than hits: several entries can belong to one
	// paper, and some papers may not be readable by the caller.
	entries, err := u.repo.Search(ctx, wsID, q, min(limit*searchOverfetch, model.MaxSearchLimit))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to search", goerr.V("workspace_id", wsID))
	}
	hits := []*SearchHit{}
	seen := map[model.PaperID]bool{}
	for _, e := range entries {
		if len(hits) >= limit {
			break
		}
		if seen[e.PaperID] {
			continue
		}
		seen[e.PaperID] = true
		paper, err := u.repo.GetPaper(ctx, wsID, e.PaperID)
		if errors.Is(err, model.ErrNotFound) {
			continue // deleted after the index lookup
		}
		if err != nil {
			return nil, goerr.Wrap(err, "failed to get paper", goerr.V("paper_id", e.PaperID))
		}
		if !p.PaperRole(ws, paper).AtLeast(model.RoleViewer) {
			continue
		}
		hits = append(hits, &SearchHit{Paper: paper, BlockID: e.BlockID, Snippet: termSnippet(e.Text, q.Terms)})
	}
	// Without a workspace role, a caller with no visible hit learns nothing,
	// as in ListPapers.
	if len(hits) == 0 && !p.WorkspaceRole(ws).AtLeast(model.RoleViewer) {
		return nil, denied(model.RoleNone, "workspace", wsID)
	}
	return hits, nil
}

const searchOverfetch = 3

// termSnippet cuts the text around the first occurrence of any term.
func termSnippet(text string, terms []string) string {
	lower := strings.ToLower(text)
	for _, t := range terms {
		lt := strings.ToLower(t)
		idx := strings.Index(lower, lt)
		if idx < 0 {
			continue
		}
		if len(lower) == len(text) {
			return snippet(text, idx, len(lt))
		}
		break // lower-casing changed byte offsets; use the head
	}
	return snippet(text, 0, 0)
}
