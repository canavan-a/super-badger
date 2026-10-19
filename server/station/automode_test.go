package station

import (
	"context"
	"encoding/json"
	"testing"

	"main/database"
)

func TestIsContextOverflow(t *testing.T) {
	overflow := []string{
		// llama.cpp, as seen live.
		`{"sessionID":"s","error":{"name":"APIError","data":{"message":"request (110096 tokens) exceeds the available context size (110080 tokens), try increasing it"}}}`,
		`{"sessionID":"s","error":{"name":"ContextOverflowError","data":{"message":"x"}}}`,
		`{"error":{"data":{"responseBody":"{\"error\":{\"code\":\"context_length_exceeded\"}}"}}}`,
		`{"error":{"data":{"message":"prompt is too long: 210000 tokens > 200000 maximum"}}}`,
	}
	for _, p := range overflow {
		if !IsContextOverflow(json.RawMessage(p)) {
			t.Errorf("not detected as overflow: %s", p)
		}
	}
	other := []string{
		`{"error":{"name":"MessageAbortedError","data":{"message":"Aborted"}}}`,
		`{"error":{"name":"APIError","data":{"message":"Cannot connect to API: Unable to connect."}}}`,
		`{"error":{"name":"APIError","data":{"message":"This request requires more credits, or fewer max_tokens."}}}`,
	}
	for _, p := range other {
		if IsContextOverflow(json.RawMessage(p)) {
			t.Errorf("wrongly detected as overflow: %s", p)
		}
	}
}

func TestAutoCompactCompactsThenContinues(t *testing.T) {
	svc, fake, id := newTestService(t)
	if _, err := svc.Update(id, map[string]any{"auto_mode": true, "auto_continue_prompt": "keep going"}); err != nil {
		t.Fatal(err)
	}
	var notified []map[string]any
	svc.SetNotifier(func(m map[string]any) { notified = append(notified, m) })

	st, _ := database.GetStation(svc.db, id)
	if !svc.beginAuto(id) {
		t.Fatal("first attempt refused")
	}
	if svc.beginAuto(id) {
		t.Fatal("second attempt allowed while one is running")
	}
	svc.autoCompact(context.Background(), st)

	if fake.compacts != 1 {
		t.Fatalf("compacts = %d, want 1", fake.compacts)
	}
	waitFor(t, "continue prompt sent", func() bool { return len(fake.sent()) == 1 })
	if got := fake.sent()[0]; len(got) != 1 || got[0] != "keep going" {
		t.Fatalf("sent %v, want the station's continue prompt", got)
	}
	if len(notified) != 1 || notified[0]["type"] != "auto_compacting" || notified[0]["station_id"] != id {
		t.Fatalf("notifications = %v", notified)
	}
	if svc.AutoCompacting(id) {
		t.Fatal("still marked as compacting")
	}
}

func TestAutoCompactDefaultPrompt(t *testing.T) {
	svc, fake, id := newTestService(t)
	if _, err := svc.Update(id, map[string]any{"auto_mode": true}); err != nil {
		t.Fatal(err)
	}
	st, _ := database.GetStation(svc.db, id)
	svc.beginAuto(id)
	svc.autoCompact(context.Background(), st)
	waitFor(t, "continue prompt sent", func() bool { return len(fake.sent()) == 1 })
	if got := fake.sent()[0][0]; got != DefaultContinuePrompt {
		t.Fatalf("sent %q, want the default prompt", got)
	}
}

func TestAutoAttemptsCapped(t *testing.T) {
	svc, _, id := newTestService(t)
	for i := 0; i < autoMaxAttempts; i++ {
		if !svc.beginAuto(id) {
			t.Fatalf("attempt %d refused", i+1)
		}
		svc.endAuto(id)
	}
	if svc.beginAuto(id) {
		t.Fatal("attempt past the cap allowed")
	}
}
