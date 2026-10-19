package station

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"main/database"
)

// Auto mode (a per-station opt-in, see database.Station.AutoMode): when a
// reply dies because the conversation outgrew the model's context window —
// usually mid-thinking, as one more step tips the request over — the server
// compacts the session and then sends the station's continue prompt,
// instead of leaving it stopped on an error until the owner compacts by
// hand.

// DefaultContinuePrompt is sent after an auto-compaction when the station
// has no continue prompt of its own.
const DefaultContinuePrompt = "Continue where you left off."

// autoMaxAttempts caps auto-compactions per station within autoWindow, so a
// task that overflows again straight after every compaction (or a summary
// that itself can't fit) stops and waits for the owner instead of looping.
const (
	autoMaxAttempts = 3
	autoWindow      = 15 * time.Minute
)

// autoIdleWait bounds how long to wait for the failed turn to wind down
// before compacting — opencode won't summarize a session that's still busy.
const autoIdleWait = 30 * time.Second

type autoState struct {
	mu       sync.Mutex
	running  map[uint]bool
	attempts map[uint][]time.Time
	notify   func(map[string]any)
}

func newAutoState() *autoState {
	return &autoState{running: map[uint]bool{}, attempts: map[uint][]time.Time{}}
}

// SetNotifier sets where auto mode's device notifications go (the
// /notifications/ws feed, via notify.Hub — see cmd/superbadger-server).
func (s *Service) SetNotifier(fn func(map[string]any)) {
	s.auto.mu.Lock()
	s.auto.notify = fn
	s.auto.mu.Unlock()
}

// AutoCompacting reports whether station id is in the middle of an
// auto-compaction — the session going idle in between isn't the agent
// finishing, so the notification feed skips it.
func (s *Service) AutoCompacting(id uint) bool {
	s.auto.mu.Lock()
	defer s.auto.mu.Unlock()
	return s.auto.running[id]
}

// RunAutoMode watches every session's errors for context-window overflows
// and recovers the stations that have auto mode on. Runs for the lifetime of
// the process.
func (s *Service) RunAutoMode(ctx context.Context) {
	events, unsubscribe := s.broker.SubscribeAll()
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			if evt.Type != "session.error" || !IsContextOverflow(evt.Properties) {
				continue
			}
			st, err := database.GetStationBySession(s.db, evt.SessionID())
			if err != nil || !st.AutoMode {
				continue
			}
			if !s.beginAuto(st.ID) {
				continue
			}
			go s.autoCompact(ctx, st)
		}
	}
}

// beginAuto claims station id for one auto-compaction: false when one is
// already running (the compaction's own failure, say) or the attempt budget
// is spent.
func (s *Service) beginAuto(id uint) bool {
	s.auto.mu.Lock()
	defer s.auto.mu.Unlock()
	if s.auto.running[id] {
		return false
	}
	recent := s.auto.attempts[id][:0]
	for _, t := range s.auto.attempts[id] {
		if time.Since(t) < autoWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) >= autoMaxAttempts {
		s.auto.attempts[id] = recent
		s.publish(id, map[string]any{
			"type": "notice",
			"text": fmt.Sprintf("Auto mode: context limit hit again after %d compactions — stopping here.", autoMaxAttempts),
		})
		return false
	}
	s.auto.attempts[id] = append(recent, time.Now())
	s.auto.running[id] = true
	return true
}

func (s *Service) endAuto(id uint) {
	s.auto.mu.Lock()
	delete(s.auto.running, id)
	s.auto.mu.Unlock()
}

func (s *Service) autoNotify(st database.Station) {
	s.auto.mu.Lock()
	fn := s.auto.notify
	s.auto.mu.Unlock()
	if fn != nil {
		fn(map[string]any{
			"type":          "auto_compacting",
			"station_id":    st.ID,
			"station_name":  st.Name,
			"station_color": st.Color,
		})
	}
}

func (s *Service) autoCompact(ctx context.Context, st database.Station) {
	defer s.endAuto(st.ID)
	log.Printf("[station %d] auto mode: context limit hit, compacting", st.ID)
	s.waitIdle(ctx, st)

	s.autoNotify(st)
	// clear_error: the error banner the overflow raised is being handled.
	s.publish(st.ID, map[string]any{"type": "notice", "text": "Context limit hit — compacting…", "clear_error": true})
	if err := s.oc.Compact(ctx, st.OpencodeSessionID, st.ProviderID, st.ModelID); err != nil {
		log.Printf("[station %d] auto mode: compact: %v", st.ID, err)
		s.publish(st.ID, map[string]any{"type": "error", "error": "Auto mode couldn't compact: " + err.Error()})
		return
	}

	// Re-read: the prompt (or auto mode itself) may have changed meanwhile.
	cur, err := database.GetStation(s.db, st.ID)
	if err != nil || !cur.AutoMode || cur.OpencodeSessionID != st.OpencodeSessionID {
		return
	}
	prompt := strings.TrimSpace(cur.AutoContinuePrompt)
	if prompt == "" {
		prompt = DefaultContinuePrompt
	}
	s.publish(st.ID, map[string]any{"type": "notice", "text": "Compacted — continuing…", "clear_error": true})
	if err := s.Enqueue(st.ID, prompt); err != nil {
		log.Printf("[station %d] auto mode: continue: %v", st.ID, err)
		s.publish(st.ID, map[string]any{"type": "error", "error": "Auto mode couldn't continue: " + err.Error()})
	}
}

// waitIdle waits (bounded by autoIdleWait) for st's session to stop running.
func (s *Service) waitIdle(ctx context.Context, st database.Station) {
	deadline := time.Now().Add(autoIdleWait)
	for time.Now().Before(deadline) {
		all, err := s.oc.SessionStatus(ctx, st.Directory)
		if err == nil {
			if status := all[st.OpencodeSessionID]; status == "" || status == "idle" {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// overflowPhrases are how providers word "the request no longer fits the
// context window" — llama.cpp's is the one seen live here ("request (110096
// tokens) exceeds the available context size (110080 tokens), try
// increasing it"); the rest cover OpenAI/Anthropic/OpenRouter-style
// backends and opencode's own overflow error.
var overflowPhrases = []string{
	"exceeds the available context size",
	"contextoverflowerror",
	"context_length_exceeded",
	"maximum context length",
	"context length exceeded",
	"exceeds the context window",
	"exceeds context window",
	"prompt is too long",
	"input is too long for",
	"too many tokens",
}

// IsContextOverflow reports whether a session.error's properties describe a
// context-window overflow. Matches on the whole payload, so it doesn't
// matter whether the wording sits in error.data.message, error.message or a
// provider's raw responseBody.
func IsContextOverflow(props json.RawMessage) bool {
	raw := strings.ToLower(string(props))
	for _, p := range overflowPhrases {
		if strings.Contains(raw, p) {
			return true
		}
	}
	return false
}
