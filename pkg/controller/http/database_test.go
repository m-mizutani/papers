package http_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

type itemsPage struct {
	Items []struct {
		Item   model.Paper `json:"item"`
		Values []struct {
			PropertyID model.PropertyID `json:"property_id"`
			Value      any              `json:"value"`
		} `json:"values"`
	} `json:"items"`
	NextCursor string `json:"next_cursor"`
}

func TestDatabaseAPI(t *testing.T) {
	ts := newTestServer(t, false)
	alice := ts.login("alice@example.com")
	base := "/api/v1/workspaces/team/papers"

	// T4: kind in and out; ordinary papers carry none of the new fields
	var db model.Paper
	alice.expect("POST", base, map[string]any{"title": "Projects", "parent": string(homeOf(alice, ws)), "kind": "database"}, http.StatusCreated, &db)
	gt.Equal(t, db.Kind, model.PaperKindDatabase)
	var plain map[string]any
	alice.expect("POST", base, map[string]any{"title": "Plain", "parent": string(homeOf(alice, ws))}, http.StatusCreated, &plain)
	gt.Equal(t, plain["kind"], any("blocks"))
	for _, k := range []string{"properties", "items", "database_id"} {
		_, ok := plain[k]
		gt.False(t, ok).Describef("ordinary paper has %s", k)
	}
	alice.expect("POST", base, map[string]any{"title": "X", "kind": "item"}, http.StatusBadRequest, nil)

	var res struct {
		Paper  model.Paper       `json:"paper"`
		Refs   map[string]string `json:"refs"`
		Items  []model.Paper     `json:"items"`
		Values []any             `json:"values"`
	}
	alice.expect("POST", base+"/"+string(db.ID)+"/ops", map[string]any{"ops": []map[string]any{
		{"op": "add_property", "name": "Status", "property_type": "select", "ref": "s"},
		{"op": "add_element", "property": "ref:s", "value": "Doing", "color": "blue", "ref": "d"},
		{"op": "add_property", "name": "Due", "property_type": "date", "ref": "due"},
		{"op": "insert_item", "title": "B", "set": map[string]any{"ref:s": "ref:d", "ref:due": "2027-01-01"}},
		{"op": "insert_item", "title": "A", "set": map[string]any{"ref:due": "2026-01-01"}},
	}}, http.StatusOK, &res)
	gt.A(t, res.Paper.Properties).Length(2)
	gt.A(t, res.Items).Length(2)
	gt.A(t, res.Values).Length(3)
	status, due := res.Refs["s"], res.Refs["due"]

	// the item's view carries its values and database
	var view struct {
		Paper    model.Paper `json:"paper"`
		Values   []any       `json:"values"`
		Database struct {
			ID         model.PaperID    `json:"id"`
			Properties []model.Property `json:"properties"`
		} `json:"database"`
		Path []struct {
			ID model.PaperID `json:"id"`
		} `json:"path"`
	}
	alice.expect("GET", base+"/"+string(res.Items[0].ID), nil, http.StatusOK, &view)
	gt.Equal(t, view.Paper.DatabaseID, db.ID)
	gt.A(t, view.Values).Length(2)
	gt.Equal(t, view.Database.ID, db.ID)
	gt.Equal(t, view.Path[len(view.Path)-1].ID, db.ID)

	// T42: items with filter and sort
	var page itemsPage
	alice.expect("GET", base+"/"+string(db.ID)+"/items", nil, http.StatusOK, &page)
	gt.A(t, page.Items).Length(2)
	gt.Equal(t, page.Items[0].Item.Title, "B")
	alice.expect("GET", base+"/"+string(db.ID)+"/items?sort="+due+":asc", nil, http.StatusOK, &page)
	gt.Equal(t, page.Items[0].Item.Title, "A")
	filter, _ := json.Marshal([]map[string]any{{"property": status, "op": "is_empty"}})
	alice.expect("GET", base+"/"+string(db.ID)+"/items?filter="+url.QueryEscape(string(filter)), nil, http.StatusOK, &page)
	gt.A(t, page.Items).Length(1)
	gt.Equal(t, page.Items[0].Item.Title, "A")
	alice.expect("GET", base+"/"+string(db.ID)+"/items?filter=nope", nil, http.StatusBadRequest, nil)
	alice.expect("GET", base+"/"+string(db.ID)+"/items?sort=title", nil, http.StatusBadRequest, nil)
	alice.expect("GET", base+"/"+string(res.Items[0].ID)+"/items", nil, http.StatusBadRequest, nil)

	// T50: a rejected op says which op and why
	var bad struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	missing := string(model.NewElementID())
	alice.expect("POST", base+"/"+string(db.ID)+"/ops", map[string]any{"ops": []map[string]any{
		{"op": "set_values", "item": string(res.Items[1].ID), "set": map[string]any{due: "2026-02-02"}},
		{"op": "set_values", "item": string(res.Items[1].ID), "set": map[string]any{status: missing}},
	}}, http.StatusBadRequest, &bad)
	gt.True(t, strings.Contains(bad.Error.Message, "op #1 (set_values)"))
	gt.True(t, strings.Contains(bad.Error.Message, "unknown element "+missing))

	// an item without values carries an empty list, never null
	alice.expect("POST", base+"/"+string(db.ID)+"/ops", map[string]any{"ops": []map[string]any{{"op": "insert_item", "title": "C"}}}, http.StatusOK, nil)
	raw := alice.do("GET", base+"/"+string(db.ID)+"/items", nil)
	body, err := io.ReadAll(raw.Body)
	_ = raw.Body.Close()
	gt.NoError(t, err)
	gt.False(t, strings.Contains(string(body), `"values":null`))

	// T33: Markdown
	resp := alice.do("GET", base+"/"+string(db.ID)+"/markdown", nil)
	defer func() { _ = resp.Body.Close() }()
	gt.Equal(t, resp.StatusCode, http.StatusOK)
}

