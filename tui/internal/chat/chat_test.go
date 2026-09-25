package chat

import "testing"

func ev(t *testing.T, s string) Event {
	t.Helper()
	e, err := ParseEvent([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestStaleHistoryReplayDoesNotClobberStreamedText(t *testing.T) {
	s := New()
	s.Apply(ev(t, `{"type":"message.updated","properties":{"info":{"id":"m1","role":"assistant"}}}`))
	s.Apply(ev(t, `{"type":"message.part.updated","properties":{"part":{"id":"p1","messageID":"m1","type":"text","text":"Hello wor"}}}`))
	s.Apply(ev(t, `{"type":"message.part.delta","properties":{"messageID":"m1","partID":"p1","field":"text","delta":"ld"}}`))
	s.Apply(ev(t, `{"type":"message.part.updated","properties":{"part":{"id":"p1","messageID":"m1","type":"text","text":"Hello wor"}}}`))
	if got := s.Turn("m1").Parts["p1"].Text; got != "Hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestShorterNonPrefixRewriteIsApplied(t *testing.T) {
	s := New()
	s.Apply(ev(t, `{"type":"message.part.updated","properties":{"part":{"id":"p1","messageID":"m1","type":"text","text":"Hello world"}}}`))
	s.Apply(ev(t, `{"type":"message.part.updated","properties":{"part":{"id":"p1","messageID":"m1","type":"text","text":"Bye"}}}`))
	if got := s.Turn("m1").Parts["p1"].Text; got != "Bye" {
		t.Fatalf("got %q", got)
	}
}

func TestCompletedAssistantMessageClearsBusy(t *testing.T) {
	s := New()
	s.Busy = true
	s.Apply(ev(t, `{"type":"message.updated","properties":{"info":{"id":"m1","role":"assistant","time":{"completed":1}}}}`))
	if s.Busy || !s.Turn("m1").Done {
		t.Fatal("busy should clear and turn be done")
	}
}

func TestReconcileLocalUserTurn(t *testing.T) {
	s := New()
	s.AddLocalUserTurn("local-1", "hi")
	s.ReconcileLocalUserTurn("local-1", "real")
	s.Apply(ev(t, `{"type":"message.part.updated","properties":{"part":{"id":"p","messageID":"real","type":"text","text":"hi"}}}`))
	if len(s.Turns) != 1 || s.Turn("real").Text() != "hi" || s.Turn("local-1") != nil {
		t.Fatalf("bad reconcile: %+v", s.Turns)
	}
}

func TestPermissionAndQuestion(t *testing.T) {
	s := New()
	s.Apply(ev(t, `{"type":"permission.asked","properties":{"id":"r1","permission":"read","patterns":["/x"]}}`))
	if s.PendingPermission == nil || s.PendingPermission.Patterns[0] != "/x" {
		t.Fatal("permission not set")
	}
	s.Apply(ev(t, `{"type":"permission.replied","properties":{}}`))
	if s.PendingPermission != nil {
		t.Fatal("permission not cleared")
	}
	s.Apply(ev(t, `{"type":"question.asked","properties":{"id":"q1","questions":[{"question":"Pick","header":"H","multiple":true,"custom":true,"options":[{"label":"a","description":"A"}]}]}}`))
	q := s.PendingQuestion
	if q == nil || !q.Multiple || !q.Custom || q.Options[0].Label != "a" {
		t.Fatalf("bad question %+v", q)
	}
}

func TestSessionResetKeepsNoticeDropsTurns(t *testing.T) {
	s := New()
	s.AddLocalUserTurn("l", "x")
	s.Apply(ev(t, `{"type":"notice","text":"Session expired"}`))
	s.Apply(ev(t, `{"type":"session_reset"}`))
	if len(s.Turns) != 0 || s.Notice != "Session expired" {
		t.Fatalf("%+v", s)
	}
}

func TestErrorFrames(t *testing.T) {
	s := New()
	s.Busy = true
	s.Apply(ev(t, `{"type":"error","error":"boom"}`))
	if s.Busy || s.Error != "boom" {
		t.Fatal("error not applied")
	}
}

func TestStepFinishSplitsContextFromThinking(t *testing.T) {
	s := New()
	s.Apply(ev(t, `{"type":"message.part.updated","properties":{"part":{"id":"sf","messageID":"m","type":"step-finish","tokens":{"total":80100,"input":1000,"output":2000,"reasoning":7500,"cache":{"read":69000,"write":600}}}}}`))
	p := s.Turn("m").Parts["sf"]
	if p.Ctx != 72600 {
		t.Errorf("ctx = %d, want 72600 (input+output+cache, the top bar's basis)", p.Ctx)
	}
	if p.Reasoning != 7500 || p.Tokens != 80100 {
		t.Errorf("reasoning=%d total=%d", p.Reasoning, p.Tokens)
	}
	// The gap between the provider's total and the context is the thinking.
	if p.Tokens-p.Ctx != p.Reasoning {
		t.Errorf("total-ctx = %d, want the thinking tokens %d", p.Tokens-p.Ctx, p.Reasoning)
	}
}

func TestStepFinishWithOnlyATotalStillWorks(t *testing.T) {
	s := New()
	s.Apply(ev(t, `{"type":"message.part.updated","properties":{"part":{"id":"sf","messageID":"m","type":"step-finish","tokens":{"total":1234}}}}`))
	if p := s.Turn("m").Parts["sf"]; p.Tokens != 1234 || p.Ctx != 0 {
		t.Errorf("%+v", p)
	}
}
