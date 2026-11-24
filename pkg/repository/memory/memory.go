// Package memory is the in-memory repository used in development and tests.
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

type userWorkspaceKey struct {
	user model.UserID
	ws   model.WorkspaceID
}

type paperData struct {
	paper       *model.Paper
	blocks      map[model.BlockID]*model.Block
	versions    map[int]*model.Version
	snapshots   map[model.SnapshotRef]*model.BlockSnapshot
	events      []*model.Event
	attachments map[model.AttachmentID]*model.Attachment
	search      map[model.BlockID]*model.SearchEntry
	values      map[model.PropertyID]*model.PropertyValue
}

func newPaperData(p *model.Paper) *paperData {
	return &paperData{
		paper:       p,
		blocks:      map[model.BlockID]*model.Block{},
		versions:    map[int]*model.Version{},
		snapshots:   map[model.SnapshotRef]*model.BlockSnapshot{},
		attachments: map[model.AttachmentID]*model.Attachment{},
		search:      map[model.BlockID]*model.SearchEntry{},
		values:      map[model.PropertyID]*model.PropertyValue{},
	}
}

// Repository keeps everything in maps guarded by one lock.
type Repository struct {
	txMu       sync.Mutex
	mu         sync.RWMutex
	users      map[model.UserID]*model.User
	identities map[identityKey]*model.Identity
	sessions   map[model.SessionID]*model.Session
	tokens     map[model.TokenID]*model.APIToken
	records    map[model.WorkspaceID]*model.WorkspaceRecord
	forwards   map[paperKey]*model.Forward
	papers     map[paperKey]*paperData
	favorites  map[userWorkspaceKey]map[model.PaperID]*model.Favorite
	recents    map[userWorkspaceKey]*model.Recent
	jobs       map[jobKey]*model.Job
	jobItems   map[jobKey]map[model.PaperID]*model.JobItem

	watchers    map[paperKey]map[int]chan *model.Event
	nextWatcher int
	eventBuffer int
}

var _ interfaces.Repository = (*Repository)(nil)

type Option func(*Repository)

// WithEventBuffer sets the size of each watcher's channel.
func WithEventBuffer(n int) Option { return func(r *Repository) { r.eventBuffer = n } }

func New(opts ...Option) *Repository {
	r := &Repository{
		users:       map[model.UserID]*model.User{},
		identities:  map[identityKey]*model.Identity{},
		sessions:    map[model.SessionID]*model.Session{},
		tokens:      map[model.TokenID]*model.APIToken{},
		records:     map[model.WorkspaceID]*model.WorkspaceRecord{},
		forwards:    map[paperKey]*model.Forward{},
		papers:      map[paperKey]*paperData{},
		favorites:   map[userWorkspaceKey]map[model.PaperID]*model.Favorite{},
		recents:     map[userWorkspaceKey]*model.Recent{},
		jobs:        map[jobKey]*model.Job{},
		jobItems:    map[jobKey]map[model.PaperID]*model.JobItem{},
		watchers:    map[paperKey]map[int]chan *model.Event{},
		eventBuffer: 256,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Repository) Close() error { return nil }

// clone deep-copies through JSON so callers never share memory with the
// store.
func clone[T any](v *T) *T {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return &out
}

func notFound(kind string, id any) error {
	return goerr.Wrap(model.ErrNotFound, kind+" not found", goerr.V("id", id))
}

// --- users, sessions, tokens

func (r *Repository) GetUser(_ context.Context, id model.UserID) (*model.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.users[id]
	if !ok {
		return nil, notFound("user", id)
	}
	return clone(u), nil
}

func (r *Repository) GetUsers(_ context.Context, ids []model.UserID) ([]*model.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*model.User
	for _, id := range ids {
		if u, ok := r.users[id]; ok {
			out = append(out, clone(u))
		}
	}
	return out, nil
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

// --- workspaces

func (r *Repository) GetWorkspaceRecord(_ context.Context, ws model.WorkspaceID) (*model.WorkspaceRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.records[ws]
	if !ok {
		return nil, notFound("workspace record", ws)
	}
	return clone(rec), nil
}

func (r *Repository) EnsureHome(_ context.Context, ws model.WorkspaceID, home *model.Paper) (*model.WorkspaceRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec, ok := r.records[ws]; ok {
		return clone(rec), nil
	}
	if err := home.Validate(); err != nil {
		return nil, err
	}
	if home.WorkspaceID != ws {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "home paper belongs to another workspace")
	}
	d := newPaperData(clone(home))
	d.indexTitle()
	r.papers[paperKey{ws, home.ID}] = d
	rec := &model.WorkspaceRecord{ID: ws, HomePaperID: home.ID}
	r.records[ws] = rec
	return clone(rec), nil
}

func (r *Repository) ListWorkspaceRecords(_ context.Context) ([]*model.WorkspaceRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*model.WorkspaceRecord, 0, len(r.records))
	for _, rec := range r.records {
		out = append(out, clone(rec))
	}
	slices.SortFunc(out, func(a, b *model.WorkspaceRecord) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (r *Repository) GetForward(_ context.Context, ws model.WorkspaceID, paper model.PaperID) (*model.Forward, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.forwards[paperKey{ws, paper}]
	if !ok {
		return nil, notFound("forward", paper)
	}
	return clone(f), nil
}

// --- favorites and recents

func (r *Repository) ListFavorites(_ context.Context, user model.UserID, ws model.WorkspaceID) ([]*model.Favorite, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*model.Favorite
	for _, f := range r.favorites[userWorkspaceKey{user, ws}] {
		out = append(out, clone(f))
	}
	slices.SortFunc(out, func(a, b *model.Favorite) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.PaperID, b.PaperID))
	})
	return out, nil
}

