package http_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
)

type paperView struct {
	Paper  model.Paper `json:"paper"`
	IsHome bool        `json:"is_home"`
	Path   []struct {
		ID model.PaperID `json:"id"`
	} `json:"path"`
}

type blocksPage struct {
	Blocks []model.Block `json:"blocks"`
	Next   string        `json:"next"`
	Papers map[model.PaperID]struct {
		Title string `json:"title"`
	} `json:"papers"`
}

type batchResp struct {
	Paper   model.Paper       `json:"paper"`
	Blocks  []model.Block     `json:"blocks"`
	Deleted []model.BlockID   `json:"deleted"`
	Refs    map[string]string `json:"refs"`
	Trashed []model.PaperID   `json:"trashed"`
}

const ws = model.WorkspaceID("team")

// homeOf reads the home paper of the workspace.
func homeOf(c *client, ws model.WorkspaceID) model.PaperID {
	var v struct {
		HomePaperID model.PaperID `json:"home_paper_id"`
	}
	c.expect("GET", "/api/v1/workspaces/"+string(ws), nil, http.StatusOK, &v)
	return v.HomePaperID
}

// setupPaper signs in alice and bob and has alice create a public paper
// with a body under the home.
func setupPaper(t *testing.T, ts *testServer) (*client, *client, model.WorkspaceID, model.PaperID) {
	t.Helper()
	alice := ts.login("alice@example.com")
	bob := ts.login("bob@example.com")
	var p model.Paper
	alice.expect("POST", "/api/v1/workspaces/team/papers", map[string]any{"title": "Runbook", "parent": string(homeOf(alice, ws)), "markdown": "# Steps\n\n1. check\n2. fix"}, http.StatusCreated, &p)
	gt.Equal(t, p.Title, "Runbook")
	return alice, bob, ws, p.ID
}

func TestWorkspaceAPI(t *testing.T) {
	ts := newTestServer(t, false)
	alice := ts.login("alice@example.com")
	var list struct {
		Workspaces []struct {
			ID          model.WorkspaceID `json:"id"`
			Name        string            `json:"name"`
			HomePaperID model.PaperID     `json:"home_paper_id"`
		} `json:"workspaces"`
	}
	alice.expect("GET", "/api/v1/workspaces", nil, http.StatusOK, &list)
	gt.A(t, list.Workspaces).Length(2)
	gt.Equal(t, list.Workspaces[0].ID, ws)

	// workspaces come from the configuration: there is no create API
	alice.expect("POST", "/api/v1/workspaces", map[string]string{"name": "x"}, http.StatusMethodNotAllowed, nil)
	alice.expect("GET", "/api/v1/workspaces/nope", nil, http.StatusNotFound, nil)

	var home paperView
	alice.expect("GET", "/api/v1/workspaces/team/papers/"+string(list.Workspaces[0].HomePaperID), nil, http.StatusOK, &home)
	gt.True(t, home.IsHome)
	gt.Equal(t, home.Paper.Title, "Team")

	var meta struct {
		Palette []string `json:"palette"`
	}
	alice.expect("GET", "/api/v1/block-types", nil, http.StatusOK, &meta)
	gt.A(t, meta.Palette).Length(9)
}

