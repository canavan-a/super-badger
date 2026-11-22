package ui

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// Inline markdown in assistant prose: `code`, **bold** / __bold__, *italic*,
// ~~strike~~. The markers are dropped and the text styled instead.
//
// Styled runs are rendered a word at a time, so every word carries its own
// escape codes: the wrapper can then break a run across lines (and the rail
// be prepended to each) without the style leaking or getting cut off.
var inlineRe = regexp.MustCompile(
	"`([^`]+)`" +
		`|\*\*([^\s*](?:[^*]*[^\s*])?)\*\*` +
		`|__([^\s_](?:[^_]*[^\s_])?)__` +
		`|~~([^\s~](?:[^~]*[^\s~])?)~~` +
		`|\*([^\s*](?:[^*]*[^\s*])?)\*`)

// renderInline styles one line of prose.
func (m *chatModel) renderInline(s string) string {
	return m.inline(s, lipgloss.NewStyle(), false)
}

func (m *chatModel) inline(s string, st lipgloss.Style, styled bool) string {
	var b strings.Builder
	last := 0
	for _, g := range inlineRe.FindAllStringSubmatchIndex(s, -1) {
		start, end := g[0], g[1]
		// __x__ and *x* only count at word edges, so snake__case, 2*3*4
		// and the like stay as they are. ** and ~~ are unambiguous enough.
		if (g[6] >= 0 || g[10] >= 0) && !(wordEdge(s, start, true) && wordEdge(s, end, false)) {
			continue
		}
		b.WriteString(styleWords(s[last:start], st, styled))
		switch {
		case g[2] >= 0:
			b.WriteString(styleWords(s[g[2]:g[3]], m.st.Code, true))
		case g[4] >= 0:
			b.WriteString(m.inline(s[g[4]:g[5]], st.Bold(true), true))
		case g[6] >= 0:
			b.WriteString(m.inline(s[g[6]:g[7]], st.Bold(true), true))
		case g[8] >= 0:
			b.WriteString(m.inline(s[g[8]:g[9]], st.Strikethrough(true), true))
		case g[10] >= 0:
			b.WriteString(m.inline(s[g[10]:g[11]], st.Italic(true), true))
		}
		last = end
	}
	b.WriteString(styleWords(s[last:], st, styled))
	return b.String()
}

// wordEdge reports whether position i in s isn't in the middle of a word:
// the rune before it (before=true) or at it isn't a letter or digit.
func wordEdge(s string, i int, before bool) bool {
	var r rune
	if before {
		if i == 0 {
			return true
		}
		r, _ = utf8.DecodeLastRuneInString(s[:i])
	} else {
		if i >= len(s) {
			return true
		}
		r, _ = utf8.DecodeRuneInString(s[i:])
	}
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// styleWords renders each space-separated word of s in st on its own,
// leaving the spaces between them plain (see inlineRe).
func styleWords(s string, st lipgloss.Style, styled bool) string {
	if !styled || s == "" {
		return s
	}
	parts := strings.Split(s, " ")
	for i, p := range parts {
		if p != "" {
			parts[i] = st.Render(p)
		}
	}
	return strings.Join(parts, " ")
}
