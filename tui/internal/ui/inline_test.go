package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderInlineStylesAndDropsMarkers(t *testing.T) {
	m := tableChat(80)
	cases := []struct{ in, plain, sgr string }{
		{"a **bold** b", "a bold b", "\x1b[1m"},
		{"a __bold__ b", "a bold b", "\x1b[1m"},
		{"a *it* b", "a it b", "\x1b[3m"},
		{"a ~~gone~~ b", "a gone b", "\x1b[9m"},
		{"run `go test` now", "run go test now", bgTriple(m.st.T.CodeBg)},
		{"**bold with `code`**", "bold with code", "\x1b[1m"},
	}
	for _, c := range cases {
		out := m.renderInline(c.in)
		if got := ansi.ReplaceAllString(out, ""); got != c.plain {
			t.Errorf("%q: text %q, want %q", c.in, got, c.plain)
		}
		if !strings.Contains(out, c.sgr) {
			t.Errorf("%q: no %q in %q", c.in, c.sgr, out)
		}
	}
}

func TestRenderInlineLeavesNonMarkupAlone(t *testing.T) {
	m := tableChat(80)
	for _, in := range []string{
		"* a list item",
		"snake__case__name",
		"2*3*4",
		"a ** b ** c",
		"unclosed **bold",
	} {
		if out := m.renderInline(in); out != in {
			t.Errorf("%q changed to %q", in, out)
		}
	}
}

func TestRenderTextBoldSurvivesWrap(t *testing.T) {
	m := tableChat(30)
	out := m.renderText("some leading words then **a bold run that wraps across lines** end", lipgloss.NewStyle().Width(24))
	plain := ansi.ReplaceAllString(out, "")
	if strings.Contains(plain, "**") {
		t.Fatalf("markers leaked:\n%s", plain)
	}
	// Every line holding part of the run starts its own bold.
	for _, l := range strings.Split(out, "\n") {
		if p := ansi.ReplaceAllString(l, ""); strings.Contains(p, "wraps") || strings.Contains(p, "across") {
			if !strings.Contains(l, "\x1b[1m") {
				t.Errorf("wrapped line lost bold: %q", l)
			}
		}
	}
}
