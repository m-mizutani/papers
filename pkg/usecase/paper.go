package usecase

import (
	"cmp"
	"context"
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
	u.publish(ctx, &model.Event{Type: model.EventDeleted, WorkspaceID: wsID, PaperID: id})
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
	u.publish(ctx, &model.Event{Type: model.EventAccess, WorkspaceID: wsID, PaperID: id})
	return paper, nil
}

// MaxCommitPage bounds one history page.
const MaxCommitPage = 200

func (u *UseCases) ListCommits(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, id model.PaperID, afterSeq int64, limit int) ([]*model.Commit, error) {
	if _, _, _, err := u.paperFor(ctx, p, wsID, id, model.RoleViewer); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > MaxCommitPage {
		limit = MaxCommitPage
	}
	commits, err := u.repo.ListCommits(ctx, wsID, id, afterSeq, limit)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list commits")
	}
	return commits, nil
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

// SearchPapers does a case-insensitive substring search over titles and
// block contents of the papers the caller can read. It scans; a deployment
// with many papers needs a search index behind this method.
func (u *UseCases) SearchPapers(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, query string, limit int) ([]*SearchHit, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "query is required")
	}
	summaries, err := u.ListPapers(ctx, p, wsID)
	if err != nil {
		return nil, err
	}
	var hits []*SearchHit
	for _, s := range summaries {
		if limit > 0 && len(hits) >= limit {
			break
		}
		if idx := strings.Index(strings.ToLower(s.Paper.Title), q); idx >= 0 {
			hits = append(hits, &SearchHit{Paper: s.Paper, Snippet: s.Paper.Title})
			continue
		}
		blocks, err := u.repo.ListBlocks(ctx, wsID, s.Paper.ID)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list blocks", goerr.V("paper_id", s.Paper.ID))
		}
		for _, b := range blocks {
			lower := strings.ToLower(b.Content)
			if idx := strings.Index(lower, q); idx >= 0 && len(lower) == len(b.Content) {
				hits = append(hits, &SearchHit{Paper: s.Paper, BlockID: b.ID, Snippet: snippet(b.Content, idx, len(q))})
				break
			} else if idx >= 0 {
				// Lower-casing changed byte lengths; fall back to a
				// head-of-block snippet rather than slicing at a wrong offset.
				hits = append(hits, &SearchHit{Paper: s.Paper, BlockID: b.ID, Snippet: snippet(b.Content, 0, 0)})
				break
			}
		}
	}
	slices.SortStableFunc(hits, func(a, b *SearchHit) int { return cmp.Compare(b.Paper.UpdatedAt.Unix(), a.Paper.UpdatedAt.Unix()) })
	return hits, nil
}
