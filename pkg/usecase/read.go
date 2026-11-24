package usecase

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase/markdown"
)

// PaperRef is what lists show about a paper.
type PaperRef struct {
	ID            model.PaperID     `json:"id"`
	WorkspaceID   model.WorkspaceID `json:"workspace_id"`
	Title         string            `json:"title"`
	Kind          model.PaperKind   `json:"kind"`
	ParentID      model.PaperID     `json:"parent_id,omitempty"`
	Visibility    model.Visibility  `json:"visibility"`
	Status        model.PaperStatus `json:"status"`
	VersionNumber int               `json:"version_number"`
	CreatedBy     model.Actor       `json:"created_by"`
}

func refOf(p *model.Paper) *PaperRef {
	return &PaperRef{
		ID: p.ID, WorkspaceID: p.WorkspaceID, Title: p.Title, Kind: p.Kind.Or(), ParentID: p.ParentID, Visibility: p.Visibility,
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
	// LastOpened is the paper the principal opened last in the workspace,
	// nil when there is none or it can no longer be shown. Only
	// ListWorkspaces fills it.
	LastOpened *LastOpened `json:"last_opened"`
}

// LastOpened is where the principal left off in a workspace.
type LastOpened struct {
	PaperID  model.PaperID `json:"paper_id"`
	Title    string        `json:"title"`
	OpenedAt time.Time     `json:"opened_at"`
}

// ListWorkspaces lists the workspaces the principal may use, in display
// order, with the display groups and the paper last opened in each. The
// recent lists and their papers are read with one batch read each.
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
	if err := u.fillLastOpened(ctx, p, out); err != nil {
		return nil, nil, err
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

// fillLastOpened sets LastOpened of each view from the principal's recent
// lists: one batch read of the lists and one of their first papers.
func (u *UseCases) fillLastOpened(ctx context.Context, p *model.Principal, views []*WorkspaceView) error {
	if len(views) == 0 {
		return nil
	}
	wss := make([]model.WorkspaceID, 0, len(views))
	for _, v := range views {
		wss = append(wss, v.ID)
	}
	recents, err := u.repo.GetRecents(ctx, p.User.ID, wss)
	if err != nil {
		return goerr.Wrap(err, "failed to read recent papers")
	}
	last := map[model.WorkspaceID]*model.Recent{}
	var keys []model.PaperKey
	for _, r := range recents {
		if len(r.PaperIDs) > 0 {
			last[r.WorkspaceID] = r
			keys = append(keys, model.PaperKey{WorkspaceID: r.WorkspaceID, PaperID: r.PaperIDs[0]})
		}
	}
	if len(keys) == 0 {
		return nil
	}
	papers, err := u.repo.GetPapersIn(ctx, keys)
	if err != nil {
		return goerr.Wrap(err, "failed to read recent papers")
	}
	byKey := map[model.PaperKey]*model.Paper{}
	for _, q := range papers {
		byKey[model.PaperKey{WorkspaceID: q.WorkspaceID, PaperID: q.ID}] = q
	}
	for _, v := range views {
		r, ok := last[v.ID]
		if !ok {
			continue
		}
		if q, ok := byKey[model.PaperKey{WorkspaceID: v.ID, PaperID: r.PaperIDs[0]}]; ok && listed(p, q) {
			v.LastOpened = &LastOpened{PaperID: q.ID, Title: q.Title, OpenedAt: r.UpdatedAt}
		}
	}
	return nil
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
	// Values are an item's property values as they show under the current
	// definitions, and Database the database the item belongs to.
	Values   []*model.PropertyValue `json:"values,omitempty"`
	Database *DatabaseRef           `json:"database,omitempty"`
}

// DatabaseRef is what an item's view tells about its database.
type DatabaseRef struct {
	ID         model.PaperID    `json:"id"`
	Title      string           `json:"title"`
	Properties []model.Property `json:"properties"`
}

// ancestors walks up from paper to the home, at most MaxTreeDepth steps.
func (u *UseCases) ancestors(ctx context.Context, paper *model.Paper) ([]*PaperRef, error) {
	out := []*PaperRef{}
	at := paper.Above()
	for depth := 0; at != "" && depth < model.MaxTreeDepth; depth++ {
		q, err := u.repo.GetPaper(ctx, paper.WorkspaceID, at)
		if errors.Is(err, model.ErrNotFound) {
			break
		}
		if err != nil {
			return nil, goerr.Wrap(err, "failed to read ancestor", goerr.V("paper_id", at))
		}
		out = append(out, refOf(q))
		at = q.Above()
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
	view := &PaperView{Workspace: w, Paper: paper, Path: path, IsHome: paper.ID == home}
	if paper.IsItem() {
		db, values, err := u.itemValues(ctx, paper)
		if err != nil {
			return nil, err
		}
		view.Database = &DatabaseRef{ID: db.ID, Title: db.Title, Properties: db.Properties}
		view.Values = values
	}
	return view, nil
}

// itemValues reads an item's database and its values as they show under
// the database's current definitions, in property order.
func (u *UseCases) itemValues(ctx context.Context, item *model.Paper) (*model.Paper, []*model.PropertyValue, error) {
	db, err := u.repo.GetPaper(ctx, item.WorkspaceID, item.DatabaseID)
	if err != nil {
		return nil, nil, goerr.Wrap(err, "failed to read the item's database", goerr.V("database_id", item.DatabaseID))
	}
	r := &itemsReader{u: u, ctx: ctx, db: db}
	vals, err := r.values([]model.PaperID{item.ID})
	if err != nil {
		return nil, nil, err
	}
	out := vals[item.ID]
	if out == nil {
		out = []*model.PropertyValue{}
	}
	return db, out, nil
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
	Home      *PaperRef   `json:"home"`
	Favorites []*PaperRef `json:"favorites"`
	Recent    []*PaperRef `json:"recent"`
	// UnfiledCount is the number of the principal's own unfiled and
	// private papers, counted up to MaxUnfiledCount (which then means "at
	// least that many").
	UnfiledCount int `json:"unfiled_count"`
}

// MaxUnfiledCount caps Sidebar.UnfiledCount.
const MaxUnfiledCount = 100

// maxListReads bounds the queries one papers list (or count) makes.
const maxListReads = 5

func (u *UseCases) Sidebar(ctx context.Context, p *model.Principal, ws model.WorkspaceID) (*Sidebar, error) {
	if _, err := u.workspaceFor(ctx, p, ws); err != nil {
		return nil, err
	}
	homeID, err := u.homeID(ctx, ws)
	if err != nil {
		return nil, err
	}
	favorites, err := u.repo.ListFavorites(ctx, p.User.ID, ws)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list favorites")
	}
	rec, err := u.repo.GetRecent(ctx, p.User.ID, ws)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get recent papers")
	}
	ids := []model.PaperID{homeID}
	for _, f := range favorites {
		ids = append(ids, f.PaperID)
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
	sb := &Sidebar{Favorites: []*PaperRef{}, Recent: []*PaperRef{}}
	if h, ok := byID[homeID]; ok {
		sb.Home = refOf(h)
	}
	for _, f := range favorites {
		if q, ok := byID[f.PaperID]; ok && p.SeesPaper(q) && !q.IsTrashed() && !q.IsPrivate() {
			sb.Favorites = append(sb.Favorites, refOf(q))
		}
	}
	for _, id := range rec.PaperIDs {
		if q, ok := byID[id]; ok && listed(p, q) {
			sb.Recent = append(sb.Recent, refOf(q))
		}
	}
	if sb.UnfiledCount, err = u.countUnfiled(ctx, p, ws, homeID); err != nil {
		return nil, err
	}
	return sb, nil
}

// countUnfiled counts the principal's own unfiled and private papers that
// are neither archived, trashed nor being moved, reading at most
// MaxUnfiledCount of them per query and stopping at MaxUnfiledCount.
func (u *UseCases) countUnfiled(ctx context.Context, p *model.Principal, ws model.WorkspaceID, home model.PaperID) (int, error) {
	n, cursor := 0, ""
	for range maxListReads {
		got, next, err := u.repo.ListPapers(ctx, ws, model.PaperQuery{
			Unfiled: true, Creator: p.User.ID, Sort: model.SortUpdated, Limit: MaxUnfiledCount, Cursor: cursor,
		})
		if err != nil {
			return 0, goerr.Wrap(err, "failed to list unfiled papers")
		}
		for _, q := range got {
			if q.ID != home && listed(p, q) && !q.IsBusy() {
				n++
			}
		}
		if n >= MaxUnfiledCount {
			return MaxUnfiledCount, nil
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return n, nil
}

// PaperPlace narrows a papers list by where the papers are.
type PaperPlace string

const (
	// PlaceUnfiled is the papers outside the tree: unfiled papers and
	// private papers.
	PlaceUnfiled PaperPlace = "unfiled"
	// PlaceFiled is the papers in the tree, other than the home.
	PlaceFiled PaperPlace = "filed"
	PlaceAll   PaperPlace = "all"
)

// PaperCreator narrows a papers list by who created the papers.
type PaperCreator string

const (
	CreatorMe  PaperCreator = "me"
	CreatorAll PaperCreator = "all"
)

// VisibilityAll lists public and private papers together.
const VisibilityAll model.Visibility = "all"

// ListPapersInput is one page of the papers list. Empty values take the
// defaults: unfiled, mine, any visibility, without archived papers, by
// update, 50 papers.
type ListPapersInput struct {
	Place      PaperPlace
	Creator    PaperCreator
	Visibility model.Visibility
	Archived   bool
	Sort       model.PaperSort
	Limit      int
	Cursor     string
}

// DefaultPaperListLimit is the page size of a papers list when none is
// given.
const DefaultPaperListLimit = 50

func (in *ListPapersInput) normalize() error {
	if in.Place == "" {
		in.Place = PlaceUnfiled
	}
	if in.Creator == "" {
		in.Creator = CreatorMe
	}
	if in.Visibility == "" {
		in.Visibility = VisibilityAll
	}
	if in.Sort == "" {
		in.Sort = model.SortUpdated
	}
	if in.Limit == 0 {
		in.Limit = DefaultPaperListLimit
	}
	switch in.Place {
	case PlaceUnfiled, PlaceFiled, PlaceAll:
	default:
		return goerr.Wrap(model.ErrInvalidArgument, "place must be unfiled, filed or all", goerr.V("place", in.Place))
	}
	switch in.Creator {
	case CreatorMe, CreatorAll:
	default:
		return goerr.Wrap(model.ErrInvalidArgument, "creator must be me or all", goerr.V("creator", in.Creator))
	}
	if in.Visibility != VisibilityAll {
		if err := in.Visibility.Validate(); err != nil {
			return goerr.Wrap(model.ErrInvalidArgument, "visibility must be all, public or private", goerr.V("visibility", in.Visibility))
		}
	}
	if err := in.Sort.Validate(); err != nil {
		return err
	}
	if in.Limit < 1 || in.Limit > model.MaxPaperListLimit {
		return goerr.Wrap(model.ErrInvalidArgument, "limit must be between 1 and 100", goerr.V("limit", in.Limit))
	}
	if in.Cursor != "" {
		if _, err := model.ParsePaperCursor(in.Cursor); err != nil {
			return err
		}
	}
	return nil
}

// ParentRef is the parent shown next to a paper in a list.
type ParentRef struct {
	ID    model.PaperID `json:"id"`
	Title string        `json:"title"`
}

// ListedPaper is one row of the papers list.
type ListedPaper struct {
	*PaperRef
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Parent    *ParentRef `json:"parent"`
}

// PapersPage is one page of the papers list.
type PapersPage struct {
	Papers []*ListedPaper `json:"papers"`
	// NextCursor continues the list; "" at the end.
	NextCursor string `json:"next_cursor"`
}

// ListPapers reads one page of the workspace's papers list. Storage filters
// only on "unfiled" and "mine"; the home, trashed and moving papers, papers
// the principal may not see, the wrong visibility or place and (unless
// asked) archived papers are dropped here. Each query asks for twice the
// page size, and reading goes on until the page is full or maxListReads
// queries were made; the parents' titles come from one batch read.
func (u *UseCases) ListPapers(ctx context.Context, p *model.Principal, ws model.WorkspaceID, in ListPapersInput) (*PapersPage, error) {
	if _, err := u.workspaceFor(ctx, p, ws); err != nil {
		return nil, err
	}
	if err := in.normalize(); err != nil {
		return nil, err
	}
	homeID, err := u.homeID(ctx, ws)
	if err != nil {
		return nil, err
	}
	q := model.PaperQuery{Unfiled: in.Place == PlaceUnfiled, Sort: in.Sort, Limit: min(in.Limit*2, model.MaxPaperListLimit), Cursor: in.Cursor}
	if in.Creator == CreatorMe {
		q.Creator = p.User.ID
	}
	keep := func(x *model.Paper) bool {
		switch {
		case x.ID == homeID, x.IsTrashed(), x.IsBusy(), !p.SeesPaper(x):
			return false
		case !in.Archived && x.IsArchived():
			return false
		case in.Visibility != VisibilityAll && x.Visibility != in.Visibility:
			return false
		case in.Place == PlaceFiled && x.Above() == "":
			return false
		}
		return true
	}
	var picked []*model.Paper
	next := ""
	for range maxListReads {
		got, more, err := u.repo.ListPapers(ctx, ws, q)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list papers")
		}
		next = more
		for i, x := range got {
			if !keep(x) {
				continue
			}
			picked = append(picked, x)
			if len(picked) == in.Limit {
				// the page is full: continue right after this paper,
				// unless it was the last one there is
				next = ""
				if i < len(got)-1 || more != "" {
					next = model.CursorOf(in.Sort, x)
				}
				break
			}
		}
		if len(picked) == in.Limit || next == "" {
			break
		}
		q.Cursor = next
	}

	page := &PapersPage{Papers: make([]*ListedPaper, 0, len(picked)), NextCursor: next}
	var parentIDs []model.PaperID
	seen := map[model.PaperID]bool{}
	for _, x := range picked {
		if up := x.Above(); up != "" && !seen[up] {
			seen[up] = true
			parentIDs = append(parentIDs, up)
		}
	}
	parents := map[model.PaperID]*model.Paper{}
	if len(parentIDs) > 0 {
		got, err := u.repo.GetPapers(ctx, ws, parentIDs)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to read parents")
		}
		for _, x := range got {
			parents[x.ID] = x
		}
	}
	for _, x := range picked {
		row := &ListedPaper{PaperRef: refOf(x), CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
		if par, ok := parents[x.Above()]; ok && p.SeesPaper(par) {
			row.Parent = &ParentRef{ID: par.ID, Title: par.Title}
		}
		page.Papers = append(page.Papers, row)
	}
	return page, nil
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
		if p.SeesPaper(x) && !x.IsTrashed() && !x.IsBusy() && (in.IncludeArchived || !x.IsArchived()) {
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
		parent[id] = q.Above()
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
				parent[q.ID] = q.Above()
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
	if paper.IsHTML() {
		return "", goerr.Wrap(model.ErrInvalidArgument, "an HTML paper has no Markdown form; read its HTML instead", goerr.V("paper_id", id))
	}
	if paper.IsDatabase() {
		return u.exportDatabase(ctx, p, paper)
	}
	page, err := u.ListBlocks(ctx, p, ws, id, "", 0)
	if err != nil {
		return "", err
	}
	titles := map[model.PaperID]string{}
	for pid, r := range page.Papers {
		titles[pid] = r.Title
	}
	if paper.IsItem() {
		db, values, err := u.itemValues(ctx, paper)
		if err != nil {
			return "", err
		}
		return markdown.RenderItem(paper.Title, db.Properties, valueMap(values), page.Blocks, titles), nil
	}
	return markdown.Render(paper.Title, page.Blocks, titles), nil
}

func valueMap(values []*model.PropertyValue) map[model.PropertyID]any {
	out := make(map[model.PropertyID]any, len(values))
	for _, v := range values {
		out[v.PropertyID] = v.Value
	}
	return out
}

// exportDatabase renders a database as a Markdown table of all its items,
// read a page at a time.
func (u *UseCases) exportDatabase(ctx context.Context, p *model.Principal, db *model.Paper) (string, error) {
	var rows []markdown.DatabaseRow
	after := ""
	for {
		page, err := u.ListItems(ctx, p, db.WorkspaceID, db.ID, ItemsInput{After: after, Limit: MaxItemsLimit})
		if err != nil {
			return "", err
		}
		for _, r := range page.Items {
			rows = append(rows, markdown.DatabaseRow{Title: r.Item.Title, Values: valueMap(r.Values)})
		}
		if page.NextCursor == "" {
			break
		}
		after = page.NextCursor
	}
	return markdown.RenderDatabase(db.Title, "Name", db.Properties, rows), nil
}
