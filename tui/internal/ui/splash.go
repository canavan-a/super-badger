package ui

import (
	_ "embed"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// splashArt is superbadger.ans: an 80x28 truecolor piece — a pixel-art badger
// drawn in colored half-blocks (▀), the wordmark, and a "press any key" line.
// It also draws its own terminal window (title bar, side rails) and a
// fake shell prompt, which would read as a terminal inside the terminal, so
// buildSplash strips those and keeps only the picture and the prompt line.
//
//go:embed splash.ans
var splashArt string

// One rendered cell: any number of SGR escapes followed by one rune.
var (
	splashCell = regexp.MustCompile(`((?:\x1b\[[0-9;]*m)*)(.)`)
	splashSGR  = regexp.MustCompile(`\x1b\[(38|48);2;(\d+);(\d+);(\d+)m`)
)

const splashBG = "16;20;34" // the art's own page color (#101422)

var (
	splashGrid [][]splashCellT // parsed cells, frame removed, blank margins trimmed
	splashRows []string        // the same, as static ANSI rows
	splashW    int             // width of each row in cells
	splashH    int
)

func init() {
	splashGrid, splashW = buildSplash(splashArt)
	splashH = len(splashGrid)
	for _, row := range splashGrid {
		splashRows = append(splashRows, joinCells(row))
	}
}

type splashCellT struct {
	esc, r   string
	fg, bg   string // "r;g;b" as last set on this cell
	blankish bool   // draws nothing: ▀ or space with both halves the page color

	// Which halves are the eye's cyan. Recorded from the original art so the
	// eye can still be found after the art is recolored for another theme.
	eyeFG, eyeBG bool
}

func parseSplashLine(line string) []splashCellT {
	line = strings.TrimSuffix(line, "\x1b[0m") // a trailing reset is not a cell
	var cells []splashCellT
	for _, m := range splashCell.FindAllStringSubmatch(line, -1) {
		c := splashCellT{esc: m[1], r: m[2]}
		for _, s := range splashSGR.FindAllStringSubmatch(m[1], -1) {
			rgb := s[2] + ";" + s[3] + ";" + s[4]
			if s[1] == "38" {
				c.fg = rgb
			} else {
				c.bg = rgb
			}
		}
		c.eyeFG, c.eyeBG = isEyeRGB(c.fg), isEyeRGB(c.bg)
		// A ▀ is fg over bg; a space shows only bg. Either is empty when
		// what it would show is the page color. (Colored ▀ cells are the
		// badger's pixels — they must survive.)
		switch c.r {
		case "▀":
			c.blankish = c.fg == splashBG && c.bg == splashBG
		case " ":
			c.blankish = c.bg == splashBG
		}
		cells = append(cells, c)
	}
	return cells
}

func joinCells(cells []splashCellT) string {
	var b strings.Builder
	for _, c := range cells {
		b.WriteString(c.esc + c.r)
	}
	return b.String()
}

func buildSplash(art string) ([][]splashCellT, int) {
	all := strings.Split(strings.TrimRight(art, "\n"), "\n")
	if len(all) < 3 {
		return nil, 0
	}
	var rows [][]splashCellT
	for _, line := range all[1 : len(all)-1] { // drop the top and bottom edges
		cells := parseSplashLine(line)
		if len(cells) > 2 {
			cells = cells[1 : len(cells)-1] // drop the side rails
		}
		var text strings.Builder
		for _, c := range cells {
			text.WriteString(c.r)
		}
		if strings.Contains(text.String(), "~ $") { // the fake shell prompt
			for i := range cells {
				cells[i] = splashCellT{
					esc: "\x1b[48;2;16;20;34m\x1b[38;2;16;20;34m", r: "▀",
					fg: splashBG, bg: splashBG, blankish: true,
				}
			}
		}
		rows = append(rows, cells)
	}
	blank := func(c []splashCellT) bool {
		for _, x := range c {
			if !x.blankish {
				return false
			}
		}
		return true
	}
	first, last := -1, -1
	for i, c := range rows {
		if !blank(c) {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return nil, 0
	}
	w := 0
	for _, c := range rows[first : last+1] {
		w = max(w, len(c))
	}
	return rows[first : last+1], w
}

// ---- animation ----

const (
	splashFrame = 60 * time.Millisecond // 16 fps: smooth enough, cheap to draw
	blinkFrames = 9                     // ~540ms on / off
	glimmerHalf = 5.0                   // half-width of the bright band, in cells
	glimmerGap  = 50                    // extra travel = pause between sweeps
	glimmerStep = 3                     // cells per frame (~50 cells/s)

	// The badger's eye is the only place the art uses its cyan accent among
	// the pixels; it catches the same sweep as the wordmark, held a little
	// wider so the glint reads on such a small target.
	eyeHalf = 8.0

	// A gentle sheen: the band only lifts colors part of the way toward white
	// (glimmerPeak), so it reads as a soft shimmer rather than a flash.
	glimmerPeak = 0.8
)

// shine is the highlight profile at signed distance d from the band's center:
// a soft triangular falloff scaled to glimmerPeak.
func shine(d, half float64) float64 {
	k := 1 - abs(d)/half
	if k <= 0 {
		return 0
	}
	return k * glimmerPeak
}

// isEyeRGB reports whether an "r;g;b" color is the eye's cyan (the enhancer
// snaps its pixels to exactly this, but allow a little slack).
func isEyeRGB(rgb string) bool {
	var r, g, b int
	if _, err := fmt.Sscanf(rgb, "%d;%d;%d", &r, &g, &b); err != nil {
		return false
	}
	d := func(a, b int) int {
		if a > b {
			return a - b
		}
		return b - a
	}
	return d(r, 170) <= 12 && d(g, 255) <= 12 && d(b, 250) <= 12
}

// splashEye reports whether c is a pixel of the badger's eye: a cell with the
// accent cyan in either half. Only meaningful on badger rows (the wordmark's
// gradient reuses the cyan).
func splashEye(c splashCellT) bool {
	return !c.blankish && (c.eyeFG || c.eyeBG)
}

func rowHasEye(row []splashCellT) bool {
	for _, c := range row {
		if splashEye(c) {
			return true
		}
	}
	return false
}

// The picture is laid out top to bottom as: the badger, one blank gap row,
// the wordmark, and the "_" prompt row last. splashGap is that blank row.
// (Layout, not glyph type, tells them apart: the badger now uses the same
// sextant glyphs as the wordmark.)
var splashGap int

func init() {
	splashGap = len(splashGrid)
	for y, row := range splashGrid {
		blank := true
		for _, c := range row {
			if !c.blankish {
				blank = false
				break
			}
		}
		if blank {
			splashGap = y
			break
		}
	}
}

// splashWordRow reports whether row y is part of the wordmark.
func splashWordRow(y int) bool { return y > splashGap && y < len(splashGrid)-1 }

// splashFrameRows renders frame n of the original (burrow) colors.
func splashFrameRows(n int) []string { return splashFor(themes["burrow"]).frame(n) }

// frame renders animation frame n: the wordmark with a diagonal glimmer
// sweeping left to right, the eye's glint, and the "_" blinking.
func (set *splashSet) frame(n int) []string {
	splashGrid, splashRows := set.grid, set.rows
	pos := float64((n * glimmerStep) % (splashW + glimmerGap))
	on := (n/blinkFrames)%2 == 0
	out := make([]string, len(splashGrid))
	for y, row := range splashGrid {
		last := y == len(splashGrid)-1
		word := splashWordRow(y)
		if !last && !word {
			if rowHasEye(row) {
				out[y] = eyeRow(row, y, pos, set.to)
			} else {
				out[y] = splashRows[y] // badger body / gaps: static
			}
			continue
		}
		var b strings.Builder
		for x, c := range row {
			switch {
			case c.blankish || c.fg == "" || c.bg == "":
				b.WriteString(c.esc + c.r)
			case last && c.r == "_":
				if on {
					b.WriteString(c.esc + c.r)
				} else {
					b.WriteString(splashSGRs(c.bg, c.bg) + " ")
				}
			case last || c.r == "▀":
				b.WriteString(c.esc + c.r)
			default:
				// Slanted band: cells are ~2x taller than wide, so weight y.
				k := shine(float64(x)+float64(y)*2-pos, glimmerHalf)
				if k <= 0 {
					b.WriteString(c.esc + c.r)
				} else {
					b.WriteString(splashSGRs(c.bg, glimmerColorTo(c.fg, k, set.to)) + c.r)
				}
			}
		}
		out[y] = b.String()
	}
	return out
}

// eyeRow renders a badger row with only the eye pixels catching the band.
func eyeRow(row []splashCellT, y int, pos float64, to string) string {
	var b strings.Builder
	for x, c := range row {
		k := shine(float64(x)+float64(y)*2-pos, eyeHalf)
		if !splashEye(c) || k <= 0 {
			b.WriteString(c.esc + c.r)
			continue
		}
		fg, bg := c.fg, c.bg
		if c.eyeFG {
			fg = glimmerColorTo(fg, k, to)
		}
		if c.eyeBG {
			bg = glimmerColorTo(bg, k, to)
		}
		b.WriteString(splashSGRs(bg, fg) + c.r)
	}
	return b.String()
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func splashSGRs(bg, fg string) string {
	return "\x1b[48;2;" + bg + "m\x1b[38;2;" + fg + "m"
}

// glimmerColor blends an "r;g;b" color toward white by k in (0,1]; k=1 is white.
func glimmerColor(fg string, k float64) string { return glimmerColorTo(fg, k, "255;255;255") }

// glimmerColorTo blends toward an arbitrary "r;g;b" (black on light themes,
// where the strokes are dark and brightening would only fade them out).
func glimmerColorTo(fg string, k float64, to string) string {
	p, q := strings.Split(fg, ";"), strings.Split(to, ";")
	if len(p) != 3 || len(q) != 3 {
		return fg
	}
	mix := func(v, target string) string {
		n, _ := strconv.Atoi(v)
		m, _ := strconv.Atoi(target)
		return strconv.Itoa(n + int(float64(m-n)*min(1, k)))
	}
	return mix(p[0], q[0]) + ";" + mix(p[1], q[1]) + ";" + mix(p[2], q[2])
}
