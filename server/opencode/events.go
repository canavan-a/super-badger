package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"
)

// Event is an opencode SSE event, kept generic (raw Properties) rather than
// modeled per-type — super-badger just forwards these to a station's
// WebSocket client, which cares about `type` and `properties.part`/`info`
// (message.part.updated / message.updated), not the full event catalogue.
type Event struct {
	ID         string          `json:"id,omitempty"`
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

// SessionID extracts properties.sessionID, present on every session-scoped
// event type we care about (message.updated, message.part.updated, ...).
func (e Event) SessionID() string {
	var p struct {
		SessionID string `json:"sessionID"`
	}
	_ = json.Unmarshal(e.Properties, &p)
	return p.SessionID
}

// historyLimit caps how many distinct message/part entities are kept per
// session — enough to reconstruct many turns of transcript on reconnect, not
// a full unbounded log. Keyed by session ID, so a Station reset (new session
// ID) naturally starts empty rather than inheriting the old session's
// history; ClearHistory additionally drops the old entry outright so it
// doesn't just sit there unused.
//
// This counts structural entities (one slot per message, one per part), not
// raw events: message.part.delta events (tens per second while a reply
// streams) are folded into the part they target in place rather than each
// taking their own slot — see mergeDelta. Counting raw events was the
// original design, and it meant one sufficiently long streamed reply could
// burn through the entire cap in delta events alone, evicting every earlier
// turn's message.updated/message.part.updated entries — confirmed live as
// "only the last reply is ever visible after a reconnect." It also meant
// refreshing mid-generation could lose the in-progress reply entirely: if
// its own message.part.updated (the entry deltas merge into) had already
// been evicted by its own deltas, replay had nothing to merge them onto.
const historyLimit = 500

// EventBroker holds one long-lived subscription to opencode's global /event
// SSE stream and fans events out to per-session subscribers, so N station
// WebSocket connections share a single upstream connection to opencode
// rather than each opening their own. It also keeps a small recent-events
// cache per session so a client that navigates away and back (or a fresh
// page load) can restore a bit of transcript instead of starting blank.
type EventBroker struct {
	client *Client

	mu           sync.Mutex
	subs         map[string]map[chan Event]struct{} // sessionID -> subscriber channels
	allSubs      map[chan Event]struct{}            // subscribers to every session's events, regardless of ID
	history      map[string][]Event                 // sessionID -> recent events, capped at historyLimit
	historyIndex map[string]map[string]int          // sessionID -> entity key -> index into history[sessionID]
}

func NewEventBroker(client *Client) *EventBroker {
	return &EventBroker{
		client:       client,
		subs:         make(map[string]map[chan Event]struct{}),
		allSubs:      make(map[chan Event]struct{}),
		history:      make(map[string][]Event),
		historyIndex: make(map[string]map[string]int),
	}
}

// History returns a copy of the recent events cached for sessionID (empty if
// none — including if the session was reset/cleared, which is deliberate:
// stale history from a session that no longer exists should not be shown).
func (b *EventBroker) History(sessionID string) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.history[sessionID]
	out := make([]Event, len(h))
	copy(out, h)
	return out
}

// ClearHistory drops any cached events for sessionID — called when a Station
// replaces a session (manual reset or auto-recovery) so the old session's
// transcript doesn't linger in memory once nothing references it, and can
// never be served as if it were still current.
func (b *EventBroker) ClearHistory(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.history, sessionID)
	delete(b.historyIndex, sessionID)
}

// Run connects to opencode's event stream and dispatches events until ctx is
// done, reconnecting with backoff on failure. Meant to run for the lifetime
// of the process in its own goroutine (see cmd/superbadger/main.go).
func (b *EventBroker) Run(ctx context.Context) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := b.connectOnce(ctx); err != nil {
			log.Printf("opencode event stream: %v (retrying in %s)", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

// heartbeatTimeout bounds how long connectOnce tolerates silence from
// opencode before treating the connection as dead. opencode sends its own
// "server.heartbeat" events on this stream specifically so a consumer can
// detect this; without this watchdog, an already-open TCP connection to a
// killed/restarted opencode process can sit forever waiting on a read that
// will never return (confirmed live: killing and restarting opencode left a
// stale broker connection that silently never reconnected).
const heartbeatTimeout = 45 * time.Second

func (b *EventBroker) connectOnce(parentCtx context.Context) error {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	resp, err := b.client.StreamEvents(ctx)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	watchdog := time.NewTimer(heartbeatTimeout)
	defer watchdog.Stop()
	go func() {
		select {
		case <-watchdog.C:
			cancel() // unblocks the scanner's read with a context error
		case <-ctx.Done():
		}
	}()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if !watchdog.Stop() {
			<-watchdog.C
		}
		watchdog.Reset(heartbeatTimeout)

		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var evt Event
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			continue
		}
		b.dispatch(evt)
	}
	return scanner.Err()
}

func (b *EventBroker) dispatch(evt Event) {
	sessionID := evt.SessionID()
	if sessionID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	b.record(sessionID, evt)

	for ch := range b.subs[sessionID] {
		select {
		case ch <- evt:
		default:
			// Slow subscriber: drop rather than block the shared dispatcher
			// (and every other station's stream) on one stuck client.
		}
	}
	for ch := range b.allSubs {
		select {
		case ch <- evt:
		default:
		}
	}
}