func TestPaperAPI(t *testing.T) {
	ts := newTestServer(t, false)
	alice, bob, ws, paper := setupPaper(t, ts)
	base := "/api/v1/workspaces/" + string(ws) + "/papers/" + string(paper)

	var page blocksPage
	bob.expect("GET", base+"/blocks?limit=100", nil, http.StatusOK, &page)
	gt.A(t, page.Blocks).Length(3)
	first := page.Blocks[0]

	// ops with a ref and a child paper
	var res batchResp
	bob.expect("POST", base+"/ops", map[string]any{"ops": []map[string]any{
		{"op": "update", "block_id": first.ID, "base_rev": first.Rev, "content": "Steps [now]{.red}"},
		{"op": "insert", "ref": "n1", "type": "paragraph", "content": "after", "after": ""},
		{"op": "insert", "type": "paper", "title": "Sub", "after": "ref:n1"},
	}}, http.StatusOK, &res)
	gt.True(t, model.ValidID(res.Refs["n1"]))
	var child model.PaperID
	for _, b := range res.Blocks {
		if b.Type == model.BlockPaper {
			child = b.Props.PaperID
		}
	}
	bob.expect("GET", base+"/blocks", nil, http.StatusOK, &page)
	gt.Equal(t, page.Papers[child].Title, "Sub")
	var sub paperView
	alice.expect("GET", "/api/v1/workspaces/team/papers/"+string(child), nil, http.StatusOK, &sub)
	gt.A(t, sub.Path).Length(2)

	// a stale base revision is a 409 with the current block
	var conflict struct {
		Error struct {
			Code      string `json:"code"`
			Conflicts []struct {
				Current *model.Block `json:"current"`
			} `json:"conflicts"`
		} `json:"error"`
	}
	alice.expect("POST", base+"/ops", map[string]any{"ops": []map[string]any{{"op": "update", "block_id": first.ID, "base_rev": first.Rev, "content": "mine"}}}, http.StatusConflict, &conflict)
	gt.Equal(t, conflict.Error.Code, "conflict")
	gt.Equal(t, conflict.Error.Conflicts[0].Current.Content, "Steps [now]{.red}")
	// a missing base revision is a 400
	alice.expect("POST", base+"/ops", map[string]any{"ops": []map[string]any{{"op": "update", "block_id": first.ID, "content": "x"}}}, http.StatusBadRequest, nil)
	alice.expect("POST", base+"/ops", map[string]any{"ops": []map[string]any{{"op": "update", "block_id": first.ID, "base_rev": first.Rev, "content": "mine"}}, "force": true}, http.StatusOK, nil)

	// versions
	var saved struct {
		Version *struct {
			Number int `json:"number"`
		} `json:"version"`
	}
	alice.expect("POST", base+"/versions", map[string]any{"note": "first"}, http.StatusOK, &saved)
	gt.Equal(t, saved.Version.Number, 1)
	alice.expect("POST", base+"/versions", map[string]any{"note": "again"}, http.StatusOK, &saved)
	gt.Nil(t, saved.Version)
	var latest struct {
		Version struct {
			Number int `json:"number"`
		} `json:"version"`
		Blocks []model.Block `json:"blocks"`
	}
	bob.expect("GET", base+"/versions/latest", nil, http.StatusOK, &latest)
	gt.Equal(t, latest.Version.Number, 1)
	var status struct {
		Changed bool `json:"changed"`
	}
	bob.expect("GET", base+"/status", nil, http.StatusOK, &status)
	gt.False(t, status.Changed)
	bob.expect("POST", base+"/ops", map[string]any{"ops": []map[string]any{{"op": "set_title", "title": "Runbook 2"}}}, http.StatusOK, nil)
	var diff struct {
		TitleAfter string `json:"title_after"`
	}
	bob.expect("GET", base+"/diff?from=latest", nil, http.StatusOK, &diff)
	gt.Equal(t, diff.TitleAfter, "Runbook 2")
	bob.expect("POST", base+"/versions/1/restore", nil, http.StatusOK, nil)

	// markdown
	resp := bob.do("GET", base+"/markdown", nil)
	md := string(gt.R1(io.ReadAll(resp.Body)).NoError(t))
	gt.True(t, strings.HasPrefix(md, "# Runbook\n"))

	// search, sidebar, favorites
	var hits struct {
		Hits []struct {
			Text string `json:"text"`
		} `json:"hits"`
	}
	bob.expect("GET", "/api/v1/workspaces/team/search?q=check", nil, http.StatusOK, &hits)
	gt.A(t, hits.Hits).Length(1)
	bob.expect("PUT", base+"/favorite", map[string]any{}, http.StatusNoContent, nil)
	var sb struct {
		Home struct {
			ID model.PaperID `json:"id"`
		} `json:"home"`
		Favorites []struct {
			ID model.PaperID `json:"id"`
		} `json:"favorites"`
		Recent []struct {
			ID model.PaperID `json:"id"`
		} `json:"recent"`
		UnfiledCount *int `json:"unfiled_count"`
	}
	bob.expect("GET", "/api/v1/workspaces/team/sidebar", nil, http.StatusOK, &sb)
	gt.Equal(t, sb.Favorites[0].ID, paper)
	gt.Equal(t, sb.Home.ID, homeOf(bob, ws))
	gt.V(t, *sb.UnfiledCount).Equal(0)
	bob.expect("PUT", "/api/v1/workspaces/team/papers/"+string(sb.Home.ID)+"/favorite", map[string]any{}, http.StatusBadRequest, nil)
	bob.expect("DELETE", base+"/favorite", nil, http.StatusNoContent, nil)
	bob.expect("GET", "/api/v1/workspaces/team/sidebar", nil, http.StatusOK, &sb)
	gt.A(t, sb.Favorites).Length(0)
	bob.expect("PUT", base+"/pin", map[string]any{}, http.StatusNotFound, nil)

	// only the creator deletes (with everything under it); others archive
	alice.expect("POST", base+"/trash", nil, http.StatusOK, nil) // bob's child is under it: archived instead
	var archived paperView
	alice.expect("GET", base, nil, http.StatusOK, &archived)
	gt.NotNil(t, archived.Paper.ArchivedAt)
	gt.Nil(t, archived.Paper.DeletedAt)
	alice.expect("POST", "/api/v1/workspaces/team/papers/"+string(child)+"/trash", nil, http.StatusForbidden, nil)
	alice.expect("POST", "/api/v1/workspaces/team/papers/"+string(child)+"/archive", map[string]any{}, http.StatusOK, nil)
	bob.expect("POST", "/api/v1/workspaces/team/papers/"+string(child)+"/trash", nil, http.StatusOK, nil)
	var trashed batchResp
	alice.expect("POST", base+"/trash", nil, http.StatusOK, &trashed)
	bob.expect("GET", base, nil, http.StatusNotFound, nil)
	var trash struct {
		Papers []struct {
			ID model.PaperID `json:"id"`
		} `json:"papers"`
	}
	alice.expect("GET", "/api/v1/workspaces/team/trash", nil, http.StatusOK, &trash)
	gt.A(t, trash.Papers).Length(1)
	alice.expect("POST", base+"/restore", nil, http.StatusOK, nil)
	bob.expect("GET", base, nil, http.StatusOK, nil)
}

