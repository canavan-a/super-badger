package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"superbadger-tui/internal/api"
)

func tableChat(w int) *chatModel {
	m := newChat(api.New("http://127.0.0.1:1", ""), NewStyles("harbor"), api.Station{ID: 1, Name: "alpha", Reachable: true}, 0, 1)
	m.w = w
	return m
}

const sampleTable = "Before.\n\n| Key | Range | Meaning |\n|---|:-:|---|\n| `x` | `[-1, 1]` | Horizontal offset of the box centre |\n| `lost` | bool | `true` = no detection |\n\nAfter."

func TestRenderTextDrawsTableGrid(t *testing.T) {
	m := tableChat(80)
	out := ansi.ReplaceAllString(m.renderText(sampleTable, lipgloss.NewStyle().Width(74)), "")
	for _, want := range []string{"┌", "│ Key  │", "├", "└", "Before.", "After."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "|---") || strings.Contains(out, "`x`") {
		t.Errorf("raw markdown leaked into grid:\n%s", out)
	}
	// Every grid line is the same width: the columns line up.
	var w int
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "┌") || strings.HasPrefix(l, "│") || strings.HasPrefix(l, "└") {
			if w == 0 {
				w = lipgloss.Width(l)
			} else if lipgloss.Width(l) != w {
				t.Errorf("ragged grid line %q (want width %d)", l, w)
			}
		}
	}
	if testing.Verbose() {
		t.Log("\n" + out)
	}
}

func TestRenderTableWrapsToFitNarrowTerminal(t *testing.T) {
	m := tableChat(40)
	out := ansi.ReplaceAllString(m.renderText(sampleTable, lipgloss.NewStyle().Width(34)), "")
	for _, l := range strings.Split(out, "\n") {
		if lipgloss.Width(l) > 34 {
			t.Errorf("line wider than 34: %q", l)
		}
	}
	if testing.Verbose() {
		t.Log("\n" + out)
	}
}

func TestSplitTableRow(t *testing.T) {
	got := splitTableRow("| `a | b` | c \\| d |  |")
	want := []string{"`a | b`", "c | d", ""}
	if strings.Join(got, "¦") != strings.Join(want, "¦") {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTableNeedsSeparator(t *testing.T) {
	if isTableStart("| a | b |", "| c | d |") {
		t.Error("two pipe rows without a separator aren't a table")
	}
	if !isTableStart("| a | b |", "|------|:---:|") {
		t.Error("missed a table")
	}
}

// Resizing the window re-renders the transcript from the raw text, so a
// table re-fits on every resize: it never runs past the edge, and grows back
// to its natural width (no wrapping) once there's room again.
func TestTableRefitsOnResize(t *testing.T) {
	m := tableChat(120)
	natural := 0
	for _, w := range []int{120, 80, 60, 40, 30, 80, 120} {
		m.resize(w, 40)
		tw := max(10, m.w-6)
		out := ansi.ReplaceAllString(m.renderText(sampleTable, lipgloss.NewStyle().Width(tw)), "")
		grid := 0
		for _, l := range strings.Split(out, "\n") {
			if lw := lipgloss.Width(l); lw > tw {
				t.Errorf("width %d: line %d wide, limit %d: %q", w, lw, tw, l)
			}
			if strings.HasPrefix(l, "│") {
				grid++
			}
		}
		if w == 120 {
			if natural == 0 {
				natural = grid
			} else if grid != natural {
				t.Errorf("back at width 120: %d grid rows, want %d (unwrapped)", grid, natural)
			}
		}
		if testing.Verbose() && (w == 120 || w == 40) {
			t.Logf("width %d:\n%s", w, out)
		}
	}
}

const modulesTable = "| Module | What it does | Used by |\n|---|---|---|\n" +
	"| `newt.nix` | Pangolin newt tunnel client plus a `newtctl` helper | nothing |\n" +
	"| `nix-helpers.nix` | nixrb, nixsync, nixdiff and other shell helpers, plus fastfetch | every host (through `common.nix`, or directly for matrix) |\n" +
	"| `server.nix` | monero-cli, tor, socat | |\n"

// Too narrow for a readable grid: each row becomes a stacked record.
func TestNarrowTableBecomesRecords(t *testing.T) {
	m := tableChat(46)
	tw := max(10, m.w-6)
	out := ansi.ReplaceAllString(m.renderText(modulesTable, lipgloss.NewStyle().Width(tw)), "")
	if strings.Contains(out, "│") || strings.Contains(out, "┌") {
		t.Errorf("expected records, got a grid:\n%s", out)
	}
	for _, want := range []string{"Module: newt.nix", "What it does: Pangolin", "Used by: nothing", "Module: server.nix", strings.Repeat("─", tw)} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Used by: \n") || strings.HasSuffix(strings.TrimSpace(out), "Used by:") {
		t.Errorf("empty cell should be skipped:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if lipgloss.Width(l) > tw {
			t.Errorf("line wider than %d: %q", tw, l)
		}
	}
	if testing.Verbose() {
		t.Log("\n" + out)
	}
	// Wide enough again: back to a grid.
	m.resize(140, 40)
	wide := ansi.ReplaceAllString(m.renderText(modulesTable, lipgloss.NewStyle().Width(134)), "")
	if !strings.Contains(wide, "┌") {
		t.Errorf("expected a grid at width 140:\n%s", wide)
	}
	if testing.Verbose() {
		t.Log("\n" + wide)
	}
}
