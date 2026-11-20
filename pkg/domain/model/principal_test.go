package model_test

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func bind(kind model.SubjectKind, id string, role model.Role) model.RoleBinding {
	return model.RoleBinding{Subject: model.Subject{Kind: kind, ID: id}, Role: role}
}

func TestPrincipalWorkspaceRole(t *testing.T) {
	alice := &model.User{ID: "alice", Email: "alice@example.com"}
	ws := &model.Workspace{ID: "ws1", Bindings: []model.RoleBinding{
		bind(model.SubjectDomain, "example.com", model.RoleViewer),
		bind(model.SubjectGroup, "eng", model.RoleEditor),
		bind(model.SubjectUser, "owner", model.RoleOwner),
	}}

	t.Run("domain binding", func(t *testing.T) {
		p := &model.Principal{User: alice}
		gt.Equal(t, p.WorkspaceRole(ws), model.RoleViewer)
	})
	t.Run("group raises role", func(t *testing.T) {
		p := &model.Principal{User: alice, Groups: []model.GroupID{"eng"}}
		gt.Equal(t, p.WorkspaceRole(ws), model.RoleEditor)
	})
	t.Run("outsider has no role", func(t *testing.T) {
		p := &model.Principal{User: &model.User{ID: "bob", Email: "bob@other.example"}}
		gt.Equal(t, p.WorkspaceRole(ws), model.RoleNone)
	})
	t.Run("domain match is case-insensitive on the email side", func(t *testing.T) {
		p := &model.Principal{User: &model.User{ID: "carol", Email: "carol@EXAMPLE.com"}}
		gt.Equal(t, p.WorkspaceRole(ws), model.RoleViewer)
	})
	t.Run("system admin owns", func(t *testing.T) {
		p := &model.Principal{User: &model.User{ID: "root", Email: "root@other.example"}, SystemAdmin: true}
		gt.Equal(t, p.WorkspaceRole(ws), model.RoleOwner)
	})
	t.Run("read token caps to viewer", func(t *testing.T) {
		p := &model.Principal{User: &model.User{ID: "owner", Email: "o@x.example"}, Token: &model.APIToken{Scope: model.ScopeRead}}
		gt.Equal(t, p.WorkspaceRole(ws), model.RoleViewer)
	})
	t.Run("write token caps to editor", func(t *testing.T) {
		p := &model.Principal{User: &model.User{ID: "owner", Email: "o@x.example"}, Token: &model.APIToken{Scope: model.ScopeWrite}}
		gt.Equal(t, p.WorkspaceRole(ws), model.RoleEditor)
	})
	t.Run("token limited to other workspace", func(t *testing.T) {
		p := &model.Principal{User: &model.User{ID: "owner", Email: "o@x.example"}, Token: &model.APIToken{Scope: model.ScopeWrite, Workspaces: []model.WorkspaceID{"ws2"}}}
		gt.Equal(t, p.WorkspaceRole(ws), model.RoleNone)
	})
}

func TestPrincipalPaperRole(t *testing.T) {
	ws := &model.Workspace{ID: "ws1", Bindings: []model.RoleBinding{
		bind(model.SubjectUser, "owner", model.RoleOwner),
		bind(model.SubjectUser, "editor", model.RoleEditor),
		bind(model.SubjectUser, "viewer", model.RoleViewer),
	}}
	user := func(id string) *model.User { return &model.User{ID: model.UserID(id), Email: id + "@example.com"} }
	paper := func(restricted bool, creator string, bs ...model.RoleBinding) *model.Paper {
		return &model.Paper{
			ID: "p1", WorkspaceID: "ws1",
			CreatedBy: model.Actor{Kind: model.ActorHuman, UserID: model.UserID(creator)},
			Access:    model.PaperAccess{Restricted: restricted, Bindings: bs},
		}
	}

	cases := []struct {
		name  string
		p     *model.Principal
		paper *model.Paper
		want  model.Role
	}{
		{"inherits workspace role", &model.Principal{User: user("viewer")}, paper(false, "owner"), model.RoleViewer},
		{"paper binding raises", &model.Principal{User: user("viewer")}, paper(false, "owner", bind(model.SubjectUser, "viewer", model.RoleEditor)), model.RoleEditor},
		{"paper binding cannot lower in unrestricted", &model.Principal{User: user("editor")}, paper(false, "owner", bind(model.SubjectUser, "editor", model.RoleViewer)), model.RoleEditor},
		{"restricted hides from workspace editor", &model.Principal{User: user("editor")}, paper(true, "owner"), model.RoleNone},
		{"restricted with explicit binding", &model.Principal{User: user("viewer")}, paper(true, "owner", bind(model.SubjectUser, "viewer", model.RoleEditor)), model.RoleEditor},
		{"restricted grants outsider", &model.Principal{User: user("guest")}, paper(true, "owner", bind(model.SubjectUser, "guest", model.RoleViewer)), model.RoleViewer},
		{"workspace owner sees restricted", &model.Principal{User: user("owner")}, paper(true, "editor"), model.RoleOwner},
		{"creator owns own paper", &model.Principal{User: user("editor")}, paper(true, "editor"), model.RoleOwner},
		{"creator demoted to viewer loses ownership", &model.Principal{User: user("viewer")}, paper(false, "viewer"), model.RoleViewer},
		{"agent token caps creator", &model.Principal{User: user("editor"), Token: &model.APIToken{Scope: model.ScopeWrite, Kind: model.TokenAgent}}, paper(false, "editor"), model.RoleEditor},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gt.Equal(t, c.p.PaperRole(ws, c.paper), c.want)
		})
	}
}

func TestPrincipalActor(t *testing.T) {
	u := &model.User{ID: "u1", Email: "u1@example.com", Name: "Alice"}

	human := (&model.Principal{User: u}).Actor()
	gt.Equal(t, human.Kind, model.ActorHuman)
	gt.Equal(t, human.Name, "Alice")

	agent := (&model.Principal{User: u, Token: &model.APIToken{ID: "t1", Kind: model.TokenAgent, AgentName: "Claude"}}).Actor()
	gt.Equal(t, agent.Kind, model.ActorAgent)
	gt.Equal(t, agent.AgentName, "Claude")
	gt.Equal(t, agent.TokenID, model.TokenID("t1"))
	gt.Equal(t, agent.UserID, model.UserID("u1"))

	viaMCP := (&model.Principal{User: u, AgentName: "MCP"}).Actor()
	gt.Equal(t, viaMCP.Kind, model.ActorAgent)
	gt.Equal(t, viaMCP.AgentName, "MCP")

	personal := (&model.Principal{User: &model.User{ID: "u2", Email: "u2@example.com"}, Token: &model.APIToken{ID: "t2", Kind: model.TokenPersonal}}).Actor()
	gt.Equal(t, personal.Kind, model.ActorHuman)
	gt.Equal(t, personal.Name, "u2@example.com")
}