func TestVisibilityAndMoveAPI(t *testing.T) {
	ts := newTestServer(t, false)
	alice, bob, ws, paper := setupPaper(t, ts)
	base := "/api/v1/workspaces/" + string(ws) + "/papers/" + string(paper)

	var memo model.Paper
	alice.expect("POST", "/api/v1/workspaces/team/papers", map[string]any{"title": "Memo", "visibility": "private"}, http.StatusCreated, &memo)
	memoBase := "/api/v1/workspaces/team/papers/" + string(memo.ID)
	bob.expect("GET", memoBase, nil, http.StatusNotFound, nil)
	bob.expect("PUT", memoBase+"/visibility", map[string]any{"visibility": "public"}, http.StatusNotFound, nil)
	alice.expect("PUT", memoBase+"/visibility", map[string]any{"visibility": "public", "parent": "Runbook"}, http.StatusOK, nil)
	bob.expect("GET", memoBase, nil, http.StatusOK, nil)

	var resolved model.Paper
	bob.expect("GET", "/api/v1/workspaces/team/resolve?path=Runbook/Memo", nil, http.StatusOK, &resolved)
	gt.Equal(t, resolved.ID, memo.ID)

	// move across workspaces: the old paper then points to the new place
	alice.expect("POST", memoBase+"/move", map[string]any{"workspace": "other"}, http.StatusBadRequest, nil) // a parent is required
	alice.expect("POST", memoBase+"/move", map[string]any{"workspace": "other", "parent": string(homeOf(alice, "other"))}, http.StatusOK, nil)
	var moved struct {
		Error struct {
			Code    string        `json:"code"`
			MovedTo model.Forward `json:"moved_to"`
		} `json:"error"`
	}
	bob.expect("GET", memoBase, nil, http.StatusNotFound, &moved)
	gt.Equal(t, moved.Error.Code, "moved")
	gt.Equal(t, moved.Error.MovedTo.ToWorkspaceID, model.WorkspaceID("other"))
	_ = base
}

func TestReadOnlyTokenAndAttachments(t *testing.T) {
	ts := newTestServer(t, false)
	alice, _, ws, paper := setupPaper(t, ts)
	base := "/api/v1/workspaces/" + string(ws) + "/papers/" + string(paper)
	ro := ts.token(alice, model.TokenPersonal, model.ScopeRead)
	ro.expect("GET", base, nil, http.StatusOK, nil)
	ro.expect("POST", base+"/ops", map[string]any{"ops": []map[string]any{{"op": "insert", "type": "paragraph"}}}, http.StatusForbidden, nil)

	// upload the raw image and read it back
	req := gt.R1(http.NewRequest("POST", ts.srv.URL+base+"/attachments?name=a.png", bytes.NewReader([]byte("\x89PNG")))).NoError(t)
	req.Header.Set("Content-Type", "image/png")
	req.Header.Set("X-Requested-With", "papers")
	resp := gt.R1(alice.hc.Do(req)).NoError(t)
	defer func() { _ = resp.Body.Close() }()
	gt.Equal(t, resp.StatusCode, http.StatusCreated)
	var att model.Attachment
	gt.NoError(t, decodeBody(resp, &att))
	get := ro.do("GET", base+"/attachments/"+string(att.ID), nil)
	gt.Equal(t, get.StatusCode, http.StatusOK)
	gt.Equal(t, get.Header.Get("Content-Type"), "image/png")
	gt.Equal(t, string(gt.R1(io.ReadAll(get.Body)).NoError(t)), "\x89PNG")
}