func TestMCPDatabase(t *testing.T) {
	// T32, T42
	ts := newTestServer(t, false)
	alice := ts.login("alice@example.com")
	agent := ts.token(alice, model.TokenAgent, model.ScopeWrite)
	session := connectMCP(t, ts, agent.bearer)

	type created struct {
		ID model.PaperID `json:"id"`
	}
	db, ok := callTool[created](t, session, "papers_create_paper", map[string]any{"workspace_id": ws, "title": "Projects", "kind": "database", "parent": string(homeOf(alice, ws))})
	gt.True(t, ok)
	type writeOut struct {
		Refs map[string]string `json:"refs"`
	}
	w, ok := callTool[writeOut](t, session, "papers_write", map[string]any{"workspace_id": ws, "paper": string(db.ID), "ops": []map[string]any{
		{"op": "add_property", "name": "Status", "property_type": "select", "ref": "s"},
		{"op": "add_element", "property": "ref:s", "value": "Doing", "ref": "d"},
		{"op": "add_property", "name": "Tags", "property_type": "multi_select", "ref": "t"},
		{"op": "add_element", "property": "ref:t", "value": "ui", "ref": "u"},
		{"op": "insert_item", "title": "One", "set": map[string]any{"ref:s": "ref:d", "ref:t": []string{"ref:u"}}, "ref": "i"},
		{"op": "insert_item", "title": "Two"},
	}})
	gt.True(t, ok)
	gt.True(t, model.ValidID(w.Refs["i"]))

	type readOut struct {
		Kind       model.PaperKind  `json:"kind"`
		DatabaseID model.PaperID    `json:"database_id"`
		Properties []model.Property `json:"properties"`
		Items      []struct {
			ID     model.PaperID `json:"id"`
			Title  string        `json:"title"`
			Values []struct {
				PropertyID    model.PropertyID `json:"property_id"`
				Property      string           `json:"property"`
				ElementValues []string         `json:"element_values"`
			} `json:"values"`
		} `json:"items"`
		Values []struct {
			Property      string   `json:"property"`
			ElementValues []string `json:"element_values"`
		} `json:"values"`
	}
	r, ok := callTool[readOut](t, session, "papers_read", map[string]any{"workspace_id": ws, "paper": string(db.ID)})
	gt.True(t, ok)
	gt.Equal(t, r.Kind, model.PaperKindDatabase)
	gt.A(t, r.Properties).Length(2)
	gt.A(t, r.Items).Length(2)
	gt.Equal(t, r.Items[0].Values[0].Property, "Status")
	gt.Equal(t, r.Items[0].Values[0].ElementValues, []string{"Doing"})
	gt.Equal(t, r.Items[0].Values[1].ElementValues, []string{"ui"})

	// the same filter as REST
	f, ok := callTool[readOut](t, session, "papers_read", map[string]any{"workspace_id": ws, "paper": string(db.ID),
		"filter": []map[string]any{{"property": w.Refs["s"], "op": "is_empty"}}, "sort": "title:desc"})
	gt.True(t, ok)
	gt.A(t, f.Items).Length(1)
	gt.Equal(t, f.Items[0].Title, "Two")

	item, ok := callTool[readOut](t, session, "papers_read", map[string]any{"workspace_id": ws, "paper": w.Refs["i"]})
	gt.True(t, ok)
	gt.Equal(t, item.Kind, model.PaperKindBlocks)
	gt.Equal(t, item.DatabaseID, db.ID)
	gt.A(t, item.Values).Length(2)

	type guideOut struct {
		Guide string `json:"guide"`
	}
	g, ok := callTool[guideOut](t, session, "papers_guide", map[string]any{})
	gt.True(t, ok)
	gt.True(t, strings.Contains(g.Guide, "set_values"))
}
