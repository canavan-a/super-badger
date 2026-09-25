package art

import (
	"os"
	"strings"
	"testing"
)

func splashArt(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../ui/splash.ans")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExtractLogoFindsTheBadgerAndTheWordmark(t *testing.T) {
	l, err := ExtractLogo(splashArt(t))
	if err != nil {
		t.Fatal(err)
	}
	if l.W != 156 {
		t.Errorf("width %d sub-pixels, want 156", l.W)
	}
	// 12 cell rows of badger, 9 of wordmark (its p and g have descenders),
	// three sub-rows each.
	if len(l.Badger) != 36 || len(l.Word) != 27 {
		t.Errorf("badger %d rows, wordmark %d rows; want 36 and 27", len(l.Badger), len(l.Word))
	}
	// The eye exists in the badger.
	eye := 0
	for _, row := range l.Badger {
		for _, s := range row {
			if IsEye(s) {
				eye++
			}
		}
	}
	if eye < 8 {
		t.Errorf("only %d eye pixels", eye)
	}
}

func TestWordmarkSlicesTileTheInkWithNoGapsOrOverlaps(t *testing.T) {
	l, err := ExtractLogo(splashArt(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Slices) != SliceCount {
		t.Fatalf("%d slices, want %d", len(l.Slices), SliceCount)
	}
	for i, s := range l.Slices {
		if s.X1 <= s.X0 {
			t.Fatalf("slice %d is empty: %v", i, s)
		}
		if i > 0 && s.X0 != l.Slices[i-1].X1 {
			t.Fatalf("slice %d does not start where slice %d ends: %v %v", i, i-1, l.Slices[i-1], s)
		}
		if s.X1-s.X0 < 5 || s.X1-s.X0 > 30 {
			t.Errorf("slice %d is %d columns wide; expected a letter-ish width", i, s.X1-s.X0)
		}
	}
	// Every inked column belongs to a slice.
	lo, hi := l.Slices[0].X0, l.Slices[len(l.Slices)-1].X1
	for _, row := range l.Word {
		for x, s := range row {
			if s.On && (x < lo || x >= hi) {
				t.Fatalf("ink at column %d is outside every slice [%d,%d)", x, lo, hi)
			}
		}
	}
}

func TestTheSIsAFourteenByTwentyOnePixelLetter(t *testing.T) {
	l, err := ExtractLogo(splashArt(t))
	if err != nil {
		t.Fatal(err)
	}
	minX, minY, maxX, maxY := Bounds(l.Word, l.S.X0, l.S.X1)
	w, h := maxX-minX+1, maxY-minY+1
	if w < 12 || w > 16 || h < 19 || h > 23 {
		t.Fatalf("the S is %dx%d, expected about 14x21", w, h)
	}
}

func TestToneRoundTripsTheArtsOwnColors(t *testing.T) {
	// The ramp's anchors map to exact ramp positions with no accent lean...
	for i, c := range ArtRamp {
		got := ToneOf(c)
		if got.U != i*UStep || got.W != 0 {
			t.Errorf("anchor %d -> %+v, want U=%d W=0", i, got, i*UStep)
		}
	}
	// ...and the cyan is the accent.
	if got := ToneOf(ArtAccent); got.W != WStep {
		t.Errorf("cyan -> %+v, want full accent lean", got)
	}
}

func TestQuantizationKeepsTheShapeCountSmall(t *testing.T) {
	l, err := ExtractLogo(splashArt(t))
	if err != nil {
		t.Fatal(err)
	}
	g := GroupOf(l.Badger, 0, l.W, func(s Sub) bool { return !IsEye(s) })
	w := GroupOf(l.Word, 0, l.W, func(Sub) bool { return true })
	runs := 0
	for _, r := range g {
		runs += len(r) / 3
	}
	for _, r := range w {
		runs += len(r) / 3
	}
	if len(g) > 80 || len(w) > 80 {
		t.Errorf("too many tones: badger %d, wordmark %d", len(g), len(w))
	}
	if runs > 9000 {
		t.Errorf("%d runs; the vector data would be too heavy", runs)
	}
	t.Logf("badger tones=%d wordmark tones=%d total runs=%d", len(g), len(w), runs)
}

const sampleTheme = `
const light: Theme = {
  name: 'light',
  bg: '#ffffff',
  surface: '#f6f8fa',
  border: '#e5e5e5',
  text: '#1b1f24',
  textMuted: '#57606a',
  primary: '#0969da',
};
const dark: Theme = {
  name: 'dark',
  bg: '#0d1117',
  surface: '#161b22',
  border: '#30363d',
  text: '#e6edf3',
  textMuted: '#8b949e',
  primary: '#2f81f7',
};
`

func TestParseThemesAndMapping(t *testing.T) {
	th, err := ParseThemes(sampleTheme)
	if err != nil {
		t.Fatal(err)
	}
	if len(th) != 2 {
		t.Fatalf("parsed %d themes", len(th))
	}
	for name, p := range th {
		// The art's page always becomes the theme's page; its top ramp end the text color.
		if got := p.Color(Tone{U: 0, W: 0}); ToHex(got) != ToHex(p.Bg) {
			t.Errorf("%s: page -> %s, want %s", name, ToHex(got), ToHex(p.Bg))
		}
		if got := p.Color(Tone{U: 4 * UStep, W: 0}); ToHex(got) != ToHex(p.Text) {
			t.Errorf("%s: strokes -> %s, want %s", name, ToHex(got), ToHex(p.Text))
		}
		if got := p.Color(Tone{U: 4 * UStep, W: WStep}); ToHex(got) != ToHex(p.Accent()) {
			t.Errorf("%s: accent -> %s, want %s", name, ToHex(got), ToHex(p.Accent()))
		}
	}
	if !th["light"].Light() || th["dark"].Light() {
		t.Error("Light() is wrong")
	}
	if _, err := ParseThemes("nothing here"); err == nil || !strings.Contains(err.Error(), "no themes") {
		t.Errorf("expected an error for a file without themes, got %v", err)
	}
}

func TestParseTheRealAppThemes(t *testing.T) {
	b, err := os.ReadFile("../../../app/src/theme.tsx")
	if err != nil {
		t.Fatal(err)
	}
	th, err := ParseThemes(string(b))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"light", "dark", "slate", "sepia", "ember"} {
		if _, ok := th[n]; !ok {
			t.Errorf("theme %s not found in theme.tsx", n)
		}
	}
}

func TestSlicesEachCarryAboutOneLettersWorthOfInk(t *testing.T) {
	l, err := ExtractLogo(splashArt(t))
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	per := make([]int, len(l.Slices))
	for _, row := range l.Word {
		for x, s := range row {
			if !s.On {
				continue
			}
			total++
			for i, sp := range l.Slices {
				if x >= sp.X0 && x < sp.X1 {
					per[i]++
				}
			}
		}
	}
	mean := float64(total) / float64(len(per))
	for i, n := range per {
		if float64(n) < mean*0.4 || float64(n) > mean*1.8 {
			t.Errorf("slice %d has %d ink pixels; mean is %.0f (slices should be balanced): %v", i, n, mean, per)
		}
	}
}
