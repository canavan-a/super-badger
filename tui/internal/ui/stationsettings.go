package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"superbadger-tui/internal/api"
)

// stationSettingsModel edits one station: identity, which top-bar actions
// show, and each data point's display settings. Data point values are shown
// as text only (no history graphs).
type stationSettingsModel struct {
	client  *api.Client
	st      Styles
	station api.Station
	dps     []api.DataPoint
	form    form
	msg     string
	err     string

	editingDP bool
	dpKey     string
	dpForm    form
	w, h      int
}

const dpPrefix = "dp:"

func newStationSettings(c *api.Client, st Styles, s api.Station) *stationSettingsModel {
	m := &stationSettingsModel{client: c, st: st, station: s}
	m.rebuild()
	return m
}

func (m *stationSettingsModel) rebuild() {
	alias := ""
	if m.station.Alias != nil {
		alias = *m.station.Alias
	}
	fields := []field{
		{kind: fHeader, label: "Identity"},
		{key: "name", label: "Name", kind: fText, value: m.station.Name},
		{key: "alias", label: "Alias", kind: fText, value: alias, note: "metric sources target this"},
		{key: "color", label: "Color", kind: fChoice, value: m.station.Color, choices: api.StationColors},
		{kind: fHeader, label: "Top bar"},
	}
	for _, a := range api.TopBarActionKeys {
		note := "button"
		if a == "tokens" {
			note = "context token count"
		}
		fields = append(fields, field{key: "act:" + a, label: a, kind: fToggle, on: m.station.HasAction(a), note: note})
	}
	fields = append(fields, field{kind: fHeader, label: "Data points"})
	if len(m.dps) == 0 {
		fields = append(fields, field{key: "nodp", label: "(none reported yet)", kind: fHeader})
	}
	for _, d := range m.dps {
		name := d.Label
		if name == "" {
			name = d.Key
		}
		note := api.FormatValue(d.Value, d.Decimals)
		if d.ShowOnTopBar {
			note += " · top bar"
		}
		fields = append(fields, field{key: dpPrefix + d.Key, label: name, kind: fAction, note: note})
	}
	keep := m.form.cursor
	m.form = newForm(fields)
	m.form.width = m.w
	if keep > 0 && keep < len(fields) && m.form.selectable(keep) {
		m.form.cursor = keep
	}
}

func (m *stationSettingsModel) Init() tea.Cmd { return m.load() }

func (m *stationSettingsModel) load() tea.Cmd {
	c, id := m.client, m.station.ID
	return func() tea.Msg {
		d, err := c.DataPoints(id)
		if err != nil {
			return opMsg{"dps", err, nil}
		}
		sort.SliceStable(d, func(i, j int) bool {
			if d[i].Order != d[j].Order {
				return d[i].Order < d[j].Order
			}
			return d[i].Key < d[j].Key
		})
		return opMsg{"dps", nil, d}
	}
}

func (m *stationSettingsModel) resize(w, h int) {
	m.w, m.h = w, h
	m.form.width = w
	m.dpForm.width = w
}

func (m *stationSettingsModel) patch(u api.StationUpdate) tea.Cmd {
	c, id := m.client, m.station.ID
	return func() tea.Msg {
		s, err := c.UpdateStation(id, u)
		return opMsg{"patch", err, s}
	}
}

func (m *stationSettingsModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case opMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			m.msg = ""
			if msg.tag == "patch" {
				m.rebuild() // drop the unsaved edit from the form
			}
			return m, nil
		}
		m.err = ""
		switch msg.tag {
		case "dps":
			m.dps = msg.val.([]api.DataPoint)
			m.rebuild()
		case "patch":
			m.station = msg.val.(api.Station)
			m.msg = "saved"
		case "dpsave":
			m.msg = "saved"
			return m, m.load()
		case "hide", "reorder":
			return m, m.load()
		}
	case tea.KeyMsg:
		if m.editingDP {
			return m.dpKeyMsg(msg)
		}
		s := msg.String()
		if !m.form.editing {
			switch s {
			case "esc":
				return m, func() tea.Msg { return backMsg{} }
			case "alt+up", "alt+down", "K", "J":
				return m, m.reorder(s == "alt+up" || s == "K")
			}
		}
		var ev *formEvent
		var cmd tea.Cmd
		m.form, ev, cmd = m.form.Update(msg)
		if ev == nil {
			return m, cmd
		}
		switch {
		case ev.key == "name":
			v := strings.TrimSpace(ev.value)
			if v == "" {
				m.err = "name can't be empty"
				m.rebuild()
				return m, nil
			}
			return m, m.patch(api.StationUpdate{Name: &v})
		case ev.key == "alias":
			v := strings.TrimSpace(ev.value)
			return m, m.patch(api.StationUpdate{Alias: &v}) // "" clears it
		case ev.key == "color":
			v := ev.value
			return m, m.patch(api.StationUpdate{Color: &v})
		case strings.HasPrefix(ev.key, "act:"):
			var acts []string
			for _, a := range api.TopBarActionKeys {
				if m.form.get("act:" + a).on {
					acts = append(acts, a)
				}
			}
			if acts == nil {
				acts = []string{}
			}
			return m, m.patch(api.StationUpdate{TopBarActions: &acts})
		case strings.HasPrefix(ev.key, dpPrefix):
			m.openDP(strings.TrimPrefix(ev.key, dpPrefix))
		}
		return m, cmd
	}
	return m, nil
}

