package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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

func connectMCP(t *testing.T, ts *testServer, bearer string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   ts.srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: &bearerTransport{token: bearer, base: http.DefaultTransport}},
	}
	session, err := client.Connect(context.Background(), transport, nil)
	gt.NoError(t, err).Required()
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestMCP(t *testing.T) {
	ts := newTestServer(t, false)
	alice, _, ws, paper := setupPaper(t, ts)
	agent := ts.token(alice, model.TokenAgent, model.ScopeWrite)
	session := connectMCP(t, ts, agent.bearer)

	tools, err := session.ListTools(context.Background(), nil)
	gt.NoError(t, err)
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, n := range []string{"papers_guide", "papers_list_workspaces", "papers_search", "papers_read", "papers_create_paper", "papers_write", "papers_save_version", "papers_versions", "papers_diff"} {
		gt.True(t, names[n]).Describef("tool %s", n)
	}

	type guideOut struct {
		WorkspaceRules string `json:"workspace_rules"`
		Home           string `json:"home"`
	}
	g, ok := callTool[guideOut](t, session, "papers_guide", map[string]any{"workspace_id": ws})
	gt.True(t, ok)
	gt.Equal(t, g.WorkspaceRules, "Write runbooks as numbered lists.")
	gt.True(t, strings.Contains(g.Home, "Runbook"))

	type readOut struct {
		ID     model.PaperID `json:"id"`
		Blocks []struct {
			ID  model.BlockID `json:"id"`
			Rev int64         `json:"rev"`
		} `json:"blocks"`
		WorkspaceRules string `json:"workspace_rules"`
	}
	rp, ok := callTool[readOut](t, session, "papers_read", map[string]any{"workspace_id": ws, "paper": "Runbook"})
	gt.True(t, ok)
	gt.Equal(t, rp.ID, paper)
	gt.A(t, rp.Blocks).Length(3)
	gt.NotEqual(t, rp.WorkspaceRules, "")

	type writeOut struct {
		Refs    map[string]string       `json:"refs"`
		Revs    map[model.BlockID]int64 `json:"revs"`
		Unsaved bool                    `json:"unsaved"`
	}
	wo, ok := callTool[writeOut](t, session, "papers_write", map[string]any{"workspace_id": ws, "paper": string(paper), "ops": []map[string]any{
		{"op": "update", "block_id": rp.Blocks[0].ID, "base_rev": rp.Blocks[0].Rev, "content": "Steps (agent)"},
		{"op": "insert", "ref": "n1", "type": "paragraph", "content": "note"},
	}})
	gt.True(t, ok)
	gt.True(t, wo.Unsaved)
	gt.Equal(t, wo.Revs[rp.Blocks[0].ID], int64(2))
	gt.True(t, model.ValidID(wo.Refs["n1"]))

	// a stale base revision is an error explaining the current state
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "papers_write", Arguments: map[string]any{"workspace_id": ws, "paper": string(paper), "ops": []map[string]any{
		{"op": "update", "block_id": rp.Blocks[0].ID, "base_rev": 1, "content": "x"},
	}}})
	gt.NoError(t, err)
	gt.True(t, res.IsError)

	type saveOut struct {
		Saved   bool `json:"saved"`
		Version int  `json:"version"`
	}
	so, ok := callTool[saveOut](t, session, "papers_save_version", map[string]any{"workspace_id": ws, "paper": string(paper), "note": "by agent"})
	gt.True(t, ok)
	gt.True(t, so.Saved)
	gt.Equal(t, so.Version, 1)

	type versionsOut struct {
		Versions []struct {
			By string `json:"by"`
		} `json:"versions"`
	}
	vo, ok := callTool[versionsOut](t, session, "papers_versions", map[string]any{"workspace_id": ws, "paper": string(paper)})
	gt.True(t, ok)
	gt.Equal(t, vo.Versions[0].By, "t (for alice)")

	type createOut struct {
		ID model.PaperID `json:"id"`
	}
	co, ok := callTool[createOut](t, session, "papers_create_paper", map[string]any{"workspace_id": ws, "title": "Child", "parent": "Runbook", "markdown": "hello"})
	gt.True(t, ok)
	type searchOut struct {
		Hits []struct {
			Paper struct {
				ID model.PaperID `json:"id"`
			} `json:"paper"`
		} `json:"hits"`
	}
	sr, ok := callTool[searchOut](t, session, "papers_search", map[string]any{"workspace_id": ws, "query": "hello", "under": "Runbook"})
	gt.True(t, ok)
	gt.A(t, sr.Hits).Length(1)
	gt.Equal(t, sr.Hits[0].Paper.ID, co.ID)
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
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.srv.URL + "/mcp"}, nil)
	gt.NoError(t, err).Required()
	defer func() { _ = session.Close() }()

	type created struct {
		ID model.PaperID `json:"id"`
	}
	cp, ok := callTool[created](t, session, "papers_create_paper", map[string]any{"workspace_id": "team", "title": "x"})
	gt.True(t, ok)
	type versionsOut struct {
		Versions []struct {
			By string `json:"by"`
		} `json:"versions"`
	}
	_, ok = callTool[struct{}](t, session, "papers_write", map[string]any{"workspace_id": "team", "paper": string(cp.ID), "ops": []map[string]any{{"op": "insert", "type": "paragraph", "content": "y"}}})
	gt.True(t, ok)
	_, ok = callTool[struct{}](t, session, "papers_save_version", map[string]any{"workspace_id": "team", "paper": string(cp.ID)})
	gt.True(t, ok)
	vo, ok := callTool[versionsOut](t, session, "papers_versions", map[string]any{"workspace_id": "team", "paper": string(cp.ID)})
	gt.True(t, ok)
	gt.Equal(t, vo.Versions[0].By, "MCP client (for Dev)")
}
