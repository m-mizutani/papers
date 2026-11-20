package http

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/utils/async"
	"github.com/m-mizutani/papers/pkg/utils/errutil"
)

// Realtime protocol (JSON text frames).
//
// Client -> server:
//   {"type":"lock","block_id":"..."}    acquire / renew a soft lock
//   {"type":"unlock","block_id":"..."}  release it
//   {"type":"heartbeat"}                renew every lock held by this connection
//
// Server -> client:
//   {"type":"hello","connection_id":"...","lock_ttl_ms":30000}
//   {"type":"lock_result","block_id":"...","ok":true|false,"lock":{...}}
//   {"type":"lock_lost","block_id":"...","lock":{...}}
//   {"type":"event","event":{...}}      commit / lock / unlock / access_changed
//   {"type":"closing","reason":"..."}
//
// A connection lives at most as long as the platform's request timeout
// (Cloud Run: up to 60 minutes); clients reconnect and resynchronise from
// the paper's revision.

const (
	liveReadLimit    = 64 << 10
	liveWriteTimeout = 10 * time.Second
)

type liveClientMsg struct {
	Type    string        `json:"type"`
	BlockID model.BlockID `json:"block_id,omitempty"`
}

type liveServerMsg struct {
	Type         string           `json:"type"`
	ConnectionID string           `json:"connection_id,omitempty"`
	LockTTLMs    int64            `json:"lock_ttl_ms,omitempty"`
	BlockID      model.BlockID    `json:"block_id,omitempty"`
	OK           *bool            `json:"ok,omitempty"`
	Lock         *model.BlockLock `json:"lock,omitempty"`
	Event        *model.Event     `json:"event,omitempty"`
	Error        string           `json:"error,omitempty"`
	Reason       string           `json:"reason,omitempty"`
}

// liveConn is the state of one WebSocket connection. It lives exactly as
// long as the connection (allowed in-memory state).
type liveConn struct {
	s     *Server
	conn  *websocket.Conn
	p     *model.Principal
	ws    model.WorkspaceID
	paper model.PaperID
	id    string
	held  map[model.BlockID]struct{}
}

func (c *liveConn) send(ctx context.Context, msg liveServerMsg) error {
	wctx, cancel := context.WithTimeout(ctx, liveWriteTimeout)
	defer cancel()
	if err := wsjson.Write(wctx, c.conn, msg); err != nil {
		return goerr.Wrap(err, "failed to write websocket message")
	}
	return nil
}

func boolPtr(v bool) *bool { return &v }

func (c *liveConn) lock(ctx context.Context, id model.BlockID) error {
	l, err := c.s.uc.AcquireLock(ctx, c.p, c.ws, c.paper, id, c.id)
	if err != nil {
		if errors.Is(err, model.ErrBlockLocked) || errors.Is(err, model.ErrNotFound) || errors.Is(err, model.ErrPermissionDenied) || errors.Is(err, model.ErrInvalidArgument) {
			return c.send(ctx, liveServerMsg{Type: "lock_result", BlockID: id, OK: boolPtr(false), Lock: l, Error: err.Error()})
		}
		return err
	}
	c.held[id] = struct{}{}
	return c.send(ctx, liveServerMsg{Type: "lock_result", BlockID: id, OK: boolPtr(true), Lock: l})
}

func (c *liveConn) unlock(ctx context.Context, id model.BlockID) error {
	delete(c.held, id)
	return c.s.uc.ReleaseLock(ctx, c.p, c.ws, c.paper, id, c.id)
}

func (c *liveConn) heartbeat(ctx context.Context) error {
	for id := range c.held {
		l, err := c.s.uc.AcquireLock(ctx, c.p, c.ws, c.paper, id, c.id)
		if err == nil {
			continue
		}
		delete(c.held, id)
		if errors.Is(err, model.ErrBlockLocked) || errors.Is(err, model.ErrNotFound) {
			if err := c.send(ctx, liveServerMsg{Type: "lock_lost", BlockID: id, Lock: l}); err != nil {
				return err
			}
			continue
		}
		return err
	}
	return nil
}

func (c *liveConn) releaseAll(ctx context.Context) {
	for id := range c.held {
		if err := c.s.uc.ReleaseLock(ctx, c.p, c.ws, c.paper, id, c.id); err != nil {
			errutil.Handle(ctx, err, "failed to release lock on disconnect")
		}
	}
}

// forward relays bus events to the client and closes the connection when
// the reader loses access or the paper is deleted.
func (c *liveConn) forward(ctx context.Context, events <-chan *model.Event) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return c.close(ctx, websocket.StatusGoingAway, "subscription ended")
			}
			if err := c.send(ctx, liveServerMsg{Type: "event", Event: ev}); err != nil {
				return nil // the client is gone; the read loop ends the connection
			}
			switch ev.Type {
			case model.EventDeleted:
				return c.close(ctx, websocket.StatusNormalClosure, "paper deleted")
			case model.EventAccess:
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

	// Authorize before upgrading so a denied client gets a plain HTTP error.
	events, unsubscribe, err := s.uc.Subscribe(ctx, p, wsID, paperID)
	if err != nil {
		writeError(ctx, w, err)
		return
	}
	defer unsubscribe()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.wsOrigins})
	if err != nil {
		// Accept has already written the HTTP error response.
		errutil.Handle(ctx, goerr.Wrap(err, "websocket upgrade failed", goerr.T(errutil.TagBenign)), "websocket upgrade failed")
		return
	}
	defer conn.CloseNow() //nolint:errcheck // best effort after a normal close
	conn.SetReadLimit(liveReadLimit)

	id, err := randomHex(16)
	if err != nil {
		errutil.Handle(ctx, err, "failed to create connection id")
		return
	}
	c := &liveConn{s: s, conn: conn, p: p, ws: wsID, paper: paperID, id: "conn:" + id, held: map[model.BlockID]struct{}{}}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer c.releaseAll(context.WithoutCancel(ctx))

	if err := c.send(ctx, liveServerMsg{Type: "hello", ConnectionID: c.id, LockTTLMs: s.uc.LockTTL()}); err != nil {
		return
	}

	async.DispatchCancelable(ctx, func(ctx context.Context) error {
		defer cancel()
		return c.forward(ctx, events)
	})

	for {
		var msg liveClientMsg
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return // closed by peer, timeout, or forward() ended the context
		}
		var herr error
		switch msg.Type {
		case "lock":
			herr = c.lock(ctx, msg.BlockID)
		case "unlock":
			herr = c.unlock(ctx, msg.BlockID)
		case "heartbeat":
			herr = c.heartbeat(ctx)
		default:
			herr = c.send(ctx, liveServerMsg{Type: "error", Error: "unknown message type"})
		}
		if herr != nil {
			if ctx.Err() == nil {
				errutil.Handle(ctx, herr, "live connection failed")
			}
			return
		}
	}
}
