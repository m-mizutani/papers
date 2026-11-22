package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestLoadRejectsBadConfiguration(t *testing.T) {
	ctx := context.Background()
	cases := map[string]map[string]string{
		"bad id":          {"a.toml": "[workspace]\nid = \"Bad_ID\"\n"},
		"duplicated id":   {"a.toml": "[workspace]\nid = \"x\"\n", "b.toml": "[workspace]\nid = \"x\"\n"},
		"unknown key":     {"a.toml": "[workspace]\nid = \"x\"\nowner = \"me\"\n"},
		"emoji and color": {"a.toml": "[workspace]\nid = \"x\"\nemoji = \"a\"\ncolor = \"red\"\n"},
		"missing policy":  {"a.toml": "[workspace]\nid = \"x\"\n[authz]\npolicy = [\"nope.rego\"]\n"},
		"bad rego":        {"a.toml": "[workspace]\nid = \"x\"\n[authz]\npolicy = [\"p.rego\"]\n", "p.rego": "package papers.authz\nallow if {"},
		"empty authz":     {"a.toml": "[workspace]\nid = \"x\"\n[authz]\n"},
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
