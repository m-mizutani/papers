package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/service/workspace"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	gt.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	gt.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

const policy = `package papers.authz

allow if input.user.domain == "example.com"
allow if input.user.email == "guest@other.com"
`

func setup(t *testing.T) string {
	dir := t.TempDir()
	write(t, dir, "security.toml", `
[workspace]
id = "security"
name = "Security Team"
description = "incident notes"
emoji = "🛡️"
language = "ja"

[authz]
policy = ["policies/security.rego"]
`)
	write(t, dir, "policies/security.rego", policy)
	write(t, dir, "open.toml", `
[workspace]
id = "open"
`)
	return dir
}

func TestLoadAndAllow(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1000, 0)
	dir := setup(t)
	global := write(t, t.TempDir(), "global.toml", `
[[group]]
name = "Teams"
workspaces = ["security"]
`)
	r := gt.R1(workspace.Load(ctx, dir, global, workspace.WithClock(func() time.Time { return now }))).NoError(t)

	list := r.List()
	gt.A(t, list).Length(2)
	gt.Equal(t, list[0].ID, model.WorkspaceID("open")) // file name order
	gt.Equal(t, list[0].Name, "open")                  // name defaults to the id
	sec, ok := r.Get("security")
	gt.True(t, ok)
	gt.Equal(t, sec.Description, "incident notes")
	gt.Equal(t, sec.Group, "Teams")
	gt.Equal(t, r.Groups(), []model.WorkspaceGroup{{Name: "Teams", Workspaces: []model.WorkspaceID{"security"}}})

	alice := &model.User{ID: model.NewUserID(), Email: "alice@example.com"}
	guest := &model.User{ID: model.NewUserID(), Email: "Guest@other.com"}
	mallory := &model.User{ID: model.NewUserID(), Email: "mallory@evil.com"}
	gt.True(t, gt.R1(r.Allow(ctx, alice, "security")).NoError(t))
	gt.True(t, gt.R1(r.Allow(ctx, guest, "security")).NoError(t))
	gt.False(t, gt.R1(r.Allow(ctx, mallory, "security")).NoError(t))
	// no rule: every signed-in user
	gt.True(t, gt.R1(r.Allow(ctx, mallory, "open")).NoError(t))
	// unknown workspace
	gt.False(t, gt.R1(r.Allow(ctx, alice, "nothing")).NoError(t))

	// decisions are cached for a minute: a changed email does not matter yet
	alice.Email = "alice@evil.com"
	gt.True(t, gt.R1(r.Allow(ctx, alice, "security")).NoError(t))
	now = now.Add(workspace.CacheTTL)
	gt.False(t, gt.R1(r.Allow(ctx, alice, "security")).NoError(t))
}

func TestAllowAnonymous(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	write(t, dir, "guests.toml", "[workspace]\nid = \"guests\"\n[authz]\npolicy = [\"g.rego\"]\n")
	write(t, dir, "g.rego", "package papers.authz\n\nallow if input.user.anonymous\n")
	write(t, dir, "team.toml", "[workspace]\nid = \"team\"\n[authz]\npolicy = [\"t.rego\"]\n")
	write(t, dir, "t.rego", "package papers.authz\n\nallow if input.user.domain == \"example.com\"\n")
	r := gt.R1(workspace.Load(ctx, dir, "")).NoError(t)

	anon := model.NewAnonymousUser(time.Now())
	alice := &model.User{ID: model.NewUserID(), Email: "alice@example.com"}
	// a rule on the anonymous flag admits anonymous users only
	gt.True(t, gt.R1(r.Allow(ctx, anon, "guests")).NoError(t))
	gt.False(t, gt.R1(r.Allow(ctx, alice, "guests")).NoError(t))
	// rules written with real domains keep anonymous users out
	gt.False(t, gt.R1(r.Allow(ctx, anon, "team")).NoError(t))
	gt.True(t, gt.R1(r.Allow(ctx, alice, "team")).NoError(t))
}

func TestLoadRejectsBadConfiguration(t *testing.T) {
	ctx := context.Background()
	cases := map[string]map[string]string{
		"bad id":           {"a.toml": "[workspace]\nid = \"Bad_ID\"\n"},
		"duplicated id":    {"a.toml": "[workspace]\nid = \"x\"\n", "b.toml": "[workspace]\nid = \"x\"\n"},
		"unknown key":      {"a.toml": "[workspace]\nid = \"x\"\nowner = \"me\"\n"},
		"emoji and color":  {"a.toml": "[workspace]\nid = \"x\"\nemoji = \"a\"\ncolor = \"red\"\n"},
		"missing policy":   {"a.toml": "[workspace]\nid = \"x\"\n[authz]\npolicy = [\"nope.rego\"]\n"},
		"bad rego":         {"a.toml": "[workspace]\nid = \"x\"\n[authz]\npolicy = [\"p.rego\"]\n", "p.rego": "package papers.authz\nallow if {"},
		"empty authz":      {"a.toml": "[workspace]\nid = \"x\"\n[authz]\n"},
		"sandbox not bool": {"a.toml": "[workspace]\nid = \"x\"\n[sandbox]\nexternal_resources = \"no\"\n"},
		"sandbox unknown":  {"a.toml": "[workspace]\nid = \"x\"\n[sandbox]\nnetwork = false\n"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for f, body := range files {
				write(t, dir, f, body)
			}
			_, err := workspace.Load(ctx, dir, "")
			gt.Error(t, err)
		})
	}

	t.Run("no workspace", func(t *testing.T) {
		_, err := workspace.Load(ctx, t.TempDir(), "")
		gt.Error(t, err)
	})
	t.Run("group with unknown workspace", func(t *testing.T) {
		g := write(t, t.TempDir(), "g.toml", "[[group]]\nname = \"x\"\nworkspaces = [\"nope\"]\n")
		_, err := workspace.Load(ctx, setup(t), g)
		gt.Error(t, err)
	})
}

