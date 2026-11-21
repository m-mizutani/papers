package http_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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

// setupPaper signs in alice and bob and has alice create a public paper
// with a body.
func setupPaper(t *testing.T, ts *testServer) (*client, *client, model.WorkspaceID, model.PaperID) {
	t.Helper()
	alice := ts.login("alice@example.com")
	bob := ts.login("bob@example.com")
	var p model.Paper
	alice.expect("POST", "/api/v1/workspaces/team/papers", map[string]any{"title": "Runbook", "markdown": "# Steps\n\n1. check\n2. fix"}, http.StatusCreated, &p)
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

	// search, sidebar, pins
	var hits struct {
		Hits []struct {
			Text string `json:"text"`
		} `json:"hits"`
	}
	bob.expect("GET", "/api/v1/workspaces/team/search?q=check", nil, http.StatusOK, &hits)
	gt.A(t, hits.Hits).Length(1)
	bob.expect("PUT", base+"/pin", map[string]any{}, http.StatusNoContent, nil)
	var sb struct {
		Pins []struct {
			ID model.PaperID `json:"id"`
		} `json:"pins"`
		Recent []struct {
			ID model.PaperID `json:"id"`
		} `json:"recent"`
	}
	bob.expect("GET", "/api/v1/workspaces/team/sidebar", nil, http.StatusOK, &sb)
	gt.Equal(t, sb.Pins[0].ID, paper)

	// only the creator deletes (with everything under it); others archive
	alice.expect("POST", base+"/trash", nil, http.StatusForbidden, nil) // bob's child is under it
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

	// move across workspaces leaves a forward
	var moved struct {
		Forward model.Forward `json:"forward"`
	}
	alice.expect("POST", memoBase+"/move", map[string]any{"workspace": "other"}, http.StatusOK, &moved)
	gt.Equal(t, moved.Forward.ToWorkspaceID, model.WorkspaceID("other"))
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

func TestBackgroundJobAPI(t *testing.T) {
	ts := newTestServerWith(t, false, func(c *usecase.Config) { c.JobInline, c.JobLease = 0, time.Nanosecond })
	alice, bob, ws, paper := setupPaper(t, ts)
	base := "/api/v1/workspaces/" + string(ws)
	var kid model.Paper
	alice.expect("POST", base+"/papers", map[string]any{"title": "Kid", "parent": string(paper)}, http.StatusCreated, &kid)

	var res struct {
		Jobs []model.Job `json:"jobs"`
	}
	alice.expect("POST", base+"/papers/"+string(paper)+"/trash", nil, http.StatusAccepted, &res)
	gt.A(t, res.Jobs).Length(1).Required()
	job := res.Jobs[0]
	gt.Equal(t, job.Kind, model.JobTrash)

	var got model.Job
	alice.expect("GET", base+"/jobs/"+string(job.ID), nil, http.StatusOK, &got)
	gt.False(t, got.State.Finished())
	bob.expect("GET", base+"/jobs/"+string(job.ID), nil, http.StatusNotFound, nil)

	// another instance (here: the sweeper) finishes it
	time.Sleep(time.Millisecond)
	gt.R1(ts.uc.RunJobs(context.Background())).NoError(t)
	alice.expect("GET", base+"/jobs/"+string(job.ID), nil, http.StatusOK, &got)
	gt.Equal(t, got.State, model.JobDone)
	gt.True(t, got.Result.Trashed)
	var view paperView
	alice.expect("GET", base+"/papers/"+string(kid.ID), nil, http.StatusOK, &view)
	gt.Equal(t, view.Paper.DeletedWith, paper)
}
