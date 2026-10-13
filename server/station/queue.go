package station

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"main/database"
)

// A Station's outgoing prompts go through a server-side queue rather than
// straight to opencode: every client (app, TUI, web) watching the station
// sees the same queue, and it survives any of them closing. The server sends
// it straight away, even while a reply is running: opencode saves a prompt
// posted to a busy session at once and its run loop picks it up at the next
// step, between tool calls, rather than after the whole turn (confirmed live
// against opencode 1.18.35). So a message only sits in the queue for the
// moment its send takes, or while sending keeps failing (opencode down),
// until the periodic sweep gets it through.

// QueueMsg is the snapshot pushed to a station's subscribers on every queue
// change (see SubscribeStation) — sent as-is over the station WebSocket.
type QueueMsg struct {
	Type  string                   `json:"type"` // always "queue"
	Items []database.QueuedMessage `json:"items"`
}

// SessionChanged is published (not forwarded to clients) when sending the
// queue had to replace an expired opencode session, so a station WebSocket
// can move its event subscription to the new session ID.
type SessionChanged struct{ SessionID string }

// sweepInterval retries every station with something still queued — a send
// that failed, or whatever was queued before a server restart.
const sweepInterval = 15 * time.Second

type queueState struct {
	locks sync.Map // station id -> *sync.Mutex
	mu    sync.Mutex
	subs  map[uint]map[chan any]struct{}
}

func newQueueState() *queueState {
	return &queueState{subs: map[uint]map[chan any]struct{}{}}
}

func (q *queueState) lock(id uint) *sync.Mutex {
	l, _ := q.locks.LoadOrStore(id, &sync.Mutex{})
	return l.(*sync.Mutex)
}

// SubscribeStation returns a channel of station id's queue snapshots
// (QueueMsg), queue-send errors/notices (forwarded to clients as-is) and
// SessionChanged. The caller must call the returned unsubscribe func.
func (s *Service) SubscribeStation(id uint) (<-chan any, func()) {
	ch := make(chan any, 16)
	s.queue.mu.Lock()
	if s.queue.subs[id] == nil {
		s.queue.subs[id] = map[chan any]struct{}{}
	}
	s.queue.subs[id][ch] = struct{}{}
	s.queue.mu.Unlock()
	return ch, func() {
		s.queue.mu.Lock()
		delete(s.queue.subs[id], ch)
		if len(s.queue.subs[id]) == 0 {
			delete(s.queue.subs, id)
		}
		s.queue.mu.Unlock()
	}
}

func (s *Service) publish(id uint, msg any) {
	s.queue.mu.Lock()
	defer s.queue.mu.Unlock()
	for ch := range s.queue.subs[id] {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (s *Service) publishQueue(id uint) {
	items, err := database.ListQueue(s.db, id)
	if err != nil {
		log.Printf("[station %d] list queue: %v", id, err)
		return
	}
	s.publish(id, QueueMsg{Type: "queue", Items: items})
}

// Queue returns station id's current queue snapshot.
func (s *Service) Queue(id uint) (QueueMsg, error) {
	items, err := database.ListQueue(s.db, id)
	return QueueMsg{Type: "queue", Items: items}, err
}

// Enqueue adds text to station id's queue and sends it right away, whether
// or not a reply is running.
func (s *Service) Enqueue(id uint, text string) error {
	if _, err := database.EnqueueMessage(s.db, id, text); err != nil {
		return err
	}
	s.publishQueue(id)
	go s.drain(context.Background(), id)
	return nil
}

// RemoveQueued drops one queued message. Removing one that's already been
// sent is a harmless no-op.
func (s *Service) RemoveQueued(id, msgID uint) error {
	removed, err := database.DeleteQueued(s.db, id, msgID)
	if err != nil {
		return err
	}
	if removed {
		s.publishQueue(id)
	}
	return nil
}

// clearQueue empties station id's queue — on Stop/Kill (Abort), Reset and
// Delete. Held under the station's send lock so a send already in progress
// finishes first rather than racing the clear.
func (s *Service) clearQueue(id uint) error {
	l := s.queue.lock(id)
	l.Lock()
	defer l.Unlock()
	if err := database.ClearQueue(s.db, id); err != nil {
		return err
	}
	s.publishQueue(id)
	return nil
}

// drain sends everything queued for station id as one prompt (one text part
// per message), busy or not. Safe to call any time, from anywhere: it's
// serialized per station, so messages go out in the order they were queued,
// and does nothing when there's nothing to send.
func (s *Service) drain(ctx context.Context, id uint) {
	l := s.queue.lock(id)
	l.Lock()
	defer l.Unlock()

	st, err := database.GetStation(s.db, id)
	if err != nil || st.OpencodeSessionID == "" {
		return
	}
	msgs, err := database.TakeQueue(s.db, id)
	if err != nil || len(msgs) == 0 {
		return
	}
	s.publishQueue(id)

	texts := make([]string, len(msgs))
	for i, m := range msgs {
		texts[i] = m.Text
	}
	if err := s.promptWithRecovery(ctx, &st, texts); err != nil {
		log.Printf("[station %d] queue: send: %v", id, err)
		if rerr := database.RestoreQueue(s.db, msgs); rerr != nil {
			log.Printf("[station %d] queue: restore: %v", id, rerr)
		}
		s.publishQueue(id)
		s.publish(id, map[string]any{"type": "error", "error": err.Error()})
	}
}

// promptWithRecovery sends texts, and if opencode reports the session no
// longer exists (e.g. opencode itself restarted, which drops all sessions
// from memory — confirmed live), transparently replaces it with a fresh
// session and retries once, telling subscribers what's happening rather
// than silently retrying behind their backs.
func (s *Service) promptWithRecovery(ctx context.Context, st *database.Station, texts []string) error {
	err := s.oc.PromptAsyncParts(ctx, st.OpencodeSessionID, texts)
	if err == nil || !strings.Contains(err.Error(), "Session not found") {
		return err
	}
	s.publish(st.ID, map[string]any{"type": "notice", "text": "Session expired — starting a new one…"})
	if err := s.activate(ctx, st); err != nil {
		return fmt.Errorf("session expired and reset failed: %w", err)
	}
	s.publish(st.ID, SessionChanged{SessionID: st.OpencodeSessionID})
	// Tells clients to discard the transcript they're showing — it belongs
	// to the now-dead session, not this new one.
	s.publish(st.ID, map[string]any{"type": "session_reset"})
	return s.oc.PromptAsyncParts(ctx, st.OpencodeSessionID, texts)
}

// RunQueue retries every station's leftover queue on a periodic sweep (sends
// that failed, and whatever was queued before a server restart). Runs for
// the lifetime of the process.
func (s *Service) RunQueue(ctx context.Context) {
	sweep := time.NewTicker(sweepInterval)
	defer sweep.Stop()
	s.sweepQueues(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			s.sweepQueues(ctx)
		}
	}
}

func (s *Service) sweepQueues(ctx context.Context) {
	ids, err := database.StationsWithQueue(s.db)
	if err != nil {
		log.Printf("queue sweep: %v", err)
		return
	}
	for _, id := range ids {
		go s.drain(ctx, id)
	}
}
