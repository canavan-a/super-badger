package ui

import (
	"strings"
	"time"
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

// dirtFrames are the specks kicked up beside the word, one per flush tick.
var dirtFrames = []string{"⠂⠄⡀", "⠄⡀⠂", "⡀⠂⠄"}

const digWordInterval = 2500 * time.Millisecond

// digWord picks the word for now's 2.5s bucket. The stride is coprime with
// len(digWords), so it walks every word in a shuffled-looking order.
func digWord(now time.Time) string {
	bucket := now.UnixMilli() / digWordInterval.Milliseconds()
	return digWords[int(bucket*7%int64(len(digWords)))]
}

func dirtFrame(now time.Time) string {
	return dirtFrames[int(now.UnixMilli()/flushInterval.Milliseconds()%int64(len(dirtFrames)))]
}

// digDots piles up 0–3 dirt clods (in place of "...") on the compacting
// line's 400ms beat, padded to a fixed width so the specks beside them
// don't shift.
func digDots(now time.Time) string {
	n := int(now.UnixMilli() / 400 % 4)
	return strings.Repeat("▪", n) + strings.Repeat(" ", 3-n)
}
