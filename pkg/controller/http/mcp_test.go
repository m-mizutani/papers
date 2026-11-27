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
	for _, n := range []string{"papers_guide", "papers_list_workspaces", "papers_search", "papers_read", "papers_create_paper", "papers_write", "papers_save_version", "papers_versions", "papers_diff", "papers_find_users"} {
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

	// T22: people to mention, and how to write a mention
	type guideText struct {
		Guide string `json:"guide"`
	}
	gtx, ok := callTool[guideText](t, session, "papers_guide", map[string]any{})
	gt.True(t, ok)
	gt.True(t, strings.Contains(gtx.Guide, "[@name]{user=<user id>}"))
	gt.True(t, strings.Contains(gtx.Guide, "person"))
	type usersOut struct {
		Users []struct {
			ID   model.UserID `json:"id"`
			Name string       `json:"name"`
		} `json:"users"`
	}
	uo, ok := callTool[usersOut](t, session, "papers_find_users", map[string]any{"workspace_id": ws, "query": "bob"})
	gt.True(t, ok)
	gt.A(t, uo.Users).Length(1)

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

	// deleting the child's paper block runs a job; its result is in the write
	rp, ok = callTool[readOut](t, session, "papers_read", map[string]any{"workspace_id": ws, "paper": "Runbook"})
	gt.True(t, ok)
	var linkID model.BlockID
	var linkRev int64
	for _, b := range rp.Blocks {
		linkID, linkRev = b.ID, b.Rev // the child's block is the last one
	}
	type deleteOut struct {
		Deleted []model.BlockID `json:"deleted"`
	}
	_, ok = callTool[deleteOut](t, session, "papers_write", map[string]any{"workspace_id": ws, "paper": string(paper), "ops": []map[string]any{
		{"op": "delete", "block_id": linkID, "base_rev": linkRev},
	}})
	gt.True(t, ok)
	after, ok := callTool[readOut](t, session, "papers_read", map[string]any{"workspace_id": ws, "paper": "Runbook"})
	gt.True(t, ok)
	for _, b := range after.Blocks {
		gt.NotEqual(t, b.ID, linkID)
	}
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

func TestMCPUnfiledAndFields(t *testing.T) {
	ts := newTestServer(t, false)
	alice, bob, ws, runbook := setupPaper(t, ts)
	agent := ts.token(alice, model.TokenAgent, model.ScopeWrite)
	session := connectMCP(t, ts, agent.bearer)

	type guideOut struct {
		Guide string `json:"guide"`
	}
	g, ok := callTool[guideOut](t, session, "papers_guide", map[string]any{})
	gt.True(t, ok)
	gt.True(t, strings.Contains(g.Guide, "unfiled"))
	gt.True(t, strings.Contains(g.Guide, "data-field"))
	gt.True(t, strings.Contains(g.Guide, "props.fields"))

	// without a parent the paper is unfiled and takes no child papers
	type created struct {
		ID model.PaperID `json:"id"`
	}
	cp, ok := callTool[created](t, session, "papers_create_paper", map[string]any{"workspace_id": ws, "title": "Loose"})
	gt.True(t, ok)
	var view paperView
	bob.expect("GET", "/api/v1/workspaces/team/papers/"+string(cp.ID), nil, http.StatusOK, &view)
	gt.Equal(t, view.Paper.ParentID, model.PaperID(""))
	_, ok = callTool[struct{}](t, session, "papers_write", map[string]any{"workspace_id": ws, "paper": string(cp.ID), "ops": []map[string]any{{"op": "insert", "type": "paper", "title": "Sub"}}})
	gt.False(t, ok)

	// move it under Runbook and back to unfiled with the batch op
	_, ok = callTool[struct{}](t, session, "papers_write", map[string]any{"workspace_id": ws, "paper": string(cp.ID), "ops": []map[string]any{{"op": "move_paper", "parent": "Runbook"}}})
	gt.True(t, ok)
	bob.expect("GET", "/api/v1/workspaces/team/papers/"+string(cp.ID), nil, http.StatusOK, &view)
	gt.Equal(t, view.Paper.ParentID, runbook)
	_, ok = callTool[struct{}](t, session, "papers_write", map[string]any{"workspace_id": ws, "paper": string(cp.ID), "ops": []map[string]any{{"op": "move_paper", "unfiled": true}}})
	gt.True(t, ok)
	view = paperView{}
	bob.expect("GET", "/api/v1/workspaces/team/papers/"+string(cp.ID), nil, http.StatusOK, &view)
	gt.Equal(t, view.Paper.ParentID, model.PaperID(""))

	// an html block with fields: the value is kept and searchable
	_, ok = callTool[struct{}](t, session, "papers_write", map[string]any{"workspace_id": ws, "paper": string(cp.ID), "ops": []map[string]any{
		{"op": "insert", "type": "html", "content": `<p>RTO <span data-field="rto">15m</span></p>`, "props": map[string]any{"fields": map[string]string{"rto": "15m"}}},
	}})
	gt.True(t, ok)
	type readOut struct {
		Blocks []struct {
			Props model.BlockProps `json:"props"`
		} `json:"blocks"`
	}
	rp, ok := callTool[readOut](t, session, "papers_read", map[string]any{"workspace_id": ws, "paper": string(cp.ID)})
	gt.True(t, ok)
	gt.Equal(t, rp.Blocks[0].Props.Fields, map[string]string{"rto": "15m"})
	_, ok = callTool[struct{}](t, session, "papers_write", map[string]any{"workspace_id": ws, "paper": string(cp.ID), "ops": []map[string]any{
		{"op": "insert", "type": "svg", "content": "<svg></svg>", "props": map[string]any{"fields": map[string]string{"Bad-Key": "x"}}},
	}})
	gt.False(t, ok)
}

func TestMCPHTMLPaper(t *testing.T) { // T20, T24, T54 (guide)
	ts := newTestServer(t, false)
	alice := ts.login("alice@example.com")
	agent := ts.token(alice, model.TokenAgent, model.ScopeWrite)
	session := connectMCP(t, ts, agent.bearer)

	type guideOut struct {
		Guide   string `json:"guide"`
		Sandbox string `json:"sandbox"`
	}
	g, ok := callTool[guideOut](t, session, "papers_guide", map[string]any{"workspace_id": "team"})
	gt.True(t, ok)
	gt.True(t, strings.Contains(g.Guide, "set_html"))
	gt.True(t, strings.Contains(g.Sandbox, "may load"))
	g, ok = callTool[guideOut](t, session, "papers_guide", map[string]any{"workspace_id": "other"})
	gt.True(t, ok)
	gt.True(t, strings.Contains(g.Sandbox, "cannot load"))

	// T62: an HTML paper needs its html
	_, ok = callTool[struct{}](t, session, "papers_create_paper", map[string]any{"workspace_id": "team", "title": "Empty", "kind": "html"})
	gt.False(t, ok)

	type created struct {
		ID model.PaperID `json:"id"`
	}
	cp, ok := callTool[created](t, session, "papers_create_paper", map[string]any{"workspace_id": "team", "title": "Weekly", "kind": "html", "html": "<p>fourteen</p>"})
	gt.True(t, ok)

	type readOut struct {
		Kind   model.PaperKind `json:"kind"`
		HTML   string          `json:"html"`
		Blocks []struct {
			ID   model.BlockID   `json:"id"`
			Type model.BlockType `json:"type"`
			Rev  int64           `json:"rev"`
		} `json:"blocks"`
	}
	r, ok := callTool[readOut](t, session, "papers_read", map[string]any{"workspace_id": "team", "paper": string(cp.ID)})
	gt.True(t, ok)
	gt.Equal(t, r.Kind, model.PaperKindHTML)
	gt.Equal(t, r.HTML, "<p>fourteen</p>")
	gt.A(t, r.Blocks).Length(1)
	gt.Equal(t, r.Blocks[0].Type, model.BlockHTMLPage)

	// title and HTML together through papers_write
	type writeOut struct {
		Revs map[model.BlockID]int64 `json:"revs"`
	}
	w, ok := callTool[writeOut](t, session, "papers_write", map[string]any{"workspace_id": "team", "paper": string(cp.ID), "ops": []map[string]any{
		{"op": "set_title", "title": "Weekly report"},
		{"op": "set_html", "html": "<p>nine</p>", "base_rev": r.Blocks[0].Rev},
	}})
	gt.True(t, ok)
	gt.Equal(t, w.Revs[r.Blocks[0].ID], int64(2))
	r, ok = callTool[readOut](t, session, "papers_read", map[string]any{"workspace_id": "team", "paper": string(cp.ID)})
	gt.True(t, ok)
	gt.Equal(t, r.HTML, "<p>nine</p>")

	// a stale base explains how to recover
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "papers_write", Arguments: map[string]any{"workspace_id": "team", "paper": string(cp.ID), "ops": []map[string]any{
		{"op": "set_html", "html": "<p>lost</p>", "base_rev": 1},
	}}})
	gt.NoError(t, err)
	gt.True(t, res.IsError)
	text := res.Content[0].(*mcp.TextContent).Text
	gt.True(t, strings.Contains(text, "conflict"))
	gt.True(t, strings.Contains(text, "papers_read"))

	// other block ops are refused with a pointer to set_html
	res, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "papers_write", Arguments: map[string]any{"workspace_id": "team", "paper": string(cp.ID), "ops": []map[string]any{
		{"op": "insert", "type": "paragraph", "content": "x"},
	}}})
	gt.NoError(t, err)
	gt.True(t, res.IsError)
	gt.True(t, strings.Contains(res.Content[0].(*mcp.TextContent).Text, "set_html"))

	// a version reads back its own html
	_, ok = callTool[struct{}](t, session, "papers_save_version", map[string]any{"workspace_id": "team", "paper": string(cp.ID)})
	gt.True(t, ok)
	_, ok = callTool[struct{}](t, session, "papers_write", map[string]any{"workspace_id": "team", "paper": string(cp.ID), "ops": []map[string]any{
		{"op": "set_html", "html": "<p>eleven</p>", "base_rev": 2},
	}})
	gt.True(t, ok)
	r, ok = callTool[readOut](t, session, "papers_read", map[string]any{"workspace_id": "team", "paper": string(cp.ID), "at": "1"})
	gt.True(t, ok)
	gt.Equal(t, r.HTML, "<p>nine</p>")
}

