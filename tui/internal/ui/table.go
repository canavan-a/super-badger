package ui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Markdown tables in assistant replies. opencode passes the model's text
// through untouched, and the tables in it are GFM pipe tables:
//
//	| Key | Range | Meaning |
//	|---|---|---|            (sometimes |:-:| for alignment)
//	| `x` | `[-1, 1]` | ... |
//
// Word-wrapped as plain text those came out as ragged rows of bars; here
// they're drawn as a box grid with every column padded to one width. A
// terminal can't scroll sideways, so a table wider than the transcript gets
// its widest columns narrowed and their cells wrapped instead — and once
// that would crush a column below gridMinCol, each row is shown as a
// stacked "Header: value" record instead (see renderRecords).

var tableSepRe = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)

type tableAlign int

const (
	alignLeft tableAlign = iota
	alignCenter
	alignRight
)

// isTableStart reports whether line is a table header: it starts with '|'
// and the next line is a |---|---| separator.
func isTableStart(line, next string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|") && strings.Contains(next, "|") && tableSepRe.MatchString(next)
}

func isTableRow(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "|") }

// splitTableRow splits one table line into cells. Pipes inside `code` spans
// and escaped \| don't split — models routinely leave a | unescaped inside
// a code cell.
func splitTableRow(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var cells []string
	var cur strings.Builder
	inCode := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' && i+1 < len(s) && s[i+1] == '|':
			cur.WriteByte('|')
			i++
		case c == '`':
			inCode = !inCode
			cur.WriteByte(c)
		case c == '|' && !inCode:
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

// cellText drops the inline markdown markers (`code`, **bold**) the rest of
// the transcript shows raw — inside a padded grid they only cost width.
var cellMarkRe = regexp.MustCompile("`([^`]*)`|\\*\\*([^*]+)\\*\\*|__([^_]+)__")

func cellText(s string) string {
	return clean(cellMarkRe.ReplaceAllString(s, "$1$2$3"))
}

// fitColumns shares avail columns of width between the table's columns:
// narrow columns keep their natural width, and whatever is left is split
// evenly between the wide ones.
func fitColumns(natural []int, avail int) []int {
	n := len(natural)
	widths := make([]int, n)
	done := make([]bool, n)
	for left := n; left > 0; {
		share := avail / left
		progressed := false
		for i, w := range natural {
			if !done[i] && w <= share {
				widths[i], done[i] = w, true
				avail -= w
				left--
				progressed = true
			}
		}
		if !progressed {
			// Everything left is wider than an even share: split evenly,
			// handing the remainder to the leftmost.
			share, extra := avail/left, avail%left
			for i := range natural {
				if !done[i] {
					widths[i] = share
					if extra > 0 {
						widths[i]++
						extra--
					}
				}
			}
			break
		}
	}
	return widths
}

// gridMinCol is the narrowest a column may be squeezed to (unless its
// content is narrower anyway) before the grid gives way to stacked records:
// past that a wrapped cell is a tall sliver of one or two words per line.
const gridMinCol = 12

// renderTable draws header/rows (already split, same column count) at most
// tw columns wide: a box grid when it fits, stacked records when it doesn't.
func (m *chatModel) renderTable(header []string, align []tableAlign, rows [][]string, tw int) string {
	cols := len(header)
	frame := 3*cols + 1 // "│ " + " │ " ... + " │"
	natural := make([]int, cols)
	for c, h := range header {
		natural[c] = max(1, lipgloss.Width(h))
	}
	for _, r := range rows {
		for c, v := range r {
			natural[c] = max(natural[c], lipgloss.Width(v))
		}
	}
	widths := natural
	sum := 0
	for _, w := range natural {
		sum += w
	}
	if sum+frame > tw {
		if tw-frame < cols {
			return m.renderRecords(header, rows, tw)
		}
		widths = fitColumns(natural, tw-frame)
		for c, w := range widths {
			if w < min(natural[c], gridMinCol) {
				return m.renderRecords(header, rows, tw)
			}
		}
	}

	border := m.st.Border
	line := func(l, mid, r string) string {
		segs := make([]string, cols)
		for c, w := range widths {
			segs[c] = strings.Repeat("─", w+2)
		}
		return border.Render(l + strings.Join(segs, mid) + r)
	}
	wrapped := false
	row := func(cells []string, style lipgloss.Style) []string {
		lines := make([][]string, cols)
		height := 1
		for c, v := range cells {
			pos := lipgloss.Left
			switch align[c] {
			case alignCenter:
				pos = lipgloss.Center
			case alignRight:
				pos = lipgloss.Right
			}
			lines[c] = strings.Split(lipgloss.NewStyle().Width(widths[c]).Align(pos).Render(v), "\n")
			height = max(height, len(lines[c]))
		}
		if height > 1 {
			wrapped = true
		}
		bar := border.Render("│")
		out := make([]string, height)
		for y := range height {
			var b strings.Builder
			b.WriteString(bar)
			for c := range cols {
				s := ""
				if y < len(lines[c]) {
					s = lines[c][y]
				}
				s += strings.Repeat(" ", max(0, widths[c]-lipgloss.Width(s)))
				b.WriteString(" " + style.Render(s) + " " + bar)
			}
			out[y] = b.String()
		}
		return out
	}

	out := []string{line("┌", "┬", "┐")}
	out = append(out, row(header, m.st.Primary.Bold(true))...)
	out = append(out, line("├", "┼", "┤"))
	body := make([][]string, len(rows))
	for i, r := range rows {
		body[i] = row(r, m.st.Text)
	}
	// Rules between body rows only once some cell has wrapped onto several
	// lines — without them a wrapped row can't be told from the next one.
	for i, b := range body {
		if i > 0 && wrapped {
			out = append(out, line("├", "┼", "┤"))
		}
		out = append(out, b...)
	}
	out = append(out, line("└", "┴", "┘"))
	return strings.Join(out, "\n")
}

// renderRecords is the narrow-window form: one block per row, a
// "Header: value" line per non-empty cell with wrapped lines indented under
// it, and a rule between rows.
//
//	Module: newt.nix
//	What it does: Pangolin newt tunnel client plus a
//	  newtctl helper
//	────────────────────────────
//	Module: server.nix
func (m *chatModel) renderRecords(header []string, rows [][]string, tw int) string {
	label := m.st.Primary.Bold(true)
	wrap := lipgloss.NewStyle().Width(max(1, tw-2))
	rule := m.st.Border.Render(strings.Repeat("─", tw))
	var out []string
	for i, r := range rows {
		if i > 0 {
			out = append(out, rule)
		}
		for c, v := range r {
			if v == "" {
				continue
			}
			name := header[c]
			if name == "" {
				name = "—"
			}
			for j, l := range strings.Split(wrap.Render(label.Render(name+":")+" "+m.st.Text.Render(v)), "\n") {
				if j > 0 {
					l = "  " + l
				}
				out = append(out, strings.TrimRight(l, " "))
			}
		}
	}
	return strings.Join(out, "\n")
}

// parseTable reads the table starting at lines[i] (a header, per
// isTableStart) and returns its rendered grid and the index after it.
func (m *chatModel) parseTable(lines []string, i, tw int) (string, int) {
	raw := splitTableRow(lines[i])
	cols := len(raw)
	header := make([]string, cols)
	for c, v := range raw {
		header[c] = cellText(v)
	}
	sep := splitTableRow(lines[i+1])
	align := make([]tableAlign, cols)
	for c := range cols {
		if c < len(sep) {
			l, r := strings.HasPrefix(sep[c], ":"), strings.HasSuffix(sep[c], ":")
			switch {
			case l && r:
				align[c] = alignCenter
			case r:
				align[c] = alignRight
			}
		}
	}
	var rows [][]string
	for i += 2; i < len(lines) && isTableRow(lines[i]); i++ {
		cells := splitTableRow(lines[i])
		r := make([]string, cols)
		for c := range cols {
			if c < len(cells) {
				r[c] = cellText(cells[c])
			}
		}
		rows = append(rows, r)
	}
	return m.renderTable(header, align, rows, tw), i
}
