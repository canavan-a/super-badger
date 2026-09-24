package ui

import (
	"strconv"
	"strings"
)

// The title art is drawn in the burrow palette. To follow the chosen theme it
// is recolored by mapping the art's own tones onto the theme's tones, in order
// from darkest to lightest:
//
//	page → Bg, deep shadow → Panel, badger body → Border (Muted on light
//	themes), mid blue → Soft, light strokes → Text, cyan → Accent
//
// Colors between the art's tones (the antialiased edge, fur texture, the
// wordmark's gradient) land the same fraction of the way between the theme's
// tones, so the picture keeps its shading. "burrow" itself is left untouched.

type vec [3]float64

var (
	artRamp   = []vec{{16, 20, 34}, {24, 30, 60}, {58, 78, 128}, {110, 132, 184}, {196, 222, 255}}
	artAccent = vec{170, 255, 250}
)

func hexVec(h string) vec {
	v, _ := strconv.ParseUint(strings.TrimPrefix(h, "#"), 16, 32)
	return vec{float64(v >> 16), float64(v >> 8 & 0xff), float64(v & 0xff)}
}

func parseVec(s string) (vec, bool) {
	p := strings.Split(s, ";")
	if len(p) != 3 {
		return vec{}, false
	}
	var v vec
	for i := range p {
		n, err := strconv.Atoi(p[i])
		if err != nil {
			return vec{}, false
		}
		v[i] = float64(n)
	}
	return v, true
}

func (v vec) str() string {
	c := func(f float64) string {
		if f < 0 {
			f = 0
		}
		if f > 255 {
			f = 255
		}
		return strconv.Itoa(int(f + 0.5))
	}
	return c(v[0]) + ";" + c(v[1]) + ";" + c(v[2])
}

func vlum(v vec) float64 { return 0.2126*v[0] + 0.7152*v[1] + 0.0722*v[2] }

func vlerp(a, b vec, t float64) vec {
	return vec{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t}
}

func vsub(a, b vec) vec { return vec{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }

func vdot(a, b vec) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

func clamp01(f float64) float64 { return min(1, max(0, f)) }

// themeLight reports whether the theme has a light page.
func themeLight(t Theme) bool { return vlum(hexVec(t.Bg)) > 128 }

// themeRamp is the theme's tones in the same dark-to-light order as artRamp
// (that is, art page → theme page, ..., art light strokes → theme text).
func themeRamp(t Theme) []vec {
	body := t.Border
	if themeLight(t) {
		body = t.Muted // Border is near-white on a light page: the badger would vanish
	}
	return []vec{hexVec(t.Bg), hexVec(t.Panel), hexVec(body), hexVec(t.Soft), hexVec(t.Text)}
}

// remapColor moves one art color into the theme.
func remapColor(c vec, tr []vec, accent vec) vec {
	// Where c falls along the art's own ramp (by luminance)...
	L := vlum(c)
	i := 0
	for i < len(artRamp)-2 && L > vlum(artRamp[i+1]) {
		i++
	}
	La, Lb := vlum(artRamp[i]), vlum(artRamp[i+1])
	t := clamp01((L - La) / (Lb - La))
	baseArt := vlerp(artRamp[i], artRamp[i+1], t)
	baseTh := vlerp(tr[i], tr[i+1], t)
	// ...and how far it leans toward the cyan accent, off that ramp.
	dir := vsub(artAccent, artRamp[len(artRamp)-1])
	w := min(1.15, max(0, vdot(vsub(c, baseArt), dir)/vdot(dir, dir)))
	acc := vsub(accent, tr[len(tr)-1])
	return vec{baseTh[0] + w*acc[0], baseTh[1] + w*acc[1], baseTh[2] + w*acc[2]}
}

// splashSet is the title art recolored for one theme.
type splashSet struct {
	grid [][]splashCellT
	rows []string // static ANSI rows of grid
	to   string   // "r;g;b" the glimmer moves toward
}

var splashSets = map[string]*splashSet{}

// splashFor returns (and caches) the art for a theme.
func splashFor(t Theme) *splashSet {
	if s, ok := splashSets[t.Name]; ok {
		return s
	}
	var s *splashSet
	if t.Name == "burrow" {
		s = &splashSet{grid: splashGrid, rows: splashRows, to: "255;255;255"}
	} else {
		tr, accent := themeRamp(t), hexVec(t.Accent)
		remap := func(rgb string) string {
			if v, ok := parseVec(rgb); ok {
				return remapColor(v, tr, accent).str()
			}
			return rgb
		}
		s = &splashSet{to: "255;255;255"}
		if themeLight(t) {
			s.to = "0;0;0" // strokes are dark on a light page: darken to make them pop
		}
		for _, row := range splashGrid {
			out := make([]splashCellT, len(row))
			for x, c := range row {
				if c.fg != "" && c.bg != "" {
					c.fg, c.bg = remap(c.fg), remap(c.bg)
					c.esc = splashSGRs(c.bg, c.fg)
				}
				out[x] = c
			}
			s.grid = append(s.grid, out)
			s.rows = append(s.rows, joinCells(out))
		}
	}
	splashSets[t.Name] = s
	return s
}
