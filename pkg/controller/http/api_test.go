package http_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

type wsResp struct {
	ID       model.WorkspaceID   `json:"id"`
	Name     string              `json:"name"`
	Role     model.Role          `json:"role"`
	Bindings []model.RoleBinding `json:"bindings"`
}

type paperViewResp struct {
	Paper     model.Paper       `json:"paper"`
	Blocks    []model.Block     `json:"blocks"`
	Locks     []model.BlockLock `json:"locks"`
	Role      model.Role        `json:"role"`
	LockTTLMs int64             `json:"lock_ttl_ms"`
}

type commitResp struct {
	Commit  *model.Commit   `json:"commit"`
	Paper   model.Paper     `json:"paper"`
	Blocks  []model.Block   `json:"blocks"`
	Deleted []model.BlockID `json:"deleted"`
}

func setupPaper(t *testing.T, ts *testServer) (*client, *client, model.WorkspaceID, model.PaperID) {
	t.Helper()
	alice := ts.login("alice@example.com")
	bob := ts.login("bob@example.com")

	var me struct {
		User model.User `json:"user"`
	}
	bob.expect("GET", "/api/auth/me", nil, http.StatusOK, &me)

	var ws wsResp
	alice.expect("POST", "/api/v1/workspaces", map[string]string{"name": "Team", "emoji": "📚"}, http.StatusCreated, &ws)
	gt.Equal(t, ws.Role, model.RoleOwner)
	bindings := append(ws.Bindings, model.RoleBinding{Subject: model.Subject{Kind: model.SubjectUser, ID: string(me.User.ID)}, Role: model.RoleEditor})
	alice.expect("PATCH", "/api/v1/workspaces/"+string(ws.ID), map[string]any{"bindings": bindings}, http.StatusOK, nil)

	var view paperViewResp
	alice.expect("POST", "/api/v1/workspaces/"+string(ws.ID)+"/papers", map[string]any{"title": "Runbook", "markdown": "# Steps\n\n1. check\n2. fix"}, http.StatusCreated, &view)
	gt.Equal(t, view.Paper.Title, "Runbook")
	gt.A(t, view.Blocks).Length(3)
	gt.Equal(t, view.LockTTLMs, int64(30000))
	return alice, bob, ws.ID, view.Paper.ID
}

