package station

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"main/database"
	"main/opencode"
)

func TestIsContextOverflow(t *testing.T) {
	overflow := []string{
		// llama.cpp, as seen live.
		"request (110096 tokens) exceeds the available context size (110080 tokens), try increasing it",
		"the request exceeds the available context size, try increasing it",
		// Strata, as seen live.
		"request exceeds the context window: prompt (169199 tokens) + max tokens (32000) exceeds the context (200000); requests are never truncated.",
		"request exceeds the context window: prompt (199990 tokens) leaves no room to answer in the context (200000); requests are never truncated",
		// opencode's own wording.
		"Context overflow: x. Please start a new session or use /compact to reduce context.",
		`{"error":{"code":"context_length_exceeded"}}`,
		"prompt is too long: 210000 tokens > 200000 maximum",
	}
	for _, p := range overflow {
		if !IsContextOverflow(p) {
			t.Errorf("not detected as overflow: %s", p)
		}
	}
	for _, p := range []string{
		"Aborted",
		"Cannot connect to API: Unable to connect.",
		"This request requires more credits, or fewer max_tokens.",
	} {
		if IsContextOverflow(p) {
			t.Errorf("wrongly detected as overflow: %s", p)
		}
	}
}

// opencode-side events for the tests, as the broker receives them.
func send(svc *Service, typ, data string) {
	svc.broker.Dispatch(opencode.Envelope{Type: typ, Data: json.RawMessage(data)})
}

func compactionEvent(svc *Service, sid, phase, reason string) {
	data := `{"sessionID":"` + sid + `","reason":"` + reason + `"`
	if phase == "failed" {
		data += `,"error":{"type":"compaction.failed","message":"Compaction summary did not match the required template"}`
	}
	send(svc, "session.compaction."+phase, data+"}")
}

// autoHarness runs RunAutoMode and records station notices and notifications.
type autoHarness struct {
	svc      *Service
	fake     *fakeOpencode
	id       uint
	mu       sync.Mutex
	notices  []string
	notified []map[string]any
	changed  []string
}

func newAutoHarness(t *testing.T, autoMode bool) *autoHarness {
	t.Helper()
	svc, fake, id := newTestService(t)
	if _, err := svc.Update(id, map[string]any{"auto_mode": autoMode, "auto_continue_prompt": "keep going"}); err != nil {
		t.Fatal(err)
	}
	h := &autoHarness{svc: svc, fake: fake, id: id}
	svc.SetNotifier(func(m map[string]any) { h.mu.Lock(); h.notified = append(h.notified, m); h.mu.Unlock() })
	msgs, unsub := svc.SubscribeStation(id)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); unsub() })
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-msgs:
				h.mu.Lock()
				switch v := m.(type) {
				case map[string]any:
					if v["type"] == "notice" {
						h.notices = append(h.notices, v["text"].(string))
					}
				case SessionChanged:
					h.changed = append(h.changed, v.SessionID)
				}
				h.mu.Unlock()
			}
		}
	}()
	go svc.RunAutoMode(ctx)
	// RunAutoMode subscribes asynchronously; give it a moment.
	time.Sleep(20 * time.Millisecond)
	return h
}

