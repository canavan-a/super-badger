package art

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Palette is the handful of an app theme's colors the logo uses. It is read
// straight from app/src/theme.tsx (ParseThemes) so the logo, the icons and
// the app can never disagree about a theme.
type Palette struct {
	Name                                      string
	Bg, Surface, Border, Muted, Text, Primary RGB
}

// Hex parses #rrggbb.
func Hex(s string) (RGB, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return RGB{}, fmt.Errorf("bad color %q", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return RGB{}, err
	}
	return RGB{float64(v >> 16), float64(v >> 8 & 0xff), float64(v & 0xff)}, nil
}

// ToHex formats a color as #rrggbb.
func ToHex(c RGB) string {
	return fmt.Sprintf("#%02x%02x%02x", clamp(c[0]), clamp(c[1]), clamp(c[2]))
}

var (
	themeBlock = regexp.MustCompile(`(?s)const (\w+): Theme = \{(.*?)\n\};`)
	themeField = regexp.MustCompile(`(\w+):\s*'(#[0-9a-fA-F]{6})'`)
)

// ParseThemes reads every `const x: Theme = {...}` block in theme.tsx.
func ParseThemes(src string) (map[string]Palette, error) {
	out := map[string]Palette{}
	for _, m := range themeBlock.FindAllStringSubmatch(src, -1) {
		f := map[string]RGB{}
		for _, kv := range themeField.FindAllStringSubmatch(m[2], -1) {
			c, err := Hex(kv[2])
			if err != nil {
				return nil, err
			}
			f[kv[1]] = c
		}
		for _, need := range []string{"bg", "surface", "border", "textMuted", "text", "primary"} {
			if _, ok := f[need]; !ok {
				return nil, fmt.Errorf("theme %s has no %s", m[1], need)
			}
		}
		out[m[1]] = Palette{m[1], f["bg"], f["surface"], f["border"], f["textMuted"], f["text"], f["primary"]}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no themes found")
	}
	return out, nil
}

// Light reports whether the theme has a light page.
func (p Palette) Light() bool { return lumOf(p.Bg) > 128 }

// Ramp is the theme's tones in the same dark→light order as ArtRamp: page,
// raised surface, badger body, mid, and text. This is mirrored exactly in
// app/src/logo/logoTheme.ts (a test compares the two).
func (p Palette) Ramp() [5]RGB {
	var body RGB
	if p.Light() {
		// border is near-white on a light page; derive a visible mid-gray
		body = vlerp(p.Muted, p.Bg, 0.45)
	} else {
		body = vlerp(p.Border, p.Muted, 0.35)
	}
	return [5]RGB{p.Bg, p.Surface, body, p.Muted, p.Text}
}

// Accent is the color for the cyan parts (the eye and the wordmark's gradient).
// Dark themes' primaries can be dull against a near-black page, so lift them.
func (p Palette) Accent() RGB {
	if p.Light() {
		return p.Primary
	}
	return vlerp(p.Primary, RGB{255, 255, 255}, 0.35)
}

// Color turns a tone into a real color for this theme.
func (p Palette) Color(t Tone) RGB {
	u, w := t.Values()
	r := p.Ramp()
	i := int(u)
	if i > 3 {
		i = 3
	}
	base := vlerp(r[i], r[i+1], u-float64(i))
	acc := vsub(p.Accent(), r[4])
	return RGB{base[0] + w*acc[0], base[1] + w*acc[1], base[2] + w*acc[2]}
}