// record folds evt into sessionID's history. message.updated and
// message.part.updated occupy one slot each, keyed by their own message/part
// ID and overwritten in place on repeat updates (a tool part alone can
// update several times as it runs); message.part.delta is merged into the
// part slot it targets rather than taking a slot of its own — see
// historyLimit's doc comment for why. mu must already be held.
func (b *EventBroker) record(sessionID string, evt Event) {
	if evt.Type == "message.part.delta" {
		b.mergeDelta(sessionID, evt)
		return
	}

	// session.error is a one-off notification, not conversation state — if
	// it were cached, every later History() replay (e.g. a client
	// reopening the station or reconnecting) would re-deliver the same
	// stale error and re-trigger the client's error banner indefinitely,
	// even long after the client dismissed it and the underlying problem
	// (if any) passed. Live subscribers still see it via dispatch; it's
	// only excluded from the replay cache.
	if evt.Type == "session.error" {
		return
	}

	key, keyed := historyKey(evt)
	if keyed {
		if idx, exists := b.historyIndex[sessionID][key]; exists {
			b.history[sessionID][idx] = evt
			return
		}
	}
	b.appendHistory(sessionID, evt)
	if keyed {
		if b.historyIndex[sessionID] == nil {
			b.historyIndex[sessionID] = make(map[string]int)
		}
		b.historyIndex[sessionID][key] = len(b.history[sessionID]) - 1
	}
}

// historyKey identifies the entity a structural event belongs to, so a later
// update to the same message/part replaces its history slot instead of
// growing it. Other event types (session.*, permission.*, ...) have no
// natural single owner to overwrite and are just appended as-is.
func historyKey(evt Event) (string, bool) {
	switch evt.Type {
	case "message.updated":
		var p struct {
			Info struct {
				ID string `json:"id"`
			} `json:"info"`
		}
		if err := json.Unmarshal(evt.Properties, &p); err != nil || p.Info.ID == "" {
			return "", false
		}
		return "message:" + p.Info.ID, true
	case "message.part.updated":
		var p struct {
			Part struct {
				ID string `json:"id"`
			} `json:"part"`
		}
		if err := json.Unmarshal(evt.Properties, &p); err != nil || p.Part.ID == "" {
			return "", false
		}
		return "part:" + p.Part.ID, true
	default:
		return "", false
	}
}

// mergeDelta appends a streamed text delta onto the message.part.updated
// entry already recorded for its part, rather than storing the delta as its
// own history entry — a streaming reply can emit hundreds of these per part,
// and this is what keeps the ring buffer bounded by actual conversation
// structure instead of by token count. If the target part isn't in history
// (evicted, or history was cleared mid-stream), the delta is simply dropped
// from history — the live subscriber still gets it; only reconnect replay
// would miss it, and there's nothing sensible to merge it onto in that case.
func (b *EventBroker) mergeDelta(sessionID string, evt Event) {
	var d struct {
		PartID string `json:"partID"`
		Field  string `json:"field"`
		Delta  string `json:"delta"`
	}
	if err := json.Unmarshal(evt.Properties, &d); err != nil || d.PartID == "" || d.Field != "text" {
		return
	}
	idx, ok := b.historyIndex[sessionID]["part:"+d.PartID]
	if !ok {
		return
	}

	var props map[string]json.RawMessage
	if err := json.Unmarshal(b.history[sessionID][idx].Properties, &props); err != nil {
		return
	}
	var part map[string]json.RawMessage
	if err := json.Unmarshal(props["part"], &part); err != nil {
		return
	}
	var text string
	_ = json.Unmarshal(part["text"], &text)
	text += d.Delta
	textJSON, err := json.Marshal(text)
	if err != nil {
		return
	}
	part["text"] = textJSON
	partJSON, err := json.Marshal(part)
	if err != nil {
		return
	}
	props["part"] = partJSON
	propsJSON, err := json.Marshal(props)
	if err != nil {
		return
	}

	stored := b.history[sessionID][idx]
	stored.Properties = propsJSON
	b.history[sessionID][idx] = stored
}

// appendHistory adds evt to sessionID's history, trimming from the front and
// rebuilding historyIndex (cheap — bounded by historyLimit) whenever that
// trim would otherwise leave historyIndex pointing at stale slice positions.
func (b *EventBroker) appendHistory(sessionID string, evt Event) {
	h := append(b.history[sessionID], evt)
	if len(h) > historyLimit {
		h = h[len(h)-historyLimit:]
		b.history[sessionID] = h
		b.rebuildIndex(sessionID)
		return
	}
	b.history[sessionID] = h
}

func (b *EventBroker) rebuildIndex(sessionID string) {
	idx := make(map[string]int, len(b.history[sessionID]))
	for i, evt := range b.history[sessionID] {
		if key, ok := historyKey(evt); ok {
			idx[key] = i
		}
	}
	b.historyIndex[sessionID] = idx
}

// Subscribe returns a channel of events for sessionID. The caller must call
// the returned unsubscribe func when done (e.g. on WebSocket disconnect).
func (b *EventBroker) Subscribe(sessionID string) (<-chan Event, func()) {
	ch := make(chan Event, 32)
	b.mu.Lock()
	if b.subs[sessionID] == nil {
		b.subs[sessionID] = make(map[chan Event]struct{})
	}
	b.subs[sessionID][ch] = struct{}{}
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		delete(b.subs[sessionID], ch)
		if len(b.subs[sessionID]) == 0 {
			delete(b.subs, sessionID)
		}
		b.mu.Unlock()
		close(ch)
	}
	return ch, unsubscribe
}

// SubscribeAll returns a channel of every dispatched event regardless of
// session — used for the cross-station notification feed (see
// api.notificationsWS), which needs to know about session.idle/
// permission.asked across every Station, not just one. The caller must call
// the returned unsubscribe func when done.
func (b *EventBroker) SubscribeAll() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	b.mu.Lock()
	b.allSubs[ch] = struct{}{}
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		delete(b.allSubs, ch)
		b.mu.Unlock()
		close(ch)
	}
	return ch, unsubscribe
}
