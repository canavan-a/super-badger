package api

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// keepaliveServer runs the same keepalive loop as stationWS/notificationsWS
// at a short interval and reports on gone when its reader gives up.
func keepaliveServer(t *testing.T, interval time.Duration, gone chan<- struct{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		beforeRead := armKeepalive(conn, interval)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				beforeRead()
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-done:
				close(gone)
				return
			case <-tick.C:
				if wsSendHeartbeat(conn) != nil {
					return
				}
			}
		}
	}))
}

func TestKeepaliveSurvivesLiveClient(t *testing.T) {
	gone := make(chan struct{})
	srv := keepaliveServer(t, 50*time.Millisecond, gone)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Reading lets gorilla's default ping handler answer with pongs, like
	// every real client does. Well past 3 intervals, it must still be up
	// and delivering heartbeat frames.
	beats := 0
	deadline := time.Now().Add(500 * time.Millisecond)
	_ = conn.SetReadDeadline(deadline.Add(time.Second))
	for time.Now().Before(deadline) {
		_, b, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("connection dropped after %d heartbeats: %v", beats, err)
		}
		if strings.Contains(string(b), `"heartbeat"`) {
			beats++
		}
	}
	if beats < 5 {
		t.Fatalf("got %d heartbeats, want at least 5", beats)
	}
	select {
	case <-gone:
		t.Fatal("server dropped a client that was answering pings")
	default:
	}
}

func TestKeepaliveDropsSilentClient(t *testing.T) {
	gone := make(chan struct{})
	srv := keepaliveServer(t, 50*time.Millisecond, gone)
	defer srv.Close()

	// A raw TCP handshake that never reads again: no pongs, like a phone
	// that vanished off the network without closing anything.
	u := "ws" + strings.TrimPrefix(srv.URL, "http")
	nc, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	conn, _, err := websocket.NewClient(nc, mustParse(t, u), nil, 1024, 1024)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn

	select {
	case <-gone:
	case <-time.After(2 * time.Second):
		t.Fatal("server never dropped a client that stopped answering pings")
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
