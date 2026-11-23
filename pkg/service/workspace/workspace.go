// Package workspace loads the workspace configuration and evaluates who may
// use each workspace.
//
// The configuration is a directory with one TOML file per workspace:
//
//	[workspace]
//	id = "security"
//	name = "Security Team"
//	description = "..."
//	emoji = "🛡️"
//	language = "ja"
//
//	[authz]
//	policy = ["policies/security.rego"]
//
// Policy paths are relative to the TOML file. A policy is Rego in package
// papers.authz and defines the boolean allow from input.workspace.id,
// input.user.id, input.user.email, input.user.domain and
// input.user.anonymous. A workspace without [authz] is open to every
// signed-in user, anonymous users included.
//
// The optional global configuration file groups workspaces for display:
//
//	[[group]]
//	name = "Teams"
//	workspaces = ["security", "platform"]
//
// Everything is validated at load time; one bad file stops the server.
package workspace

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/m-mizutani/goerr/v2"
	"github.com/open-policy-agent/opa/v1/rego"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

// Query is the Rego rule a policy must define.
const Query = "data.papers.authz.allow"

// CacheTTL is how long one decision is reused.
const CacheTTL = 60 * time.Second

type fileConfig struct {
	Workspace struct {
		ID          string `toml:"id"`
		Name        string `toml:"name"`
		Description string `toml:"description"`
		Emoji       string `toml:"emoji"`
		Color       string `toml:"color"`
		Language    string `toml:"language"`
	} `toml:"workspace"`
	Authz *struct {
		Policy []string `toml:"policy"`
	} `toml:"authz"`
}

type globalConfig struct {
	Group []struct {
		Name       string   `toml:"name"`
		Workspaces []string `toml:"workspaces"`
	} `toml:"group"`
}

type entry struct {
	ws     *model.Workspace
	policy *rego.PreparedEvalQuery // nil: open to every signed-in user
}

type decision struct {
	allow bool
	at    time.Time
}

type cacheKey struct {
	user model.UserID
	ws   model.WorkspaceID
}

// Registry is the loaded configuration.
type Registry struct {
	order   []model.WorkspaceID
	entries map[model.WorkspaceID]*entry
	groups  []model.WorkspaceGroup

	mu    sync.Mutex
	cache map[cacheKey]decision
	now   func() time.Time
}

var _ interfaces.Workspaces = (*Registry)(nil)

type Option func(*Registry)

// WithClock replaces the clock of the decision cache (tests).
func WithClock(now func() time.Time) Option { return func(r *Registry) { r.now = now } }

// Load reads every *.toml file of dir and, if globalPath is not empty, the
// global configuration.
func Load(ctx context.Context, dir, globalPath string, opts ...Option) (*Registry, error) {
	if dir == "" {
		return nil, goerr.New("workspace configuration directory is required")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.toml"))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list workspace configuration", goerr.V("dir", dir))
	}
	if len(files) == 0 {
		return nil, goerr.New("no workspace is configured", goerr.V("dir", dir))
	}
	slices.Sort(files)
	r := &Registry{entries: map[model.WorkspaceID]*entry{}, cache: map[cacheKey]decision{}, now: time.Now}
	for _, opt := range opts {
		opt(r)
	}
	for _, f := range files {
		e, err := loadFile(ctx, f)
		if err != nil {
			return nil, err
		}
		if _, dup := r.entries[e.ws.ID]; dup {
			return nil, goerr.New("workspace id is defined twice", goerr.V("id", e.ws.ID), goerr.V("file", f))
		}
		r.entries[e.ws.ID] = e
		r.order = append(r.order, e.ws.ID)
	}
	if globalPath != "" {
		if err := r.loadGlobal(globalPath); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func loadFile(ctx context.Context, path string) (*entry, error) {
	var fc fileConfig
	md, err := toml.DecodeFile(path, &fc)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to parse workspace configuration", goerr.V("file", path))
	}
	if undec := md.Undecoded(); len(undec) > 0 {
		keys := make([]string, len(undec))
		for i, k := range undec {
			keys[i] = k.String()
		}
		return nil, goerr.New("unknown keys in workspace configuration", goerr.V("file", path), goerr.V("keys", keys))
	}
	w := fc.Workspace
	ws := &model.Workspace{
		ID: model.WorkspaceID(w.ID), Name: strings.TrimSpace(w.Name), Description: strings.TrimSpace(w.Description),
		Emoji: w.Emoji, Color: w.Color, Language: w.Language,
	}
	if ws.Name == "" {
		ws.Name = string(ws.ID)
	}
	if err := ws.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid workspace configuration", goerr.V("file", path))
	}
	e := &entry{ws: ws}
	if fc.Authz != nil {
		if len(fc.Authz.Policy) == 0 {
			return nil, goerr.New("[authz] needs at least one policy file", goerr.V("file", path))
		}
		var mods []func(*rego.Rego)
		mods = append(mods, rego.Query(Query))
		for _, p := range fc.Authz.Policy {
			full := p
			if !filepath.IsAbs(full) {
				full = filepath.Join(filepath.Dir(path), p)
			}
			src, err := os.ReadFile(full) // #nosec G304 -- path comes from the deploy-time configuration
			if err != nil {
				return nil, goerr.Wrap(err, "failed to read policy", goerr.V("file", path), goerr.V("policy", full))
			}
			mods = append(mods, rego.Module(full, string(src)))
		}
		q, err := rego.New(mods...).PrepareForEval(ctx)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to compile policy", goerr.V("file", path))
		}
		e.policy = &q
	}
	return e, nil
}

