package art

import (
	"fmt"
	"math"
	"strings"
)

// The mobile app draws the same logo as the terminal title screen, so this
// file turns the (enhanced) ANSI art back into a raster of sub-pixels and
// describes every pixel by its *tone* rather than its color. A tone is where
// the pixel falls on the art's own dark→light ramp plus how far it leans
// toward the cyan accent; an app theme then supplies real colors for tones.
// That is what lets one set of vector data serve every theme.

// Sub is one sub-pixel of the picture (a sextant cell is 2x3 of them).
type Sub struct {
	C  RGB
	On bool // draws something (is not the page color)
}

// Logo is the picture split into its parts. Rows are sub-pixel rows; every row
// is W sub-pixels wide.
type Logo struct {
	W      int
	Badger [][]Sub
	// Gap is the number of blank sub-rows between the badger and the wordmark.
	Gap  int
	Word [][]Sub
	// Slices cut the wordmark into left-to-right pieces for a staggered
	// reveal. The letters are slanted and overlap in columns (and a "u" has a
	// gap between its own stems), so they cannot be separated at empty
	// columns; slices are cut at the emptiest columns near even spacing.
	Slices []Span
	// S is the wordmark's first letter (the app icon), as a column span.
	S Span
}

// SliceCount is how many pieces the wordmark is cut into (one per letter of
// "superbadger", so the reveal reads letter by letter).
const SliceCount = 11

// Span is a half-open column range [X0, X1).
type Span struct{ X0, X1 int }

func near6(c RGB) bool { return near(c, Page, 6) }

// cellSubs decodes one cell into its 2x3 sub-pixels, row-major.
func cellSubs(c Cell) ([6]RGB, error) {
	if p, ok := Pixels(c); ok {
		return p, nil
	}
	if c.R == '▀' { // upper half: the top row and a bit more, then the rest
		return [6]RGB{c.FG, c.FG, c.FG, c.FG, c.BG, c.BG}, nil
	}
	return [6]RGB{}, fmt.Errorf("unsupported glyph %q", c.R)
}

// ExtractLogo reads the title art (frame lines and the fake shell prompt are
// ignored) and returns its badger and wordmark. The last picture row, the
// "press any key" line, is dropped: it is terminal UI, not logo.
func ExtractLogo(ansi string) (*Logo, error) {
	lines := strings.Split(strings.TrimRight(ansi, "\n"), "\n")
	if len(lines) < 3 {
		return nil, fmt.Errorf("art too short")
	}
	var cellRows [][]Cell
	for _, l := range lines[1 : len(lines)-1] {
		cells := ParseLine(l)
		if len(cells) > 2 {
			cells = cells[1 : len(cells)-1]
		}
		var text strings.Builder
		for _, c := range cells {
			text.WriteRune(c.R)
		}
		if strings.Contains(text.String(), "~ $") {
			continue // the fake shell prompt
		}
		cellRows = append(cellRows, cells)
	}
	if len(cellRows) == 0 {
		return nil, fmt.Errorf("no rows")
	}
	w := 0
	for _, r := range cellRows {
		w = max(w, len(r))
	}
	// decode every cell row into 3 sub-rows
	type cellRow struct {
		sub   [3][]Sub
		blank bool
		text  bool // holds ordinary text glyphs (the prompt line), not pixels
	}
	rows := make([]cellRow, len(cellRows))
	for i, cells := range cellRows {
		var cr cellRow
		for k := range cr.sub {
			cr.sub[k] = make([]Sub, w*2)
		}
		cr.blank = true
		for x := range w {
			p := [6]RGB{Page, Page, Page, Page, Page, Page}
			if x < len(cells) {
				var err error
				if p, err = cellSubs(cells[x]); err != nil {
					cr.text, cr.blank = true, false
					break
				}
			}
			for k := 0; k < 6; k++ {
				s := Sub{C: p[k], On: !near6(p[k])}
				if s.On {
					cr.blank = false
				}
				cr.sub[k/2][x*2+k%2] = s
			}
		}
		rows[i] = cr
	}
	// three runs of non-blank rows: badger, wordmark, prompt line
	var runs [][2]int
	start := -1
	for i, r := range rows {
		switch {
		case !r.blank && start < 0:
			start = i
		case r.blank && start >= 0:
			runs = append(runs, [2]int{start, i - 1})
			start = -1
		}
	}
	if start >= 0 {
		runs = append(runs, [2]int{start, len(rows) - 1})
	}
	if len(runs) != 3 {
		return nil, fmt.Errorf("expected badger, wordmark and prompt (3 picture blocks), found %d", len(runs))
	}
	build := func(r [2]int) ([][]Sub, error) {
		var out [][]Sub
		for i := r[0]; i <= r[1]; i++ {
			if rows[i].text {
				return nil, fmt.Errorf("row %d has text glyphs where pixel art was expected", i)
			}
			for k := 0; k < 3; k++ {
				out = append(out, rows[i].sub[k])
			}
		}
		return out, nil
	}
	badger, err := build(runs[0])
	if err != nil {
		return nil, err
	}
	word, err := build(runs[1])
	if err != nil {
		return nil, err
	}
	if !rows[runs[2][0]].text {
		return nil, fmt.Errorf("the last block should be the text prompt line")
	}
	l := &Logo{W: w * 2, Badger: badger, Word: word, Gap: (runs[1][0] - runs[0][1] - 1) * 3}

	// column ink counts
	ink := make([]int, l.W)
	for _, row := range l.Word {
		for x, sub := range row {
			if sub.On {
				ink[x]++
			}
		}
	}
	first, last := -1, -1
	for x, n := range ink {
		if n > 0 {
			if first < 0 {
				first = x
			}
			last = x
		}
	}
	if first < 0 {
		return nil, fmt.Errorf("the wordmark is empty")
	}
	// The S: the first solid run of inked columns.
	x := first
	for x < l.W && ink[x] > 0 {
		x++
	}
	l.S = Span{first, x}

	// Slices: cut where the cumulative ink reaches k/SliceCount of the total (so
	// each slice holds about one letter's worth, and the space between the two
	// words doesn't produce an empty slice), nudged to the emptiest nearby column.
	total := 0
	for _, n := range ink {
		total += n
	}
	cuts := []int{first}
	run := 0
	for x := first; x <= last && len(cuts) < SliceCount; x++ {
		run += ink[x]
		if run*SliceCount >= total*len(cuts) {
			best, bestScore := x+1, 1<<30
			for c := x - 3; c <= x+4; c++ {
				if c <= cuts[len(cuts)-1] || c >= last {
					continue
				}
				if score := ink[c]*100 + abs(c-(x+1)); score < bestScore {
					best, bestScore = c, score
				}
			}
			cuts = append(cuts, best)
		}
	}
	for len(cuts) < SliceCount { // degenerate art: pad so there are always SliceCount slices
		cuts = append(cuts, min(last, cuts[len(cuts)-1]+1))
	}
	cuts = append(cuts, last+1)
	for k := 0; k < SliceCount; k++ {
		l.Slices = append(l.Slices, Span{cuts[k], cuts[k+1]})
	}
	return l, nil
}

