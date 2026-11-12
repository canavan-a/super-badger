package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"slices"
	"sync"
	"time"
)

// EventBroker holds one long-lived subscription to opencode's global
// /api/event stream, translates it into super-badger events (Translator) and
// fans those out to per-session subscribers, so N station WebSockets share
// one upstream connection.
//
// There is no transcript cache: opencode stores every session's messages,
// and History pages through them (FromMessages), so history survives
// restarts of either process. The only in-memory transcript state is text
// still streaming, which History overlays so a client opening a station
// mid-reply sees the reply so far.
type EventBroker struct {
	client *Client

	mu      sync.Mutex
	tr      *Translator
	subs    map[string]map[chan Event]struct{} // sessionID -> subscriber channels
	allSubs map[chan Event]struct{}
	// connected counts successful stream connects; any after the first
	// means events may have been missed, so subscribers get a resync.
	connected int

	// markerMu guards markers and orders compaction-marker deliveries
	// (see compactionMarker).
	markerMu sync.Mutex
	markers  map[string]*markerState

	limitsMu  sync.Mutex
	limits    map[ModelRef]int
	limitsAt  time.Time
	limitsTTL time.Duration
}

func NewEventBroker(client *Client) *EventBroker {
	return &EventBroker{
		client:    client,
		tr:        NewTranslator(),
		subs:      make(map[string]map[chan Event]struct{}),
		allSubs:   make(map[chan Event]struct{}),
		markers:   make(map[string]*markerState),
		limitsTTL: 15 * time.Second,
	}
}

// Forget drops a session's translator state (deleted or replaced).
func (b *EventBroker) Forget(sessionID string) {
	b.mu.Lock()
	b.tr.Forget(sessionID)
	b.mu.Unlock()
	b.markerMu.Lock()
	delete(b.markers, sessionID)
	b.markerMu.Unlock()
}

// History returns one page of sessionID's transcript as events, oldest
// first: the newest limit messages when cursor is "", else the page cursor
// (a previous call's next) points at. hasMore reports whether an older page
// may exist.
func (b *EventBroker) History(ctx context.Context, sessionID, cursor string, limit int) (events []Event, next string, hasMore bool, err error) {
	page, err := b.client.Messages(ctx, sessionID, MessagesParams{Limit: limit, Cursor: cursor})
	if err != nil {
		return nil, "", false, err
	}
	msgs := slices.Clone(page.Messages)
	slices.Reverse(msgs)
	events = FromMessages(sessionID, msgs)
	if cursor == "" {
		// Text that hasn't finished streaming isn't stored yet.
		b.mu.Lock()
		streaming := b.tr.Streaming(sessionID)
		b.mu.Unlock()
		events = append(events, streaming...)
	}
	if events == nil {
		events = []Event{}
	}
	hasMore = len(page.Messages) >= limit && page.Next != ""
	if !hasMore {
		page.Next = ""
	}
	return events, page.Next, hasMore, nil
}

// Inbox is sessionID's inbox snapshot (see the inbox event).
func (b *EventBroker) Inbox(sessionID string) Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tr.Inbox(sessionID)
}

