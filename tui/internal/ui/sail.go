package ui

import (
	"strings"
	"time"

	"superbadger-tui/internal/chat"
)

// sailWords mirror the app's SailingSpinner list (app/src/components/SailingSpinner.tsx).
var sailWords = []string{
	"Sailing", "Charting", "Navigating", "Tacking", "Reaching", "Trimming",
	"Plotting", "Steering", "Sounding", "Reckoning", "Surveying", "Piloting",
	"Beating", "Bearing", "Heading", "Running",
	"Plundering", "Hoisting", "Parleying", "Swashbuckling",
}

const sailWordInterval = 2500 * time.Millisecond

// sailWord picks the word for now's 2.5s bucket. The stride is coprime with
// len(sailWords), so it walks every word in a shuffled-looking order.
func sailWord(now time.Time) string {
	bucket := now.UnixMilli() / sailWordInterval.Milliseconds()
	return sailWords[int(bucket*7%int64(len(sailWords)))]
}

// sprayDots draws a wake of 0–4 waves (in place of "...") behind the sailing
// word, on a 400ms beat.
func sprayDots(now time.Time) string {
	return strings.Repeat("~", int(now.UnixMilli()/400%5))
}

// thinking reports whether the sailing line should show: only while a reply
// is running (busy, which the server's status events drive — sent on every
// connect too, so a reply started elsewhere counts) and the model is
// thinking — the latest reply's newest part is reasoning, or nothing has
// streamed yet — never during plain text output or while a tool call hangs.
//
// Compaction is no longer inferred from the transcript: the server reports
// it explicitly (compaction events and parts, see chat.State.Compacting),
// and the compacting line takes the sailing line's place.
func thinking(turns []*chat.Turn, busy bool) bool {
	if !busy {
		return false
	}
	if len(turns) == 0 {
		return true
	}
	t := turns[len(turns)-1]
	if t.Role != "assistant" || t.Marker() {
		return true
	}
	for i := len(t.PartOrder) - 1; i >= 0; i-- {
		p := t.Parts[t.PartOrder[i]]
		if p == nil {
			continue
		}
		if p.Kind == chat.KindStepFinish {
			// A finished step: the next one hasn't streamed anything yet.
			return true
		}
		return p.Kind == chat.KindReasoning
	}
	return true
}
