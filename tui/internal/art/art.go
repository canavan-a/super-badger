// Package art enhances superbadger.ans: it re-renders the badger's pixel art
// at a finer grid, keeping the picture but adding resolution and fur texture.
//
// The original draws the badger with half-blocks (▀, two pixels per cell:
// fg over bg). Enhance rebuilds that raster, upsamples it to sextants (2x3
// pixels per cell, 3x the pixels in the same footprint), then re-encodes every
// cell as the best sextant glyph with two colors. Everything else in the file
// (frame, wordmark, prompt) is passed through byte for byte. The result is
// deterministic, so the generated file is stable in git.
package art

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

type RGB [3]float64

var (
	Page = RGB{16, 20, 34}    // the art's page color (#101422)
	Eye  = RGB{170, 255, 250} // the badger's eye (the accent cyan)
)

// Cell is one character cell: its escapes, glyph and the colors they set.
type Cell struct {
	Esc    string
	R      rune
	FG, BG RGB
}

var (
	cellRe = regexp.MustCompile(`((?:\x1b\[[0-9;]*m)*)(.)`)
	sgrRe  = regexp.MustCompile(`\x1b\[(38|48);2;(\d+);(\d+);(\d+)m`)
)

// ParseLine splits one ANSI line into cells (a trailing reset is not a cell).
func ParseLine(line string) []Cell {
	line = strings.TrimSuffix(line, "\x1b[0m")
	var cells []Cell
	for _, m := range cellRe.FindAllStringSubmatch(line, -1) {
		c := Cell{Esc: m[1], R: []rune(m[2])[0], FG: Page, BG: Page}
		for _, s := range sgrRe.FindAllStringSubmatch(m[1], -1) {
			r, _ := strconv.Atoi(s[2])
			g, _ := strconv.Atoi(s[3])
			b, _ := strconv.Atoi(s[4])
			if s[1] == "38" {
				c.FG = RGB{float64(r), float64(g), float64(b)}
			} else {
				c.BG = RGB{float64(r), float64(g), float64(b)}
			}
		}
		cells = append(cells, c)
	}
	return cells
}

func (c Cell) String() string { return c.Esc + string(c.R) }

func near(a, b RGB, tol float64) bool {
	return math.Abs(a[0]-b[0]) <= tol && math.Abs(a[1]-b[1]) <= tol && math.Abs(a[2]-b[2]) <= tol
}

func (c RGB) sgr(kind int) string {
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", kind, clamp(c[0]), clamp(c[1]), clamp(c[2]))
}

func clamp(v float64) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return int(v + 0.5)
}

// ---- sextants ----

// Sub-pixels of a cell are numbered row-major (0 1 / 2 3 / 4 5); a mask has
// bit i set when sub-pixel i shows the foreground color.

// Glyph returns the character for a 6-bit mask.
func Glyph(mask int) rune {
	switch {
	case mask == 0:
		return ' '
	case mask == 63:
		return '█'
	case mask == 21: // left half
		return '▌'
	case mask == 42: // right half
		return '▐'
	case mask < 21:
		return rune(0x1FB00 + mask - 1)
	case mask < 42:
		return rune(0x1FB00 + mask - 2)
	default:
		return rune(0x1FB00 + mask - 3)
	}
}

// MaskOf is Glyph's inverse; ok is false for glyphs that aren't sextants.
func MaskOf(r rune) (int, bool) {
	switch {
	case r == ' ':
		return 0, true
	case r == '█':
		return 63, true
	case r == '▌':
		return 21, true
	case r == '▐':
		return 42, true
	case r >= 0x1FB00 && r <= 0x1FB3B:
		n := int(r - 0x1FB00)
		switch {
		case n < 20:
			return n + 1, true
		case n < 40:
			return n + 2, true
		default:
			return n + 3, true
		}
	}
	return 0, false
}

// Pixels returns a sextant cell's six sub-pixel colors; ok is false for any
// other glyph.
func Pixels(c Cell) (p [6]RGB, ok bool) {
	m, ok := MaskOf(c.R)
	if !ok {
		return p, false
	}
	for i := 0; i < 6; i++ {
		if m>>i&1 == 1 {
			p[i] = c.FG
		} else {
			p[i] = c.BG
		}
	}
	return p, true
}

// ---- Enhance ----

// Options tunes the enhancement.
type Options struct {
	Sharpen   float64 // unsharp amount
	FurAmount float64 // texture strength (0 = none)
}

var DefaultOptions = Options{Sharpen: 0.45, FurAmount: 0.75}

