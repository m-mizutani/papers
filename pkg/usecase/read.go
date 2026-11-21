package usecase

import (
	"context"
	"errors"
	"slices"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase/markdown"
)

// PaperRef is what lists show about a paper.
type PaperRef struct {
	ID            model.PaperID     `json:"id"`
	WorkspaceID   model.WorkspaceID `json:"workspace_id"`
	Title         string            `json:"title"`
	ParentID      model.PaperID     `json:"parent_id,omitempty"`
	Visibility    model.Visibility  `json:"visibility"`
	Status        model.PaperStatus `json:"status"`
	VersionNumber int               `json:"version_number"`
	CreatedBy     model.Actor       `json:"created_by"`
}

func refOf(p *model.Paper) *PaperRef {
	return &PaperRef{
		ID: p.ID, WorkspaceID: p.WorkspaceID, Title: p.Title, ParentID: p.ParentID, Visibility: p.Visibility,
		Status: p.Status(), VersionNumber: p.VersionNumber, CreatedBy: p.CreatedBy,
	}
}

// listed reports whether a paper shows in lists: visible, not trashed and
// not archived.
func listed(p *model.Principal, q *model.Paper) bool {
	return p.SeesPaper(q) && !q.IsTrashed() && !q.IsArchived()
}

// WorkspaceView is a workspace the principal may use.
type WorkspaceView struct {
	*model.Workspace
	HomePaperID model.PaperID `json:"home_paper_id"`
}

// ListWorkspaces lists the workspaces the principal may use, in display
// order, with the display groups.
func (u *UseCases) ListWorkspaces(ctx context.Context, p *model.Principal) ([]*WorkspaceView, []model.WorkspaceGroup, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, nil, err
	}
	var out []*WorkspaceView
	allowed := map[model.WorkspaceID]bool{}
	for _, w := range u.workspaces.List() {
		if _, err := u.workspaceFor(ctx, p, w.ID); err != nil {
			continue
		}
		home, err := u.homeID(ctx, w.ID)
		if err != nil {
			return nil, nil, err
		}
		allowed[w.ID] = true
		out = append(out, &WorkspaceView{Workspace: w, HomePaperID: home})
	}
	var groups []model.WorkspaceGroup
	for _, g := range u.workspaces.Groups() {
		g.Workspaces = slices.DeleteFunc(g.Workspaces, func(id model.WorkspaceID) bool { return !allowed[id] })
		if len(g.Workspaces) > 0 {
			groups = append(groups, g)
		}
	}
	return out, groups, nil
}

func (u *UseCases) GetWorkspace(ctx context.Context, p *model.Principal, ws model.WorkspaceID) (*WorkspaceView, error) {
	w, err := u.workspaceFor(ctx, p, ws)
	if err != nil {
		return nil, err
	}
	home, err := u.homeID(ctx, ws)
	if err != nil {
		return nil, err
	}
	return &WorkspaceView{Workspace: w, HomePaperID: home}, nil
}

// PaperView is a paper with where it sits.
type PaperView struct {
	Workspace *model.Workspace `json:"workspace"`
	Paper     *model.Paper     `json:"paper"`
	// Path lists the ancestors from the home down to the parent.
	Path   []*PaperRef `json:"path"`
	IsHome bool        `json:"is_home"`
}

// ancestors walks up from paper to the home, at most MaxTreeDepth steps.
func (u *UseCases) ancestors(ctx context.Context, paper *model.Paper) ([]*PaperRef, error) {
	out := []*PaperRef{}
	at := paper.ParentID
	for depth := 0; at != "" && depth < model.MaxTreeDepth; depth++ {
		q, err := u.repo.GetPaper(ctx, paper.WorkspaceID, at)
		if errors.Is(err, model.ErrNotFound) {
			break
		}
		if err != nil {
			return nil, goerr.Wrap(err, "failed to read ancestor", goerr.V("paper_id", at))
		}
		out = append(out, refOf(q))
		at = q.ParentID
	}
	slices.Reverse(out)
	return out, nil
}

