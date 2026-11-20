package memory

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

func (r *Repository) CreatePaper(_ context.Context, paper *model.Paper) error {
	if err := paper.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := paperKey{paper.WorkspaceID, paper.ID}
	if _, ok := r.papers[key]; ok {
		return goerr.Wrap(model.ErrAlreadyExists, "paper already exists", goerr.V("id", paper.ID))
	}
	r.papers[key] = &paperData{
		paper:    clone(paper),
		blocks:   map[model.BlockID]*model.Block{},
		versions: map[model.BlockID][]*model.Block{},
		locks:    map[model.BlockID]*model.BlockLock{},
		search:   map[model.BlockID]*model.SearchEntry{},
	}
	r.papers[key].indexTitle()
	return nil
}

func (r *Repository) getPaperData(ws model.WorkspaceID, id model.PaperID) (*paperData, error) {
	d, ok := r.papers[paperKey{ws, id}]
	if !ok {
		return nil, notFound("paper", id)
	}
	return d, nil
}

func (r *Repository) GetPaper(_ context.Context, ws model.WorkspaceID, id model.PaperID) (*model.Paper, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.getPaperData(ws, id)
	if err != nil {
		return nil, err
	}
	return clone(d.paper), nil
}

func (r *Repository) ListPapers(_ context.Context, ws model.WorkspaceID) ([]*model.Paper, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*model.Paper
	for k, d := range r.papers {
		if k.ws == ws {
			out = append(out, clone(d.paper))
		}
	}
	slices.SortFunc(out, func(a, b *model.Paper) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (r *Repository) UpdatePaperAccess(_ context.Context, ws model.WorkspaceID, id model.PaperID, access model.PaperAccess, updatedBy model.Actor, updatedAt time.Time) (*model.Paper, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, err := r.getPaperData(ws, id)
	if err != nil {
		return nil, err
	}
	p := clone(d.paper)
	p.Access = access
	p.UpdatedBy = updatedBy
	p.UpdatedAt = updatedAt
	if err := p.Validate(); err != nil {
		return nil, err
	}
	d.paper = clone(p)
	r.emit(paperKey{ws, id}, &model.Event{Type: model.EventPaperUpdated, Paper: clone(p)})
	return p, nil
}

func (r *Repository) DeletePaper(_ context.Context, ws model.WorkspaceID, id model.PaperID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.getPaperData(ws, id); err != nil {
		return err
	}
	key := paperKey{ws, id}
	delete(r.papers, key)
	r.emit(key, &model.Event{Type: model.EventPaperDeleted})
	return nil
}

func (r *Repository) GetBlock(_ context.Context, ws model.WorkspaceID, paper model.PaperID, id model.BlockID) (*model.Block, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.getPaperData(ws, paper)
	if err != nil {
		return nil, err
	}
	b, ok := d.blocks[id]
	if !ok {
		return nil, notFound("block", id)
	}
	return clone(b), nil
}

func compareBlocks(a, b *model.Block) int {
	if c := cmp.Compare(a.ParentID, b.ParentID); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Order, b.Order); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

func (r *Repository) ListBlocks(_ context.Context, ws model.WorkspaceID, paper model.PaperID) ([]*model.Block, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.getPaperData(ws, paper)
	if err != nil {
		return nil, err
	}
	out := make([]*model.Block, 0, len(d.blocks))
	for _, b := range d.blocks {
		out = append(out, clone(b))
	}
	slices.SortFunc(out, compareBlocks)
	return out, nil
}

func (r *Repository) GetBlockVersion(_ context.Context, ws model.WorkspaceID, paper model.PaperID, id model.BlockID, version int64) (*model.Block, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.getPaperData(ws, paper)
	if err != nil {
		return nil, err
	}
	for _, v := range d.versions[id] {
		if v.Version == version {
			return clone(v), nil
		}
	}
	return nil, notFound("block version", string(id)+"@"+strconv.FormatInt(version, 10))
}

func (r *Repository) ListBlockVersions(_ context.Context, ws model.WorkspaceID, paper model.PaperID, id model.BlockID, limit int) ([]*model.Block, error) {
	if limit <= 0 {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "limit must be positive")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.getPaperData(ws, paper)
	if err != nil {
		return nil, err
	}
	vs := d.versions[id]
	out := make([]*model.Block, 0, min(limit, len(vs)))
	for i := len(vs) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, clone(vs[i]))
	}
	return out, nil
}

