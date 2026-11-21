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
	paper    *model.Paper
	blocks   map[model.BlockID]*model.Block
	versions map[model.BlockID][]*model.Block
	commits  []*model.Commit
	// search holds the search entries of the paper keyed by block ID; the
	// title entry is under the empty block ID.
	search map[model.BlockID]*model.SearchEntry
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
	// watchers receive change events of a paper (this process only).
	watchers    map[paperKey]map[int]chan *model.Event
	nextWatcher int
	eventBuffer int
}

var _ interfaces.Repository = (*Repository)(nil)

// Option configures the memory repository.
type Option func(*Repository)

// WithEventBuffer sets how many events a watcher may lag behind before its
// watch is closed (default 256).
func WithEventBuffer(n int) Option { return func(r *Repository) { r.eventBuffer = n } }

func New(opts ...Option) *Repository {
	r := &Repository{
		users:       map[model.UserID]*model.User{},
		identities:  map[identityKey]*model.Identity{},
		sessions:    map[model.SessionID]*model.Session{},
		tokens:      map[model.TokenID]*model.APIToken{},
		groups:      map[model.GroupID]*model.Group{},
		workspaces:  map[model.WorkspaceID]*model.Workspace{},
		papers:      map[paperKey]*paperData{},
		watchers:    map[paperKey]map[int]chan *model.Event{},
		eventBuffer: 256,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
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
