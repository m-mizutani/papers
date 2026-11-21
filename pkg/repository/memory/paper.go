package memory

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

func compareBlocks(a, b *model.Block) int {
	if c := cmp.Compare(a.ParentID, b.ParentID); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Order, b.Order); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

func (r *Repository) data(ws model.WorkspaceID, id model.PaperID) (*paperData, error) {
	d, ok := r.papers[paperKey{ws, id}]
	if !ok {
		return nil, notFound("paper", id)
	}
	return d, nil
}

func (r *Repository) GetPaper(_ context.Context, ws model.WorkspaceID, id model.PaperID) (*model.Paper, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.data(ws, id)
	if err != nil {
		return nil, err
	}
	return clone(d.paper), nil
}

func (r *Repository) GetPapers(_ context.Context, ws model.WorkspaceID, ids []model.PaperID) ([]*model.Paper, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*model.Paper
	for _, id := range ids {
		if d, ok := r.papers[paperKey{ws, id}]; ok {
			out = append(out, clone(d.paper))
		}
	}
	return out, nil
}

func (r *Repository) listPapers(ws model.WorkspaceID, keep func(*model.Paper) bool) []*model.Paper {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*model.Paper
	for k, d := range r.papers {
		if k.ws == ws && keep(d.paper) {
			out = append(out, clone(d.paper))
		}
	}
	slices.SortFunc(out, func(a, b *model.Paper) int {
		return cmp.Or(cmp.Compare(a.Title, b.Title), cmp.Compare(a.ID, b.ID))
	})
	return out
}

func (r *Repository) ListChildren(_ context.Context, ws model.WorkspaceID, parent model.PaperID) ([]*model.Paper, error) {
	return r.listPapers(ws, func(p *model.Paper) bool { return p.ParentID == parent && parent != "" }), nil
}

func (r *Repository) ListChildrenOf(_ context.Context, ws model.WorkspaceID, parents []model.PaperID) ([]*model.Paper, error) {
	want := map[model.PaperID]bool{}
	for _, id := range parents {
		if id != "" {
			want[id] = true
		}
	}
	return r.listPapers(ws, func(p *model.Paper) bool { return want[p.ParentID] }), nil
}

func (r *Repository) ListPrivatePapers(_ context.Context, ws model.WorkspaceID, owner model.UserID) ([]*model.Paper, error) {
	return r.listPapers(ws, func(p *model.Paper) bool {
		return p.Visibility == model.VisibilityPrivate && p.Owner() == owner && p.DeletedAt == nil
	}), nil
}

func (r *Repository) ListTrash(_ context.Context, ws model.WorkspaceID, owner model.UserID) ([]*model.Paper, error) {
	return r.listPapers(ws, func(p *model.Paper) bool { return p.DeletedAt != nil && p.Owner() == owner }), nil
}

func (r *Repository) ListTrashedBefore(_ context.Context, ws model.WorkspaceID, t time.Time) ([]*model.Paper, error) {
	return r.listPapers(ws, func(p *model.Paper) bool { return p.DeletedAt != nil && p.DeletedAt.Before(t) }), nil
}

func (r *Repository) PurgePaper(_ context.Context, ws model.WorkspaceID, id model.PaperID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := paperKey{ws, id}
	if _, ok := r.papers[key]; !ok {
		return notFound("paper", id)
	}
	delete(r.papers, key)
	for wid, c := range r.watchers[key] {
		close(c)
		delete(r.watchers[key], wid)
	}
	return nil
}

func (r *Repository) GetBlocks(_ context.Context, ws model.WorkspaceID, paper model.PaperID, ids []model.BlockID) ([]*model.Block, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.data(ws, paper)
	if err != nil {
		return nil, err
	}
	var out []*model.Block
	for _, id := range ids {
		if b, ok := d.blocks[id]; ok {
			out = append(out, b.Clone())
		}
	}
	return out, nil
}