func compareCommitsDesc(a, b *model.Commit) int {
	if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
		return c
	}
	return cmp.Compare(b.ID, a.ID)
}

func (r *Repository) ListCommits(_ context.Context, ws model.WorkspaceID, paper model.PaperID, before model.CommitID, limit int) ([]*model.Commit, error) {
	if limit <= 0 {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "limit must be positive")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.getPaperData(ws, paper)
	if err != nil {
		return nil, err
	}
	sorted := slices.Clone(d.commits)
	slices.SortFunc(sorted, compareCommitsDesc)
	start := 0
	if before != "" {
		idx := slices.IndexFunc(sorted, func(c *model.Commit) bool { return c.ID == before })
		if idx < 0 {
			return nil, notFound("commit", before)
		}
		start = idx + 1
	}
	var out []*model.Commit
	for _, c := range sorted[start:] {
		if len(out) >= limit {
			break
		}
		out = append(out, clone(c))
	}
	return out, nil
}

// paperTx stages writes and applies them when the transaction function
// succeeds. The whole repository is locked for the duration, which
// serialises transactions.
type paperTx struct {
	data    *paperData
	paper   *model.Paper
	puts    map[model.BlockID]*model.Block
	deletes map[model.BlockID]struct{}
	commits []*model.Commit
	paperW  bool
}

func (t *paperTx) Paper() *model.Paper { return clone(t.paper) }

func (t *paperTx) GetBlock(id model.BlockID) (*model.Block, error) {
	if _, ok := t.deletes[id]; ok {
		return nil, notFound("block", id)
	}
	if b, ok := t.puts[id]; ok {
		return clone(b), nil
	}
	if b, ok := t.data.blocks[id]; ok {
		return clone(b), nil
	}
	return nil, notFound("block", id)
}

func (t *paperTx) Children(parent model.BlockID) ([]*model.Block, error) {
	var out []*model.Block
	for id, b := range t.data.blocks {
		if _, ok := t.puts[id]; ok {
			continue
		}
		if _, ok := t.deletes[id]; ok {
			continue
		}
		if b.ParentID == parent {
			out = append(out, clone(b))
		}
	}
	for _, b := range t.puts {
		if b.ParentID == parent {
			out = append(out, clone(b))
		}
	}
	slices.SortFunc(out, compareBlocks)
	return out, nil
}

func (t *paperTx) FirstAfter(parent model.BlockID, order string) (*model.Block, error) {
	children, err := t.Children(parent)
	if err != nil {
		return nil, err
	}
	for _, b := range children {
		if b.Order > order {
			return b, nil
		}
	}
	return nil, nil
}

func (t *paperTx) Last(parent model.BlockID) (*model.Block, error) {
	children, err := t.Children(parent)
	if err != nil {
		return nil, err
	}
	if len(children) == 0 {
		return nil, nil
	}
	return children[len(children)-1], nil
}

func (t *paperTx) PutBlock(block *model.Block) error {
	if err := block.Validate(); err != nil {
		return err
	}
	if block.WorkspaceID != t.paper.WorkspaceID || block.PaperID != t.paper.ID {
		return goerr.Wrap(model.ErrInvalidArgument, "block belongs to another paper", goerr.V("block_id", block.ID))
	}
	delete(t.deletes, block.ID)
	t.puts[block.ID] = clone(block)
	return nil
}

func (t *paperTx) DeleteBlock(id model.BlockID) error {
	delete(t.puts, id)
	t.deletes[id] = struct{}{}
	return nil
}

func (t *paperTx) PutPaper(paper *model.Paper) error {
	if err := paper.Validate(); err != nil {
		return err
	}
	if paper.ID != t.paper.ID || paper.WorkspaceID != t.paper.WorkspaceID {
		return goerr.Wrap(model.ErrInvalidArgument, "cannot replace another paper in a paper transaction")
	}
	t.paper = clone(paper)
	t.paperW = true
	return nil
}

func (t *paperTx) AddCommit(commit *model.Commit) error {
	if err := commit.Validate(); err != nil {
		return err
	}
	if commit.PaperID != t.paper.ID || commit.WorkspaceID != t.paper.WorkspaceID {
		return goerr.Wrap(model.ErrInvalidArgument, "commit belongs to another paper")
	}
	t.commits = append(t.commits, clone(commit))
	return nil
}

