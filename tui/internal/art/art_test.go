package art

import (
	"math"
	"os"
	"strings"
	"testing"
)

func load(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../art/superbadger.ans")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSextantGlyphMappingRoundTrips(t *testing.T) {
	for m := 0; m < 64; m++ {
		got, ok := MaskOf(Glyph(m))
		if !ok || got != m {
			t.Fatalf("mask %d -> %q -> %d (ok=%v)", m, Glyph(m), got, ok)
		}
	}
	// Spot checks against the Unicode block: SEXTANT-1, -2, -12, and the
	// last one (all but the top-left pixel... i.e. mask 62).
	for m, want := range map[int]rune{1: 0x1FB00, 2: 0x1FB01, 3: 0x1FB02, 62: 0x1FB3B, 21: '▌', 42: '▐', 63: '█', 0: ' '} {
		if Glyph(m) != want {
			t.Errorf("Glyph(%d)=%U, want %U", m, Glyph(m), want)
		}
	}
	seen := map[rune]bool{}
	for m := 0; m < 64; m++ {
		if seen[Glyph(m)] {
			t.Fatalf("glyph %U used for two masks", Glyph(m))
		}
		seen[Glyph(m)] = true
	}
}

func TestSextantGlyphsMatchTheOriginalsOwnWordmark(t *testing.T) {
	// The wordmark in the original already uses sextants; decoding them must
	// yield only masks (never fail), i.e. the mapping agrees with the file.
	n := 0
	for _, l := range strings.Split(load(t), "\n") {
		for _, c := range ParseLine(l) {
			if c.R >= 0x1FB00 && c.R <= 0x1FB3B {
				if _, ok := MaskOf(c.R); !ok {
					t.Fatalf("%U not decodable", c.R)
				}
				n++
			}
		}
	}
	if n < 50 {
		t.Fatalf("expected the wordmark's sextants, found %d", n)
	}
}

func enhance(t *testing.T) (src, out string) {
	t.Helper()
	src = load(t)
	out, err := Enhance(src, DefaultOptions)
	if err != nil {
		t.Fatal(err)
	}
	return src, out
}

func TestEnhanceKeepsEverythingButTheBadgerByteForByte(t *testing.T) {
	src, out := enhance(t)
	a, b := strings.Split(src, "\n"), strings.Split(out, "\n")
	if len(a) != len(b) {
		t.Fatalf("line count changed: %d -> %d", len(a), len(b))
	}
	rows := make([][]Cell, len(a))
	for i, l := range a {
		rows[i] = ParseLine(l)
	}
	first, last := badgerRows(rows)
	if first < 0 || last-first+1 < 8 {
		t.Fatalf("badger rows not found (%d..%d)", first, last)
	}
	for i := range a {
		if i >= first && i <= last {
			if len(ParseLine(b[i])) != len(rows[i]) {
				t.Fatalf("row %d changed width", i)
			}
			continue
		}
		if a[i] != b[i] {
			t.Fatalf("row %d outside the badger was modified", i)
		}
	}
}

func TestEnhanceIsDeterministic(t *testing.T) {
	src := load(t)
	a, _ := Enhance(src, DefaultOptions)
	b, _ := Enhance(src, DefaultOptions)
	if a != b {
		t.Fatal("output differs between runs; the generated file would churn in git")
	}
}

// rasters: source (1 wide x 2 tall per cell) and enhanced (2 x 3 per cell).
func rasters(t *testing.T) (srcR, outR [][]RGB, first, last int, srcRows, outRows [][]Cell) {
	src, out := enhance(t)
	for _, l := range strings.Split(src, "\n") {
		srcRows = append(srcRows, ParseLine(l))
	}
	for _, l := range strings.Split(out, "\n") {
		outRows = append(outRows, ParseLine(l))
	}
	first, last = badgerRows(srcRows)
	n := last - first + 1
	w := len(srcRows[first]) - 2
	srcR = make([][]RGB, n*2)
	outR = make([][]RGB, n*3)
	for y := range srcR {
		srcR[y] = make([]RGB, w)
	}
	for y := range outR {
		outR[y] = make([]RGB, w*2)
	}
	for r := 0; r < n; r++ {
		for x := 0; x < w; x++ {
			c := srcRows[first+r][x+1]
			srcR[r*2][x], srcR[r*2+1][x] = c.FG, c.BG
			p, ok := Pixels(outRows[first+r][x+1])
			if !ok {
				t.Fatalf("enhanced cell (%d,%d) has a non-sextant glyph %q", x, r, c.R)
			}
			for i := 0; i < 6; i++ {
				outR[r*3+i/2][x*2+i%2] = p[i]
			}
		}
	}
	return
}

