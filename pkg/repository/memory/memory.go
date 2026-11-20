// Package memory is an in-process Repository for development and tests. It
// keeps everything in this process only, so it must not be used by a
// deployment that runs more than one instance.
package memory

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

type identityKey struct {
	provider model.IdentityProvider
	subject  string
}

type paperKey struct {
	ws    model.WorkspaceID
	paper model.PaperID
}

type paperData struct {
	paper   *model.Paper
	blocks  map[model.BlockID]*model.Block
	commits []*model.Commit
	locks   map[model.BlockID]*model.BlockLock
}

type Repository struct {
	mu         sync.RWMutex
	users      map[model.UserID]*model.User
	identities map[identityKey]*model.Identity
	sessions   map[model.SessionID]*model.Session
	tokens     map[model.TokenID]*model.APIToken
	groups     map[model.GroupID]*model.Group
	workspaces map[model.WorkspaceID]*model.Workspace
	papers     map[paperKey]*paperData
}

var _ interfaces.Repository = (*Repository)(nil)

func New() *Repository {
	return &Repository{
		users:      map[model.UserID]*model.User{},
		identities: map[identityKey]*model.Identity{},
		sessions:   map[model.SessionID]*model.Session{},
		tokens:     map[model.TokenID]*model.APIToken{},
		groups:     map[model.GroupID]*model.Group{},
		workspaces: map[model.WorkspaceID]*model.Workspace{},
		papers:     map[paperKey]*paperData{},
	}
}

func (r *Repository) Close() error { return nil }

// clone deep-copies through JSON, the same round trip a real backend's
// serialisation imposes, so a test cannot pass by sharing pointers with the
// repository.
func clone[T any](v *T) *T {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		panic("memory repository: failed to marshal: " + err.Error())
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		panic("memory repository: failed to unmarshal: " + err.Error())
	}
	return &out
}

func notFound(kind string, id any) error {
	return goerr.Wrap(model.ErrNotFound, kind+" not found", goerr.V("id", id))
}

// --- users ---------------------------------------------------------------

func (r *Repository) GetUser(_ context.Context, id model.UserID) (*model.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.users[id]
	if !ok {
		return nil, notFound("user", id)
	}
	return clone(u), nil
}

func (r *Repository) GetUserByEmail(_ context.Context, email string) (*model.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, u := range r.users {
		if strings.EqualFold(u.Email, email) {
			return clone(u), nil
		}
	}
	return nil, notFound("user", email)
}

