package station

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"main/database"
	"main/opencode"
)

// fakeOpencode stands in for the opencode 2 HTTP API: a settable run state,
// a record of every prompt, compaction, synthetic message and created
// session, and settable stored messages.
type fakeOpencode struct {
	mu         sync.Mutex
	status     string // "" = idle (absent from /api/session/active)
	fail       bool
	gone       map[string]bool // session IDs that 404
	prompts    [][]string      // one entry per prompt (a single text each)
	promptSess []string
	compacts   int
	synthetic  []string
	created    []string
	messages   map[string][]opencode.Message // newest first
	contextLim int
}

func (f *fakeOpencode) handler() http.Handler {
	mux := http.NewServeMux()
	wrap := func(w http.ResponseWriter, v any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
	}
	notFound := func(w http.ResponseWriter, id string) bool {
		if f.gone[id] {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"_tag":"SessionNotFoundError","sessionID":%q,"message":"Session not found: %s"}`, id, id)
			return true
		}
		return false
	}
	mux.HandleFunc("GET /api/session/active", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		out := map[string]any{}
		if f.status != "" {
			for _, id := range append([]string{"ses_test"}, f.created...) {
				out[id] = map[string]string{"type": f.status}
			}
		}
		wrap(w, out)
	})
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id := fmt.Sprintf("ses_new%d", len(f.created)+1)
		f.created = append(f.created, id)
		wrap(w, map[string]any{"id": id})
	})
	mux.HandleFunc("GET /api/session/{id}", func(w http.ResponseWriter, r *http.Request) {
		wrap(w, map[string]any{"id": r.PathValue("id"), "cost": 0.5})
	})
	mux.HandleFunc("POST /api/session/{id}/prompt", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text     string `json:"text"`
			Delivery string `json:"delivery"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if notFound(w, r.PathValue("id")) {
			return
		}
		if f.fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		f.prompts = append(f.prompts, []string{body.Text})
		f.promptSess = append(f.promptSess, r.PathValue("id"))
		f.status = "running"
		wrap(w, map[string]any{"id": fmt.Sprintf("msg_%d", len(f.prompts)), "delivery": body.Delivery})
	})
	mux.HandleFunc("POST /api/session/{id}/compact", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.compacts++
		wrap(w, map[string]any{"id": "msg_compact"})
	})
	mux.HandleFunc("POST /api/session/{id}/synthetic", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.synthetic = append(f.synthetic, r.PathValue("id")+": "+body.Text)
		wrap(w, map[string]any{"id": "msg_syn"})
	})
	mux.HandleFunc("POST /api/session/{id}/interrupt", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.status = ""
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"interrupted":true}`))
	})
	mux.HandleFunc("GET /api/session/{id}/message", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var out []opencode.Message
		for _, m := range f.messages[r.PathValue("id")] {
			if t := r.URL.Query().Get("type"); t == "" || t == m.Type {
				out = append(out, m)
			}
		}
		if out == nil {
			out = []opencode.Message{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out, "cursor": map[string]any{"next": nil}})
	})
	mux.HandleFunc("GET /api/model", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		wrap(w, []map[string]any{{"id": "m", "providerID": "p", "enabled": true, "limit": map[string]int{"context": f.contextLim, "output": 2048}}})
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

func (f *fakeOpencode) get(fn func(f *fakeOpencode) any) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(f)
}

const testSession = "ses_test"

func newTestService(t *testing.T) (*Service, *fakeOpencode, uint) {
	t.Helper()
	db, err := database.Connect(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeOpencode{gone: map[string]bool{}, messages: map[string][]opencode.Message{}, contextLim: 32768}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	oc := opencode.NewClient(srv.URL, "pw")
	svc := NewService(db, oc, opencode.NewEventBroker(oc))
	st := database.Station{Name: "s", ProviderID: "p", ModelID: "m", Agent: agentBuild,
		OpencodeSessionID: testSession, Status: database.StatusActive}
	if err := database.CreateStation(db, &st); err != nil {
		t.Fatal(err)
	}
	return svc, fake, st.ID
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