func TestTokenAPI(t *testing.T) {
	ts := newTestServer(t, false)
	alice := ts.login("alice@example.com")
	agent := ts.token(alice, model.TokenAgent, model.ScopeWrite)
	// tokens cannot manage tokens
	agent.expect("GET", "/api/v1/tokens", nil, http.StatusForbidden, nil)
	var list struct {
		Tokens []struct {
			ID model.TokenID `json:"id"`
		} `json:"tokens"`
	}
	alice.expect("GET", "/api/v1/tokens", nil, http.StatusOK, &list)
	gt.A(t, list.Tokens).Length(1)
	alice.expect("DELETE", "/api/v1/tokens/"+string(list.Tokens[0].ID), nil, http.StatusNoContent, nil)
	agent.expect("GET", "/api/v1/workspaces", nil, http.StatusUnauthorized, nil)
}

func TestTrashRunsTheJobAndAnswers(t *testing.T) {
	ts := newTestServerWith(t, false, func(c *usecase.Config) { c.JobStep = 1 })
	alice, _, ws, paper := setupPaper(t, ts)
	base := "/api/v1/workspaces/" + string(ws)
	var kid model.Paper
	alice.expect("POST", base+"/papers", map[string]any{"title": "Kid", "parent": string(paper)}, http.StatusCreated, &kid)

	var res struct {
		Paper model.Paper `json:"paper"`
		Jobs  []any       `json:"jobs"`
	}
	alice.expect("POST", base+"/papers/"+string(paper)+"/trash", nil, http.StatusOK, &res)
	gt.Equal(t, res.Paper.ID, paper)
	gt.A(t, res.Jobs).Length(0)
	var view paperView
	alice.expect("GET", base+"/papers/"+string(kid.ID), nil, http.StatusOK, &view)
	gt.Equal(t, view.Paper.DeletedWith, paper)
	alice.expect("GET", base+"/jobs/x", nil, http.StatusNotFound, nil)
}

