package ui

import (
	"strings"
	"time"

	"superbadger-tui/internal/chat"
)

// digWords mirror the app's DiggingSpinner list (app/src/components/DiggingSpinner.tsx).
var digWords = []string{
	"Burrowing", "Tunneling", "Excavating", "Unearthing", "Delving",
	"Spelunking", "Trenching", "Shoveling", "Scooping", "Rummaging",
	"Digging", "Pawing", "Clawing", "Scraping", "Sifting",
	"Tilling", "Churning", "Boring", "Drilling", "Mining",
	"Quarrying", "Dredging", "Grubbing", "Rooting", "Prospecting",
	"Spading", "Hollowing", "Undermining", "Subterraneaning", "Bedrocking",
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

// toolRunning reports whether the latest assistant turn has a tool call in
// flight — its own ◔ status already says something is happening, so the
// digging line steps aside until the model is generating again.
func toolRunning(turns []*chat.Turn) bool {
	for i := len(turns) - 1; i >= 0; i-- {
		t := turns[i]
		if t.Role != "assistant" {
			continue
		}
		for _, id := range t.PartOrder {
			if p := t.Parts[id]; p != nil && p.Kind == chat.KindTool && (p.Status == "pending" || p.Status == "running") {
				return true
			}
		}
		return false
	}
	return false
}