func (r *Registry) loadGlobal(path string) error {
	var gc globalConfig
	md, err := toml.DecodeFile(path, &gc)
	if err != nil {
		return goerr.Wrap(err, "failed to parse global configuration", goerr.V("file", path))
	}
	if undec := md.Undecoded(); len(undec) > 0 {
		return goerr.New("unknown keys in global configuration", goerr.V("file", path), goerr.V("key", undec[0].String()))
	}
	grouped := map[model.WorkspaceID]string{}
	for _, g := range gc.Group {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			return goerr.New("group name is required", goerr.V("file", path))
		}
		group := model.WorkspaceGroup{Name: name}
		for _, id := range g.Workspaces {
			ws := model.WorkspaceID(id)
			e, ok := r.entries[ws]
			if !ok {
				return goerr.New("group refers to an unknown workspace", goerr.V("group", name), goerr.V("workspace", id))
			}
			if prev, dup := grouped[ws]; dup {
				return goerr.New("workspace is in two groups", goerr.V("workspace", id), goerr.V("groups", []string{prev, name}))
			}
			grouped[ws] = name
			e.ws.Group = name
			group.Workspaces = append(group.Workspaces, ws)
		}
		r.groups = append(r.groups, group)
	}
	return nil
}

// LogOpen logs the workspaces that have no access rule.
func (r *Registry) LogOpen(logger *slog.Logger) {
	for _, id := range r.order {
		if r.entries[id].policy == nil {
			logger.Warn("workspace has no [authz] rule; every signed-in user can use it", slog.String("workspace", string(id)))
		}
	}
}

func (r *Registry) List() []*model.Workspace {
	out := make([]*model.Workspace, len(r.order))
	for i, id := range r.order {
		w := *r.entries[id].ws
		out[i] = &w
	}
	return out
}

func (r *Registry) Get(id model.WorkspaceID) (*model.Workspace, bool) {
	e, ok := r.entries[id]
	if !ok {
		return nil, false
	}
	w := *e.ws
	return &w, true
}

func (r *Registry) Groups() []model.WorkspaceGroup {
	out := make([]model.WorkspaceGroup, len(r.groups))
	for i, g := range r.groups {
		out[i] = model.WorkspaceGroup{Name: g.Name, Workspaces: slices.Clone(g.Workspaces)}
	}
	return out
}

func (r *Registry) Allow(ctx context.Context, user *model.User, ws model.WorkspaceID) (bool, error) {
	e, ok := r.entries[ws]
	if !ok || user == nil {
		return false, nil
	}
	if e.policy == nil {
		return true, nil
	}
	key := cacheKey{user.ID, ws}
	now := r.now()
	r.mu.Lock()
	if d, ok := r.cache[key]; ok && now.Sub(d.at) < CacheTTL {
		r.mu.Unlock()
		return d.allow, nil
	}
	r.mu.Unlock()

	input := map[string]any{
		"workspace": map[string]any{"id": string(ws)},
		"user":      map[string]any{"id": string(user.ID), "email": strings.ToLower(user.Email), "domain": model.EmailDomain(user.Email), "anonymous": user.Anonymous},
	}
	rs, err := e.policy.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return false, goerr.Wrap(err, "failed to evaluate workspace policy", goerr.V("workspace", ws))
	}
	allow := false
	if len(rs) == 1 && len(rs[0].Expressions) == 1 {
		b, isBool := rs[0].Expressions[0].Value.(bool)
		if !isBool {
			return false, goerr.New("workspace policy allow is not a boolean", goerr.V("workspace", ws))
		}
		allow = b
	}
	r.mu.Lock()
	r.cache[key] = decision{allow: allow, at: now}
	if len(r.cache) > 100_000 {
		// A crude bound: drop everything rather than track ages.
		r.cache = map[cacheKey]decision{key: {allow: allow, at: now}}
	}
	r.mu.Unlock()
	return allow, nil
}

// Static builds a registry from definitions without policies (tests and the
// demo). Every signed-in user may use every workspace.
func Static(wss ...*model.Workspace) *Registry {
	r := &Registry{entries: map[model.WorkspaceID]*entry{}, cache: map[cacheKey]decision{}, now: time.Now}
	for _, w := range wss {
		c := *w
		if c.Name == "" {
			c.Name = string(c.ID)
		}
		r.entries[c.ID] = &entry{ws: &c}
		r.order = append(r.order, c.ID)
	}
	return r
}