func TestUnfiledAPI(t *testing.T) {
	ts := newTestServer(t, false)
	alice, bob, ws, runbook := setupPaper(t, ts)
	papers := "/api/v1/workspaces/" + string(ws) + "/papers"

	// without a parent a paper is unfiled and takes no children
	var m model.Paper
	alice.expect("POST", papers, map[string]any{"title": "Notes"}, http.StatusCreated, &m)
	gt.Equal(t, m.ParentID, model.PaperID(""))
	gt.Equal(t, m.Visibility, model.VisibilityPublic)
	mBase := papers + "/" + string(m.ID)
	alice.expect("POST", mBase+"/ops", map[string]any{"ops": []map[string]any{{"op": "insert", "type": "paper", "title": "Sub"}}}, http.StatusBadRequest, nil)
	alice.expect("POST", papers, map[string]any{"title": "Kid", "parent": string(m.ID)}, http.StatusBadRequest, nil)
	alice.expect("PUT", mBase+"/favorite", map[string]any{}, http.StatusNoContent, nil)
	var memo model.Paper
	alice.expect("POST", papers, map[string]any{"title": "Memo", "visibility": "private"}, http.StatusCreated, &memo)
	alice.expect("PUT", papers+"/"+string(memo.ID)+"/favorite", map[string]any{}, http.StatusBadRequest, nil)

	var sb struct {
		UnfiledCount int `json:"unfiled_count"`
	}
	alice.expect("GET", "/api/v1/workspaces/team/sidebar", nil, http.StatusOK, &sb)
	gt.V(t, sb.UnfiledCount).Equal(2)

	// the papers list: mine and unfiled by default
	type listed struct {
		Papers []struct {
			ID         model.PaperID    `json:"id"`
			Title      string           `json:"title"`
			Visibility model.Visibility `json:"visibility"`
			CreatedAt  *string          `json:"created_at"`
			UpdatedAt  *string          `json:"updated_at"`
			Parent     *struct {
				ID    model.PaperID `json:"id"`
				Title string        `json:"title"`
			} `json:"parent"`
		} `json:"papers"`
		NextCursor *string `json:"next_cursor"`
	}
	var page listed
	alice.expect("GET", papers, nil, http.StatusOK, &page)
	gt.A(t, page.Papers).Length(2)
	gt.Equal(t, *page.NextCursor, "")
	gt.NotNil(t, page.Papers[0].CreatedAt)
	gt.NotNil(t, page.Papers[0].UpdatedAt)
	gt.Nil(t, page.Papers[0].Parent)
	bob.expect("GET", papers, nil, http.StatusOK, &page)
	gt.A(t, page.Papers).Length(0)
	bob.expect("GET", papers+"?creator=all", nil, http.StatusOK, &page)
	gt.A(t, page.Papers).Length(1) // alice's unfiled paper, not her private one
	gt.Equal(t, page.Papers[0].ID, m.ID)
	alice.expect("GET", papers+"?place=filed&creator=all&visibility=public&archived=true&sort=created&limit=1", nil, http.StatusOK, &page)
	gt.A(t, page.Papers).Length(1)
	gt.Equal(t, page.Papers[0].ID, runbook)
	gt.Equal(t, page.Papers[0].Parent.ID, homeOf(alice, ws))
	gt.Equal(t, page.Papers[0].Parent.Title, "Team")
	alice.expect("GET", papers+"?limit=1", nil, http.StatusOK, &page)
	gt.A(t, page.Papers).Length(1)
	gt.NotEqual(t, *page.NextCursor, "")
	alice.expect("GET", papers+"?limit=1&cursor="+*page.NextCursor, nil, http.StatusOK, &page)
	gt.A(t, page.Papers).Length(1)
	gt.Equal(t, *page.NextCursor, "")
	for _, bad := range []string{"place=x", "creator=x", "visibility=x", "archived=x", "sort=x", "limit=0", "limit=101", "limit=x", "cursor=x"} {
		alice.expect("GET", papers+"?"+bad, nil, http.StatusBadRequest, nil)
	}

	// move under a parent, then back to unfiled
	alice.expect("POST", mBase+"/move", map[string]any{"parent": string(runbook)}, http.StatusOK, nil)
	var view paperView
	bob.expect("GET", mBase, nil, http.StatusOK, &view)
	gt.Equal(t, view.Paper.ParentID, runbook)
	gt.A(t, view.Path).Length(2)
	alice.expect("POST", mBase+"/move", map[string]any{}, http.StatusBadRequest, nil) // a parent or unfiled is required
	alice.expect("POST", mBase+"/move", map[string]any{"unfiled": true, "parent": string(runbook)}, http.StatusBadRequest, nil)
	alice.expect("POST", papers+"/"+string(runbook)+"/move", map[string]any{"unfiled": true}, http.StatusBadRequest, nil) // has a child
	alice.expect("POST", mBase+"/move", map[string]any{"unfiled": true}, http.StatusOK, nil)
	view = paperView{}
	bob.expect("GET", mBase, nil, http.StatusOK, &view)
	gt.Equal(t, view.Paper.ParentID, model.PaperID(""))
	gt.A(t, view.Path).Length(0)

	// a private paper made public without a parent is unfiled
	alice.expect("PUT", papers+"/"+string(memo.ID)+"/visibility", map[string]any{"visibility": "public"}, http.StatusOK, nil)
	view = paperView{}
	bob.expect("GET", papers+"/"+string(memo.ID), nil, http.StatusOK, &view)
	gt.Equal(t, view.Paper.ParentID, model.PaperID(""))
	gt.Equal(t, view.Paper.Visibility, model.VisibilityPublic)
}

func TestWorkspacesLastOpenedAPI(t *testing.T) {
	ts := newTestServer(t, false)
	alice, _, ws, paper := setupPaper(t, ts)
	type list struct {
		Workspaces []struct {
			ID         model.WorkspaceID `json:"id"`
			LastOpened *struct {
				PaperID  model.PaperID `json:"paper_id"`
				Title    string        `json:"title"`
				OpenedAt string        `json:"opened_at"`
			} `json:"last_opened"`
		} `json:"workspaces"`
	}
	var got list
	alice.expect("GET", "/api/v1/workspaces", nil, http.StatusOK, &got)
	gt.Nil(t, got.Workspaces[0].LastOpened)
	alice.expect("GET", "/api/v1/workspaces/"+string(ws)+"/papers/"+string(paper), nil, http.StatusOK, nil)
	alice.expect("GET", "/api/v1/workspaces", nil, http.StatusOK, &got)
	gt.Equal(t, got.Workspaces[0].ID, ws)
	gt.Equal(t, got.Workspaces[0].LastOpened.PaperID, paper)
	gt.Equal(t, got.Workspaces[0].LastOpened.Title, "Runbook")
	gt.NotEqual(t, got.Workspaces[0].LastOpened.OpenedAt, "")
	gt.Nil(t, got.Workspaces[1].LastOpened)
	// last_opened is always present, null when there is none
	resp := alice.do("GET", "/api/v1/workspaces", nil)
	body := string(gt.R1(io.ReadAll(resp.Body)).NoError(t))
	gt.True(t, strings.Contains(body, `"last_opened":null`))
}