func (h *autoHarness) hasNotice(sub string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, n := range h.notices {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func (h *autoHarness) recovering(t *testing.T) {
	t.Helper()
	waitFor(t, "recovery to start", func() bool { return h.svc.AutoCompacting(h.id) })
}

func TestHealedCompactionContinuesInAutoMode(t *testing.T) {
	h := newAutoHarness(t, true)
	compactionEvent(h.svc, testSession, "failed", "auto")
	h.recovering(t)
	compactionEvent(h.svc, testSession, "started", "manual") // the plugin's redo
	compactionEvent(h.svc, testSession, "ended", "manual")

	waitFor(t, "continue prompt", func() bool { return len(h.fake.sent()) == 1 })
	if got := h.fake.sent()[0][0]; got != "keep going" {
		t.Fatalf("sent %q, want the station's continue prompt", got)
	}
	waitFor(t, "recovery to end", func() bool { return !h.svc.AutoCompacting(h.id) })
	if h.fake.get(func(f *fakeOpencode) any { return f.compacts }).(int) != 0 {
		t.Fatal("server compacted although the plugin's redo started")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.notified) != 1 || h.notified[0]["type"] != "auto_compacting" {
		t.Fatalf("notifications = %v", h.notified)
	}
}

func TestHealedCompactionWithoutAutoModeAsksForAMessage(t *testing.T) {
	h := newAutoHarness(t, false)
	compactionEvent(h.svc, testSession, "failed", "auto")
	h.recovering(t)
	compactionEvent(h.svc, testSession, "started", "manual")
	compactionEvent(h.svc, testSession, "ended", "manual")
	waitFor(t, "notice", func() bool { return h.hasNotice("Send a message to continue") })
	if len(h.fake.sent()) != 0 {
		t.Fatalf("sent %v without auto mode", h.fake.sent())
	}
}

func TestManualCompactionFailureHealedSendsNothing(t *testing.T) {
	h := newAutoHarness(t, true)
	compactionEvent(h.svc, testSession, "failed", "manual")
	h.recovering(t)
	compactionEvent(h.svc, testSession, "started", "manual")
	compactionEvent(h.svc, testSession, "ended", "manual")
	waitFor(t, "notice", func() bool { return h.hasNotice("Compacted.") })
	time.Sleep(50 * time.Millisecond)
	if len(h.fake.sent()) != 0 {
		t.Fatalf("continued a turn a manual compaction didn't cut short: %v", h.fake.sent())
	}
}

func TestServerRedoesCompactionWhenPluginDoesnt(t *testing.T) {
	old := healStart
	healStart = 30 * time.Millisecond
	t.Cleanup(func() { healStart = old })
	h := newAutoHarness(t, true)
	compactionEvent(h.svc, testSession, "failed", "auto")
	waitFor(t, "server compaction", func() bool { return h.fake.get(func(f *fakeOpencode) any { return f.compacts }).(int) == 1 })
	compactionEvent(h.svc, testSession, "started", "manual")
	compactionEvent(h.svc, testSession, "ended", "manual")
	waitFor(t, "continue prompt", func() bool { return len(h.fake.sent()) == 1 })
}

func TestFailedRedoRollsOver(t *testing.T) {
	h := newAutoHarness(t, true)
	h.fake.set(func(f *fakeOpencode) {
		// Newest first, as opencode lists them.
		f.messages[testSession] = []opencode.Message{
			{ID: "a2", Type: "assistant", Content: json.RawMessage(`[{"type":"reasoning","text":"hmm"},{"type":"text","text":"Patched main.go."}]`)},
			{ID: "u2", Type: "user", Text: "now fix the bug in main.go"},
			{ID: "c1", Type: "compaction", Status: "completed", Summary: "## Objective\n- Port the server to opencode 2."},
			{ID: "u1", Type: "user", Text: "an old message the summary covers"},
		}
	})
	compactionEvent(h.svc, testSession, "failed", "auto")
	h.recovering(t)
	compactionEvent(h.svc, testSession, "started", "manual")
	compactionEvent(h.svc, testSession, "failed", "manual")

	waitFor(t, "continue prompt", func() bool { return len(h.fake.sent()) == 1 })
	st, _ := database.GetStation(h.svc.db, h.id)
	if st.OpencodeSessionID != "ses_new1" {
		t.Fatalf("station session = %q, want the new one", st.OpencodeSessionID)
	}
	if s := h.fake.get(func(f *fakeOpencode) any { return f.promptSess[0] }); s != "ses_new1" {
		t.Fatalf("continue went to %v", s)
	}
	syn := h.fake.get(func(f *fakeOpencode) any { return strings.Join(f.synthetic, "\n") }).(string)
	for _, want := range []string{"ses_new1: ", "Port the server to opencode 2.", "User: now fix the bug in main.go", "Assistant: Patched main.go."} {
		if !strings.Contains(syn, want) {
			t.Errorf("seed lacks %q:\n%s", want, syn)
		}
	}
	for _, unwanted := range []string{"an old message", "hmm"} {
		if strings.Contains(syn, unwanted) {
			t.Errorf("seed has %q:\n%s", unwanted, syn)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.changed) != 1 || h.changed[0] != "ses_new1" {
		t.Fatalf("SessionChanged = %v", h.changed)
	}
}

func TestOverflowInAutoModeCompactsThenContinues(t *testing.T) {
	h := newAutoHarness(t, true)
	send(h.svc, "session.execution.failed", `{"sessionID":"ses_test","error":{"type":"provider.invalid-request","message":"request exceeds the context window: prompt (33000 tokens) leaves no room to answer in the context (32768)"}}`)
	waitFor(t, "compaction", func() bool { return h.fake.get(func(f *fakeOpencode) any { return f.compacts }).(int) == 1 })
	compactionEvent(h.svc, testSession, "started", "manual")
	compactionEvent(h.svc, testSession, "ended", "manual")
	waitFor(t, "continue prompt", func() bool { return len(h.fake.sent()) == 1 })
}

func TestOverflowWithoutAutoModeIsLeftAlone(t *testing.T) {
	h := newAutoHarness(t, false)
	send(h.svc, "session.execution.failed", `{"sessionID":"ses_test","error":{"type":"provider.invalid-request","message":"the request exceeds the available context size, try increasing it"}}`)
	time.Sleep(100 * time.Millisecond)
	if h.svc.AutoCompacting(h.id) || h.fake.get(func(f *fakeOpencode) any { return f.compacts }).(int) != 0 {
		t.Fatal("recovered an overflow without auto mode")
	}
}

func TestContinuesCapped(t *testing.T) {
	svc, _, id := newTestService(t)
	for i := 0; i < autoMaxAttempts; i++ {
		if !svc.allowContinue(id) {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if svc.allowContinue(id) {
		t.Fatal("attempt past the cap allowed")
	}
}

func TestWaitCompaction(t *testing.T) {
	ch := make(chan opencode.Event, 4)
	ch <- opencode.Event{Type: "compaction", Phase: "failed", Error: "bad"}
	ch <- opencode.Event{Type: "compaction", Phase: "started"}
	ch <- opencode.Event{Type: "compaction", Phase: "ended"}
	if err := waitCompaction(context.Background(), ch, time.Second); err != nil {
		t.Fatalf("redone compaction reported %v", err)
	}
	ch <- opencode.Event{Type: "compaction", Phase: "failed", Error: "bad"}
	if err := waitCompaction(context.Background(), ch, 20*time.Millisecond); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("err = %v", err)
	}
}

func TestFormAnswer(t *testing.T) {
	f := opencode.Form{Fields: []opencode.FormField{
		{Key: "q0", Type: "string", Options: []opencode.FormOption{{Value: "blue", Label: "Blue"}}},
		{Key: "q1", Type: "multiselect", Options: []opencode.FormOption{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}},
		{Key: "q2", Type: "string"},
	}}
	got := FormAnswer(f, [][]string{{"Blue"}, {"A", "B"}, {"typed"}})
	b, _ := json.Marshal(got)
	if string(b) != `{"q0":"blue","q1":["a","b"],"q2":"typed"}` {
		t.Fatalf("answer = %s", b)
	}
	b, _ = json.Marshal(FormAnswer(f, nil))
	if string(b) != `{"q0":"","q1":[],"q2":""}` {
		t.Fatalf("empty answer = %s", b)
	}
}
