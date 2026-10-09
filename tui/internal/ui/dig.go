package ui

import (
	"strings"
	"time"

	"superbadger-tui/internal/chat"
)

// digWords mirror the app's DiggingSpinner list (app/src/components/DiggingSpinner.tsx).
var digWords = []string{
	"Digging", "Mining", "Grinding", "Drilling", "Tunneling", "Burrowing",
	"Excavating", "Chiseling", "Quarrying", "Prospecting", "Unearthing", "Delving",
	"Shoveling", "Boring", "Dredging", "Spelunking",
}

const digWordInterval = 2500 * time.Millisecond

// digWord picks the word for now's 2.5s bucket. The stride is coprime with
// len(digWords), so it walks every word in a shuffled-looking order.
func digWord(now time.Time) string {
	bucket := now.UnixMilli() / digWordInterval.Milliseconds()
	return digWords[int(bucket*7%int64(len(digWords)))]
}

// digDots piles up 0–4 dirt clods (in place of "...") on the compacting
// line's 400ms beat.
func digDots(now time.Time) string {
	return strings.Repeat("▪", int(now.UnixMilli()/400%5))
}

// thinking reports whether the digging line should show: only while the
// model is thinking — the in-flight reply's latest part is reasoning, or
// nothing has streamed yet — never during plain text output or while a tool
// call hangs. The reply counts as in flight when this chat view is Busy or
// the latest turn is an unfinished assistant message; Busy only turns on for
// replies this view sent, so the latter catches a reply still running when
// the station was reopened (or started from another client).
func thinking(turns []*chat.Turn, busy bool) bool {
	if len(turns) == 0 {
		return busy
	}
	t := turns[len(turns)-1]
	if t.Role != "assistant" {
		return busy
	}
	if t.Done && !busy {
		return false
	}
	for i := len(t.PartOrder) - 1; i >= 0; i-- {
		p := t.Parts[t.PartOrder[i]]
		if p == nil || p.Kind == chat.KindStepFinish {
			continue
		}
		return p.Kind == chat.KindReasoning
	}
	return true
}
