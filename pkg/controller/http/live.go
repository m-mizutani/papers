package http

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/utils/async"
	"github.com/m-mizutani/papers/pkg/utils/errutil"
)

// Realtime protocol (JSON text frames, server -> client only).
//
//   {"type":"hello"}                   the feed is live; reload the paper now
//   {"type":"event","event":{...}}     block_put / block_deleted / paper_updated / paper_deleted
//   {"type":"closing","reason":"..."}  "resync", "access revoked" or "paper deleted"
//
// There are no client messages: writes go through the REST API (commits),
// and concurrent edits are resolved by block versions, not locks. Events
// carry resulting state, so applying them is idempotent (a block event older
// than the client's copy, by Version, is ignored). The client reloads the
// paper after every hello, which covers anything missed while disconnected.
// A connection lives at most as long as the platform's request timeout
// (Cloud Run: up to 60 minutes); clients then reconnect.

const (
	liveReadLimit    = 4 << 10
	liveWriteTimeout = 10 * time.Second
)

type liveServerMsg struct {
	Type   string       `json:"type"`
	Event  *model.Event `json:"event,omitempty"`
	Error  string       `json:"error,omitempty"`
	Reason string       `json:"reason,omitempty"`
}

// liveConn is the state of one WebSocket connection. It lives exactly as
// long as the connection (allowed in-memory state).
type liveConn struct {
	s     *Server
	conn  *websocket.Conn
	p     *model.Principal
	ws    model.WorkspaceID
	paper model.PaperID
}

func (c *liveConn) send(ctx context.Context, msg liveServerMsg) error {
	wctx, cancel := context.WithTimeout(ctx, liveWriteTimeout)
	defer cancel()
	if err := wsjson.Write(wctx, c.conn, msg); err != nil {
		return goerr.Wrap(err, "failed to write websocket message")
	}
	return nil
}

// forward relays change events to the client and closes the connection
// when the reader loses access, the paper is deleted, or the feed ended
// early (the client then reconnects and reloads).
func (c *liveConn) forward(ctx context.Context, events <-chan *model.Event) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return c.close(ctx, websocket.StatusTryAgainLater, "resync")
			}
			if err := c.send(ctx, liveServerMsg{Type: "event", Event: ev}); err != nil {
				return nil // the client is gone; the read loop ends the connection
			}
			switch ev.Type {
			case model.EventPaperDeleted:
				return c.close(ctx, websocket.StatusNormalClosure, "paper deleted")
			case model.EventPaperUpdated:
				ok, err := c.s.uc.CanRead(ctx, c.p, c.ws, c.paper)
				if err != nil {
					return err
				}
				if !ok {
					return c.close(ctx, websocket.StatusPolicyViolation, "access revoked")
				}
			}
		}
	}
}

func (c *liveConn) close(ctx context.Context, code websocket.StatusCode, reason string) error {
	if err := c.send(ctx, liveServerMsg{Type: "closing", Reason: reason}); err != nil {
		return nil
	}
	if err := c.conn.Close(code, reason); err != nil {
		return nil // already closed by the peer
	}
	return nil
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	wsID, paperID := wsParam(r), paperParam(r)

	// Authorize and start the change feed before upgrading, so a denied
	// client gets a plain HTTP error and nothing written after the hello
	// can be missed.
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	events, err := s.uc.Watch(watchCtx, p, wsID, paperID)
	if err != nil {
		writeError(ctx, w, err)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.wsOrigins})
	if err != nil {
		// Accept has already written the HTTP error response.
		errutil.Handle(ctx, goerr.Wrap(err, "websocket upgrade failed", goerr.T(errutil.TagBenign)), "websocket upgrade failed")
		return
	}
	defer conn.CloseNow() //nolint:errcheck // best effort after a normal close
	conn.SetReadLimit(liveReadLimit)

	c := &liveConn{s: s, conn: conn, p: p, ws: wsID, paper: paperID}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := c.send(ctx, liveServerMsg{Type: "hello"}); err != nil {
		return
	}

	async.DispatchCancelable(ctx, func(ctx context.Context) error {
		defer cancel()
		return c.forward(ctx, events)
	})

	// The client sends nothing; reading only notices when it goes away.
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return // closed by peer, timeout, or forward() ended the context
		}
		if err := c.send(ctx, liveServerMsg{Type: "error", Error: "the live feed takes no messages"}); err != nil {
			return
		}
	}
}