func (r *Repository) ListUsers(_ context.Context) ([]*model.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*model.User, 0, len(r.users))
	for _, u := range r.users {
		out = append(out, clone(u))
	}
	slices.SortFunc(out, func(a, b *model.User) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (r *Repository) PutUser(_ context.Context, user *model.User) error {
	if err := user.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.users {
		if u.ID != user.ID && strings.EqualFold(u.Email, user.Email) {
			return goerr.Wrap(model.ErrAlreadyExists, "email is already used by another user", goerr.V("email", user.Email))
		}
	}
	r.users[user.ID] = clone(user)
	return nil
}

func (r *Repository) GetIdentity(_ context.Context, provider model.IdentityProvider, subject string) (*model.Identity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	i, ok := r.identities[identityKey{provider, subject}]
	if !ok {
		return nil, notFound("identity", string(provider)+"/"+subject)
	}
	return clone(i), nil
}

func (r *Repository) PutIdentity(_ context.Context, identity *model.Identity) error {
	if err := identity.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.identities[identityKey{identity.Provider, identity.Subject}] = clone(identity)
	return nil
}

// --- sessions --------------------------------------------------------------

func (r *Repository) GetSession(_ context.Context, id model.SessionID) (*model.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[id]
	if !ok {
		return nil, notFound("session", id)
	}
	return clone(s), nil
}

func (r *Repository) PutSession(_ context.Context, session *model.Session) error {
	if err := session.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[session.ID] = clone(session)
	return nil
}

func (r *Repository) DeleteSession(_ context.Context, id model.SessionID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
	return nil
}

// --- tokens ----------------------------------------------------------------

func (r *Repository) GetToken(_ context.Context, id model.TokenID) (*model.APIToken, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tokens[id]
	if !ok {
		return nil, notFound("token", id)
	}
	return clone(t), nil
}

func (r *Repository) ListTokens(_ context.Context, userID model.UserID) ([]*model.APIToken, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*model.APIToken
	for _, t := range r.tokens {
		if t.UserID == userID {
			out = append(out, clone(t))
		}
	}
	slices.SortFunc(out, func(a, b *model.APIToken) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (r *Repository) PutToken(_ context.Context, token *model.APIToken) error {
	if err := token.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[token.ID] = clone(token)
	return nil
}

func (r *Repository) DeleteToken(_ context.Context, id model.TokenID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tokens, id)
	return nil
}

// --- groups ----------------------------------------------------------------

func (r *Repository) GetGroup(_ context.Context, id model.GroupID) (*model.Group, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.groups[id]
	if !ok {
		return nil, notFound("group", id)
	}
	return clone(g), nil
}

func (r *Repository) ListGroups(_ context.Context) ([]*model.Group, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*model.Group, 0, len(r.groups))
	for _, g := range r.groups {
		out = append(out, clone(g))
	}
	slices.SortFunc(out, func(a, b *model.Group) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (r *Repository) ListGroupsByMember(_ context.Context, userID model.UserID) ([]*model.Group, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*model.Group
	for _, g := range r.groups {
		if g.HasMember(userID) {
			out = append(out, clone(g))
		}
	}
	slices.SortFunc(out, func(a, b *model.Group) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (r *Repository) PutGroup(_ context.Context, group *model.Group) error {
	if err := group.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.groups[group.ID] = clone(group)
	return nil
}

func (r *Repository) DeleteGroup(_ context.Context, id model.GroupID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.groups, id)
	return nil
}

// --- workspaces --------------------------------------------------------------

func (r *Repository) GetWorkspace(_ context.Context, id model.WorkspaceID) (*model.Workspace, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.workspaces[id]
	if !ok {
		return nil, notFound("workspace", id)
	}
	return clone(w), nil
}

func (r *Repository) ListWorkspaces(_ context.Context) ([]*model.Workspace, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*model.Workspace, 0, len(r.workspaces))
	for _, w := range r.workspaces {
		out = append(out, clone(w))
	}
	slices.SortFunc(out, func(a, b *model.Workspace) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (r *Repository) PutWorkspace(_ context.Context, ws *model.Workspace) error {
	if err := ws.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workspaces[ws.ID] = clone(ws)
	return nil
}

// --- papers ------------------------------------------------------------------

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
		paper:  clone(paper),
		blocks: map[model.BlockID]*model.Block{},
		locks:  map[model.BlockID]*model.BlockLock{},
	}
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
	return p, nil
}

func (r *Repository) DeletePaper(_ context.Context, ws model.WorkspaceID, id model.PaperID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.getPaperData(ws, id); err != nil {
		return err
	}
	delete(r.papers, paperKey{ws, id})
	return nil
}

func sortBlocks(bs []*model.Block) {
	slices.SortFunc(bs, func(a, b *model.Block) int {
		if c := cmp.Compare(a.ParentID, b.ParentID); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Order, b.Order); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
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
	sortBlocks(out)
	return out, nil
}

func (r *Repository) ListCommits(_ context.Context, ws model.WorkspaceID, paper model.PaperID, afterSeq int64, limit int) ([]*model.Commit, error) {
	if limit <= 0 {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "limit must be positive")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.getPaperData(ws, paper)
	if err != nil {
		return nil, err
	}
	var out []*model.Commit
	for _, c := range d.commits {
		if c.Seq > afterSeq {
			out = append(out, clone(c))
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// paperTx stages writes and applies them when the transaction function
// succeeds. The whole repository is locked for the duration, which trivially
// serialises transactions.
type paperTx struct {
	data    *paperData
	paper   *model.Paper
	puts    map[model.BlockID]*model.Block
	deletes map[model.BlockID]struct{}
	commits []*model.Commit
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
	seen := map[model.BlockID]struct{}{}
	var out []*model.Block
	for id, b := range t.puts {
		seen[id] = struct{}{}
		if b.ParentID == parent {
			out = append(out, clone(b))
		}
	}
	for id, b := range t.data.blocks {
		if _, ok := seen[id]; ok {
			continue
		}
		if _, ok := t.deletes[id]; ok {
			continue
		}
		if b.ParentID == parent {
			out = append(out, clone(b))
		}
	}
	sortBlocks(out)
	return out, nil
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

	// Commits must extend the history contiguously, which is what a real
	// backend's unique (paper, seq) key enforces.
	last := int64(0)
	if n := len(d.commits); n > 0 {
		last = d.commits[n-1].Seq
	}
	for _, c := range tx.commits {
		if c.Seq != last+1 {
			return goerr.Wrap(model.ErrInvalidArgument, "commit seq is not contiguous", goerr.V("seq", c.Seq), goerr.V("last", last))
		}
		last = c.Seq
	}

	for id := range tx.deletes {
		delete(d.blocks, id)
	}
	for id, b := range tx.puts {
		d.blocks[id] = b
	}
	d.commits = append(d.commits, tx.commits...)
	d.paper = tx.paper
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