// GetPaper reads a paper and records it as recently opened.
func (u *UseCases) GetPaper(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID) (*PaperView, error) {
	w, paper, err := u.paperFor(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	path, err := u.ancestors(ctx, paper)
	if err != nil {
		return nil, err
	}
	home, err := u.homeID(ctx, ws)
	if err != nil {
		return nil, err
	}
	if !paper.IsTrashed() {
		u.touchRecent(ctx, p, ws, id)
	}
	return &PaperView{Workspace: w, Paper: paper, Path: path, IsHome: paper.ID == home}, nil
}

// BlocksPage is one range of a paper's blocks.
type BlocksPage struct {
	Blocks []*model.Block `json:"blocks"`
	// Next is the cursor of the following range, "" at the end.
	Next string `json:"next"`
	// Papers describes the child papers of the paper blocks in the range
	// that the caller may see; hidden ones are absent.
	Papers map[model.PaperID]*PaperRef `json:"papers"`
}

// MaxBlocksRange bounds one range read; 0 or less reads everything.
const MaxBlocksRange = 500

// ListBlocks reads the current blocks of a paper, a range at a time (limit
// top-level blocks after the cursor, with their table cells), or all at once
// with limit 0 (printing, export). Child papers are read in one batch.
func (u *UseCases) ListBlocks(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, after string, limit int) (*BlocksPage, error) {
	if _, _, err := u.paperFor(ctx, p, ws, id); err != nil {
		return nil, err
	}
	var page BlocksPage
	if limit <= 0 {
		blocks, err := u.repo.ListBlocks(ctx, ws, id)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list blocks")
		}
		page.Blocks = blocks
	} else {
		blocks, next, err := u.repo.ListBlocksRange(ctx, ws, id, after, min(limit, MaxBlocksRange))
		if err != nil {
			return nil, goerr.Wrap(err, "failed to read blocks")
		}
		page.Blocks, page.Next = blocks, next
	}
	refs, err := u.childRefs(ctx, p, ws, page.Blocks)
	if err != nil {
		return nil, err
	}
	page.Papers = refs
	return &page, nil
}

// childRefs reads the child papers of the paper blocks in one batch.
func (u *UseCases) childRefs(ctx context.Context, p *model.Principal, ws model.WorkspaceID, blocks []*model.Block) (map[model.PaperID]*PaperRef, error) {
	var ids []model.PaperID
	for _, b := range blocks {
		if b.Type == model.BlockPaper {
			ids = append(ids, b.Props.PaperID)
		}
	}
	out := map[model.PaperID]*PaperRef{}
	if len(ids) == 0 {
		return out, nil
	}
	papers, err := u.repo.GetPapers(ctx, ws, ids)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read child papers")
	}
	for _, q := range papers {
		if p.SeesPaper(q) && !q.IsTrashed() {
			out[q.ID] = refOf(q)
		}
	}
	return out, nil
}

// ListChildren lists the visible child papers of a paper (archived ones only
// with includeArchived) in the order their paper blocks have in its body.
func (u *UseCases) ListChildren(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, includeArchived bool) ([]*PaperRef, error) {
	if _, _, err := u.paperFor(ctx, p, ws, id); err != nil {
		return nil, err
	}
	blocks, err := u.repo.ListPaperBlocks(ctx, ws, id)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list paper blocks")
	}
	ids := make([]model.PaperID, 0, len(blocks))
	for _, b := range blocks {
		ids = append(ids, b.Props.PaperID)
	}
	papers, err := u.repo.GetPapers(ctx, ws, ids)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read child papers")
	}
	byID := map[model.PaperID]*model.Paper{}
	for _, q := range papers {
		byID[q.ID] = q
	}
	out := []*PaperRef{}
	for _, cid := range ids {
		k, ok := byID[cid]
		if ok && k.ParentID == id && p.SeesPaper(k) && !k.IsTrashed() && (includeArchived || !k.IsArchived()) {
			out = append(out, refOf(k))
		}
	}
	return out, nil
}

// Sidebar is what the side navigation shows for a workspace.
type Sidebar struct {
	Home    *PaperRef   `json:"home"`
	Pins    []*PaperRef `json:"pins"`
	Recent  []*PaperRef `json:"recent"`
	Private []*PaperRef `json:"private"`
}

func (u *UseCases) Sidebar(ctx context.Context, p *model.Principal, ws model.WorkspaceID) (*Sidebar, error) {
	if _, err := u.workspaceFor(ctx, p, ws); err != nil {
		return nil, err
	}
	homeID, err := u.homeID(ctx, ws)
	if err != nil {
		return nil, err
	}
	pins, err := u.repo.ListPins(ctx, p.User.ID, ws)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list pins")
	}
	rec, err := u.repo.GetRecent(ctx, p.User.ID, ws)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get recent papers")
	}
	ids := []model.PaperID{homeID}
	for _, pin := range pins {
		ids = append(ids, pin.PaperID)
	}
	ids = append(ids, rec.PaperIDs...)
	papers, err := u.repo.GetPapers(ctx, ws, ids)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read papers")
	}
	byID := map[model.PaperID]*model.Paper{}
	for _, q := range papers {
		byID[q.ID] = q
	}
	sb := &Sidebar{Pins: []*PaperRef{}, Recent: []*PaperRef{}, Private: []*PaperRef{}}
	if h, ok := byID[homeID]; ok {
		sb.Home = refOf(h)
	}
	for _, pin := range pins {
		if q, ok := byID[pin.PaperID]; ok && p.SeesPaper(q) && !q.IsTrashed() {
			sb.Pins = append(sb.Pins, refOf(q))
		}
	}
	for _, id := range rec.PaperIDs {
		if q, ok := byID[id]; ok && listed(p, q) {
			sb.Recent = append(sb.Recent, refOf(q))
		}
	}
	priv, err := u.repo.ListPrivatePapers(ctx, ws, p.User.ID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list private papers")
	}
	for _, q := range priv {
		if !q.IsArchived() {
			sb.Private = append(sb.Private, refOf(q))
		}
	}
	return sb, nil
}