func TestPaperAPI(t *testing.T) {
	ts := newTestServer(t, false)
	alice, bob, ws, paper := setupPaper(t, ts)
	base := "/api/v1/workspaces/" + string(ws) + "/papers/" + string(paper)

	var list struct {
		Papers []struct {
			ID   model.PaperID `json:"id"`
			Role model.Role    `json:"role"`
		} `json:"papers"`
	}
	bob.expect("GET", "/api/v1/workspaces/"+string(ws)+"/papers", nil, http.StatusOK, &list)
	gt.A(t, list.Papers).Length(1)
	gt.Equal(t, list.Papers[0].Role, model.RoleEditor)

	var view paperViewResp
	bob.expect("GET", base, nil, http.StatusOK, &view)
	first := view.Blocks[0]

	// commit an update and an insert
	var cr commitResp
	bob.expect("POST", base+"/commits", map[string]any{
		"message": "edit",
		"ops": []map[string]any{
			{"op": "update", "block_id": first.ID, "content": "Procedure", "base_version": first.Version},
			{"op": "insert", "type": "callout", "content": "Be careful", "props": map[string]any{"icon": "⚠️"}, "after": first.ID},
		},
	}, http.StatusOK, &cr)
	gt.A(t, cr.Commit.Changes).Length(2)
	gt.A(t, cr.Blocks).Length(2)
	gt.Equal(t, cr.Blocks[0].Content, "Procedure")
	gt.Equal(t, cr.Blocks[0].Version, int64(2))
	gt.A(t, cr.Deleted).Length(0)

	// block history
	var versions struct {
		Versions []model.Block `json:"versions"`
	}
	bob.expect("GET", base+"/blocks/"+string(first.ID)+"/versions", nil, http.StatusOK, &versions)
	gt.A(t, versions.Versions).Length(2)
	gt.Equal(t, versions.Versions[1].Content, "Steps")
	bob.expect("GET", base+"/blocks/"+string(first.ID)+"/versions?limit=0", nil, http.StatusBadRequest, nil)

	// history
	var hist struct {
		Commits []model.Commit `json:"commits"`
	}
	alice.expect("GET", base+"/commits?limit=1", nil, http.StatusOK, &hist)
	gt.A(t, hist.Commits).Length(1)
	gt.Equal(t, hist.Commits[0].Actor.Name, "bob")
	alice.expect("GET", base+"/commits?before="+string(hist.Commits[0].ID), nil, http.StatusOK, &hist)
	gt.A(t, hist.Commits).Length(1)
	gt.Equal(t, hist.Commits[0].Message, "Initial content")
	alice.expect("GET", base+"/commits?limit=x", nil, http.StatusBadRequest, nil)

	// markdown export
	resp := alice.do("GET", base+"/markdown", nil)
	gt.Equal(t, resp.StatusCode, http.StatusOK)
	md := string(gt.R1(io.ReadAll(resp.Body)).NoError(t))
	gt.True(t, strings.HasPrefix(md, "# Runbook\n\n## Procedure\n\n> [!NOTE]\n> Be careful"))

	// search
	var hits struct {
		Hits []struct {
			Snippet string `json:"snippet"`
		} `json:"hits"`
	}
	bob.expect("GET", "/api/v1/workspaces/"+string(ws)+"/search?q=careful", nil, http.StatusOK, &hits)
	gt.A(t, hits.Hits).Length(1)
	bob.expect("GET", "/api/v1/workspaces/"+string(ws)+"/search?q=", nil, http.StatusBadRequest, nil)

	// invalid op and unknown fields
	bob.expect("POST", base+"/commits", map[string]any{"ops": []map[string]any{{"op": "explode"}}}, http.StatusBadRequest, nil)
	bob.expect("POST", base+"/commits", map[string]any{"ops": []any{}, "bogus": 1}, http.StatusBadRequest, nil)

	// access: restrict the paper; bob (workspace editor) loses it
	alice.expect("PUT", base+"/access", map[string]any{"restricted": true, "bindings": []any{}}, http.StatusOK, nil)
	bob.expect("GET", base, nil, http.StatusNotFound, nil)
	bob.expect("DELETE", base, nil, http.StatusNotFound, nil)

	alice.expect("DELETE", base, nil, http.StatusNoContent, nil)
	alice.expect("GET", base, nil, http.StatusNotFound, nil)
}

func TestLockAPIAndConflict(t *testing.T) {
	ts := newTestServer(t, false)
	alice, bob, ws, paper := setupPaper(t, ts)
	base := "/api/v1/workspaces/" + string(ws) + "/papers/" + string(paper)
	var view paperViewResp
	alice.expect("GET", base, nil, http.StatusOK, &view)
	block := view.Blocks[1]

	var lock model.BlockLock
	alice.expect("PUT", base+"/blocks/"+string(block.ID)+"/lock", map[string]string{"connection_id": "tab-a"}, http.StatusOK, &lock)
	gt.Equal(t, lock.HolderKey, "tab-a")

	var conflict struct {
		Error struct {
			Code string           `json:"code"`
			Lock *model.BlockLock `json:"lock"`
		} `json:"error"`
	}
	bob.expect("PUT", base+"/blocks/"+string(block.ID)+"/lock", map[string]string{"connection_id": "tab-b"}, http.StatusConflict, &conflict)
	gt.Equal(t, conflict.Error.Code, "locked")
	gt.Equal(t, conflict.Error.Lock.HolderKey, "tab-a")

	bob.expect("POST", base+"/commits", map[string]any{"connection_id": "tab-b", "ops": []map[string]any{{"op": "update", "block_id": block.ID, "content": "x"}}}, http.StatusConflict, nil)
	bob.expect("POST", base+"/commits", map[string]any{"connection_id": "tab-b", "force": true, "ops": []map[string]any{{"op": "update", "block_id": block.ID, "content": "x"}}}, http.StatusOK, nil)

	alice.expect("DELETE", base+"/blocks/"+string(block.ID)+"/lock", map[string]string{"connection_id": "tab-a"}, http.StatusNoContent, nil)
	bob.expect("PUT", base+"/blocks/"+string(block.ID)+"/lock", map[string]string{"connection_id": "tab-b"}, http.StatusOK, nil)
}