func (r *Repository) PutFavorite(_ context.Context, f *model.Favorite) error {
	if err := f.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := userWorkspaceKey{f.UserID, f.WorkspaceID}
	if r.favorites[k] == nil {
		r.favorites[k] = map[model.PaperID]*model.Favorite{}
	}
	r.favorites[k][f.PaperID] = clone(f)
	return nil
}

func (r *Repository) DeleteFavorite(_ context.Context, user model.UserID, ws model.WorkspaceID, paper model.PaperID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.favorites[userWorkspaceKey{user, ws}], paper)
	return nil
}

func (r *Repository) GetRecent(_ context.Context, user model.UserID, ws model.WorkspaceID) (*model.Recent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if rec, ok := r.recents[userWorkspaceKey{user, ws}]; ok {
		return clone(rec), nil
	}
	return &model.Recent{UserID: user, WorkspaceID: ws, PaperIDs: []model.PaperID{}}, nil
}

func (r *Repository) GetRecents(_ context.Context, user model.UserID, wss []model.WorkspaceID) ([]*model.Recent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*model.Recent
	for _, ws := range wss {
		if rec, ok := r.recents[userWorkspaceKey{user, ws}]; ok {
			out = append(out, clone(rec))
		}
	}
	return out, nil
}

func (r *Repository) PutRecent(_ context.Context, rec *model.Recent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recents[userWorkspaceKey{rec.UserID, rec.WorkspaceID}] = clone(rec)
	return nil
}

// --- attachments

func (r *Repository) GetAttachment(_ context.Context, ws model.WorkspaceID, paper model.PaperID, id model.AttachmentID) (*model.Attachment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.papers[paperKey{ws, paper}]
	if !ok {
		return nil, notFound("attachment", id)
	}
	a, ok := d.attachments[id]
	if !ok {
		return nil, notFound("attachment", id)
	}
	return clone(a), nil
}

func (r *Repository) FindAttachmentByHash(_ context.Context, ws model.WorkspaceID, paper model.PaperID, sum string) (*model.Attachment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if d, ok := r.papers[paperKey{ws, paper}]; ok {
		for _, a := range d.attachments {
			if a.SHA256 == sum {
				return clone(a), nil
			}
		}
	}
	return nil, notFound("attachment", sum)
}

func (r *Repository) PutAttachment(_ context.Context, a *model.Attachment) error {
	if err := a.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.papers[paperKey{a.WorkspaceID, a.PaperID}]
	if !ok {
		return notFound("paper", a.PaperID)
	}
	d.attachments[a.ID] = clone(a)
	return nil
}
