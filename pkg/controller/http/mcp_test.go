package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/m-mizutani/gt"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b *bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

func callTool[T any](t *testing.T, s *mcp.ClientSession, name string, args any) (T, bool) {
	t.Helper()
	var out T
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	gt.NoError(t, err)
	if res.IsError {
		return out, false
	}
	data, err := json.Marshal(res.StructuredContent)
	gt.NoError(t, err)
	gt.NoError(t, json.Unmarshal(data, &out))
	return out, true
}

func TestMCP(t *testing.T) {
	ts := newTestServer(t, false)
	alice, _, ws, paper := setupPaper(t, ts)
	agent := ts.token(alice, model.TokenAgent, model.ScopeWrite)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   ts.srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: &bearerTransport{token: agent.bearer, base: http.DefaultTransport}},
	}
	session, err := client.Connect(context.Background(), transport, nil)
	gt.NoError(t, err)
	defer func() { _ = session.Close() }()

	tools, err := session.ListTools(context.Background(), nil)
	gt.NoError(t, err)
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, n := range []string{"papers_guide", "papers_list_workspaces", "papers_list_papers", "papers_search", "papers_read_paper", "papers_create_paper", "papers_commit", "papers_history"} {
		gt.True(t, names[n])
	}

	type wsOut struct {
		Workspaces []struct {
			ID model.WorkspaceID `json:"id"`
		} `json:"workspaces"`
	}
	wl, ok := callTool[wsOut](t, session, "papers_list_workspaces", map[string]any{})
	gt.True(t, ok)
	gt.A(t, wl.Workspaces).Length(1)
	gt.Equal(t, wl.Workspaces[0].ID, ws)

	type readOut struct {
		Blocks []struct {
			ID      model.BlockID `json:"id"`
			Version int64         `json:"version"`
		} `json:"blocks"`
		Markdown string `json:"markdown"`
	}
	rp, ok := callTool[readOut](t, session, "papers_read_paper", map[string]any{"workspace_id": ws, "paper_id": paper})
	gt.True(t, ok)
	gt.A(t, rp.Blocks).Length(3)

	type commitOut struct {
		Revision int64           `json:"revision"`
		Inserted []model.BlockID `json:"inserted"`
	}
	co, ok := callTool[commitOut](t, session, "papers_commit", map[string]any{
		"workspace_id": ws, "paper_id": paper, "message": "add diagram",
		"ops": []map[string]any{{"op": "insert_markdown", "markdown": "## Flow\n\n```mermaid\ngraph TD; A-->B\n```", "after": rp.Blocks[0].ID}},
	})
	gt.True(t, ok)
	gt.A(t, co.Inserted).Length(2)
	gt.Equal(t, co.Revision, int64(2))

	// a human-held lock makes the agent's write fail with a readable error
	alice.expect("PUT", "/api/v1/workspaces/"+string(ws)+"/papers/"+string(paper)+"/blocks/"+string(co.Inserted[0])+"/lock", map[string]string{"connection_id": "tab"}, http.StatusOK, nil)
	_, ok = callTool[commitOut](t, session, "papers_commit", map[string]any{
		"workspace_id": ws, "paper_id": paper, "message": "x",
		"ops": []map[string]any{{"op": "delete", "block_id": co.Inserted[0]}},
	})
	gt.False(t, ok)

	type histOut struct {
		Commits []struct {
			Kind  model.ActorKind `json:"actor_kind"`
			Actor string          `json:"actor"`
		} `json:"commits"`
	}
	h, ok := callTool[histOut](t, session, "papers_history", map[string]any{"workspace_id": ws, "paper_id": paper})
	gt.True(t, ok)
	gt.A(t, h.Commits).Length(2)
	gt.Equal(t, h.Commits[1].Kind, model.ActorAgent)

	type created struct {
		ID model.PaperID `json:"id"`
	}
	cp, ok := callTool[created](t, session, "papers_create_paper", map[string]any{"workspace_id": ws, "title": "AI notes", "markdown": "hello"})
	gt.True(t, ok)
	gt.NotEqual(t, cp.ID, model.PaperID(""))

	type searchOut struct {
		Hits []struct {
			PaperID model.PaperID `json:"paper_id"`
		} `json:"hits"`
	}
	so, ok := callTool[searchOut](t, session, "papers_search", map[string]any{"workspace_id": ws, "query": "hello"})
	gt.True(t, ok)
	gt.A(t, so.Hits).Length(1)

	type papersOut struct {
		Papers []struct {
			ID model.PaperID `json:"id"`
		} `json:"papers"`
	}
	pl, ok := callTool[papersOut](t, session, "papers_list_papers", map[string]any{"workspace_id": ws})
	gt.True(t, ok)
	gt.A(t, pl.Papers).Length(2)

	type guideOut struct {
		Guide string `json:"guide"`
	}
	g, ok := callTool[guideOut](t, session, "papers_guide", map[string]any{})
	gt.True(t, ok)
	gt.True(t, len(g.Guide) > 100)
}

func TestMCPRequiresAuth(t *testing.T) {
	ts := newTestServer(t, false)
	resp := ts.anonymous().do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	gt.Equal(t, resp.StatusCode, http.StatusUnauthorized)

	// a browser session cookie is not enough for MCP
	c := ts.login("alice@example.com")
	resp = c.do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	gt.Equal(t, resp.StatusCode, http.StatusUnauthorized)
}

func TestMCPInDevModeActsAsAgent(t *testing.T) {
	ts := newTestServer(t, true)
	c := ts.anonymous()
	var ws wsResp
	c.expect("POST", "/api/v1/workspaces", map[string]string{"name": "W"}, http.StatusCreated, &ws)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.srv.URL + "/mcp"}, nil)
	gt.NoError(t, err).Required()
	defer func() { _ = session.Close() }()

	type created struct {
		ID        model.PaperID `json:"id"`
		UpdatedBy string        `json:"updated_by"`
	}
	cp, ok := callTool[created](t, session, "papers_create_paper", map[string]any{"workspace_id": ws.ID, "title": "x", "markdown": "y"})
	gt.True(t, ok)
	gt.Equal(t, cp.UpdatedBy, "MCP client (for Dev)")
}
