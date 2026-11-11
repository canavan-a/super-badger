package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

func ev(t *testing.T, s string) Event {
	t.Helper()
	e, err := ParseEvent([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func apply(t *testing.T, s *State, events ...string) {
	t.Helper()
	for _, e := range events {
		s.Apply(ev(t, e))
	}
}

func TestDeltaStreaming(t *testing.T) {
	s := New()
	apply(t, s,
		`{"type":"msg","messageID":"m1","role":"assistant"}`,
		`{"type":"part","messageID":"m1","partID":"m1/reasoning/0","kind":"reasoning"}`,
		`{"type":"part","messageID":"m1","partID":"m1/reasoning/0","kind":"reasoning","delta":"Let me "}`,
		`{"type":"part","messageID":"m1","partID":"m1/reasoning/0","kind":"reasoning","delta":"think"}`,
		// A delta for a part never started creates it.
		`{"type":"part","messageID":"m1","partID":"m1/text/0","kind":"text","delta":"Hel"}`,
		`{"type":"part","messageID":"m1","partID":"m1/text/0","kind":"text","delta":"lo"}`,
	)
	tr := s.Turn("m1")
	if r := tr.Parts["m1/reasoning/0"]; r.Kind != KindReasoning || r.Text != "Let me think" {
		t.Fatalf("reasoning = %+v", r)
	}
	if tr.Text() != "Hello" {
		t.Fatalf("text = %q", tr.Text())
	}
	if strings.Join(tr.PartOrder, ",") != "m1/reasoning/0,m1/text/0" {
		t.Fatalf("order = %v", tr.PartOrder)
	}
	// The ended event carries the full text, replacing.
	apply(t, s, `{"type":"part","messageID":"m1","partID":"m1/text/0","kind":"text","text":"Hello there"}`)
	if tr.Text() != "Hello there" {
		t.Fatalf("full text didn't replace: %q", tr.Text())
	}
}

func TestDeltaForUnknownTurnCreatesAnAssistantTurn(t *testing.T) {
	s := New()
	apply(t, s, `{"type":"part","messageID":"m9","partID":"m9/text/0","kind":"text","delta":"hi"}`)
	if tr := s.Turn("m9"); tr == nil || tr.Role != "assistant" || tr.Text() != "hi" {
		t.Fatalf("%+v", tr)
	}
}

func TestStaleHistoryReplayDoesNotClobberStreamedText(t *testing.T) {
	s := New()
	apply(t, s,
		`{"type":"msg","messageID":"m1","role":"assistant"}`,
		`{"type":"part","messageID":"m1","partID":"p1","kind":"text","text":"Hello wor"}`,
		`{"type":"part","messageID":"m1","partID":"p1","kind":"text","delta":"ld"}`,
		// The history page, fetched before the delta, lands after it.
		`{"type":"part","messageID":"m1","partID":"p1","kind":"text","text":"Hello wor"}`,
		// A started event (no text) for an existing part is a stale prefix too.
		`{"type":"part","messageID":"m1","partID":"p1","kind":"text"}`,
	)
	if got := s.Turn("m1").Parts["p1"].Text; got != "Hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestShorterNonPrefixRewriteIsApplied(t *testing.T) {
	s := New()
	apply(t, s,
		`{"type":"part","messageID":"m1","partID":"p1","kind":"text","text":"Hello world"}`,
		`{"type":"part","messageID":"m1","partID":"p1","kind":"text","text":"Bye"}`,
	)
	if got := s.Turn("m1").Parts["p1"].Text; got != "Bye" {
		t.Fatalf("got %q", got)
	}
}

func TestToolLifecycle(t *testing.T) {
	s := New()
	apply(t, s, `{"type":"part","messageID":"m1","partID":"call_1","kind":"tool","tool":"bash","status":"pending"}`)
	p := s.Turn("m1").Parts["call_1"]
	if p.Kind != KindTool || p.Status != "pending" || p.Tool != "bash" {
		t.Fatalf("pending = %+v", p)
	}
	apply(t, s, `{"type":"part","messageID":"m1","partID":"call_1","kind":"tool","tool":"bash","status":"running","input":{"command":"ls","description":"List files"}}`)
	p = s.Turn("m1").Parts["call_1"]
	in, _ := p.Input.(map[string]any)
	if p.Status != "running" || in["command"] != "ls" || p.Title != "List files" {
		t.Fatalf("running = %+v", p)
	}
	apply(t, s, `{"type":"part","messageID":"m1","partID":"call_1","kind":"tool","tool":"bash","status":"completed","input":{"command":"ls"},"output":"a\nb"}`)
	p = s.Turn("m1").Parts["call_1"]
	if p.Status != "completed" || p.Output != "a\nb" {
		t.Fatalf("completed = %+v", p)
	}
	apply(t, s, `{"type":"part","messageID":"m1","partID":"call_2","kind":"tool","tool":"read","status":"error","error":"no such file"}`)
	if p := s.Turn("m1").Parts["call_2"]; p.Status != "error" || p.Error != "no such file" {
		t.Fatalf("error = %+v", p)
	}
	if n := len(s.Turn("m1").PartOrder); n != 2 {
		t.Fatalf("updates must replace the part in place, got %d parts", n)
	}
}

func TestStatusAloneDrivesBusy(t *testing.T) {
	s := New()
	apply(t, s, `{"type":"status","status":"busy"}`)
	if !s.Busy {
		t.Fatal("busy status should set busy")
	}
	// Finishing a turn — even with a final finish reason — doesn't end the
	// reply; only status does.
	apply(t, s, `{"type":"msg","messageID":"a1","role":"assistant","done":true,"finish":"stop"}`)
	if !s.Busy || !s.Turn("a1").Done || s.Turn("a1").Finish != "stop" {
		t.Fatal("a finished turn must not clear busy")
	}
	apply(t, s, `{"type":"error","error":"boom","kind":"provider"}`)
	if !s.Busy || s.Error != "boom" {
		t.Fatal("an error sets the banner, not busy")
	}
	apply(t, s, `{"type":"status","status":"retry","attempt":2,"error":"rate limited"}`)
	if !s.Busy || s.Notice != "Retrying (attempt 2): rate limited" {
		t.Fatalf("retry: busy=%v notice=%q", s.Busy, s.Notice)
	}
	apply(t, s, `{"type":"status","status":"idle"}`)
	if s.Busy {
		t.Fatal("idle status should clear busy")
	}
	// A started assistant turn doesn't set busy either.
	apply(t, s, `{"type":"msg","messageID":"a2","role":"assistant"}`)
	if s.Busy {
		t.Fatal("a started turn must not set busy")
	}
}

func TestFailedStepKeepsItsError(t *testing.T) {
	s := New()
	apply(t, s, `{"type":"msg","messageID":"a1","role":"assistant","done":true,"finish":"error","error":"context overflow"}`)
	tr := s.Turn("a1")
	if !tr.Done || tr.Finish != "error" || tr.Error != "context overflow" {
		t.Fatalf("%+v", tr)
	}
	// A stale history copy without done can't reopen it.
	apply(t, s, `{"type":"msg","messageID":"a1","role":"assistant"}`)
	if !tr.Done {
		t.Fatal("done was undone")
	}
}

func TestCompactionMarkers(t *testing.T) {
	s := New()
	apply(t, s, `{"type":"compaction","phase":"started","reason":"auto"}`)
	if !s.Compacting || s.Notice != "Compacting…" {
		t.Fatalf("started: compacting=%v notice=%q", s.Compacting, s.Notice)
	}
	apply(t, s, `{"type":"compaction","phase":"ended","reason":"auto"}`)
	if s.Compacting || s.Notice != "Conversation compacted" {
		t.Fatalf("ended: compacting=%v notice=%q", s.Compacting, s.Notice)
	}
	apply(t, s, `{"type":"compaction","phase":"started","reason":"manual"}`,
		`{"type":"compaction","phase":"failed","reason":"manual","error":"x"}`)
	if s.Compacting {
		t.Fatal("failed should end compacting")
	}

	// History's compaction parts are transcript markers.
	h := New()
	apply(t, h,
		`{"type":"msg","messageID":"c1","role":"assistant"}`,
		`{"type":"part","messageID":"c1","partID":"c1/compaction","kind":"compaction","status":"running","reason":"auto"}`,
	)
	p := h.Turn("c1").Parts["c1/compaction"]
	if p.Kind != KindNotice || p.Text != "Compacting…" || !h.Compacting || !h.Turn("c1").Marker() {
		t.Fatalf("running marker = %+v compacting=%v", p, h.Compacting)
	}
	apply(t, h, `{"type":"part","messageID":"c1","partID":"c1/compaction","kind":"compaction","status":"completed","reason":"auto"}`)
	if p := h.Turn("c1").Parts["c1/compaction"]; p.Text != "Conversation compacted" || h.Compacting {
		t.Fatalf("completed marker = %+v", p)
	}
	apply(t, h, `{"type":"part","messageID":"c2","partID":"c2/compaction","kind":"compaction","status":"failed","error":"template mismatch"}`)
	if p := h.Turn("c2").Parts["c2/compaction"]; p.Text != "Compaction failed: template mismatch" {
		t.Fatalf("failed marker = %+v", p)
	}
}

func TestNoticePartOnSyntheticUserTurn(t *testing.T) {
	s := New()
	apply(t, s,
		`{"type":"msg","messageID":"s1","role":"user","done":true}`,
		`{"type":"part","messageID":"s1","partID":"s1/notice","kind":"notice","text":"Context carried over from the previous session"}`,
	)
	tr := s.Turn("s1")
	if tr.Role != "user" || !tr.Marker() || tr.Parts["s1/notice"].Text != "Context carried over from the previous session" {
		t.Fatalf("%+v", tr)
	}
}

func TestPermissionAndQuestion(t *testing.T) {
	s := New()
	apply(t, s, `{"type":"permission.asked","sessionID":"x","id":"r1","permission":"read","patterns":["/x"],"metadata":{"a":1}}`)
	if s.PendingPermission == nil || s.PendingPermission.ID != "r1" || s.PendingPermission.Patterns[0] != "/x" {
		t.Fatal("permission not set")
	}
	apply(t, s, `{"type":"permission.replied","id":"other"}`)
	if s.PendingPermission == nil {
		t.Fatal("a reply to another request must not clear this one")
	}
	apply(t, s, `{"type":"permission.replied","id":"r1"}`)
	if s.PendingPermission != nil {
		t.Fatal("permission not cleared")
	}
	apply(t, s, `{"type":"question.asked","id":"q1","questions":[{"key":"pick","question":"Pick","header":"H","multiple":true,"custom":true,"options":[{"label":"a","description":"A","value":"va"}]}]}`)
	q := s.PendingQuestion
	if q == nil || q.Key != "pick" || !q.Multiple || !q.Custom || q.Options[0].Label != "a" || q.Options[0].Value != "va" {
		t.Fatalf("bad question %+v", q)
	}
	if all, done := q.Answer([]string{"a"}); !done || len(all) != 1 || all[0][0] != "a" {
		t.Fatalf("answers = %v done=%v", all, done)
	}
	apply(t, s, `{"type":"question.rejected","id":"q1"}`)
	if s.PendingQuestion != nil {
		t.Fatal("question not cleared")
	}
}

func TestMultiQuestionStepsThrough(t *testing.T) {
	s := New()
	apply(t, s, `{"type":"question.asked","id":"q1","questions":[{"key":"a","question":"A?","options":[{"label":"x"}]},{"key":"b","question":"B?","custom":true}]}`)
	q := s.PendingQuestion
	if q.Count() != 2 || q.Index() != 0 || q.Key != "a" {
		t.Fatalf("%+v", q)
	}
	if _, done := q.Answer([]string{"x"}); done || q.Key != "b" || q.Question != "B?" || !q.Custom {
		t.Fatalf("should show the second question: %+v", q)
	}
	all, done := q.Answer([]string{"free text"})
	if !done || len(all) != 2 || all[0][0] != "x" || all[1][0] != "free text" {
		t.Fatalf("answers = %v", all)
	}
}

func TestSessionResetKeepsNoticeDropsTurns(t *testing.T) {
	s := New()
	s.AddLocalUserTurn("l", "x")
	apply(t, s, `{"type":"notice","text":"Session expired"}`, `{"type":"session_reset"}`)
	if len(s.Turns) != 0 || s.Notice != "Session expired" {
		t.Fatalf("%+v", s)
	}
}

func TestNoticeClearError(t *testing.T) {
	s := New()
	apply(t, s, `{"type":"error","error":"overflow"}`, `{"type":"notice","text":"compacting","clear_error":true}`)
	if s.Error != "" || s.Notice != "compacting" {
		t.Fatalf("%+v", s)
	}
}

func TestStepPartMapsToStepFinish(t *testing.T) {
	s := New()
	apply(t, s, `{"type":"part","messageID":"m","partID":"m/step","kind":"step","tokens":{"input":1000,"output":2000,"reasoning":7500,"cacheRead":69000,"cacheWrite":600,"ctx":72600}}`)
	p := s.Turn("m").Parts["m/step"]
	if p.Kind != KindStepFinish || p.Ctx != 72600 || p.Reasoning != 7500 || p.Tokens != 10500 {
		t.Errorf("%+v", p)
	}
}

func TestUsageEvents(t *testing.T) {
	s := New()
	apply(t, s, `{"type":"usage","tokens":{"input":10,"output":5,"ctx":7100},"cost":0.25,"contextLimit":12000}`)
	if s.Usage.Context() != 7100 || s.Usage.ContextLimit != 12000 || s.Usage.Cost != 0.25 {
		t.Fatalf("%+v", s.Usage)
	}
	// Live events may leave the limit out; the known one stays.
	apply(t, s, `{"type":"usage","tokens":{"ctx":8000},"cost":0.3}`)
	if s.Usage.Context() != 8000 || s.Usage.ContextLimit != 12000 {
		t.Fatalf("%+v", s.Usage)
	}
	apply(t, s, `{"type":"compaction","phase":"ended"}`)
	if s.Usage.Context() != 0 || s.Usage.ContextLimit != 12000 {
		t.Fatalf("compaction should drop the stale ctx, keep the limit: %+v", s.Usage)
	}
}

func TestRebuiltReplacesTurnsKeepsPrompts(t *testing.T) {
	s := New()
	s.fresh = false // "old" is from an earlier load
	s.AddLocalUserTurn("old", "x")
	apply(t, s, `{"type":"status","status":"busy"}`, `{"type":"permission.asked","id":"r1","permission":"bash"}`)
	n := s.Rebuilt([]json.RawMessage{
		json.RawMessage(`{"type":"msg","messageID":"u1","role":"user","done":true}`),
		json.RawMessage(`{"type":"part","messageID":"u1","partID":"u1/text/0","kind":"text","text":"hi"}`),
	})
	if n.Turn("old") != nil || n.Turn("u1").Text() != "hi" || !n.Busy || n.PendingPermission == nil {
		t.Fatalf("%+v", n)
	}
}

func TestPrependPutsOlderPageAboveAndKeepsIndex(t *testing.T) {
	s := New()
	apply(t, s, `{"type":"msg","messageID":"m3","role":"user"}`)
	s.Prepend([]json.RawMessage{
		json.RawMessage(`{"type":"msg","messageID":"m1","role":"user"}`),
		json.RawMessage(`{"type":"part","messageID":"m2","partID":"p2","kind":"text","text":"old"}`),
	})
	var ids []string
	for _, tr := range s.Turns {
		ids = append(ids, tr.MessageID)
	}
	if strings.Join(ids, ",") != "m1,m2,m3" {
		t.Fatalf("order = %v", ids)
	}
	// Live events after a prepend still land on the right (shifted) turn.
	apply(t, s, `{"type":"part","messageID":"m3","partID":"p3","kind":"text","text":"new"}`)
	if s.Turn("m3").Text() != "new" || s.Turn("m2").Text() != "old" {
		t.Fatal("turn index is stale after prepend")
	}
}

func turnIDs(s *State) string {
	var ids []string
	for _, t := range s.Turns {
		ids = append(ids, t.MessageID)
	}
	return strings.Join(ids, ",")
}

func TestRebuiltKeepsLiveTurnsNewerThanSnapshot(t *testing.T) {
	s := New()
	s.fresh = false
	apply(t, s,
		`{"type":"msg","messageID":"older","role":"user","done":true}`,
		`{"type":"msg","messageID":"u1","role":"user","done":true}`,
		// Arrived live while the history fetch was in flight.
		`{"type":"msg","messageID":"u2","role":"user","done":true}`,
		`{"type":"part","messageID":"u2","partID":"u2/text/0","kind":"text","text":"steer"}`)
	n := s.Rebuilt([]json.RawMessage{json.RawMessage(`{"type":"msg","messageID":"u1","role":"user","done":true}`)})
	if got := turnIDs(n); got != "u1,u2" {
		t.Fatalf("turns = %s, want u1,u2 (older dropped, live u2 kept)", got)
	}
	if n.Turn("u2").Text() != "steer" {
		t.Fatal("live turn lost its text")
	}
}

func TestRebuiltKeepsLiveGrowthInSharedTurn(t *testing.T) {
	s := New()
	s.fresh = false
	apply(t, s,
		`{"type":"msg","messageID":"a1","role":"assistant"}`,
		`{"type":"part","messageID":"a1","partID":"a1/text/0","kind":"text","text":"Hello world"}`,
		`{"type":"part","messageID":"a1","partID":"call1","kind":"tool","tool":"bash","status":"running"}`,
		`{"type":"msg","messageID":"a1","role":"assistant","done":true,"finish":"stop"}`)
	n := s.Rebuilt([]json.RawMessage{
		json.RawMessage(`{"type":"msg","messageID":"a1","role":"assistant"}`),
		json.RawMessage(`{"type":"part","messageID":"a1","partID":"a1/text/0","kind":"text","text":"Hello"}`),
	})
	a := n.Turn("a1")
	if a.Text() != "Hello world" || a.Parts["call1"] == nil || !a.Done || a.Finish != "stop" {
		t.Fatalf("live growth lost: text=%q tool=%v done=%v finish=%q", a.Text(), a.Parts["call1"], a.Done, a.Finish)
	}
}

func TestRebuiltFreshKeepsEverythingLiveUntilAnchored(t *testing.T) {
	// Opening a station mid-reply: the reply's deltas arrive before the first
	// page. They go after the page's turns, not before.
	s := New()
	apply(t, s, `{"type":"part","messageID":"a1","partID":"a1/text/0","kind":"text","delta":"Hel"}`)
	n := s.Rebuilt([]json.RawMessage{
		json.RawMessage(`{"type":"msg","messageID":"u1","role":"user","done":true}`),
		json.RawMessage(`{"type":"msg","messageID":"a1","role":"assistant"}`),
	})
	if got := turnIDs(n); got != "u1,a1" {
		t.Fatalf("turns = %s, want u1,a1", got)
	}
	if n.fresh {
		t.Fatal("a non-empty snapshot anchors the state")
	}
	// An empty snapshot (a new session) keeps it fresh, and the live turns.
	e := New()
	apply(t, e, `{"type":"msg","messageID":"u1","role":"user","done":true}`)
	if n := e.Rebuilt(nil); turnIDs(n) != "u1" || !n.fresh {
		t.Fatalf("empty snapshot dropped live turns: %s fresh=%v", turnIDs(n), n.fresh)
	}
}

func TestSessionResetKeepsContextLimitAndIsFresh(t *testing.T) {
	s := New()
	s.fresh = false
	apply(t, s, `{"type":"usage","contextLimit":32768}`, `{"type":"msg","messageID":"old","role":"user"}`, `{"type":"session_reset"}`)
	if len(s.Turns) != 0 || s.Usage.ContextLimit != 32768 || !s.fresh {
		t.Fatalf("after reset: turns=%d limit=%d fresh=%v", len(s.Turns), s.Usage.ContextLimit, s.fresh)
	}
}
