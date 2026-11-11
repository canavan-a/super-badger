package station

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"main/database"
	"main/opencode"
)

// Compaction recovery. opencode 2 compacts on its own before a session
// outgrows the model's context window (the super-badger plugin keeps the
// window right), and when the engine still rejects a request as too long it
// compacts and retries the step. What it can't fix on its own is a
// compaction that fails — every later prompt would try to compact and fail
// the same way. This file owns that:
//
//  1. A compaction fails: the plugin redoes it at once with its own summary.
//     If no redo starts within healStart, the server requests one. If the
//     redo ends, the session is healthy again.
//  2. If the redo fails too (or never finishes), the station is rolled over
//     to a fresh session seeded with the last good summary and the most
//     recent turns, so it keeps working instead of failing every prompt.
//  3. Either way, a turn the failure cut short is continued — with the
//     station's continue prompt when auto mode is on; otherwise the owner is
//     told to send a message.
//
// Auto mode also covers an overflow opencode's own recovery couldn't fix: it
// compacts and continues (rolling over if the compaction fails).

// DefaultContinuePrompt is sent after a recovery when the station has no
// continue prompt of its own.
const DefaultContinuePrompt = "Continue where you left off."

// autoMaxAttempts caps automatic continues per station within autoWindow, so
// a task that overflows again straight after every recovery stops and waits
// for the owner instead of looping.
const (
	autoMaxAttempts = 3
	autoWindow      = 15 * time.Minute
)

// autoIdleWait bounds how long to wait for a failed turn to wind down.
const autoIdleWait = 30 * time.Second

// healStart is how long the plugin gets to start redoing a failed
// compaction before the server asks for one itself.
var healStart = 20 * time.Second

type autoState struct {
	mu       sync.Mutex
	running  map[uint]bool
	attempts map[uint][]time.Time
	notify   func(map[string]any)
}

func newAutoState() *autoState {
	return &autoState{running: map[uint]bool{}, attempts: map[uint][]time.Time{}}
}

// SetNotifier sets where recovery's device notifications go (the
// /notifications/ws feed, via notify.Hub — see cmd/superbadger-server).
func (s *Service) SetNotifier(fn func(map[string]any)) {
	s.auto.mu.Lock()
	s.auto.notify = fn
	s.auto.mu.Unlock()
}

// AutoCompacting reports whether station id is recovering — the session
// going idle in between isn't the agent finishing, so the notification feed
// skips it.
func (s *Service) AutoCompacting(id uint) bool {
	s.auto.mu.Lock()
	defer s.auto.mu.Unlock()
	return s.auto.running[id]
}

// RunAutoMode watches every session for failed compactions and unrecovered
// overflows. Runs for the lifetime of the process.
func (s *Service) RunAutoMode(ctx context.Context) {
	events, unsubscribe := s.broker.SubscribeAll()
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-events:
			s.handleAutoEvent(ctx, evt)
		}
	}
}

func (s *Service) handleAutoEvent(ctx context.Context, evt opencode.Event) {
	var compactionFailed, overflow bool
	switch {
	case evt.Type == "compaction" && evt.Phase == "failed":
		compactionFailed = true
	case evt.Type == "error" && evt.Kind == "overflow":
		overflow = true
	default:
		return
	}
	st, err := database.GetStationBySession(s.db, evt.SessionID)
	if err != nil {
		return
	}
	if overflow && !st.AutoMode {
		return // the error stays on screen; the next prompt compacts first
	}
	// Subscribe before returning to the event loop (and before claiming, which
	// is what AutoCompacting reports), so the redo's events can't slip past.
	sessEvents, unsub := s.broker.Subscribe(evt.SessionID)
	if !s.claim(st.ID) {
		unsub()
		return // already recovering this station
	}
	go func() {
		defer s.release(st.ID)
		defer unsub()
		if compactionFailed {
			s.recoverCompaction(ctx, st, evt, sessEvents)
		} else {
			s.recoverOverflow(ctx, st, sessEvents)
		}
	}()
}

func (s *Service) claim(id uint) bool {
	s.auto.mu.Lock()
	defer s.auto.mu.Unlock()
	if s.auto.running[id] {
		return false
	}
	s.auto.running[id] = true
	return true
}

func (s *Service) release(id uint) {
	s.auto.mu.Lock()
	delete(s.auto.running, id)
	s.auto.mu.Unlock()
}

