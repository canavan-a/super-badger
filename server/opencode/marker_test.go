package opencode

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeCompactions serves GET .../message?type=compaction with whatever
// newest compaction message is set.
type fakeCompactions struct {
	mu     sync.Mutex
	newest string // JSON of one message, "" for none
}

func (f *fakeCompactions) set(id, status string, created int64) {
	f.mu.Lock()
	f.newest = fmt.Sprintf(`{"id":%q,"type":"compaction","status":%q,"reason":"auto","time":{"created":%d}}`, id, status, created)
	f.mu.Unlock()
}

func (f *fakeCompactions) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data := "[]"
	if f.newest != "" {
		data = "[" + f.newest + "]"
	}
	fmt.Fprintf(w, `{"data":%s,"cursor":{}}`, data)
}

func compactionEnv(sid, phase string, created int64) Envelope {
	return Envelope{
		Type:    "session.compaction." + phase,
		Created: created,
		Data:    []byte(fmt.Sprintf(`{"sessionID":%q,"reason":"auto"}`, sid)),
	}
}

// markers collects the compaction markers (part kind=compaction) arriving
// on ch within d, as "id:status".
func markers(ch <-chan Event, d time.Duration) []string {
	var out []string
	timeout := time.After(d)
	for {
		select {
		case e := <-ch:
			if e.Type == "part" && e.Kind == "compaction" {
				out = append(out, e.MessageID+":"+e.Status)
			}
		case <-timeout:
			return out
		}
	}
}

func TestCompactionMarkerSkipsOlderCompaction(t *testing.T) {
	fake := &fakeCompactions{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	b := NewEventBroker(NewClient(srv.URL, ""))
	ch, unsub := b.Subscribe("s1")
	defer unsub()

	// The new compaction isn't stored yet when it starts; the newest stored
	// one is from an hour ago and must not be sent out again.
	fake.set("old", "completed", 1_000)
	b.Dispatch(compactionEnv("s1", "started", 3_600_000))
	if got := markers(ch, 200*time.Millisecond); len(got) != 0 {
		t.Fatalf("started: got markers %v, want none (older compaction)", got)
	}

	fake.set("new", "completed", 3_600_010)
	b.Dispatch(compactionEnv("s1", "ended", 3_700_000))
	if got := markers(ch, 200*time.Millisecond); len(got) != 1 || got[0] != "new:completed" {
		t.Fatalf("ended: got markers %v, want [new:completed]", got)
	}
}

func TestCompactionMarkerUnseenStart(t *testing.T) {
	fake := &fakeCompactions{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	b := NewEventBroker(NewClient(srv.URL, ""))
	ch, unsub := b.Subscribe("s1")
	defer unsub()

	// super-badger started mid-compaction: no started event seen, so only
	// a recent compaction counts as the current one.
	fake.set("old", "completed", 1_000)
	b.Dispatch(compactionEnv("s1", "ended", 10_000_000))
	if got := markers(ch, 200*time.Millisecond); len(got) != 0 {
		t.Fatalf("got markers %v, want none (older compaction)", got)
	}
	fake.set("cur", "completed", 9_900_000)
	b.Dispatch(compactionEnv("s1", "ended", 10_000_000))
	if got := markers(ch, 200*time.Millisecond); len(got) != 1 || got[0] != "cur:completed" {
		t.Fatalf("got markers %v, want [cur:completed]", got)
	}
}

func TestCompactionMarkerRunningAfterFinished(t *testing.T) {
	fake := &fakeCompactions{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	b := NewEventBroker(NewClient(srv.URL, ""))
	ch, unsub := b.Subscribe("s1")
	defer unsub()

	since := b.markCompaction("s1", "started", 5_000)
	fake.set("c", "completed", 5_000)
	b.compactionMarker("s1", since)
	// A slow fetch from the started event that still read "running".
	fake.set("c", "running", 5_000)
	b.compactionMarker("s1", since)
	if got := markers(ch, 100*time.Millisecond); len(got) != 1 || got[0] != "c:completed" {
		t.Fatalf("got markers %v, want only [c:completed]", got)
	}
}