const reportHTML = `<!doctype html><html><body><h1>Weekly</h1><p>fourteen incidents</p><script>document.body.dataset.ran = "1"</script></body></html>`

type htmlDoc struct {
	BlockID      model.BlockID      `json:"block_id"`
	Rev          int64              `json:"rev"`
	AttachmentID model.AttachmentID `json:"attachment_id"`
	Size         int64              `json:"size"`
	HTML         string             `json:"html"`
}

func TestHTMLPaperAPI(t *testing.T) {
	ts := newTestServer(t, false)
	alice := ts.login("alice@example.com")
	bob := ts.login("bob@example.com")
	home := homeOf(alice, ws)

	// T7: create with kind and html, under the home, in one request
	var p model.Paper
	alice.expect("POST", "/api/v1/workspaces/team/papers", map[string]any{"title": "Weekly", "parent": string(home), "kind": "html", "html": reportHTML}, http.StatusCreated, &p)
	gt.Equal(t, p.Kind, model.PaperKindHTML)
	base := "/api/v1/workspaces/team/papers/" + string(p.ID)
	var view paperView
	bob.expect("GET", base, nil, http.StatusOK, &view)
	gt.Equal(t, view.Paper.Kind, model.PaperKindHTML)
	// PaperRef in the parent's blocks says the kind too
	var raw map[string]any
	bob.expect("GET", "/api/v1/workspaces/team/papers/"+string(home)+"/blocks", nil, http.StatusOK, &raw)
	refs := raw["papers"].(map[string]any)
	gt.Equal(t, refs[string(p.ID)].(map[string]any)["kind"], "html")

	// T8: html with markdown, html for a blocks paper, html_paper without html
	alice.expect("POST", "/api/v1/workspaces/team/papers", map[string]any{"title": "x", "kind": "html", "html": reportHTML, "markdown": "# x"}, http.StatusBadRequest, nil)
	alice.expect("POST", "/api/v1/workspaces/team/papers", map[string]any{"title": "x", "html": reportHTML}, http.StatusBadRequest, nil)
	alice.expect("POST", "/api/v1/workspaces/team/papers", map[string]any{"title": "x", "kind": "html"}, http.StatusBadRequest, nil)

	// T23: read the document as JSON
	var doc htmlDoc
	bob.expect("GET", base+"/html", nil, http.StatusOK, &doc)
	gt.Equal(t, doc.HTML, reportHTML)
	gt.Equal(t, doc.Rev, int64(1))
	gt.Equal(t, doc.Size, int64(len(reportHTML)))
	bob.expect("GET", "/api/v1/workspaces/team/papers/"+string(home)+"/html", nil, http.StatusBadRequest, nil)

	// T18: title and HTML in one batch
	var res batchResp
	alice.expect("POST", base+"/ops", map[string]any{"ops": []map[string]any{
		{"op": "set_title", "title": "Weekly report"},
		{"op": "set_html", "html": "<p>nine incidents</p>", "base_rev": doc.Rev},
	}}, http.StatusOK, &res)
	gt.Equal(t, res.Paper.Title, "Weekly report")
	gt.A(t, res.Blocks).Length(1)
	gt.Equal(t, res.Blocks[0].Rev, int64(2))
	gt.Equal(t, res.Blocks[0].Type, model.BlockHTMLPage)

	// T19: a stale base answers 409 like any conflict, and changes nothing
	var conflict struct {
		Error struct {
			Code      string `json:"code"`
			Conflicts []struct {
				BlockID model.BlockID `json:"block_id"`
				Current model.Block   `json:"current"`
			} `json:"conflicts"`
		} `json:"error"`
	}
	alice.expect("POST", base+"/ops", map[string]any{"ops": []map[string]any{
		{"op": "set_title", "title": "lost"},
		{"op": "set_html", "html": "<p>lost</p>", "base_rev": 1},
	}}, http.StatusConflict, &conflict)
	gt.Equal(t, conflict.Error.Code, "conflict")
	gt.Equal(t, conflict.Error.Conflicts[0].Current.Rev, int64(2))
	bob.expect("GET", base, nil, http.StatusOK, &view)
	gt.Equal(t, view.Paper.Title, "Weekly report")

	// block ops on an HTML paper are refused
	alice.expect("POST", base+"/ops", map[string]any{"ops": []map[string]any{{"op": "insert", "type": "paragraph", "content": "x"}}}, http.StatusBadRequest, nil)

	// T35: no Markdown form
	bob.expect("GET", base+"/markdown", nil, http.StatusBadRequest, nil)

	// T46: the document is served raw and sandboxed, revalidated every time
	bob.expect("GET", base+"/html", nil, http.StatusOK, &doc)
	resp := bob.do("GET", base+"/html/"+string(doc.AttachmentID), nil)
	gt.Equal(t, resp.StatusCode, http.StatusOK)
	gt.Equal(t, resp.Header.Get("Content-Type"), "text/html; charset=utf-8")
	gt.Equal(t, resp.Header.Get("Content-Security-Policy"), "sandbox allow-scripts; frame-ancestors 'self'")
	gt.Equal(t, resp.Header.Get("Cache-Control"), "private, no-cache")
	etag := resp.Header.Get("ETag")
	gt.Equal(t, etag, `"`+string(doc.AttachmentID)+`.open"`)
	gt.Equal(t, resp.Header.Get("X-Content-Type-Options"), "nosniff")
	gt.Equal(t, string(gt.R1(io.ReadAll(resp.Body)).NoError(t)), "<p>nine incidents</p>")
	// revalidation answers 304 without the body while document and setting stay
	req304 := gt.R1(http.NewRequest("GET", ts.srv.URL+base+"/html/"+string(doc.AttachmentID), nil)).NoError(t)
	req304.Header.Set("If-None-Match", etag)
	not := gt.R1(bob.hc.Do(req304)).NoError(t)
	defer func() { _ = not.Body.Close() }()
	gt.Equal(t, not.StatusCode, http.StatusNotModified)
	gt.Equal(t, len(gt.R1(io.ReadAll(not.Body)).NoError(t)), 0)

	// T47: another paper's or an image attachment is not served here
	bob.expect("GET", "/api/v1/workspaces/team/papers/"+string(home)+"/html/"+string(doc.AttachmentID), nil, http.StatusNotFound, nil)
	bob.expect("GET", base+"/html/"+string(model.NewAttachmentID()), nil, http.StatusNotFound, nil)
	var mine model.Paper
	alice.expect("POST", "/api/v1/workspaces/team/papers", map[string]any{"title": "Mine", "visibility": "private", "kind": "html", "html": reportHTML}, http.StatusCreated, &mine)
	var mineDoc htmlDoc
	alice.expect("GET", "/api/v1/workspaces/team/papers/"+string(mine.ID)+"/html", nil, http.StatusOK, &mineDoc)
	bob.expect("GET", "/api/v1/workspaces/team/papers/"+string(mine.ID)+"/html/"+string(mineDoc.AttachmentID), nil, http.StatusNotFound, nil)

	// T45: HTML cannot be uploaded as an attachment
	req := gt.R1(http.NewRequest("POST", ts.srv.URL+"/api/v1/workspaces/team/papers/"+string(home)+"/attachments?name=a.html", strings.NewReader("<p>x</p>"))).NoError(t)
	req.Header.Set("Content-Type", "text/html")
	req.Header.Set("X-Requested-With", "papers")
	up := gt.R1(alice.hc.Do(req)).NoError(t)
	defer func() { _ = up.Body.Close() }()
	gt.Equal(t, up.StatusCode, http.StatusBadRequest)

	// block types list html_page as not editable by people
	var meta struct {
		BlockTypes []struct {
			Type          model.BlockType `json:"type"`
			HumanEditable bool            `json:"human_editable"`
		} `json:"block_types"`
	}
	alice.expect("GET", "/api/v1/block-types", nil, http.StatusOK, &meta)
	found := false
	for _, bt := range meta.BlockTypes {
		if bt.Type == model.BlockHTMLPage {
			found = true
			gt.False(t, bt.HumanEditable)
		}
	}
	gt.True(t, found)
}