// recoverCompaction handles a failed compaction (step 1-3 above).
func (s *Service) recoverCompaction(ctx context.Context, st database.Station, failed opencode.Event, events <-chan opencode.Event) {
	log.Printf("[station %d] compaction failed (%s): %s", st.ID, failed.Reason, failed.Error)
	s.autoNotify(st)
	s.publish(st.ID, map[string]any{"type": "notice", "text": "Compaction failed — retrying with a simpler summary…", "clear_error": true})

	healed := s.awaitHeal(ctx, st, events)
	// A failed auto compaction ends the turn that needed it.
	cutShort := failed.Reason != "manual"

	if healed {
		log.Printf("[station %d] compaction redone", st.ID)
		s.afterRecovery(ctx, st, cutShort, "Compacted.")
		return
	}
	log.Printf("[station %d] compaction could not be redone; rolling over", st.ID)
	if err := s.rollover(ctx, &st); err != nil {
		log.Printf("[station %d] rollover: %v", st.ID, err)
		s.publish(st.ID, map[string]any{"type": "error", "error": "Couldn't compact or move to a new session: " + err.Error() + ". Reset the station to continue."})
		return
	}
	s.afterRecovery(ctx, st, cutShort, "Moved to a new session.")
}

// awaitHeal waits for the failed compaction to be redone: true once one
// ends, false when the redo fails or nothing ends in time.
func (s *Service) awaitHeal(ctx context.Context, st database.Station, events <-chan opencode.Event) bool {
	start := time.NewTimer(healStart)
	defer start.Stop()
	deadline := time.NewTimer(healGrace + compactWait/3)
	defer deadline.Stop()
	started := false
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-start.C:
			if !started {
				// No plugin redo (plugin missing, or its event subscription
				// lagging): ask for one. With the plugin it writes the
				// summary itself; without, opencode tries again.
				s.waitIdle(ctx, st)
				if _, err := s.oc.Compact(ctx, st.OpencodeSessionID); err != nil {
					log.Printf("[station %d] compact: %v", st.ID, err)
					return false
				}
			}
		case e := <-events:
			if e.Type != "compaction" {
				continue
			}
			switch e.Phase {
			case "started":
				started = true
			case "ended":
				return true
			case "failed":
				return false
			}
		}
	}
}

// recoverOverflow handles an overflow opencode's own compact-and-retry
// couldn't fix, for an auto-mode station: compact, then continue.
func (s *Service) recoverOverflow(ctx context.Context, st database.Station, events <-chan opencode.Event) {
	log.Printf("[station %d] context overflow not recovered by opencode; compacting", st.ID)
	s.autoNotify(st)
	s.publish(st.ID, map[string]any{"type": "notice", "text": "Context limit hit — compacting…", "clear_error": true})
	s.waitIdle(ctx, st)
	if _, err := s.oc.Compact(ctx, st.OpencodeSessionID); err != nil {
		log.Printf("[station %d] compact: %v", st.ID, err)
	} else {
		cctx, cancel := context.WithTimeout(ctx, compactWait)
		err = waitCompaction(cctx, events, healGrace)
		cancel()
		if err == nil {
			s.afterRecovery(ctx, st, true, "Compacted.")
			return
		}
		log.Printf("[station %d] %v", st.ID, err)
	}
	if err := s.rollover(ctx, &st); err != nil {
		s.publish(st.ID, map[string]any{"type": "error", "error": "Couldn't compact or move to a new session: " + err.Error() + ". Reset the station to continue."})
		return
	}
	s.afterRecovery(ctx, st, true, "Moved to a new session.")
}

// afterRecovery continues a turn the failure cut short: with the continue
// prompt in auto mode (within the attempt budget), else by telling the owner.
func (s *Service) afterRecovery(ctx context.Context, st database.Station, cutShort bool, done string) {
	if !cutShort {
		s.publish(st.ID, map[string]any{"type": "notice", "text": done, "clear_error": true})
		return
	}
	// Re-read: auto mode or the prompt may have changed meanwhile.
	cur, err := database.GetStation(s.db, st.ID)
	if err != nil {
		return
	}
	if !cur.AutoMode || !s.allowContinue(cur.ID) {
		s.publish(st.ID, map[string]any{"type": "notice", "text": done + " Send a message to continue.", "clear_error": true})
		return
	}
	prompt := strings.TrimSpace(cur.AutoContinuePrompt)
	if prompt == "" {
		prompt = DefaultContinuePrompt
	}
	s.waitIdle(ctx, cur)
	s.publish(st.ID, map[string]any{"type": "notice", "text": done + " Continuing…", "clear_error": true})
	if err := s.Enqueue(st.ID, prompt); err != nil {
		log.Printf("[station %d] continue: %v", st.ID, err)
		s.publish(st.ID, map[string]any{"type": "error", "error": "Couldn't continue: " + err.Error()})
	}
}

