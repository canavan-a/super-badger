package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type fieldKind int

const (
	fText fieldKind = iota
	fToggle
	fChoice
	fAction
	fHeader // non-selectable section label
)

// field is one row of a form. Forms are the shared building block for every
// settings screen: up/down move, enter edits/toggles/runs, left/right cycles a
// choice. Changes are reported to the owner immediately (autosave style).
type field struct {
	key     string
	label   string
	kind    fieldKind
	value   string // text value, or choice label
	on      bool
	choices []string
	secret  bool
	note    string // right-aligned dim hint
}

type formEvent struct {
	key    string
	kind   fieldKind
	value  string
	on     bool
	commit bool
}

type form struct {
	fields  []field
	cursor  int
	editing bool
	ti      textinput.Model
	width   int
}

func newForm(fields []field) form {
	ti := textinput.New()
	ti.Prompt = ""
	f := form{fields: fields, ti: ti}
	f.cursor = f.firstSelectable(0, 1)
	return f
}

func (f form) selectable(i int) bool {
	return i >= 0 && i < len(f.fields) && f.fields[i].kind != fHeader
}

func (f form) firstSelectable(from, dir int) int {
	for i := from; i >= 0 && i < len(f.fields); i += dir {
		if f.selectable(i) {
			return i
		}
	}
	return from
}

func (f *form) set(key string, fn func(*field)) {
	for i := range f.fields {
		if f.fields[i].key == key {
			fn(&f.fields[i])
		}
	}
}

func (f form) get(key string) field {
	for _, x := range f.fields {
		if x.key == key {
			return x
		}
	}
	return field{}
}

func (f *form) move(dir int) {
	for i := f.cursor + dir; i >= 0 && i < len(f.fields); i += dir {
		if f.selectable(i) {
			f.cursor = i
			return
		}
	}
}

func cycle(choices []string, cur string, dir int) string {
	if len(choices) == 0 {
		return cur
	}
	idx := 0
	for i, c := range choices {
		if c == cur {
			idx = i
		}
	}
	return choices[(idx+dir+len(choices))%len(choices)]
}

// Update returns an event when the user changed something or ran an action.
func (f form) Update(msg tea.Msg) (form, *formEvent, tea.Cmd) {
	k, isKey := msg.(tea.KeyMsg)
	if f.editing {
		if isKey {
			switch k.String() {
			case "enter":
				fd := &f.fields[f.cursor]
				fd.value = f.ti.Value()
				f.editing = false
				f.ti.Blur()
				return f, &formEvent{key: fd.key, kind: fText, value: fd.value, commit: true}, nil
			case "esc":
				f.editing = false
				f.ti.Blur()
				return f, nil, nil
			}
		}
		var cmd tea.Cmd
		f.ti, cmd = f.ti.Update(msg)
		return f, nil, cmd
	}
	if !isKey || len(f.fields) == 0 {
		return f, nil, nil
	}
	fd := &f.fields[f.cursor]
	switch k.String() {
	case "up", "k":
		f.move(-1)
	case "down", "j":
		f.move(1)
	case "left", "h", "right", "l":
		if fd.kind == fChoice {
			dir := 1
			if k.String() == "left" || k.String() == "h" {
				dir = -1
			}
			fd.value = cycle(fd.choices, fd.value, dir)
			return f, &formEvent{key: fd.key, kind: fChoice, value: fd.value}, nil
		}
	case " ", "enter":
		switch fd.kind {
		case fToggle:
			fd.on = !fd.on
			return f, &formEvent{key: fd.key, kind: fToggle, on: fd.on}, nil
		case fChoice:
			fd.value = cycle(fd.choices, fd.value, 1)
			return f, &formEvent{key: fd.key, kind: fChoice, value: fd.value}, nil
		case fAction:
			return f, &formEvent{key: fd.key, kind: fAction}, nil
		case fText:
			if k.String() == "enter" {
				f.editing = true
				f.ti.SetValue(fd.value)
				f.ti.CursorEnd()
				f.ti.EchoMode = textinput.EchoNormal
				if fd.secret {
					f.ti.EchoMode = textinput.EchoPassword
				}
				f.ti.Width = max(10, f.width-len(fd.label)-8)
				return f, nil, f.ti.Focus()
			}
		}
	}
	return f, nil, nil
}

func (f form) View(s Styles) string {
	var b strings.Builder
	pad := 0
	for _, fd := range f.fields {
		if fd.kind != fHeader && fd.kind != fAction {
			pad = max(pad, len([]rune(fd.label)))
		}
	}
	for i, fd := range f.fields {
		if fd.kind == fHeader {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(s.Muted.Render("── "+fd.label+" ") + "\n")
			continue
		}
		cur := "  "
		if i == f.cursor {
			cur = s.Primary.Render("▸ ")
		}
		label := fd.label
		for len([]rune(label)) < pad {
			label += " "
		}
		var val string
		switch fd.kind {
		case fText:
			val = fd.value
			if fd.secret && val != "" {
				val = strings.Repeat("•", min(len(val), 12))
			}
			if val == "" {
				val = s.Muted.Render("(empty)")
			}
			if f.editing && i == f.cursor {
				val = f.ti.View()
			}
		case fToggle:
			if fd.on {
				val = s.Success.Render("[x] on")
			} else {
				val = s.Muted.Render("[ ] off")
			}
		case fChoice:
			val = "‹ " + fd.value + " ›"
		case fAction:
			label = s.Primary.Render(fd.label)
			val = ""
		}
		line := cur + label
		if fd.kind != fAction {
			line += "  " + val
		}
		if fd.note != "" {
			line += "  " + s.Muted.Render(fd.note)
		}
		if i == f.cursor && !f.editing {
			line = s.Bold.Render(strings.TrimRight(line, " "))
			if fd.kind == fAction {
				line = cur + s.Sel.Render(fd.label)
			}
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