func (r *Repository) ListBlocks(_ context.Context, ws model.WorkspaceID, paper model.PaperID) ([]*model.Block, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.data(ws, paper)
	if err != nil {
		return nil, err
	}
	out := make([]*model.Block, 0, len(d.blocks))
	for _, b := range d.blocks {
		out = append(out, b.Clone())
	}
	slices.SortFunc(out, compareBlocks)
	return out, nil
}

func (r *Repository) ListPaperBlocks(_ context.Context, ws model.WorkspaceID, paper model.PaperID) ([]*model.Block, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.data(ws, paper)
	if err != nil {
		return nil, err
	}
	var out []*model.Block
	for _, b := range d.blocks {
		if b.Type == model.BlockPaper && b.ParentID == "" {
			out = append(out, b.Clone())
		}
	}
	slices.SortFunc(out, compareBlocks)
	return out, nil
}

func (r *Repository) ListBlocksRange(_ context.Context, ws model.WorkspaceID, paper model.PaperID, after string, limit int) ([]*model.Block, string, error) {
	if limit < 1 {
		return nil, "", goerr.Wrap(model.ErrInvalidArgument, "limit must be positive")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.data(ws, paper)
	if err != nil {
		return nil, "", err
	}
	var top []*model.Block
	for _, b := range d.blocks {
		if b.ParentID == "" && b.Order > after {
			top = append(top, b)
		}
	}
	slices.SortFunc(top, compareBlocks)
	next := ""
	if len(top) > limit {
		top = top[:limit]
		next = top[len(top)-1].Order
	}
	var out []*model.Block
	tables := map[model.BlockID]bool{}
	for _, b := range top {
		out = append(out, b.Clone())
		if b.Type == model.BlockTable {
			tables[b.ID] = true
		}
	}
	for _, b := range d.blocks {
		if tables[b.ParentID] {
			out = append(out, b.Clone())
		}
	}
	slices.SortFunc(out, compareBlocks)
	return out, next, nil
}

func (r *Repository) ListVersions(_ context.Context, ws model.WorkspaceID, paper model.PaperID, before, limit int) ([]*model.Version, error) {
	if limit < 1 {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "limit must be positive")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.data(ws, paper)
	if err != nil {
		return nil, err
	}
	var out []*model.Version
	for n, v := range d.versions {
		if before <= 0 || n < before {
			out = append(out, clone(v))
		}
	}
	slices.SortFunc(out, func(a, b *model.Version) int { return cmp.Compare(b.Number, a.Number) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *Repository) GetVersion(_ context.Context, ws model.WorkspaceID, paper model.PaperID, number int) (*model.Version, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.data(ws, paper)
	if err != nil {
		return nil, err
	}
	v, ok := d.versions[number]
	if !ok {
		return nil, notFound("version", number)
	}
	return clone(v), nil
}

func (r *Repository) GetSnapshots(_ context.Context, ws model.WorkspaceID, paper model.PaperID, refs []model.SnapshotRef) ([]*model.BlockSnapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.data(ws, paper)
	if err != nil {
		return nil, err
	}
	var out []*model.BlockSnapshot
	for _, ref := range refs {
		if s, ok := d.snapshots[ref]; ok {
			out = append(out, clone(s))
		}
	}
	return out, nil
}

// --- transactions

type stagedSnapshot struct {
	paper model.PaperID
	snap  *model.BlockSnapshot
}

type memTx struct {
	r         *Repository
	ws        model.WorkspaceID
	papers    map[model.PaperID]*model.Paper
	blocks    map[model.PaperID]map[model.BlockID]*model.Block
	deleted   map[model.PaperID]map[model.BlockID]bool
	versions  []*model.Version
	snapshots []stagedSnapshot
	forwards  []*model.Forward
	events    []*model.Event
	jobs      map[model.JobID]*model.Job
	items     map[model.JobID]map[model.PaperID]*model.JobItem
}

func (t *memTx) paperExists(id model.PaperID) bool {
	t.r.mu.RLock()
	defer t.r.mu.RUnlock()
	if _, ok := t.papers[id]; ok {
		return true
	}
	_, ok := t.r.papers[paperKey{t.ws, id}]
	return ok
}

func (t *memTx) GetPaper(id model.PaperID) (*model.Paper, error) {
	t.r.mu.RLock()
	defer t.r.mu.RUnlock()
	if p, ok := t.papers[id]; ok {
		return clone(p), nil
	}
	d, err := t.r.data(t.ws, id)
	if err != nil {
		return nil, err
	}
	return clone(d.paper), nil
}

func (t *memTx) GetPapers(ids []model.PaperID) ([]*model.Paper, error) {
	var out []*model.Paper
	for _, id := range ids {
		if p, err := t.GetPaper(id); err == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func (t *memTx) PutPaper(paper *model.Paper) error {
	if err := paper.Validate(); err != nil {
		return err
	}
	if paper.WorkspaceID != t.ws {
		return goerr.Wrap(model.ErrInvalidArgument, "paper belongs to another workspace")
	}
	t.papers[paper.ID] = clone(paper)
	return nil
}

func (t *memTx) GetBlock(paper model.PaperID, id model.BlockID) (*model.Block, error) {
	t.r.mu.RLock()
	defer t.r.mu.RUnlock()
	if t.deleted[paper][id] {
		return nil, notFound("block", id)
	}
	if b, ok := t.blocks[paper][id]; ok {
		return b.Clone(), nil
	}
	d, ok := t.r.papers[paperKey{t.ws, paper}]
	if !ok {
		return nil, notFound("block", id)
	}
	b, ok := d.blocks[id]
	if !ok {
		return nil, notFound("block", id)
	}
	return b.Clone(), nil
}

func (t *memTx) GetBlocks(paper model.PaperID, ids []model.BlockID) ([]*model.Block, error) {
	var out []*model.Block
	for _, id := range ids {
		if b, err := t.GetBlock(paper, id); err == nil {
			out = append(out, b)
		}
	}
	return out, nil
}

func (t *memTx) all(paper model.PaperID, keep func(*model.Block) bool) []*model.Block {
	t.r.mu.RLock()
	defer t.r.mu.RUnlock()
	var out []*model.Block
	for _, b := range t.blocks[paper] {
		if keep == nil || keep(b) {
			out = append(out, b.Clone())
		}
	}
	if d, ok := t.r.papers[paperKey{t.ws, paper}]; ok {
		for id, b := range d.blocks {
			if _, staged := t.blocks[paper][id]; staged || t.deleted[paper][id] {
				continue
			}
			if keep == nil || keep(b) {
				out = append(out, b.Clone())
			}
		}
	}
	slices.SortFunc(out, compareBlocks)
	return out
}

func (t *memTx) Children(paper model.PaperID, parent model.BlockID) ([]*model.Block, error) {
	return t.all(paper, func(b *model.Block) bool { return b.ParentID == parent }), nil
}

func (t *memTx) FirstAfter(paper model.PaperID, parent model.BlockID, order string) (*model.Block, error) {
	children, _ := t.Children(paper, parent)
	for _, b := range children {
		if b.Order > order {
			return b, nil
		}
	}
	return nil, nil
}

func (t *memTx) Last(paper model.PaperID, parent model.BlockID) (*model.Block, error) {
	children, _ := t.Children(paper, parent)
	if len(children) == 0 {
		return nil, nil
	}
	return children[len(children)-1], nil
}

func (t *memTx) ListBlocks(paper model.PaperID) ([]*model.Block, error) {
	return t.all(paper, nil), nil
}

func (t *memTx) CountBlocks(paper model.PaperID) (int, error) {
	t.r.mu.RLock()
	defer t.r.mu.RUnlock()
	n := 0
	for id := range t.blocks[paper] {
		if d, ok := t.r.papers[paperKey{t.ws, paper}]; !ok || d.blocks[id] == nil {
			n++
		}
	}
	if d, ok := t.r.papers[paperKey{t.ws, paper}]; ok {
		for id := range d.blocks {
			if !t.deleted[paper][id] {
				n++
			}
		}
	}
	return n, nil
}

func (t *memTx) PutBlock(block *model.Block) error {
	if err := block.Validate(); err != nil {
		return err
	}
	if block.WorkspaceID != t.ws || !t.paperExists(block.PaperID) {
		return goerr.Wrap(model.ErrInvalidArgument, "block belongs to an unknown paper", goerr.V("paper_id", block.PaperID))
	}
	if t.blocks[block.PaperID] == nil {
		t.blocks[block.PaperID] = map[model.BlockID]*model.Block{}
	}
	delete(t.deleted[block.PaperID], block.ID)
	t.blocks[block.PaperID][block.ID] = clone(block)
	return nil
}

func (t *memTx) DeleteBlock(paper model.PaperID, id model.BlockID) error {
	delete(t.blocks[paper], id)
	if t.deleted[paper] == nil {
		t.deleted[paper] = map[model.BlockID]bool{}
	}
	t.deleted[paper][id] = true
	return nil
}

func (t *memTx) GetVersion(paper model.PaperID, number int) (*model.Version, error) {
	t.r.mu.RLock()
	defer t.r.mu.RUnlock()
	for _, v := range t.versions {
		if v.PaperID == paper && v.Number == number {
			return clone(v), nil
		}
	}
	d, err := t.r.data(t.ws, paper)
	if err != nil {
		return nil, err
	}
	v, ok := d.versions[number]
	if !ok {
		return nil, notFound("version", number)
	}
	return clone(v), nil
}

func (t *memTx) PutVersion(v *model.Version) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if _, err := t.GetVersion(v.PaperID, v.Number); err == nil {
		return goerr.Wrap(model.ErrAlreadyExists, "version already exists", goerr.V("number", v.Number))
	}
	t.versions = append(t.versions, clone(v))
	return nil
}

func (t *memTx) GetSnapshots(paper model.PaperID, refs []model.SnapshotRef) ([]*model.BlockSnapshot, error) {
	t.r.mu.RLock()
	defer t.r.mu.RUnlock()
	var out []*model.BlockSnapshot
	d := t.r.papers[paperKey{t.ws, paper}]
	for _, ref := range refs {
		found := false
		for _, s := range t.snapshots {
			if s.paper == paper && s.snap.BlockID == ref.BlockID && s.snap.Rev == ref.Rev {
				out, found = append(out, clone(s.snap)), true
				break
			}
		}
		if !found && d != nil {
			if s, ok := d.snapshots[ref]; ok {
				out = append(out, clone(s))
			}
		}
	}
	return out, nil
}

func (t *memTx) PutSnapshot(paper model.PaperID, s *model.BlockSnapshot) error {
	t.snapshots = append(t.snapshots, stagedSnapshot{paper, clone(s)})
	return nil
}

func (t *memTx) PutForward(f *model.Forward) error {
	t.forwards = append(t.forwards, clone(f))
	return nil
}

func (t *memTx) AddEvent(ev *model.Event) error {
	t.events = append(t.events, clone(ev))
	return nil
}

var _ interfaces.Tx = (*memTx)(nil)

// RunTx serialises transactions with txMu. The transaction function reads
// under the read lock, so it may also call the repository's other methods;
// the writes are applied under the write lock at the end.
func (r *Repository) RunTx(_ context.Context, ws model.WorkspaceID, fn func(tx interfaces.Tx) error) error {
	r.txMu.Lock()
	defer r.txMu.Unlock()
	t := &memTx{
		r: r, ws: ws,
		papers:  map[model.PaperID]*model.Paper{},
		blocks:  map[model.PaperID]map[model.BlockID]*model.Block{},
		deleted: map[model.PaperID]map[model.BlockID]bool{},
		jobs:    map[model.JobID]*model.Job{},
		items:   map[model.JobID]map[model.PaperID]*model.JobItem{},
	}
	if err := fn(t); err != nil {
		return err
	}
	r.mu.Lock()
	type emit struct {
		key paperKey
		ev  *model.Event
	}
	var emits []emit
	for id, p := range t.papers {
		key := paperKey{ws, id}
		d, ok := r.papers[key]
		if !ok {
			d = newPaperData(p)
			r.papers[key] = d
		}
		d.paper = clone(p)
		d.indexTitle()
	}
	for paper, deleted := range t.deleted {
		d := r.papers[paperKey{ws, paper}]
		if d == nil {
			continue
		}
		for id := range deleted {
			delete(d.blocks, id)
			delete(d.search, id)
		}
	}
	for paper, blocks := range t.blocks {
		d := r.papers[paperKey{ws, paper}]
		for id, b := range blocks {
			d.blocks[id] = b.Clone()
			d.indexBlock(b)
		}
	}
	for _, v := range t.versions {
		r.papers[paperKey{ws, v.PaperID}].versions[v.Number] = clone(v)
	}
	for _, s := range t.snapshots {
		d := r.papers[paperKey{ws, s.paper}]
		ref := model.SnapshotRef{BlockID: s.snap.BlockID, Rev: s.snap.Rev}
		if _, ok := d.snapshots[ref]; !ok {
			d.snapshots[ref] = clone(s.snap)
		}
	}
	for _, f := range t.forwards {
		r.forwards[paperKey{f.WorkspaceID, f.PaperID}] = clone(f)
	}
	for id, j := range t.jobs {
		r.jobs[jobKey{ws, id}] = j.Clone()
	}
	for job, items := range t.items {
		key := jobKey{ws, job}
		if r.jobItems[key] == nil {
			r.jobItems[key] = map[model.PaperID]*model.JobItem{}
		}
		for id, it := range items {
			r.jobItems[key][id] = it.Clone()
		}
	}
	for _, ev := range t.events {
		key := paperKey{ws, ev.PaperID}
		if d, ok := r.papers[key]; ok {
			d.appendEvent(ev)
		}
		emits = append(emits, emit{key, ev})
	}
	r.mu.Unlock()
	for _, e := range emits {
		r.emit(e.key, e.ev)
	}
	return nil
}

// --- feed

func (d *paperData) appendEvent(ev *model.Event) {
	cutoff := ev.At.Add(-model.EventTTL)
	keep := d.events[:0]
	for _, e := range d.events {
		if e.At.After(cutoff) {
			keep = append(keep, e)
		}
	}
	d.events = append(keep, clone(ev))
}

func (r *Repository) PutEvent(_ context.Context, ev *model.Event) error {
	key := paperKey{ev.WorkspaceID, ev.PaperID}
	r.mu.Lock()
	d, ok := r.papers[key]
	if !ok {
		r.mu.Unlock()
		return notFound("paper", ev.PaperID)
	}
	d.appendEvent(ev)
	r.mu.Unlock()
	r.emit(key, ev)
	return nil
}

func (r *Repository) ListEvents(_ context.Context, ws model.WorkspaceID, paper model.PaperID, since time.Time) ([]*model.Event, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.data(ws, paper)
	if err != nil {
		return nil, err
	}
	var out []*model.Event
	for _, e := range d.events {
		if !e.At.Before(since) {
			out = append(out, clone(e))
		}
	}
	return out, nil
}

func (r *Repository) emit(key paperKey, ev *model.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, ch := range r.watchers[key] {
		select {
		case ch <- clone(ev):
		default:
			// a slow reader loses its feed and must reload
			close(ch)
			delete(r.watchers[key], id)
		}
	}
}

func (r *Repository) Watch(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) (<-chan *model.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.data(ws, paper); err != nil {
		return nil, err
	}
	key := paperKey{ws, paper}
	if r.watchers[key] == nil {
		r.watchers[key] = map[int]chan *model.Event{}
	}
	id := r.nextWatcher
	r.nextWatcher++
	ch := make(chan *model.Event, r.eventBuffer)
	r.watchers[key][id] = ch
	context.AfterFunc(ctx, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if c, ok := r.watchers[key][id]; ok {
			close(c)
			delete(r.watchers[key], id)
		}
	})
	return ch, nil
}