// allowContinue spends one of station id's automatic continues, or reports
// the budget is spent.
func (s *Service) allowContinue(id uint) bool {
	s.auto.mu.Lock()
	defer s.auto.mu.Unlock()
	recent := s.auto.attempts[id][:0]
	for _, t := range s.auto.attempts[id] {
		if time.Since(t) < autoWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) >= autoMaxAttempts {
		s.auto.attempts[id] = recent
		return false
	}
	s.auto.attempts[id] = append(recent, time.Now())
	return true
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

// waitIdle waits (bounded by autoIdleWait) for st's session to stop running.
func (s *Service) waitIdle(ctx context.Context, st database.Station) {
	deadline := time.Now().Add(autoIdleWait)
	for time.Now().Before(deadline) {
		active, err := s.oc.Active(ctx)
		if err == nil && active[st.OpencodeSessionID] == "" {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// carryOverShare is the part of the context window a rolled-over session's
// seed may take: the last summary plus the newest turns.
const carryOverShare = 4

// fallbackWindow is used when opencode reports no window for the model.
const fallbackWindow = 32_768

// rollover moves st to a fresh session seeded with what the old one knew.
func (s *Service) rollover(ctx context.Context, st *database.Station) error {
	old := st.OpencodeSessionID
	seed := s.carryOver(ctx, *st)
	s.waitIdle(ctx, *st)
	if err := s.activate(ctx, st); err != nil {
		return err
	}
	if seed != "" {
		if err := s.oc.Synthetic(ctx, st.OpencodeSessionID, seed, "Context carried over from the previous session", false); err != nil {
			log.Printf("[station %d] seed new session: %v", st.ID, err)
		}
	}
	log.Printf("[station %d] rolled over %s -> %s", st.ID, old, st.OpencodeSessionID)
	s.publish(st.ID, SessionChanged{SessionID: st.OpencodeSessionID})
	s.publish(st.ID, map[string]any{"type": "session_reset"})
	s.publish(st.ID, map[string]any{"type": "notice", "text": "The conversation couldn't be compacted, so it moved to a new session with a summary of it."})
	return nil
}

// carryOver builds a rolled-over session's seed: the old session's last
// good compaction summary and its newest user/assistant text, together at
// most a quarter of the context window.
func (s *Service) carryOver(ctx context.Context, st database.Station) string {
	window := s.broker.ContextLimit(ctx, opencode.ModelRef{ID: st.ModelID, ProviderID: st.ProviderID})
	if window <= 0 {
		window = fallbackWindow
	}
	budget := window / carryOverShare * 4 // characters, at ~4 per token

	var summary string
	if page, err := s.oc.Messages(ctx, st.OpencodeSessionID, opencode.MessagesParams{Limit: 10, Type: "compaction"}); err == nil {
		for _, m := range page.Messages {
			if m.Status == "completed" && strings.TrimSpace(m.Summary) != "" {
				summary = clip(strings.TrimSpace(m.Summary), budget/2)
				break
			}
		}
	}

	var turns []string
	left := budget - len(summary)
	if page, err := s.oc.Messages(ctx, st.OpencodeSessionID, opencode.MessagesParams{Limit: 80}); err == nil {
	collect:
		for _, m := range page.Messages { // newest first
			var text string
			switch m.Type {
			case "compaction":
				if m.Status == "completed" {
					break collect // older turns are in the summary
				}
			case "user":
				text = "User: " + m.Text
			case "assistant":
				for _, e := range opencode.FromMessages("", []opencode.Message{m}) {
					if e.Type == "part" && e.Kind == "text" && e.Text != "" {
						text += e.Text + "\n"
					}
				}
				if text != "" {
					text = "Assistant: " + strings.TrimSpace(text)
				}
			}
			if text == "" {
				continue
			}
			if len(text) > left {
				if len(turns) == 0 {
					turns = append(turns, "…"+text[len(text)-left:])
				}
				break
			}
			turns = append(turns, text)
			left -= len(text)
		}
	}
	slices.Reverse(turns)

	if summary == "" && len(turns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("This session continues an earlier one whose context could no longer be compacted. What it covered:\n")
	if summary != "" {
		fmt.Fprintf(&b, "\n<summary>\n%s\n</summary>\n", summary)
	}
	if len(turns) > 0 {
		fmt.Fprintf(&b, "\nIts most recent messages:\n\n%s\n", strings.Join(turns, "\n\n"))
	}
	return b.String()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// IsContextOverflow reports whether an error text describes a
// context-window overflow (see opencode.IsOverflowText).
func IsContextOverflow(text string) bool { return opencode.IsOverflowText(text) }