func TestNonBooleanAllowIsAnError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	write(t, dir, "a.toml", "[workspace]\nid = \"x\"\n[authz]\npolicy = [\"p.rego\"]\n")
	write(t, dir, "p.rego", "package papers.authz\nallow := \"yes\"\n")
	r := gt.R1(workspace.Load(ctx, dir, "")).NoError(t)
	_, err := r.Allow(ctx, &model.User{ID: model.NewUserID(), Email: "a@b.c"}, "x")
	gt.Error(t, err)
}

func TestSandboxExternalResources(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	write(t, dir, "closed.toml", "[workspace]\nid = \"closed\"\n[sandbox]\nexternal_resources = false\n")
	write(t, dir, "open.toml", "[workspace]\nid = \"open\"\n")
	write(t, dir, "explicit.toml", "[workspace]\nid = \"explicit\"\n[sandbox]\nexternal_resources = true\n")
	r := gt.R1(workspace.Load(ctx, dir, "")).NoError(t)

	closed, ok := r.Get("closed")
	gt.True(t, ok)
	gt.False(t, closed.Sandbox.ExternalResources)
	open, ok := r.Get("open")
	gt.True(t, ok)
	gt.False(t, open.Sandbox.ExternalResources) // T13: default is false
	explicit, ok := r.Get("explicit")
	gt.True(t, ok)
	gt.True(t, explicit.Sandbox.ExternalResources)
}

func ids(r *workspace.Registry) []model.WorkspaceID {
	var out []model.WorkspaceID
	for _, w := range r.List() {
		out = append(out, w.ID)
	}
	return out
}

func TestLoadReadsSubdirectories(t *testing.T) {
	ctx := context.Background()

	t.Run("files two levels down are read", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "a.toml", "[workspace]\nid = \"a\"\n")
		write(t, dir, "x/y/b.toml", "[workspace]\nid = \"b\"\n")
		r := gt.R1(workspace.Load(ctx, dir, "")).NoError(t)
		gt.Equal(t, ids(r), []model.WorkspaceID{"a", "b"})
	})

	t.Run("a flat directory loads as before", func(t *testing.T) {
		r := gt.R1(workspace.Load(ctx, setup(t), "")).NoError(t)
		gt.Equal(t, ids(r), []model.WorkspaceID{"open", "security"})
	})

	t.Run("subdirectories without toml files configure nothing", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "policies/p.rego", policy)
		_, err := workspace.Load(ctx, dir, "")
		gt.Error(t, err)
	})

	t.Run("the id comes from the file, not the directory", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "team/ws.toml", "[workspace]\nid = \"alpha\"\n")
		r := gt.R1(workspace.Load(ctx, dir, "")).NoError(t)
		gt.Equal(t, ids(r), []model.WorkspaceID{"alpha"})
	})

	t.Run("policies resolve from the directory of their file", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "sec/sec.toml", "[workspace]\nid = \"sec\"\n[authz]\npolicy = [\"sec.rego\"]\n")
		write(t, dir, "sec/sec.rego", policy)
		r := gt.R1(workspace.Load(ctx, dir, "")).NoError(t)
		in := &model.User{ID: model.NewUserID(), Email: "alice@example.com"}
		out := &model.User{ID: model.NewUserID(), Email: "bob@other.com"}
		gt.True(t, gt.R1(r.Allow(ctx, in, "sec")).NoError(t))
		gt.False(t, gt.R1(r.Allow(ctx, out, "sec")).NoError(t))
	})

	t.Run("names starting with a dot are skipped", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "a.toml", "[workspace]\nid = \"a\"\n")
		write(t, dir, ".hidden/b.toml", "not toml")
		write(t, dir, ".c.toml", "not toml")
		r := gt.R1(workspace.Load(ctx, dir, "")).NoError(t)
		gt.Equal(t, ids(r), []model.WorkspaceID{"a"})
	})

	t.Run("an id defined in two directories names both files", func(t *testing.T) {
		dir := t.TempDir()
		x := write(t, dir, "a/x.toml", "[workspace]\nid = \"same\"\n")
		y := write(t, dir, "b/y.toml", "[workspace]\nid = \"same\"\n")
		_, err := workspace.Load(ctx, dir, "")
		gt.Error(t, err)
		gt.Equal(t, goerr.Values(err)["files"], any([]string{x, y}))
	})

	t.Run("workspaces are ordered by relative path", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "z.toml", "[workspace]\nid = \"z\"\n")
		write(t, dir, "a/m.toml", "[workspace]\nid = \"m\"\n")
		write(t, dir, "a-b.toml", "[workspace]\nid = \"ab\"\n")
		r := gt.R1(workspace.Load(ctx, dir, "")).NoError(t)
		gt.Equal(t, ids(r), []model.WorkspaceID{"ab", "m", "z"})
	})

	t.Run("the directory itself may be a symbolic link", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "real/a.toml", "[workspace]\nid = \"a\"\n")
		link := filepath.Join(dir, "link")
		gt.NoError(t, os.Symlink(filepath.Join(dir, "real"), link))
		r := gt.R1(workspace.Load(ctx, link, "")).NoError(t)
		gt.Equal(t, ids(r), []model.WorkspaceID{"a"})
	})
}
