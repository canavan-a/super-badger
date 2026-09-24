package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palettes. "burrow" is the house look, taken from the superbadger.ans art
// (deep navy, dusty blues, one electric cyan accent); the rest are the app's
// themes (app/src/theme.tsx). Every theme paints its own background, so the
// TUI looks the same whatever the terminal's colors are.
type Theme struct {
	Name    string
	Bg      string // page
	Panel   string // raised surface (your messages, chips)
	Border  string
	Muted   string // dimmest readable text
	Soft    string // secondary text
	Text    string
	Accent  string // you / focus / primary
	Danger  string
	Success string
	CodeBg  string
}

var themes = map[string]Theme{
	"burrow": {"burrow", "#101422", "#1a2140", "#3a4e80", "#5a6c9c", "#6e84b8", "#c4deff", "#aafffa", "#ff7a90", "#7ee6b0", "#0b0e1a"},
	"dark":   {"dark", "#0d1117", "#161b22", "#30363d", "#6e7681", "#8b949e", "#e6edf3", "#2f81f7", "#f85149", "#3fb950", "#161b22"},
	"slate":  {"slate", "#1e2530", "#262e3b", "#3c4757", "#6b7a90", "#93a0b3", "#dde3ea", "#5aa9e6", "#ef6a6a", "#6fcf97", "#232b37"},
	"ember":  {"ember", "#050403", "#0c0908", "#3a2620", "#6a5045", "#8a6c5f", "#e8d9d0", "#e0703c", "#c93a3a", "#4f9e70", "#0c0908"},
	"light":  {"light", "#ffffff", "#eef2f7", "#d0d7de", "#8c959f", "#57606a", "#1b1f24", "#0969da", "#cf222e", "#1a7f37", "#f6f8fa"},
	"sepia":  {"sepia", "#f4ecd8", "#e8dcc0", "#d8c6a0", "#a39373", "#7a6a4d", "#3b2f1e", "#a0651b", "#a13c2c", "#4e7c3a", "#ece0c6"},
}

type Styles struct {
	T       Theme
	Text    lipgloss.Style
	Muted   lipgloss.Style
	Soft    lipgloss.Style
	Primary lipgloss.Style // accent
	Danger  lipgloss.Style
	Success lipgloss.Style
	Bold    lipgloss.Style
	Border  lipgloss.Style
	Code    lipgloss.Style
	Sel     lipgloss.Style
}

func NewStyles(name string) Styles {
	t, ok := themes[name]
	if !ok {
		t = themes["burrow"]
	}
	fg := func(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }
	return Styles{
		T:       t,
		Text:    fg(t.Text),
		Muted:   fg(t.Muted),
		Soft:    fg(t.Soft),
		Primary: fg(t.Accent),
		Danger:  fg(t.Danger),
		Success: fg(t.Success),
		Bold:    lipgloss.NewStyle().Bold(true),
		Border:  fg(t.Border),
		Code:    lipgloss.NewStyle().Background(lipgloss.Color(t.CodeBg)).Foreground(lipgloss.Color(t.Text)),
		Sel:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.Accent)),
	}
}

func rgb(hex string) (int, int, int) {
	hex = strings.TrimPrefix(hex, "#")
	v, _ := strconv.ParseUint(hex, 16, 32)
	return int(v >> 16), int(v >> 8 & 0xff), int(v & 0xff)
}

func sgr(kind int, hex string) string {
	r, g, b := rgb(hex)
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", kind, r, g, b)
}

// Paint fills a w×h screen with the theme's background and default text
// color. Styled spans end in a reset, which would drop back to the terminal's
// own colors mid-line, so the base colors are re-applied after every reset.
func (s Styles) Paint(view string, w, h int) string {
	base := sgr(48, s.T.Bg) + sgr(38, s.T.Text)
	lines := strings.Split(view, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	clip := lipgloss.NewStyle().MaxWidth(w)
	for i, l := range lines {
		l = clip.Render(l)
		l = strings.ReplaceAll(l, "\x1b[0m", "\x1b[0m"+base)
		l = strings.ReplaceAll(l, "\x1b[m", "\x1b[m"+base)
		if pad := w - lipgloss.Width(l); pad > 0 {
			l += strings.Repeat(" ", pad)
		}
		lines[i] = base + l + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}

// chip is a filled label, e.g. the "you" / station-name tags.
func chip(text, fg, bg string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg)).Render(" " + text + " ")
}

// box draws a rounded frame of total width w around content.
func box(content string, w int, color string) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(color)).
		Width(max(1, w-2)).
		Render(content)
}

// lerpHex blends two colors, t in [0,1].
func lerpHex(a, b string, t float64) string {
	ar, ag, ab := rgb(a)
	br, bg, bb := rgb(b)
	mix := func(x, y int) int { return int(float64(x) + (float64(y)-float64(x))*t) }
	return fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb))
}