// reorder moves the highlighted data point up or down and persists the order.
func (m *stationSettingsModel) reorder(up bool) tea.Cmd {
	key := m.form.fields[m.form.cursor].key
	if !strings.HasPrefix(key, dpPrefix) {
		return nil
	}
	k := strings.TrimPrefix(key, dpPrefix)
	i := -1
	for j, d := range m.dps {
		if d.Key == k {
			i = j
		}
	}
	j := i + 1
	if up {
		j = i - 1
	}
	if i < 0 || j < 0 || j >= len(m.dps) {
		return nil
	}
	m.dps[i], m.dps[j] = m.dps[j], m.dps[i]
	order := make([]string, len(m.dps))
	for n, d := range m.dps {
		order[n] = d.Key
	}
	m.rebuild()
	m.form.cursor = m.form.cursor + (j - i)
	c, id := m.client, m.station.ID
	return func() tea.Msg { return opMsg{"reorder", c.ReorderDataPoints(id, order), nil} }
}

func (m *stationSettingsModel) openDP(key string) {
	var d api.DataPoint
	for _, x := range m.dps {
		if x.Key == key {
			d = x
		}
	}
	dir := d.ThresholdDirection
	if dir == "" {
		dir = "above"
	}
	m.editingDP, m.dpKey = true, key
	m.dpForm = newForm([]field{
		{key: "label", label: "Label", kind: fText, value: d.Label, note: "blank = " + key},
		{key: "decimals", label: "Decimals", kind: fChoice, value: strconv.Itoa(d.Decimals), choices: []string{"0", "1", "2", "3", "4", "5", "6"}},
		{key: "top", label: "Show on top bar", kind: fToggle, on: d.ShowOnTopBar},
		{kind: fHeader, label: "Threshold alert"},
		{key: "th_on", label: "Enabled", kind: fToggle, on: d.ThresholdEnabled},
		{key: "th_val", label: "Value", kind: fText, value: strconv.FormatFloat(d.ThresholdValue, 'f', -1, 64)},
		{key: "th_dir", label: "Direction", kind: fChoice, value: dir, choices: []string{"above", "below"}},
		{kind: fHeader, label: "Data point"},
		{key: "hide", label: "Hide until next value", kind: fAction},
	})
	m.dpForm.width = m.w
}

func (m *stationSettingsModel) dpKeyMsg(k tea.KeyMsg) (screen, tea.Cmd) {
	if !m.dpForm.editing && k.String() == "esc" {
		m.editingDP, m.err = false, ""
		return m, nil
	}
	var ev *formEvent
	var cmd tea.Cmd
	m.dpForm, ev, cmd = m.dpForm.Update(k)
	if ev == nil {
		return m, cmd
	}
	c, id, key := m.client, m.station.ID, m.dpKey
	if ev.key == "hide" {
		m.editingDP = false
		return m, func() tea.Msg { return opMsg{"hide", c.HideDataPoint(id, key), nil} }
	}
	dec, _ := strconv.Atoi(m.dpForm.get("decimals").value)
	th, err := strconv.ParseFloat(strings.TrimSpace(m.dpForm.get("th_val").value), 64)
	if err != nil {
		m.err = "threshold value must be a number"
		return m, nil
	}
	m.err = ""
	set := api.DataPointSettings{
		Label:              strings.TrimSpace(m.dpForm.get("label").value),
		Decimals:           dec,
		ShowOnTopBar:       m.dpForm.get("top").on,
		ThresholdEnabled:   m.dpForm.get("th_on").on,
		ThresholdValue:     th,
		ThresholdDirection: m.dpForm.get("th_dir").value,
	}
	// Keep the local copy current so reopening shows what was just saved.
	for i := range m.dps {
		if m.dps[i].Key == key {
			d := &m.dps[i]
			d.Label, d.Decimals, d.ShowOnTopBar = set.Label, set.Decimals, set.ShowOnTopBar
			d.ThresholdEnabled, d.ThresholdValue, d.ThresholdDirection = set.ThresholdEnabled, set.ThresholdValue, set.ThresholdDirection
		}
	}
	return m, func() tea.Msg { return opMsg{"dpsave", c.UpdateDataPointSettings(id, key, set), nil} }
}

func (m *stationSettingsModel) View() string {
	s := m.st
	stripe := lipgloss.NewStyle().Foreground(lipgloss.Color(m.station.Color)).Render("▌")
	var b strings.Builder
	if m.editingDP {
		b.WriteString(fmt.Sprintf("%s %s\n\n", s.Bold.Render("Data point"), s.Muted.Render(m.dpKey)))
		b.WriteString(m.dpForm.View(s))
		b.WriteString("\n" + s.Muted.Render("changes save immediately · esc back") + "\n")
	} else {
		b.WriteString(stripe + " " + s.Bold.Render(m.station.Name+" settings") + "\n\n")
		b.WriteString(m.form.View(s))
		b.WriteString("\n" + s.Muted.Render("enter edit/toggle · ←/→ change choice · alt+↑/↓ reorder data point · esc back") + "\n")
	}
	if m.msg != "" && m.err == "" {
		b.WriteString(s.Muted.Render(m.msg) + "\n")
	}
	if m.err != "" {
		b.WriteString(s.Danger.Render(m.err) + "\n")
	}
	return b.String()
}