// --- search

// SearchInput is a search request. Under limits the hits to a paper and
// everything below it.
type SearchInput struct {
	Query           string
	Under           model.PaperID
	Limit           int
	IncludeArchived bool
}

type SearchHit struct {
	Paper   *PaperRef       `json:"paper"`
	BlockID model.BlockID   `json:"block_id,omitempty"`
	Type    model.BlockType `json:"block_type,omitempty"`
	Text    string          `json:"text"`
}

// Search finds papers and blocks of the current state. Hits on papers the
// caller may not see, and on archived or trashed papers, are dropped after
// the search, so the engine is asked for more than the limit.
func (u *UseCases) Search(ctx context.Context, p *model.Principal, ws model.WorkspaceID, in SearchInput) ([]*SearchHit, error) {
	if _, err := u.workspaceFor(ctx, p, ws); err != nil {
		return nil, err
	}
	q, err := model.ParseSearchQuery(in.Query)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 || limit > model.MaxSearchLimit {
		limit = 20
	}
	if in.Under != "" {
		if _, _, err := u.paperFor(ctx, p, ws, in.Under); err != nil {
			return nil, err
		}
	}
	entries, err := u.repo.Search(ctx, ws, q, min(limit*4, model.MaxSearchLimit))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to search")
	}
	var ids []model.PaperID
	seen := map[model.PaperID]bool{}
	for _, e := range entries {
		if !seen[e.PaperID] {
			seen[e.PaperID] = true
			ids = append(ids, e.PaperID)
		}
	}
	papers, err := u.repo.GetPapers(ctx, ws, ids)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read papers")
	}
	byID := map[model.PaperID]*model.Paper{}
	for _, x := range papers {
		if p.SeesPaper(x) && !x.IsTrashed() && (in.IncludeArchived || !x.IsArchived()) {
			byID[x.ID] = x
		}
	}
	if in.Under != "" {
		if byID, err = u.within(ctx, ws, in.Under, byID); err != nil {
			return nil, err
		}
	}
	var out []*SearchHit
	for _, e := range entries {
		x, ok := byID[e.PaperID]
		if !ok {
			continue
		}
		out = append(out, &SearchHit{Paper: refOf(x), BlockID: e.BlockID, Type: e.BlockType, Text: e.Text})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// within keeps the papers that are root or under it, walking parents a
// level at a time with one batch read per level.
func (u *UseCases) within(ctx context.Context, ws model.WorkspaceID, root model.PaperID, papers map[model.PaperID]*model.Paper) (map[model.PaperID]*model.Paper, error) {
	parent := map[model.PaperID]model.PaperID{}
	for id, q := range papers {
		parent[id] = q.ParentID
	}
	// at[id] is the ancestor currently reached from paper id
	at := map[model.PaperID]model.PaperID{}
	for id := range papers {
		at[id] = id
	}
	in := map[model.PaperID]bool{}
	for depth := 0; len(at) > 0 && depth <= model.MaxTreeDepth; depth++ {
		var need []model.PaperID
		for id, cur := range at {
			if cur == root {
				in[id] = true
				delete(at, id)
				continue
			}
			pid, ok := parent[cur]
			if !ok {
				need = append(need, cur)
				continue
			}
			if pid == "" {
				delete(at, id)
				continue
			}
			at[id] = pid
		}
		if len(need) > 0 {
			got, err := u.repo.GetPapers(ctx, ws, need)
			if err != nil {
				return nil, goerr.Wrap(err, "failed to read ancestors")
			}
			found := map[model.PaperID]bool{}
			for _, q := range got {
				parent[q.ID] = q.ParentID
				found[q.ID] = true
			}
			for id, cur := range at {
				if _, ok := parent[cur]; !ok && !found[cur] {
					delete(at, id) // dangling
				}
			}
		}
	}
	out := map[model.PaperID]*model.Paper{}
	for id := range in {
		out[id] = papers[id]
	}
	return out, nil
}

// Export renders the current state of a paper as Markdown.
func (u *UseCases) Export(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID) (string, error) {
	_, paper, err := u.paperFor(ctx, p, ws, id)
	if err != nil {
		return "", err
	}
	page, err := u.ListBlocks(ctx, p, ws, id, "", 0)
	if err != nil {
		return "", err
	}
	titles := map[model.PaperID]string{}
	for pid, r := range page.Papers {
		titles[pid] = r.Title
	}
	return markdown.Render(paper.Title, page.Blocks, titles), nil
}
