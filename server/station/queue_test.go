package station

import (
	"context"
	"strings"
	"testing"
	"time"

	"main/database"
)

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

func TestEnqueueWhileBusySendsAtOnce(t *testing.T) {
	svc, fake, id := newTestService(t)
	fake.set(func(f *fakeOpencode) { f.status = "busy" })
	_ = svc.Enqueue(id, "mid-reply")
	waitFor(t, "prompt", func() bool { return len(fake.sent()) == 1 })
	if got := fake.sent()[0]; len(got) != 1 || got[0] != "mid-reply" {
		t.Fatalf("sent %v", got)
	}
	if n := queueLen(t, svc, id); n != 0 {
		t.Fatalf("queue len %d after send", n)
	}
}

func TestSentInQueueOrder(t *testing.T) {
	svc, fake, id := newTestService(t)
	for _, text := range []string{"one", "two", "three"} {
		_ = svc.Enqueue(id, text)
	}
	var got []string
	waitFor(t, "all three sent", func() bool {
		got = nil
		for _, p := range fake.sent() {
			got = append(got, p...)
		}
		return len(got) == 3
	})
	if strings.Join(got, ",") != "one,two,three" {
		t.Fatalf("sent in order %v", got)
	}
}

func TestFailedSendRestoresQueueAndReportsError(t *testing.T) {
	svc, fake, id := newTestService(t)
	msgs, unsubscribe := svc.SubscribeStation(id)
	defer unsubscribe()
	fake.set(func(f *fakeOpencode) { f.fail = true })
	_, _ = database.EnqueueMessage(svc.db, id, "a")
	_, _ = database.EnqueueMessage(svc.db, id, "b")
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

func TestRetriedSendKeepsOrderAheadOfNewMessages(t *testing.T) {
	svc, fake, id := newTestService(t)
	fake.set(func(f *fakeOpencode) { f.fail = true })
	_, _ = database.EnqueueMessage(svc.db, id, "old")
	svc.drain(context.Background(), id)
	fake.set(func(f *fakeOpencode) { f.fail = false })
	_, _ = database.EnqueueMessage(svc.db, id, "new")
	svc.drain(context.Background(), id)
	got := fake.sent()
	if len(got) != 2 || got[0][0] != "old" || got[1][0] != "new" {
		t.Fatalf("sent %v, want [old] then [new]", got)
	}
}

func TestRemoveQueued(t *testing.T) {
	svc, _, id := newTestService(t)
	// Straight into the table: Enqueue would send them at once.
	_, _ = database.EnqueueMessage(svc.db, id, "keep")
	_, _ = database.EnqueueMessage(svc.db, id, "drop")
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
	_, _ = database.EnqueueMessage(svc.db, id, "x")
	if err := svc.Abort(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if n := queueLen(t, svc, id); n != 0 {
		t.Fatalf("queue len %d after abort", n)
	}
	svc.drain(context.Background(), id)
	if len(fake.sent()) != 0 {
		t.Fatalf("sent after abort: %v", fake.sent())
	}
}

func TestExpiredSessionIsReplaced(t *testing.T) {
	svc, fake, id := newTestService(t)
	msgs, unsubscribe := svc.SubscribeStation(id)
	defer unsubscribe()
	fake.set(func(f *fakeOpencode) { f.gone[testSession] = true })
	if err := svc.Enqueue(id, "hi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "prompt on the new session", func() bool { return len(fake.sent()) == 1 })
	st, _ := database.GetStation(svc.db, id)
	if st.OpencodeSessionID != "ses_new1" {
		t.Fatalf("session = %q", st.OpencodeSessionID)
	}
	var changed, reset bool
	for !changed || !reset {
		select {
		case m := <-msgs:
			if c, ok := m.(SessionChanged); ok && c.SessionID == "ses_new1" {
				changed = true
			}
			if e, ok := m.(map[string]any); ok && e["type"] == "session_reset" {
				reset = true
			}
		case <-time.After(time.Second):
			t.Fatalf("changed=%v reset=%v", changed, reset)
		}
	}
}

func TestStationWithoutSessionGetsOneOnSend(t *testing.T) {
	svc, fake, id := newTestService(t)
	_ = database.UpdateStationSession(svc.db, id, "", database.StatusIdle) // the opencode 2 cutover
	if err := svc.Enqueue(id, "hi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "prompt", func() bool { return len(fake.sent()) == 1 })
	if s := fake.get(func(f *fakeOpencode) any { return f.promptSess[0] }); s != "ses_new1" {
		t.Fatalf("prompted session %v", s)
	}
}