// Enhance re-renders the badger in src and returns the new ANSI file.
func Enhance(src string, o Options) (string, error) {
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	if len(lines) < 3 {
		return "", fmt.Errorf("art too short")
	}
	rows := make([][]Cell, len(lines))
	for i, l := range lines {
		rows[i] = ParseLine(l)
	}
	first, last := badgerRows(rows)
	if first < 0 {
		return "", fmt.Errorf("no badger pixels found")
	}
	n := last - first + 1
	width := len(rows[first]) - 2 // without the side rails

	// Source raster: two pixels per cell, top = fg, bottom = bg.
	sw, sh := width, n*2
	col := make([][]RGB, sh)
	alpha := make([][]float64, sh)
	eye := make([][]float64, sh)
	for y := range col {
		col[y], alpha[y], eye[y] = make([]RGB, sw), make([]float64, sw), make([]float64, sw)
	}
	for r := 0; r < n; r++ {
		for x := 0; x < sw; x++ {
			c := rows[first+r][x+1]
			top, bot := c.FG, c.BG
			if c.R == ' ' {
				top = c.BG
			}
			for k, p := range [2]RGB{top, bot} {
				y := r*2 + k
				col[y][x] = p
				if !near(p, Page, 2) {
					alpha[y][x] = 1
				}
				if near(p, Eye, 8) {
					eye[y][x] = 1
				}
			}
		}
	}

	tw, th := sw*2, n*3
	px := upsample(col, alpha, eye, sw, sh, tw, th, o)

	out := append([]string(nil), lines...)
	for r := 0; r < n; r++ {
		var b strings.Builder
		b.WriteString(rows[first+r][0].String()) // left rail, untouched
		for cx := 0; cx < sw; cx++ {
			var p [6]RGB
			for i := 0; i < 6; i++ {
				p[i] = px[r*3+i/2][cx*2+i%2]
			}
			b.WriteString(encode(p))
		}
		b.WriteString(rows[first+r][len(rows[first+r])-1].String()) // right rail
		b.WriteString("\x1b[0m")
		out[first+r] = b.String()
	}
	return strings.Join(out, "\n") + "\n", nil
}

// badgerRows finds the longest run of rows that are pure ▀ pixel art with at
// least one non-page pixel (not text, not blank).
func badgerRows(rows [][]Cell) (first, last int) {
	first, last = -1, -1
	bestLen := 0
	start := -1
	isPix := func(r []Cell) bool {
		if len(r) < 3 {
			return false
		}
		some := false
		for _, c := range r[1 : len(r)-1] {
			if c.R != '▀' && c.R != ' ' {
				return false
			}
			if !near(c.FG, Page, 2) || !near(c.BG, Page, 2) {
				some = true
			}
		}
		return some
	}
	for i := 1; i < len(rows)-1; i++ {
		if isPix(rows[i]) {
			if start < 0 {
				start = i
			}
			if l := i - start + 1; l > bestLen {
				bestLen, first, last = l, start, i
			}
		} else {
			start = -1
		}
	}
	return
}

func smooth(a, b, x float64) float64 {
	t := math.Min(1, math.Max(0, (x-a)/(b-a)))
	return t * t * (3 - 2*t)
}

func hash(x, y int, seed uint32) float64 {
	h := uint32(x)*374761393 + uint32(y)*668265263 + seed*2246822519
	h = (h ^ (h >> 13)) * 1274126177
	return float64((h^(h>>16))&0xffff) / 65535
}

func valueNoise(x, y float64, seed uint32) float64 {
	x0, y0 := math.Floor(x), math.Floor(y)
	fx, fy := x-x0, y-y0
	sx, sy := fx*fx*(3-2*fx), fy*fy*(3-2*fy)
	i, j := int(x0), int(y0)
	a := hash(i, j, seed)*(1-sx) + hash(i+1, j, seed)*sx
	b := hash(i, j+1, seed)*(1-sx) + hash(i+1, j+1, seed)*sx
	return a*(1-sy) + b*sy
}

func lum(c RGB) float64 { return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2] }