// down averages the enhanced raster onto the source grid.
func down(out [][]RGB, x, y int) RGB {
	y0, y1 := float64(y)*1.5, float64(y)*1.5+1.5
	var acc RGB
	var wt float64
	for ty := int(math.Floor(y0)); ty < int(math.Ceil(y1)) && ty < len(out); ty++ {
		wy := math.Min(y1, float64(ty+1)) - math.Max(y0, float64(ty))
		for tx := 2 * x; tx <= 2*x+1; tx++ {
			for k := 0; k < 3; k++ {
				acc[k] += wy * out[ty][tx][k]
			}
			wt += wy
		}
	}
	for k := range acc {
		acc[k] /= wt
	}
	return acc
}

func TestEnhancedBadgerIsTheSamePicture(t *testing.T) {
	srcR, outR, _, _, _, _ := rasters(t)
	var inter, union int
	var diff float64
	var both int
	for y := range srcR {
		for x := range srcR[y] {
			s := srcR[y][x]
			d := down(outR, x, y)
			sIn := !near(s, Page, 2)
			dIn := math.Abs(lum(d)-lum(Page)) > 25
			if sIn && dIn {
				inter++
			}
			if sIn || dIn {
				union++
			}
			if sIn && dIn {
				both++
				for k := 0; k < 3; k++ {
					diff += math.Abs(s[k] - d[k])
				}
			}
		}
	}
	if iou := float64(inter) / float64(union); iou < 0.85 {
		t.Errorf("silhouette drifted: IoU %.2f (want >= 0.85)", iou)
	}
	if mean := diff / float64(both*3); mean > 30 {
		t.Errorf("colors drifted: mean per-channel error %.1f (want <= 30)", mean)
	}
}

func TestEnhancedBadgerHasMoreDetailAndTexture(t *testing.T) {
	srcR, outR, _, _, _, _ := rasters(t)
	count := func(g [][]RGB) int {
		seen := map[[3]int]bool{}
		for _, row := range g {
			for _, c := range row {
				if !near(c, Page, 2) {
					seen[[3]int{clamp(c[0]), clamp(c[1]), clamp(c[2])}] = true
				}
			}
		}
		return len(seen)
	}
	s, o := count(srcR), count(outR)
	if s > 10 || o < 60 {
		t.Fatalf("expected a flat source (%d colors) and a textured result (%d colors, want >= 60)", s, o)
	}
	pixels := func(g [][]RGB) int {
		n := 0
		for _, row := range g {
			for _, c := range row {
				if !near(c, Page, 2) {
					n++
				}
			}
		}
		return n
	}
	// 3x the pixels in the same footprint.
	if ratio := float64(pixels(outR)) / float64(pixels(srcR)); ratio < 2.5 || ratio > 3.5 {
		t.Errorf("badger pixel count ratio %.2f, want ~3x", ratio)
	}
}

func TestEyeSurvivesAndStaysPut(t *testing.T) {
	_, _, first, last, srcRows, outRows := rasters(t)
	centroid := func(rows [][]Cell) (x, y float64, n int) {
		for r := first; r <= last; r++ {
			for cx, c := range rows[r][1 : len(rows[r])-1] {
				if near(c.FG, Eye, 8) || near(c.BG, Eye, 8) {
					x += float64(cx)
					y += float64(r - first)
					n++
				}
			}
		}
		if n > 0 {
			x, y = x/float64(n), y/float64(n)
		}
		return
	}
	sx, sy, sn := centroid(srcRows)
	ox, oy, on := centroid(outRows)
	if sn == 0 || on == 0 {
		t.Fatalf("eye cells: source %d, enhanced %d", sn, on)
	}
	if math.Abs(sx-ox) > 2 || math.Abs(sy-oy) > 1.5 {
		t.Errorf("eye moved from (%.1f,%.1f) to (%.1f,%.1f)", sx, sy, ox, oy)
	}
}
