package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func wait(t *testing.T, c *Conn, pred func(Msg) bool) Msg {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-c.Out():
			if !ok {
				t.Fatal("channel closed")
			}
			if pred(m) {
				return m
			}
		case <-timeout:
			t.Fatal("timed out")
		}
	}
}

func TestSendReceiveAndReconnect(t *testing.T) {
	got := make(chan map[string]any, 4)
	conns := make(chan struct{}, 4)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conns <- struct{}{}
		conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"notice","text":"hi"}`))
		_, b, err := conn.ReadMessage()
		if err == nil {
			var m map[string]any
			json.Unmarshal(b, &m)
			got <- m
		}
		conn.Close() // force a reconnect
	}))
	defer srv.Close()

	c := Dial("ws" + strings.TrimPrefix(srv.URL, "http") + "?token=tok")
	defer c.Close()

	wait(t, c, func(m Msg) bool { return m.Status == "open" })
	if m := wait(t, c, func(m Msg) bool { return m.Frame != nil }); !strings.Contains(string(m.Frame), `"hi"`) {
		t.Fatalf("frame = %s", m.Frame)
	}
	if !c.Send(Prompt("hello")) {
		t.Fatal("send failed while open")
	}
	select {
	case m := <-got:
		if m["type"] != "prompt" || m["text"] != "hello" {
			t.Fatalf("server got %v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never got the prompt")
	}
	// Server closed on us: expect closed, then a fresh connection.
	wait(t, c, func(m Msg) bool { return m.Status == "closed" })
	wait(t, c, func(m Msg) bool { return m.Status == "open" })
	if len(conns) != 2 {
		t.Fatalf("expected 2 connections, got %d", len(conns))
	}
}

func TestSendDroppedWhileDisconnected(t *testing.T) {
	c := Dial("ws://127.0.0.1:1/nope")
	defer c.Close()
	if c.Send(Prompt("x")) {
		t.Fatal("send should fail with no connection")
	}
}