// upsample builds the tw x th raster: an anti-aliased silhouette, colors
// interpolated only among the badger's own pixels (so the page never bleeds
// into the fur), sharpened edges, and fur texture.
func upsample(col [][]RGB, alpha, eye [][]float64, sw, sh, tw, th int, o Options) [][]RGB {
	get := func(g [][]float64, x, y int) float64 {
		if x < 0 || y < 0 || x >= sw || y >= sh {
			return 0
		}
		return g[y][x]
	}
	type samp struct {
		c   RGB
		a   float64
		eye float64
	}
	s := make([][]samp, th)
	for ty := 0; ty < th; ty++ {
		s[ty] = make([]samp, tw)
		sy := (float64(ty)+0.5)*float64(sh)/float64(th) - 0.5
		y0 := int(math.Floor(sy))
		fy := sy - float64(y0)
		for tx := 0; tx < tw; tx++ {
			sx := (float64(tx)+0.5)*float64(sw)/float64(tw) - 0.5
			x0 := int(math.Floor(sx))
			fx := sx - float64(x0)
			var a, e, wsum float64
			var sum RGB
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					w := (1 - math.Abs(fx-float64(dx))) * (1 - math.Abs(fy-float64(dy)))
					xx, yy := x0+dx, y0+dy
					aa := get(alpha, xx, yy)
					a += w * aa
					e += w * get(eye, xx, yy)
					if aa > 0 && xx >= 0 && yy >= 0 && xx < sw && yy < sh {
						for k := 0; k < 3; k++ {
							sum[k] += w * aa * col[yy][xx][k]
						}
						wsum += w * aa
					}
				}
			}
			c := Page
			if wsum > 1e-9 {
				c = RGB{sum[0] / wsum, sum[1] / wsum, sum[2] / wsum}
			}
			s[ty][tx] = samp{c: c, a: smooth(0.3, 0.7, a), eye: e}
		}
	}

	// Unsharp mask, using only neighbours inside the silhouette.
	sharp := make([][]RGB, th)
	for ty := range sharp {
		sharp[ty] = make([]RGB, tw)
		for tx := range sharp[ty] {
			c := s[ty][tx].c
			if s[ty][tx].a > 0.5 && o.Sharpen > 0 {
				var acc RGB
				var wt float64
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						yy, xx := ty+dy, tx+dx
						if yy < 0 || xx < 0 || yy >= th || xx >= tw || s[yy][xx].a <= 0.5 {
							continue
						}
						w := float64((2 - abs(dx)) * (2 - abs(dy)))
						for k := 0; k < 3; k++ {
							acc[k] += w * s[yy][xx].c[k]
						}
						wt += w
					}
				}
				for k := 0; k < 3; k++ {
					c[k] += o.Sharpen * (c[k] - acc[k]/wt)
				}
			}
			sharp[ty][tx] = c
		}
	}

	// Fur: strands run along one direction, so brightness varies mostly across
	// them, with a slower wander along their length plus the odd bright hair.
	out := make([][]RGB, th)
	for ty := range out {
		out[ty] = make([]RGB, tw)
		for tx := range out[ty] {
			p := s[ty][tx]
			c := sharp[ty][tx]
			if p.eye > 0.5 {
				c = Eye
			} else if p.a > 0.05 && o.FurAmount > 0 {
				across := float64(tx)*0.55 - float64(ty)
				strand := hash(int(math.Floor(across*1.6)), 7, 11)
				wander := valueNoise(float64(tx)*0.18, float64(ty)*0.7, 5)
				amp := 0.13
				switch l := lum(c); {
				case l > 170:
					amp = 0.07 // light fur: keep the stripe clean
				case l < 50:
					amp = 0.05
				}
				m := 1 + o.FurAmount*amp*((strand-0.5)*1.4+(wander-0.5)*0.6)*2
				if hash(tx, ty, 3) > 0.965 {
					m += 0.12 * o.FurAmount // a stray bright hair
				}
				for k := 0; k < 3; k++ {
					c[k] *= m
				}
			}
			for k := 0; k < 3; k++ {
				out[ty][tx][k] = Page[k] + (c[k]-Page[k])*p.a
			}
			if p.eye > 0.5 {
				out[ty][tx] = Eye
			}
		}
	}
	return out
}

func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

// encode picks, for six sub-pixels, the glyph and two colors with the least
// squared error and returns the cell's ANSI.
func encode(p [6]RGB) string {
	bestErr := math.MaxFloat64
	var bestMask int
	var bestFG, bestBG RGB
	try := func(mask int) {
		var fg, bg RGB
		var nf, nb float64
		for i := 0; i < 6; i++ {
			if mask>>i&1 == 1 {
				for k := 0; k < 3; k++ {
					fg[k] += p[i][k]
				}
				nf++
			} else {
				for k := 0; k < 3; k++ {
					bg[k] += p[i][k]
				}
				nb++
			}
		}
		if nf > 0 {
			for k := range fg {
				fg[k] /= nf
			}
		}
		if nb > 0 {
			for k := range bg {
				bg[k] /= nb
			}
		}
		var e float64
		for i := 0; i < 6; i++ {
			c := bg
			if mask>>i&1 == 1 {
				c = fg
			}
			for k := 0; k < 3; k++ {
				d := p[i][k] - c[k]
				e += d * d
			}
		}
		if e < bestErr-1e-9 { // strict: earlier (simpler) masks win ties
			bestErr, bestMask, bestFG, bestBG = e, mask, fg, bg
		}
	}
	try(0)
	try(63)
	for m := 1; m < 63; m++ {
		try(m)
	}
	snap := func(c RGB) RGB {
		switch {
		case near(c, Page, 3):
			return Page
		case near(c, Eye, 8):
			return Eye
		}
		return c
	}
	fg, bg := snap(bestFG), snap(bestBG)
	if bestMask == 0 {
		fg = bg
	}
	if bestMask == 63 {
		bg = fg
	}
	return bg.sgr(48) + fg.sgr(38) + string(Glyph(bestMask))
}
