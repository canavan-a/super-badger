package opencode

import (
	"encoding/json"
	"fmt"
	"testing"
)

func msgEvent(id string) Event {
	p, _ := json.Marshal(map[string]any{"info": map[string]any{"id": id, "role": "assistant"}})
	return Event{Type: "message.updated", Properties: p}
}

func partEvent(msgID, partID string) Event {
	p, _ := json.Marshal(map[string]any{"part": map[string]any{"id": partID, "messageID": msgID, "type": "text"}})
	return Event{Type: "message.part.updated", Properties: p}
}

func TestHistoryPage(t *testing.T) {
	b := NewEventBroker(nil)
	const sid = "s1"
	b.mu.Lock()
	for i := range 5 {
		m := fmt.Sprintf("m%d", i)
		b.record(sid, msgEvent(m))
		b.record(sid, partEvent(m, m+"p0"))
		b.record(sid, partEvent(m, m+"p1"))
	}
	b.record(sid, Event{Type: "session.idle", Properties: json.RawMessage(`{"sessionID":"s1"}`)})
	b.mu.Unlock()

	owners := func(evts []Event) []string {
		var out []string
		for _, e := range evts {
			out = append(out, e.Type+":"+eventMessageID(e))
		}
		return out
	}

	evts, cursor, more := b.HistoryPage(sid, "", 2)
	if cursor != "m3" || !more {
		t.Fatalf("page1 cursor=%q more=%v", cursor, more)
	}
	// 2 messages × (1 message + 2 parts) + the trailing session.idle.
	if len(evts) != 7 || evts[6].Type != "session.idle" {
		t.Fatalf("page1 events = %v", owners(evts))
	}

	evts, cursor, more = b.HistoryPage(sid, cursor, 2)
	if cursor != "m1" || !more || len(evts) != 6 || eventMessageID(evts[0]) != "m1" {
		t.Fatalf("page2 cursor=%q more=%v events=%v", cursor, more, owners(evts))
	}

	evts, cursor, more = b.HistoryPage(sid, cursor, 2)
	if cursor != "m0" || more || len(evts) != 3 {
		t.Fatalf("page3 cursor=%q more=%v events=%v", cursor, more, owners(evts))
	}

	evts, _, more = b.HistoryPage(sid, "gone", 2)
	if len(evts) != 0 || more {
		t.Fatalf("unknown cursor should yield empty page, got %v", owners(evts))
	}
}
