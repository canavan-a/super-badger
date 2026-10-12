package station

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"main/database"
	"main/opencode"
)

// fakeOpencode stands in for the opencode HTTP API: it reports a settable
// session status and records every prompt_async body it receives.
type fakeOpencode struct {
	mu      sync.Mutex
	status  string // "" = idle (absent from /session/status, like opencode)
	fail    bool
	prompts [][]string
}

func (f *fakeOpencode) handler(sessionID string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /session/status", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		out := map[string]any{}
		if f.status != "" {
			out[sessionID] = map[string]string{"type": f.status}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("POST /session/{id}/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		texts := []string{}
		for _, p := range body.Parts {
			texts = append(texts, p.Text)
		}
		f.prompts = append(f.prompts, texts)
		f.status = "busy"
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /session/{id}/abort", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func (f *fakeOpencode) set(fn func(f *fakeOpencode)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeOpencode) sent() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.prompts...)
}

func newTestService(t *testing.T) (*Service, *fakeOpencode, uint) {
	t.Helper()
	db, err := database.Connect(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	const sessionID = "ses_test"
	fake := &fakeOpencode{}
	srv := httptest.NewServer(fake.handler(sessionID))
	t.Cleanup(srv.Close)
	oc := opencode.NewClient(srv.URL, "")
	svc := NewService(db, oc, opencode.NewEventBroker(oc))
	st := database.Station{Name: "s", ProviderID: "p", ModelID: "m", Agent: agentBuild,
		OpencodeSessionID: sessionID, Status: database.StatusActive}
	if err := database.CreateStation(db, &st); err != nil {
		t.Fatal(err)
	}
	return svc, fake, st.ID
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func queueLen(t *testing.T, svc *Service, id uint) int {
	t.Helper()
	q, err := svc.Queue(id)
	if err != nil {
		t.Fatal(err)
	}
	return len(q.Items)
}

func TestEnqueueWhileIdleSendsAtOnce(t *testing.T) {
	svc, fake, id := newTestService(t)
	if err := svc.Enqueue(id, "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "prompt", func() bool { return len(fake.sent()) == 1 })
	if got := fake.sent()[0]; len(got) != 1 || got[0] != "hello" {
		t.Fatalf("sent %v", got)
	}
	if n := queueLen(t, svc, id); n != 0 {
		t.Fatalf("queue len %d after send", n)
	}
}

func TestQueuedWhileBusySentTogetherOnIdle(t *testing.T) {
	svc, fake, id := newTestService(t)
	fake.set(func(f *fakeOpencode) { f.status = "busy" })
	_ = svc.Enqueue(id, "one")
	_ = svc.Enqueue(id, "two")
	time.Sleep(50 * time.Millisecond)
	if len(fake.sent()) != 0 {
		t.Fatalf("sent while busy: %v", fake.sent())
	}
	if n := queueLen(t, svc, id); n != 2 {
		t.Fatalf("queue len %d, want 2", n)
	}

	fake.set(func(f *fakeOpencode) { f.status = "" })
	svc.drain(context.Background(), id)
	got := fake.sent()
	if len(got) != 1 || strings.Join(got[0], ",") != "one,two" {
		t.Fatalf("sent %v, want one prompt [one two]", got)
	}
}

func TestNoOverlappingSendRightAfterPrompt(t *testing.T) {
	svc, fake, id := newTestService(t)
	_ = svc.Enqueue(id, "first")
	waitFor(t, "first prompt", func() bool { return len(fake.sent()) == 1 })
	// opencode hasn't caught up yet and still reports idle.
	fake.set(func(f *fakeOpencode) { f.status = "" })
	_ = svc.Enqueue(id, "second")
	time.Sleep(50 * time.Millisecond)
	if len(fake.sent()) != 1 {
		t.Fatalf("second message sent during the in-flight grace: %v", fake.sent())
	}
	if n := queueLen(t, svc, id); n != 1 {
		t.Fatalf("queue len %d, want 1", n)
	}
}

func TestFailedSendRestoresQueueAndReportsError(t *testing.T) {
	svc, fake, id := newTestService(t)
	msgs, unsubscribe := svc.SubscribeStation(id)
	defer unsubscribe()
	fake.set(func(f *fakeOpencode) { f.status = "busy" })
	_ = svc.Enqueue(id, "a")
	_ = svc.Enqueue(id, "b")
	fake.set(func(f *fakeOpencode) { f.status = ""; f.fail = true })
	svc.drain(context.Background(), id)

	q, _ := svc.Queue(id)
	if len(q.Items) != 2 || q.Items[0].Text != "a" || q.Items[1].Text != "b" {
		t.Fatalf("queue after failed send = %+v", q.Items)
	}
	for {
		select {
		case m := <-msgs:
			if e, ok := m.(map[string]any); ok && e["type"] == "error" {
				return
			}
		case <-time.After(time.Second):
			t.Fatal("no error published")
		}
	}
}

func TestRemoveQueued(t *testing.T) {
	svc, fake, id := newTestService(t)
	fake.set(func(f *fakeOpencode) { f.status = "busy" })
	_ = svc.Enqueue(id, "keep")
	_ = svc.Enqueue(id, "drop")
	q, _ := svc.Queue(id)
	if err := svc.RemoveQueued(id, q.Items[1].ID); err != nil {
		t.Fatal(err)
	}
	// Already gone (e.g. sent meanwhile): a no-op, not an error.
	if err := svc.RemoveQueued(id, q.Items[1].ID); err != nil {
		t.Fatal(err)
	}
	q, _ = svc.Queue(id)
	if len(q.Items) != 1 || q.Items[0].Text != "keep" {
		t.Fatalf("queue = %+v", q.Items)
	}
}

func TestAbortClearsQueue(t *testing.T) {
	svc, fake, id := newTestService(t)
	fake.set(func(f *fakeOpencode) { f.status = "busy" })
	_ = svc.Enqueue(id, "x")
	if err := svc.Abort(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if n := queueLen(t, svc, id); n != 0 {
		t.Fatalf("queue len %d after abort", n)
	}
	fake.set(func(f *fakeOpencode) { f.status = "" })
	svc.drain(context.Background(), id)
	if len(fake.sent()) != 0 {
		t.Fatalf("sent after abort: %v", fake.sent())
	}
}
