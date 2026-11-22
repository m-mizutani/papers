package http_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/utils/async"
)

type liveMsg struct {
	Type   string       `json:"type"`
	Event  *model.Event `json:"event"`
	Error  string       `json:"error"`
	Reason string       `json:"reason"`
}

func dial(t *testing.T, ts *testServer, c *client, path string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{}
	for _, ck := range c.hc.Jar.Cookies(nil) {
		header.Add("Cookie", ck.Name+"="+ck.Value)
	}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.srv.URL, "http")+path, &websocket.DialOptions{HTTPHeader: header})
	gt.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func read(t *testing.T, conn *websocket.Conn, want string) liveMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var m liveMsg
		gt.NoError(t, wsjson.Read(ctx, conn, &m))
		if m.Type == want {
			return m
		}
	}
}

func readEvent(t *testing.T, conn *websocket.Conn, want model.EventType) *model.Event {
	t.Helper()
	for {
		m := read(t, conn, "event")
		if m.Event.Type == want {
			return m.Event
		}
	}
}

func write(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gt.NoError(t, wsjson.Write(ctx, conn, v))
}

func TestLiveConnection(t *testing.T) {
	ts := newTestServer(t, false)
	alice, bob, ws, paper := setupPaper(t, ts)
	base := "/api/v1/workspaces/" + string(ws) + "/papers/" + string(paper)
	var page blocksPage
	alice.expect("GET", base+"/blocks", nil, http.StatusOK, &page)
	block := page.Blocks[0]

	ac := dial(t, ts, alice, base+"/live")
	read(t, ac, "hello")
	bc := dial(t, ts, bob, base+"/live")
	read(t, bc, "hello")

	// alice writes; bob receives the resulting block
	alice.expect("POST", base+"/ops", map[string]any{"ops": []map[string]any{{"op": "update", "block_id": block.ID, "content": "live!", "base_rev": block.Rev}}}, http.StatusOK, nil)
	ev := readEvent(t, bc, model.EventBlockPut)
	gt.Equal(t, ev.Block.ID, block.ID)
	gt.Equal(t, ev.Block.Content, "live!")
	gt.Equal(t, ev.Block.Rev, block.Rev+1)

	// presence goes through HTTP and arrives on the feed
	alice.expect("POST", base+"/presence", map[string]any{"block_id": block.ID}, http.StatusNoContent, nil)
	pe := readEvent(t, bc, model.EventPresence)
	gt.Equal(t, pe.Presence.BlockID, block.ID)

	// the feed takes no messages from clients
	write(t, ac, map[string]any{"type": "lock", "block_id": block.ID})
	gt.NotEqual(t, read(t, ac, "error").Error, "")
	gt.NoError(t, ac.Close(websocket.StatusNormalClosure, "bye"))

	// sending the paper to the trash closes bob's connection
	alice.expect("POST", base+"/trash", nil, http.StatusOK, nil)
	m := read(t, bc, "closing")
	gt.Equal(t, m.Reason, "access revoked")

	async.Wait()
}

func TestLiveRequiresAccess(t *testing.T) {
	ts := newTestServer(t, false)
	alice := ts.login("alice@example.com")
	var memo model.Paper
	alice.expect("POST", "/api/v1/workspaces/team/papers", map[string]any{"title": "Memo", "visibility": "private"}, http.StatusCreated, &memo)
	carol := ts.login("carol@example.com")
	resp := carol.do("GET", "/api/v1/workspaces/team/papers/"+string(memo.ID)+"/live", nil)
	gt.Equal(t, resp.StatusCode, http.StatusNotFound)
}