func TestHTMLPaperSandboxSetting(t *testing.T) { // T54
	ts := newTestServer(t, false)
	alice := ts.login("alice@example.com")
	var list struct {
		Workspaces []struct {
			ID      model.WorkspaceID `json:"id"`
			Sandbox struct {
				ExternalResources bool `json:"external_resources"`
			} `json:"sandbox"`
		} `json:"workspaces"`
	}
	alice.expect("GET", "/api/v1/workspaces", nil, http.StatusOK, &list)
	open := map[model.WorkspaceID]bool{}
	for _, w := range list.Workspaces {
		open[w.ID] = w.Sandbox.ExternalResources
	}
	gt.True(t, open["team"])
	gt.False(t, open["other"])

	var p model.Paper
	alice.expect("POST", "/api/v1/workspaces/other/papers", map[string]any{"title": "Closed", "kind": "html", "html": reportHTML}, http.StatusCreated, &p)
	var doc htmlDoc
	alice.expect("GET", "/api/v1/workspaces/other/papers/"+string(p.ID)+"/html", nil, http.StatusOK, &doc)
	resp := alice.do("GET", "/api/v1/workspaces/other/papers/"+string(p.ID)+"/html/"+string(doc.AttachmentID), nil)
	gt.Equal(t, resp.StatusCode, http.StatusOK)
	gt.Equal(t, resp.Header.Get("Content-Security-Policy"), "sandbox allow-scripts; frame-ancestors 'self'; "+model.SandboxCSP)
	// the setting is part of the ETag, so changing it makes browsers fetch again
	gt.Equal(t, resp.Header.Get("ETag"), `"`+string(doc.AttachmentID)+`.closed"`)
}

