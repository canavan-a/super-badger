package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// replay feeds a recorded opencode 2.2.5 SSE fixture through readSSE and a
// fresh Translator, returning every super-badger event produced.
func replay(t *testing.T, name string) []Event {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "v2", name+".sse"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := NewTranslator()
	var out []Event
	_ = readSSE(f, func() {}, func(b []byte) {
		var env Envelope
		if err := json.Unmarshal(b, &env); err != nil {
			t.Fatalf("bad frame %s: %v", b, err)
		}
		out = append(out, tr.Translate(env)...)
	})
	return out
}

func ofType(evs []Event, typ string) []Event {
	var out []Event
	for _, e := range evs {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// transcript folds part events like a client does and returns, per message,
// the final parts.
type foldedPart struct {
	Kind, Text, Tool, Status, Output string
}

func fold(evs []Event) (order []string, msgs map[string][]foldedPart, done map[string]Event) {
	msgs = map[string][]foldedPart{}
	idx := map[string]map[string]int{}
	done = map[string]Event{}
	for _, e := range evs {
		switch e.Type {
		case "msg":
			if _, ok := idx[e.MessageID]; !ok {
				idx[e.MessageID] = map[string]int{}
				order = append(order, e.MessageID)
			}
			if e.Done {
				done[e.MessageID] = e
			}
		case "part":
			if _, ok := idx[e.MessageID]; !ok {
				idx[e.MessageID] = map[string]int{}
				order = append(order, e.MessageID)
			}
			i, ok := idx[e.MessageID][e.PartID]
			if !ok {
				i = len(msgs[e.MessageID])
				idx[e.MessageID][e.PartID] = i
				msgs[e.MessageID] = append(msgs[e.MessageID], foldedPart{})
			}
			p := &msgs[e.MessageID][i]
			p.Kind = e.Kind
			if e.Delta != "" {
				p.Text += e.Delta
			} else if e.Kind == "text" || e.Kind == "reasoning" {
				p.Text = e.Text
			}
			if e.Kind == "tool" {
				p.Tool, p.Status, p.Output = e.Tool, e.Status, e.Output
			}
		}
	}
	return
}

func TestTranslateTools(t *testing.T) {
	evs := replay(t, "tools")
	order, msgs, done := fold(evs)
	if len(order) != 3 {
		t.Fatalf("messages = %v, want user + 2 assistant", order)
	}
	user := msgs[order[0]]
	if len(user) != 1 || user[0].Text != "run SHELL please" {
		t.Fatalf("user turn = %+v", user)
	}
	step1 := msgs[order[1]]
	if step1[0].Kind != "tool" || step1[0].Tool != "shell" || step1[0].Status != "completed" || step1[0].Output != "hi\n" {
		t.Fatalf("tool part = %+v", step1[0])
	}
	if done[order[1]].Finish != "tool-calls" || done[order[2]].Finish != "stop" {
		t.Fatalf("finishes = %q, %q", done[order[1]].Finish, done[order[2]].Finish)
	}
	final := msgs[order[2]]
	if final[0].Kind != "reasoning" || final[0].Text != "thinking..." || final[1].Kind != "text" || !strings.HasPrefix(final[1].Text, "ok (") {
		t.Fatalf("final parts = %+v", final)
	}
	st := ofType(evs, "status")
	if len(st) != 2 || st[0].Status != "busy" || st[1].Status != "idle" {
		t.Fatalf("status = %+v", st)
	}
	// usage follows the last step, not the session's running total.
	us := ofType(evs, "usage")
	last := us[len(us)-1]
	if last.Tokens == nil || last.Tokens.Input != 5518 || last.Tokens.Ctx != 5518+8 {
		t.Fatalf("last usage = %+v", last.Tokens)
	}
}

func TestTranslatePermission(t *testing.T) {
	evs := replay(t, "permission")
	asked := ofType(evs, "permission.asked")
	if len(asked) != 1 || asked[0].Permission != "edit" || len(asked[0].Patterns) != 1 || asked[0].Patterns[0] != "hello.txt" || asked[0].ID == "" {
		t.Fatalf("asked = %+v", asked)
	}
	replied := ofType(evs, "permission.replied")
	if len(replied) != 1 || replied[0].ID != asked[0].ID {
		t.Fatalf("replied = %+v", replied)
	}
}

func TestTranslateQuestion(t *testing.T) {
	asked := ofType(replay(t, "question"), "question.asked")
	evs := replay(t, "question-reply")
	if len(asked) != 1 {
		t.Fatalf("asked = %+v", asked)
	}
	q := asked[0].Questions
	if len(q) != 1 || q[0].Key != "q0" || q[0].Question != "Pick a colour?" || q[0].Header != "Colour" || len(q[0].Options) != 2 || q[0].Options[1].Label != "Blue" || !q[0].Custom {
		t.Fatalf("questions = %+v", q)
	}
	if asked[0].SessionID == "" {
		t.Fatal("question has no session ID")
	}
	replied := ofType(evs, "question.replied")
	if len(replied) != 1 || replied[0].ID != asked[0].ID {
		t.Fatalf("replied = %+v", replied)
	}
}

func TestTranslateInterrupt(t *testing.T) {
	evs := replay(t, "interrupt")
	_, _, done := fold(evs)
	aborted := 0
	for _, d := range done {
		if d.Finish == "aborted" {
			aborted++
		}
	}
	if aborted != 1 {
		t.Fatalf("aborted messages = %d, want 1", aborted)
	}
	if len(ofType(evs, "error")) != 0 {
		t.Fatal("an interrupt must not surface as an error")
	}
	st := ofType(evs, "status")
	if st[len(st)-1].Status != "idle" {
		t.Fatalf("last status = %+v", st[len(st)-1])
	}
}

func TestTranslateSteer(t *testing.T) {
	order, msgs, _ := fold(replay(t, "steer"))
	var users []string
	for _, id := range order {
		if p := msgs[id]; len(p) == 1 && p[0].Kind == "text" && (p[0].Text == "SLOW SHELL first" || p[0].Text == "also this") {
			users = append(users, p[0].Text)
		}
	}
	if len(users) != 2 || users[0] != "SLOW SHELL first" || users[1] != "also this" {
		t.Fatalf("user turns = %v", users)
	}
}

func TestTranslateAutoCompact(t *testing.T) {
	evs := replay(t, "autocompact")
	c := ofType(evs, "compaction")
	if len(c) < 2 || c[0].Phase != "started" || c[0].Reason != "auto" || c[1].Phase != "ended" {
		t.Fatalf("compaction = %+v", c)
	}
	if len(ofType(evs, "error")) != 0 {
		t.Fatal("auto-compaction must not surface an error")
	}
}

func TestTranslateOverflowRecovered(t *testing.T) {
	evs := replay(t, "overflow")
	if len(ofType(evs, "compaction")) == 0 {
		t.Fatal("no compaction for the overflow")
	}
	if e := ofType(evs, "error"); len(e) != 0 {
		t.Fatalf("recovered overflow surfaced errors: %+v", e)
	}
}

func TestTranslateFailedCompaction(t *testing.T) {
	evs := replay(t, "heal")
	var failed, ended bool
	for _, e := range ofType(evs, "compaction") {
		switch e.Phase {
		case "failed":
			failed = true
			if !strings.Contains(e.Error, "template") {
				t.Fatalf("failed compaction error = %q", e.Error)
			}
		case "ended":
			if failed {
				ended = true
			}
		}
	}
	if !failed || !ended {
		t.Fatalf("want a failed compaction healed by a later one (failed=%v ended=%v)", failed, ended)
	}
	errs := ofType(evs, "error")
	if len(errs) == 0 || errs[0].Kind != "compaction" {
		t.Fatalf("errors = %+v, want a compaction-kind error", errs)
	}
}

func TestTranslateProviderDown(t *testing.T) {
	evs := replay(t, "down")
	var retries []Event
	for _, e := range ofType(evs, "status") {
		if e.Status == "retry" {
			retries = append(retries, e)
		}
	}
	if len(retries) == 0 || retries[0].Attempt < 1 || !strings.Contains(retries[0].Error, "ECONNREFUSED") {
		t.Fatalf("retries = %+v", retries)
	}
}

func TestStreamingOverlay(t *testing.T) {
	tr := NewTranslator()
	send := func(typ, data string) {
		tr.Translate(Envelope{Type: typ, Data: json.RawMessage(data)})
	}
	send("session.text.started", `{"sessionID":"s","assistantMessageID":"m","ordinal":0}`)
	send("session.text.delta", `{"sessionID":"s","assistantMessageID":"m","ordinal":0,"delta":"hel"}`)
	send("session.text.delta", `{"sessionID":"s","assistantMessageID":"m","ordinal":0,"delta":"lo"}`)
	got := tr.Streaming("s")
	if len(got) != 1 || got[0].Text != "hello" || got[0].PartID != "m/text/0" {
		t.Fatalf("streaming = %+v", got)
	}
	send("session.text.ended", `{"sessionID":"s","assistantMessageID":"m","ordinal":0,"text":"hello"}`)
	if len(tr.Streaming("s")) != 0 {
		t.Fatal("ended part still streaming")
	}
}

func TestFromMessagesMatchesLiveIDs(t *testing.T) {
	// The tools fixture's session as GET /api/session/:id/message returns it.
	raw := `[
	 {"id":"u1","type":"user","text":"run SHELL please","time":{"created":1}},
	 {"id":"a1","type":"assistant","finish":"tool-calls","time":{"created":2,"completed":3},"tokens":{"input":5480,"output":20,"reasoning":0,"cache":{"read":0,"write":0}},
	  "content":[{"type":"tool","id":"call_1","name":"shell","state":{"status":"completed","input":{"command":"echo hi"},"content":[{"type":"text","text":"hi\n"}]}}]},
	 {"id":"a2","type":"assistant","finish":"stop","time":{"created":4,"completed":5},"tokens":{"input":5518,"output":8,"reasoning":0,"cache":{"read":0,"write":0}},
	  "content":[{"type":"reasoning","text":"thinking..."},{"type":"text","text":"ok"}]},
	 {"id":"c1","type":"compaction","status":"completed","reason":"auto","time":{"created":6}},
	 {"id":"i1","type":"idle","outcome":"succeeded","time":{"created":7}}
	]`
	var msgs []Message
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		t.Fatal(err)
	}
	evs := FromMessages("s", msgs)
	order, parts, done := fold(evs)
	if strings.Join(order, ",") != "u1,a1,a2,c1" {
		t.Fatalf("order = %v", order)
	}
	ids := map[string]bool{}
	for _, e := range ofType(evs, "part") {
		ids[e.PartID] = true
	}
	for _, want := range []string{"u1/text/0", "call_1", "a1/step", "a2/reasoning/0", "a2/text/0", "c1/compaction"} {
		if !ids[want] {
			t.Errorf("missing part %s (have %v)", want, ids)
		}
	}
	if parts["a1"][0].Status != "completed" || parts["a1"][0].Output != "hi\n" {
		t.Fatalf("tool = %+v", parts["a1"][0])
	}
	if done["a1"].Finish != "tool-calls" || done["a2"].Finish != "stop" {
		t.Fatal("finishes lost")
	}
}

func TestLastUsageStopsAtCompaction(t *testing.T) {
	newest := []Message{
		{Type: "idle"},
		{Type: "compaction", Status: "completed"},
		{Type: "assistant", Tokens: &StepTokens{Input: 30000}},
	}
	if u := LastUsage("s", newest, 1); u != nil {
		t.Fatalf("usage before compaction reported: %+v", u)
	}
	newest = []Message{{Type: "assistant", Tokens: &StepTokens{Input: 900, Output: 100}}}
	if u := LastUsage("s", newest, 1); u == nil || u.Tokens.Ctx != 1000 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestErrorKind(t *testing.T) {
	cases := map[string]StructuredErr{
		"overflow":   {Type: "provider.invalid-request", Message: "request exceeds the context window: prompt (31000 tokens) + max tokens (4096) exceeds the context (32768)"},
		"compaction": {Type: "compaction.failed", Message: "Compaction summary did not match the required template"},
		"provider":   {Type: "provider.transport", Message: "ECONNREFUSED"},
		"aborted":    {Type: "aborted", Message: "Step interrupted"},
	}
	for want, e := range cases {
		if got := ErrorKind(&e); got != want {
			t.Errorf("ErrorKind(%+v) = %s, want %s", e, got, want)
		}
	}
	for _, s := range []string{
		"the request exceeds the available context size, try increasing it",
		"prompt (199990 tokens) leaves no room to answer in the context (200000)",
		"Context overflow: x. Please start a new session",
	} {
		if !IsOverflowText(s) {
			t.Errorf("not overflow: %s", s)
		}
	}
	if IsOverflowText("This request requires more credits, or fewer max_tokens.") {
		t.Error("credits error read as overflow")
	}
}

func TestReadSSE(t *testing.T) {
	in := ": heartbeat\n\ndata: {\"a\":1}\n\nevent: x\ndata: {\"b\":\ndata: 2}\n\n"
	var frames []string
	lines := 0
	_ = readSSE(strings.NewReader(in), func() { lines++ }, func(b []byte) { frames = append(frames, string(b)) })
	if len(frames) != 2 || frames[0] != `{"a":1}` || frames[1] != "{\"b\":\n2}" {
		t.Fatalf("frames = %q", frames)
	}
	if lines < 7 {
		t.Fatalf("activity on %d lines, want every line (comments too)", lines)
	}
}
