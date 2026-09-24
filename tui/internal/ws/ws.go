// Package ws is the client for a station's chat WebSocket
// (/stations/:id/ws) and the global notifications feed (/notifications/ws).
// Both auto-reconnect 2s after any close, like useStationChat.ts.
package ws

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const reconnectDelay = 2 * time.Second

// Frame is one raw JSON text frame, or a connection status change.
type Msg struct {
	Frame  json.RawMessage // set for data
	Status string          // "connecting"|"open"|"closed" when Frame is nil
}

type Conn struct {
	url    string
	out    chan Msg
	send   chan []byte
	cancel context.CancelFunc
	mu     sync.Mutex
	cur    *websocket.Conn
}

// Dial starts a reconnecting connection. Read frames from Out(); call Close
// to stop it.
func Dial(url string) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{url: url, out: make(chan Msg, 256), send: make(chan []byte, 16), cancel: cancel}
	go c.run(ctx)
	return c
}

func (c *Conn) Out() <-chan Msg { return c.out }

func (c *Conn) emit(ctx context.Context, m Msg) {
	select {
	case c.out <- m:
	case <-ctx.Done():
	}
}

func (c *Conn) run(ctx context.Context) {
	defer close(c.out)
	for ctx.Err() == nil {
		c.emit(ctx, Msg{Status: "connecting"})
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.url, nil)
		if err == nil {
			c.mu.Lock()
			c.cur = conn
			c.mu.Unlock()
			c.emit(ctx, Msg{Status: "open"})
			c.pump(ctx, conn)
			c.mu.Lock()
			c.cur = nil
			c.mu.Unlock()
			conn.Close()
		}
		if ctx.Err() != nil {
			return
		}
		c.emit(ctx, Msg{Status: "closed"})
		select {
		case <-time.After(reconnectDelay):
		case <-ctx.Done():
			return
		}
	}
}

func (c *Conn) pump(ctx context.Context, conn *websocket.Conn) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, b, err := conn.ReadMessage()
			if err != nil {
				return
			}
			c.emit(ctx, Msg{Frame: b})
		}
	}()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case b := <-c.send:
			// This goroutine is the only writer, as gorilla requires.
			if conn.WriteMessage(websocket.TextMessage, b) != nil {
				return
			}
		}
	}
}

// Send queues a JSON frame. Frames sent while disconnected are dropped
// rather than replayed after a reconnect (a stale prompt or permission reply
// against a new session would be wrong).
func (c *Conn) Send(v any) bool {
	c.mu.Lock()
	open := c.cur != nil
	c.mu.Unlock()
	if !open {
		return false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	select {
	case c.send <- b:
		return true
	default:
		return false
	}
}

// Reconnect drops the current connection so the loop re-dials (e.g. after a
// station reset, when the server resolves the new session on connect).
func (c *Conn) Reconnect() {
	c.mu.Lock()
	if c.cur != nil {
		c.cur.Close()
	}
	c.mu.Unlock()
}

func (c *Conn) Close() { c.cancel(); c.Reconnect() }

// Client → server frames (server/api/ws.go wsClientMessage).

func Prompt(text string) map[string]any { return map[string]any{"type": "prompt", "text": text} }

func PermissionReply(id, reply string) map[string]any {
	return map[string]any{"type": "permission_reply", "request_id": id, "reply": reply}
}

func QuestionReply(id string, answers [][]string) map[string]any {
	return map[string]any{"type": "question_reply", "request_id": id, "answers": answers}
}

func QuestionReject(id string) map[string]any {
	return map[string]any{"type": "question_reject", "request_id": id}
}