func TestMCPIcon(t *testing.T) {
	ts := newTestServer(t, false)
	alice, _, ws, paper := setupPaper(t, ts)
	agent := ts.token(alice, model.TokenAgent, model.ScopeWrite)
	session := connectMCP(t, ts, agent.bearer)

	type ref struct {
		ID   model.PaperID `json:"id"`
		Icon string        `json:"icon"`
	}
	cp, ok := callTool[ref](t, session, "papers_create_paper", map[string]any{"workspace_id": ws, "title": "Lab", "icon": "🧪"})
	gt.True(t, ok)
	gt.Equal(t, cp.Icon, "🧪")

	_, ok = callTool[struct{}](t, session, "papers_write", map[string]any{"workspace_id": ws, "paper": string(paper), "ops": []map[string]any{{"op": "set_icon", "icon": "🚀"}}})
	gt.True(t, ok)
	var view paperView
	alice.expect("GET", "/api/v1/workspaces/team/papers/"+string(paper), nil, http.StatusOK, &view)
	gt.Equal(t, view.Paper.Icon, "🚀")
	_, ok = callTool[struct{}](t, session, "papers_write", map[string]any{"workspace_id": ws, "paper": string(paper), "ops": []map[string]any{{"op": "set_icon", "icon": "🚀🚀"}}})
	gt.False(t, ok)

	read, ok := callTool[ref](t, session, "papers_read", map[string]any{"workspace_id": ws, "paper": string(paper)})
	gt.True(t, ok)
	gt.Equal(t, read.Icon, "🚀")

	type guide struct {
		Guide string `json:"guide"`
	}
	g, ok := callTool[guide](t, session, "papers_guide", map[string]any{})
	gt.True(t, ok)
	gt.True(t, strings.Contains(g.Guide, "set_icon"))
}