// ---- tones ----

// ArtRamp is the art's own dark→light ramp: page, deep shadow, badger body,
// mid blue, light strokes. ArtAccent is its cyan.
var (
	ArtRamp   = [5]RGB{{16, 20, 34}, {24, 30, 60}, {58, 78, 128}, {110, 132, 184}, {196, 222, 255}}
	ArtAccent = RGB{170, 255, 250}
)

// Quantization: the ramp position is kept in sixths of a segment and the
// accent lean in halves, which keeps the shading but merges the fur's noise
// into a few dozen flat tones (far fewer shapes to draw).
const (
	UStep = 6 // U is 0..4*UStep
	WStep = 2 // W is 0..WStep
)

// Tone is a quantized (ramp position, accent lean).
type Tone struct{ U, W int }

func lumOf(c RGB) float64 { return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2] }

func vlerp(a, b RGB, t float64) RGB {
	return RGB{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t}
}

func vsub(a, b RGB) RGB { return RGB{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }

func vdot(a, b RGB) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

// ToneOf places a color on the art's ramp and measures its lean toward cyan.
func ToneOf(c RGB) Tone {
	L := lumOf(c)
	i := 0
	for i < len(ArtRamp)-2 && L > lumOf(ArtRamp[i+1]) {
		i++
	}
	La, Lb := lumOf(ArtRamp[i]), lumOf(ArtRamp[i+1])
	t := math.Min(1, math.Max(0, (L-La)/(Lb-La)))
	base := vlerp(ArtRamp[i], ArtRamp[i+1], t)
	dir := vsub(ArtAccent, ArtRamp[len(ArtRamp)-1])
	w := math.Min(1, math.Max(0, vdot(vsub(c, base), dir)/vdot(dir, dir)))
	return Tone{
		U: int(math.Round((float64(i) + t) * UStep)),
		W: int(math.Round(w * WStep)),
	}
}

// Values are the unquantized ramp position (0..4) and accent lean (0..1).
func (t Tone) Values() (u, w float64) {
	return float64(t.U) / UStep, float64(t.W) / WStep
}

// Runs is a set of horizontal runs of pixels, flattened as x, y, length.
type Runs []int

// Group collects the sub-pixels of a region by tone.
type Group map[Tone]Runs

// GroupOf gathers the pixels of rows[y][x0:x1] accepted by keep into runs of
// equal tone, with y relative to the first row.
func GroupOf(rows [][]Sub, x0, x1 int, keep func(Sub) bool) Group {
	g := Group{}
	for y, row := range rows {
		x := x0
		for x < x1 {
			s := row[x]
			if !s.On || !keep(s) {
				x++
				continue
			}
			t := ToneOf(s.C)
			run := 1
			for x+run < x1 {
				n := row[x+run]
				if !n.On || !keep(n) || ToneOf(n.C) != t {
					break
				}
				run++
			}
			g[t] = append(g[t], x, y, run)
			x += run
		}
	}
	return g
}

// IsEye reports whether a sub-pixel is the eye's cyan.
func IsEye(s Sub) bool { return s.On && near(s.C, ArtAccent, 12) }

// Bounds returns the inked bounding box of rows[y][x0:x1] (x1 exclusive).
func Bounds(rows [][]Sub, x0, x1 int) (minX, minY, maxX, maxY int) {
	minX, minY, maxX, maxY = math.MaxInt, math.MaxInt, -1, -1
	for y, row := range rows {
		for x := x0; x < x1; x++ {
			if row[x].On {
				minX, maxX = min(minX, x), max(maxX, x)
				minY, maxY = min(minY, y), max(maxY, y)
			}
		}
	}
	return
}