func TestTokenAPI(t *testing.T) {
	ts := newTestServer(t, false)
	alice, _, ws, paper := setupPaper(t, ts)
	base := "/api/v1/workspaces/" + string(ws) + "/papers/" + string(paper)

	agent := ts.token(alice, model.TokenAgent, model.ScopeWrite)
	gt.True(t, strings.HasPrefix(agent.bearer, "pap_"))

	var cr commitResp
	agent.expect("POST", base+"/commits", map[string]any{"ops": []map[string]any{{"op": "insert_markdown", "markdown": "```mermaid\ngraph TD; A-->B\n```"}}}, http.StatusOK, &cr)
	gt.Equal(t, cr.Commit.Actor.Kind, model.ActorAgent)

	// tokens cannot manage tokens or administer
	agent.expect("GET", "/api/v1/tokens", nil, http.StatusForbidden, nil)
	agent.expect("PUT", base+"/access", map[string]any{"restricted": true}, http.StatusForbidden, nil)

	reader := ts.token(alice, model.TokenPersonal, model.ScopeRead)
	reader.expect("GET", base, nil, http.StatusOK, nil)
	reader.expect("POST", base+"/commits", map[string]any{"ops": []map[string]any{{"op": "set_title", "title": "x"}}}, http.StatusForbidden, nil)

	var toks struct {
		Tokens []map[string]any `json:"tokens"`
	}
	alice.expect("GET", "/api/v1/tokens", nil, http.StatusOK, &toks)
	gt.A(t, toks.Tokens).Length(2)
	for _, tk := range toks.Tokens {
		_, leaked := tk["secret_hash"]
		gt.False(t, leaked)
	}
	id := toks.Tokens[0]["id"].(string)
	alice.expect("DELETE", "/api/v1/tokens/"+id, nil, http.StatusNoContent, nil)
}

func TestGroupsUsersAndMeta(t *testing.T) {
	ts := newTestServer(t, false)
	admin := ts.login("admin@example.com")
	alice := ts.login("alice@example.com")

	var users struct {
		Users []struct {
			ID    model.UserID `json:"id"`
			Email string       `json:"email"`
		} `json:"users"`
	}
	alice.expect("GET", "/api/v1/users", nil, http.StatusOK, &users)
	gt.A(t, users.Users).Length(2)

	alice.expect("POST", "/api/v1/groups", map[string]any{"name": "eng"}, http.StatusForbidden, nil)
	var g model.Group
	admin.expect("POST", "/api/v1/groups", map[string]any{"name": "eng", "members": []model.UserID{users.Users[0].ID}}, http.StatusCreated, &g)
	admin.expect("PUT", "/api/v1/groups/"+string(g.ID), map[string]any{"name": "platform"}, http.StatusOK, nil)

	var groups struct {
		Groups []model.Group `json:"groups"`
	}
	alice.expect("GET", "/api/v1/groups", nil, http.StatusOK, &groups)
	gt.A(t, groups.Groups).Length(1)
	gt.Equal(t, groups.Groups[0].Name, "platform")
	admin.expect("DELETE", "/api/v1/groups/"+string(g.ID), nil, http.StatusNoContent, nil)

	var types struct {
		BlockTypes []struct {
			Type          model.BlockType `json:"type"`
			HumanEditable bool            `json:"human_editable"`
		} `json:"block_types"`
	}
	alice.expect("GET", "/api/v1/block-types", nil, http.StatusOK, &types)
	gt.A(t, types.BlockTypes).Length(len(model.BlockTypes()))

	// workspace visibility
	var ws wsResp
	admin.expect("POST", "/api/v1/workspaces", map[string]string{"name": "Private"}, http.StatusCreated, &ws)
	alice.expect("GET", "/api/v1/workspaces/"+string(ws.ID), nil, http.StatusNotFound, nil)
	var wl struct {
		Workspaces []wsResp `json:"workspaces"`
	}
	alice.expect("GET", "/api/v1/workspaces", nil, http.StatusOK, &wl)
	gt.A(t, wl.Workspaces).Length(0)
}