func TestMCPHTMLDatabaseGuide(t *testing.T) { // T23, T28
	ts := newTestServer(t, false)
	alice := ts.login("alice@example.com")
	agent := ts.token(alice, model.TokenAgent, model.ScopeWrite)
	session := connectMCP(t, ts, agent.bearer)

	type guideOut struct {
		Guide string `json:"guide"`
	}
	g, ok := callTool[guideOut](t, session, "papers_guide", map[string]any{})
	gt.True(t, ok)
	gt.True(t, strings.Contains(g.Guide, "papers.db"))
	gt.True(t, strings.Contains(g.Guide, `topic:"html_database"`))

	full, ok := callTool[guideOut](t, session, "papers_guide", map[string]any{"topic": "html_database"})
	gt.True(t, ok)
	for _, want := range []string{"papers.db.databases()", "papers.db.items(", "papers.db.setValues(", "papers.db.addItem(", "papers.db.item(", "papers.db.writeItem(", "papers.db.subscribe(", "not_connected", "read_only", "unavailable", "insert_markdown", "<script>"} {
		gt.True(t, strings.Contains(full.Guide, want))
	}
	gt.False(t, strings.Contains(full.Guide, "Block types (type: props)"))

	_, ok = callTool[guideOut](t, session, "papers_guide", map[string]any{"topic": "nope"})
	gt.False(t, ok)

	type created struct {
		ID model.PaperID `json:"id"`
	}
	db, ok := callTool[created](t, session, "papers_create_paper", map[string]any{"workspace_id": "team", "title": "Roadmap", "kind": "database"})
	gt.True(t, ok)
	cp, ok := callTool[created](t, session, "papers_create_paper", map[string]any{"workspace_id": "team", "title": "Board", "kind": "html", "html": "<p>board</p>",
		"databases": []map[string]any{{"id": db.ID, "write": true}}})
	gt.True(t, ok)
	type readOut struct {
		Databases []model.DatabaseLink `json:"databases"`
	}
	r, ok := callTool[readOut](t, session, "papers_read", map[string]any{"workspace_id": "team", "paper": string(cp.ID)})
	gt.True(t, ok)
	gt.Equal(t, r.Databases, []model.DatabaseLink{{ID: db.ID, Write: true}})

	// the tool's description points to the topic
	tools, err := session.ListTools(context.Background(), nil)
	gt.NoError(t, err)
	for _, tool := range tools.Tools {
		if tool.Name == "papers_create_paper" {
			raw, _ := json.Marshal(tool.InputSchema)
			gt.True(t, strings.Contains(string(raw), "html_database"))
		}
	}
}