// Usage is sessionID's current context usage: from the live stream when a
// step has finished since super-badger started, else from stored messages.
func (b *EventBroker) Usage(ctx context.Context, sessionID string) (*Event, error) {
	b.mu.Lock()
	u, model := b.tr.Usage(sessionID)
	b.mu.Unlock()
	if u == nil {
		sess, err := b.client.GetSession(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		page, err := b.client.Messages(ctx, sessionID, MessagesParams{Limit: 10})
		if err != nil {
			return nil, err
		}
		u = LastUsage(sessionID, page.Messages, sess.Cost)
		if u == nil {
			c := sess.Cost
			u = &Event{Type: "usage", SessionID: sessionID, Cost: &c}
		}
		if model.ID == "" {
			for _, m := range page.Messages {
				if m.Model != nil {
					model = *m.Model
					break
				}
			}
		}
	}
	u.ContextLimit = b.ContextLimit(ctx, model)
	return u, nil
}

// ContextLimit is the context window opencode uses for model (cached
// briefly), or 0 when unknown.
func (b *EventBroker) ContextLimit(ctx context.Context, model ModelRef) int {
	if model.ID == "" {
		return 0
	}
	b.limitsMu.Lock()
	defer b.limitsMu.Unlock()
	if b.limits == nil || time.Since(b.limitsAt) > b.limitsTTL {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if models, err := b.client.Models(ctx); err == nil {
			b.limits = make(map[ModelRef]int, len(models))
			for _, m := range models {
				b.limits[ModelRef{ID: m.ID, ProviderID: m.ProviderID}] = m.Limit.Context
			}
			b.limitsAt = time.Now()
		}
	}
	return b.limits[model]
}

// Run connects to opencode's event stream and dispatches events until ctx is
// done, reconnecting with backoff on failure.
func (b *EventBroker) Run(ctx context.Context) {
	backoff := time.Second
	for {
		start := time.Now()
		err := b.connectOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		log.Printf("opencode event stream: %v (retrying in %s)", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// heartbeatTimeout bounds how long connectOnce tolerates silence before
// treating the connection as dead. opencode sends ": heartbeat" comment
// lines on this stream; without the watchdog an open TCP connection to a
// killed opencode can wait on a read forever.
const heartbeatTimeout = 45 * time.Second

// maxLineBytes caps one SSE line. A frame past it (a huge tool result) is
// dropped, not the connection.
const maxLineBytes = 32 << 20

func (b *EventBroker) connectOnce(parentCtx context.Context) error {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	resp, err := b.client.StreamEvents(ctx)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	b.mu.Lock()
	b.connected++
	reconnect := b.connected > 1
	b.mu.Unlock()
	if reconnect {
		b.resync()
	}

	watchdog := time.NewTimer(heartbeatTimeout)
	defer watchdog.Stop()
	timedOut := make(chan struct{})
	go func() {
		select {
		case <-watchdog.C:
			close(timedOut)
			cancel() // unblocks the read
		case <-ctx.Done():
		}
	}()

	err = readSSE(resp.Body, func() { watchdog.Reset(heartbeatTimeout) }, b.handleFrame)
	select {
	case <-timedOut:
		return context.DeadlineExceeded
	default:
	}
	return err
}

// readSSE reads server-sent events from r, calling activity on every line
// (comments included) and frame with each event's data.
func readSSE(r io.Reader, activity func(), frame func([]byte)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var line, data []byte
	tooLong := false
	for {
		line = line[:0]
		tooLong = false
		var err error
		for {
			var frag []byte
			frag, err = br.ReadSlice('\n')
			if len(line)+len(frag) <= maxLineBytes {
				line = append(line, frag...)
			} else {
				tooLong = true
			}
			if err != bufio.ErrBufferFull {
				break
			}
			activity()
		}
		if err != nil {
			return err
		}
		activity()
		line = bytes.TrimRight(line, "\r\n")
		switch {
		case tooLong:
			log.Printf("opencode event stream: dropped an event line over %d bytes", maxLineBytes)
			data = nil
		case len(line) == 0: // end of frame
			if len(data) > 0 {
				frame(data)
			}
			data = data[:0]
		case line[0] == ':': // comment: ": heartbeat"
		case bytes.HasPrefix(line, []byte("data:")):
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, bytes.TrimPrefix(line[5:], []byte(" "))...)
		}
	}
}

func (b *EventBroker) handleFrame(payload []byte) {
	var env Envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return
	}
	b.Dispatch(env)
}

// Dispatch translates one opencode event and delivers the result.
func (b *EventBroker) Dispatch(env Envelope) {
	b.mu.Lock()
	events := b.tr.Translate(env)
	var model ModelRef
	if len(events) > 0 {
		_, model = b.tr.Usage(events[0].SessionID)
	}
	b.mu.Unlock()

	for _, e := range events {
		switch e.Type {
		case "usage":
			e.ContextLimit = b.ContextLimit(context.Background(), model)
		case "compaction":
			// The compaction's transcript marker comes from its stored
			// message, so live and history markers share an ID.
			since := b.markCompaction(e.SessionID, e.Phase, env.Created)
			go b.compactionMarker(e.SessionID, since)
		}
		b.deliver(e)
	}
}

// markerState tracks the compaction a session is in, so the only marker
// delivered live is that compaction's.
type markerState struct {
	// since is when the current compaction started (opencode's clock, ms),
	// 0 if its start wasn't seen.
	since int64
	// finished holds compaction messages already delivered as completed or
	// failed, so a slow "running" fetch can't land after them.
	finished map[string]bool
}

// markerSlack allows for the compaction message being stored a little
// before its started event is stamped.
const markerSlack = 1000 // ms

// markerFallbackAge bounds how old a compaction whose start wasn't seen
// (super-badger started mid-compaction) may be and still get a live marker.
const markerFallbackAge = 15 * 60 * 1000 // ms

// markCompaction records a compaction event for sessionID and returns the
// earliest creation time the compaction message its marker shows may have.
func (b *EventBroker) markCompaction(sessionID, phase string, created int64) (since int64) {
	b.markerMu.Lock()
	defer b.markerMu.Unlock()
	m := b.markers[sessionID]
	if m == nil {
		m = &markerState{finished: map[string]bool{}}
		b.markers[sessionID] = m
	}
	if phase == "started" {
		m.since = created
	}
	since = m.since
	if since == 0 {
		since = created - markerFallbackAge
	}
	return since - markerSlack
}

// compactionMarker delivers the transcript marker of sessionID's current
// compaction: its newest stored compaction message, unless that is an
// older one (the current one isn't stored yet). An older compaction must
// not go out live: a client that hasn't paged back that far appends it at
// the bottom of the transcript, where it stays.
func (b *EventBroker) compactionMarker(sessionID string, since int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	page, err := b.client.Messages(ctx, sessionID, MessagesParams{Limit: 1, Type: "compaction"})
	if err != nil || len(page.Messages) == 0 {
		return
	}
	m := page.Messages[0]
	if m.Time.Created < since {
		return
	}
	b.markerMu.Lock()
	defer b.markerMu.Unlock()
	st := b.markers[sessionID]
	if st == nil {
		return // session forgotten meanwhile
	}
	if m.Status == "running" {
		if st.finished[m.ID] {
			return // stale: this compaction's outcome already went out
		}
	} else {
		st.finished[m.ID] = true
	}
	for _, e := range FromMessages(sessionID, []Message{m}) {
		b.deliver(e)
	}
}

// Publish delivers a server-made event to its session's subscribers.
func (b *EventBroker) Publish(e Event) { b.deliver(e) }

func (b *EventBroker) deliver(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[e.SessionID] {
		select {
		case ch <- e:
		default:
			// Slow subscriber: drop rather than block every station's
			// stream on one stuck client. Clients rebuild from history on
			// reconnect.
		}
	}
	for ch := range b.allSubs {
		select {
		case ch <- e:
		default:
		}
	}
}

// resync tells every session subscriber that events may have been missed
// while the opencode stream was down.
func (b *EventBroker) resync() {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Prompt texts and tool state from before the gap may be stale.
	b.tr = NewTranslator()
	for sid, chans := range b.subs {
		for ch := range chans {
			select {
			case ch <- Event{Type: "resync", SessionID: sid}:
			default:
			}
		}
	}
	for ch := range b.allSubs {
		select {
		case ch <- Event{Type: "resync"}:
		default:
		}
	}
}

// Subscribe returns a channel of events for sessionID. The caller must call
// the returned unsubscribe func when done.
func (b *EventBroker) Subscribe(sessionID string) (<-chan Event, func()) {
	ch := make(chan Event, 256)
	b.mu.Lock()
	if b.subs[sessionID] == nil {
		b.subs[sessionID] = make(map[chan Event]struct{})
	}
	b.subs[sessionID][ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs[sessionID], ch)
			if len(b.subs[sessionID]) == 0 {
				delete(b.subs, sessionID)
			}
			b.mu.Unlock()
		})
	}
}

// SubscribeAll returns a channel of every session's events, for the
// cross-station notification feed and auto mode.
func (b *EventBroker) SubscribeAll() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	b.mu.Lock()
	b.allSubs[ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.allSubs, ch)
			b.mu.Unlock()
		})
	}
}

// InvalidateLimits makes the next ContextLimit re-read opencode's models
// (after an override changed).
func (b *EventBroker) InvalidateLimits() {
	b.limitsMu.Lock()
	b.limitsAt = time.Time{}
	b.limitsMu.Unlock()
}