func (r *Repository) RunPaperTx(_ context.Context, ws model.WorkspaceID, paper model.PaperID, fn func(tx interfaces.PaperTx) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, err := r.getPaperData(ws, paper)
	if err != nil {
		return err
	}
	tx := &paperTx{
		data:    d,
		paper:   clone(d.paper),
		puts:    map[model.BlockID]*model.Block{},
		deletes: map[model.BlockID]struct{}{},
	}
	if err := fn(tx); err != nil {
		return err
	}

	// A block version is written once; rewriting one would corrupt the
	// history, which is what a real backend's create-only write prevents.
	for id, b := range tx.puts {
		for _, v := range d.versions[id] {
			if v.Version == b.Version {
				return goerr.Wrap(model.ErrAlreadyExists, "block version already exists", goerr.V("block_id", id), goerr.V("version", b.Version))
			}
		}
	}

	key := paperKey{ws, paper}
	for id := range tx.deletes {
		if _, ok := d.blocks[id]; !ok {
			continue
		}
		delete(d.blocks, id)
		delete(d.search, id)
		r.emit(key, &model.Event{Type: model.EventBlockDeleted, BlockID: id})
	}
	for id, b := range tx.puts {
		d.blocks[id] = b
		d.versions[id] = append(d.versions[id], clone(b))
		d.indexBlock(b)
		r.emit(key, &model.Event{Type: model.EventBlockPut, Block: clone(b)})
	}
	d.commits = append(d.commits, tx.commits...)
	if tx.paperW {
		d.paper = tx.paper
		d.indexTitle()
		r.emit(key, &model.Event{Type: model.EventPaperUpdated, Paper: clone(tx.paper)})
	}
	return nil
}

// --- locks -------------------------------------------------------------------

func (r *Repository) AcquireLock(_ context.Context, lock *model.BlockLock, now time.Time) (*model.BlockLock, error) {
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, err := r.getPaperData(lock.WorkspaceID, lock.PaperID)
	if err != nil {
		return nil, err
	}
	if cur, ok := d.locks[lock.BlockID]; ok && cur.Active(now) && cur.HolderKey != lock.HolderKey {
		return clone(cur), goerr.Wrap(model.ErrBlockLocked, "block is locked", goerr.V("block_id", lock.BlockID))
	}
	d.locks[lock.BlockID] = clone(lock)
	r.emit(paperKey{lock.WorkspaceID, lock.PaperID}, &model.Event{Type: model.EventLock, Lock: clone(lock)})
	return clone(lock), nil
}

func (r *Repository) ReleaseLock(_ context.Context, ws model.WorkspaceID, paper model.PaperID, block model.BlockID, holderKey string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, err := r.getPaperData(ws, paper)
	if err != nil {
		return false, err
	}
	cur, ok := d.locks[block]
	if !ok || cur.HolderKey != holderKey {
		return false, nil
	}
	delete(d.locks, block)
	r.emit(paperKey{ws, paper}, &model.Event{Type: model.EventUnlock, BlockID: block, HolderKey: holderKey})
	return true, nil
}

func (r *Repository) ListLocks(_ context.Context, ws model.WorkspaceID, paper model.PaperID, now time.Time) ([]*model.BlockLock, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, err := r.getPaperData(ws, paper)
	if err != nil {
		return nil, err
	}
	var out []*model.BlockLock
	for id, l := range d.locks {
		if !l.Active(now) {
			delete(d.locks, id)
			continue
		}
		out = append(out, clone(l))
	}
	slices.SortFunc(out, func(a, b *model.BlockLock) int { return cmp.Compare(a.BlockID, b.BlockID) })
	return out, nil
}

// --- watch -------------------------------------------------------------------

// emit delivers an event to the paper's watchers. Callers hold r.mu. A
// watcher whose buffer is full is dropped (its channel closed) so it
// reloads instead of silently missing a change.
func (r *Repository) emit(key paperKey, ev *model.Event) {
	ev.WorkspaceID, ev.PaperID = key.ws, key.paper
	for id, ch := range r.watchers[key] {
		select {
		case ch <- clone(ev):
		default:
			close(ch)
			delete(r.watchers[key], id)
		}
	}
	if ev.Type == model.EventPaperDeleted {
		for id, ch := range r.watchers[key] {
			close(ch)
			delete(r.watchers[key], id)
		}
	}
}

func (r *Repository) WatchPaper(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) (<-chan *model.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.getPaperData(ws, paper); err != nil {
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