func TestHTMLPaperRequestLimit(t *testing.T) { // T61
	ts := newTestServer(t, false)
	alice := ts.login("alice@example.com")
	// a document of 5 MiB, sent as JSON larger than the usual 4 MiB, is accepted
	doc := "<p>" + strings.Repeat("a", model.MaxHTMLSize-7) + "</p>"
	var p model.Paper
	alice.expect("POST", "/api/v1/workspaces/team/papers", map[string]any{"title": "Big", "kind": "html", "html": doc}, http.StatusCreated, &p)
	// a document escaped to six times its size (Go escapes < as \u003c) is accepted
	var esc model.Paper
	alice.expect("POST", "/api/v1/workspaces/team/papers", map[string]any{"title": "Escaped", "kind": "html", "html": strings.Repeat("<", model.MaxHTMLSize)}, http.StatusCreated, &esc)
	// a body over 32 MiB is refused
	// each escaped character is six bytes of JSON but one of HTML
	huge := strings.Repeat("\\u0041", (32<<20)/6+1)
	req := gt.R1(http.NewRequest("POST", ts.srv.URL+"/api/v1/workspaces/team/papers/"+string(p.ID)+"/ops", strings.NewReader(`{"ops":[{"op":"set_html","base_rev":1,"html":"`+huge+`"}]}`))).NoError(t)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "papers")
	resp := gt.R1(alice.hc.Do(req)).NoError(t)
	defer func() { _ = resp.Body.Close() }()
	gt.Equal(t, resp.StatusCode, http.StatusBadRequest)
	gt.S(t, string(gt.R1(io.ReadAll(resp.Body)).NoError(t))).Contains("too large")
	// other requests keep the 4 MiB limit
	big := strings.Repeat("a", 5<<20)
	alice.expect("PUT", "/api/v1/workspaces/team/papers/"+string(p.ID)+"/visibility", map[string]any{"visibility": "public", "parent": big}, http.StatusBadRequest, nil)
}

func TestUsersAPI(t *testing.T) {
	// T19, T21, T24 through REST
	ts := newTestServer(t, false)
	alice, bob, ws, _ := setupPaper(t, ts)
	type usersResp struct {
		Users []map[string]any `json:"users"`
	}
	var r usersResp
	alice.expect("GET", "/api/v1/workspaces/"+string(ws)+"/users?q=BOB", nil, http.StatusOK, &r)
	gt.A(t, r.Users).Length(1)
	_, hasEmail := r.Users[0]["email"]
	gt.False(t, hasEmail)
	bobID, _ := r.Users[0]["id"].(string)

	var byID usersResp
	alice.expect("GET", "/api/v1/workspaces/"+string(ws)+"/users?ids="+bobID+","+string(model.NewUserID()), nil, http.StatusOK, &byID)
	gt.A(t, byID.Users).Length(1)
	gt.Equal(t, byID.Users[0]["id"], any(bobID))

	alice.expect("GET", "/api/v1/workspaces/"+string(ws)+"/users?ids=short", nil, http.StatusBadRequest, nil)
	alice.expect("GET", "/api/v1/workspaces/"+string(ws)+"/users?ids="+bobID+"&q=b", nil, http.StatusBadRequest, nil)
	bob.expect("GET", "/api/v1/workspaces/nowhere/users", nil, http.StatusNotFound, nil)

	// T10 through REST: the papers list by a user's ID
	var page usecase.PapersPage
	alice.expect("GET", "/api/v1/workspaces/"+string(ws)+"/papers?place=all&creator="+bobID, nil, http.StatusOK, &page)
	gt.A(t, page.Papers).Length(0)
	alice.expect("GET", "/api/v1/workspaces/"+string(ws)+"/papers?creator=bob", nil, http.StatusBadRequest, nil)
}
