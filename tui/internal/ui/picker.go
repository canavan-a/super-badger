package ui

import (
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"superbadger-tui/internal/api"
)

// screen is any non-chat page. Chat is driven separately by the root.
type screen interface {
	Init() tea.Cmd
	Update(tea.Msg) (screen, tea.Cmd)
	View() string
	resize(w, h int)
}

// Shared navigation messages emitted by screens for the root to act on.
type (
	backMsg            struct{}
	navMsg             struct{ to string }
	openStationMsg     struct{ id uint }
	settingsChangedMsg struct{}
	stationsMsg        struct {
		list []api.Station
		err  error
	}
	// opMsg is the result of any async API call a screen kicked off.
	opMsg struct {
		tag string
		err error
		val any
	}
)

func sortedByID(in []api.Station) []api.Station {
	out := append([]api.Station(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

type pickerModel struct {
	client   *api.Client
	st       Styles
	stations []api.Station
	cursor   int
	current  uint // currently open station (0 = none)
	err      string
	w, h     int

	creating  bool
	form      form
	providers api.ProvidersResponse
	loaded    bool
	confirm   bool

	metrics map[uint][]api.DataPoint // top-bar data points per station
	gen     int
}

func newPicker(c *api.Client, st Styles, stations []api.Station, current uint) *pickerModel {
	genCounter++
	p := &pickerModel{client: c, st: st, current: current, metrics: map[uint][]api.DataPoint{}, gen: genCounter}
	p.setStations(stations)
	return p
}

func (p *pickerModel) setStations(list []api.Station) {
	p.stations = list
	sort.Slice(p.stations, func(i, j int) bool { return p.stations[i].Name < p.stations[j].Name })
	for i, s := range p.stations {
		if s.ID == p.current {
			p.cursor = i
		}
	}
	p.cursor = min(p.cursor, max(0, len(p.stations)-1))
}

func (p *pickerModel) Init() tea.Cmd { return tea.Batch(p.refresh(), p.tick()) }

// refresh reloads the station list and every station's metrics.
func (p *pickerModel) refresh() tea.Cmd {
	c := p.client
	return tea.Batch(func() tea.Msg {
		l, err := c.ListStations()
		return stationsMsg{l, err}
	}, p.fetchMetrics())
}

// tick re-fetches the metrics every few seconds while the screen is open.
func (p *pickerModel) tick() tea.Cmd {
	gen := p.gen
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return opMsg{"tick", nil, gen} })
}

// fetchMetrics loads every station's data points concurrently.
func (p *pickerModel) fetchMetrics() tea.Cmd {
	c := p.client
	ids := make([]uint, len(p.stations))
	for i, s := range p.stations {
		ids[i] = s.ID
	}
	return func() tea.Msg {
		type res struct {
			id  uint
			dps []api.DataPoint
		}
		ch := make(chan res, len(ids))
		for _, id := range ids {
			go func(id uint) {
				d, err := c.DataPoints(id)
				if err != nil {
					d = nil
				}
				ch <- res{id, d}
			}(id)
		}
		out := map[uint][]api.DataPoint{}
		for range ids {
			r := <-ch
			var top []api.DataPoint
			for _, d := range r.dps {
				if d.ShowOnTopBar {
					top = append(top, d)
				}
			}
			sort.SliceStable(top, func(i, j int) bool {
				if top[i].Order != top[j].Order {
					return top[i].Order < top[j].Order
				}
				return top[i].Key < top[j].Key
			})
			out[r.id] = top
		}
		return opMsg{"metrics", nil, out}
	}
}

func (p *pickerModel) resize(w, h int) { p.w, p.h = w, h; p.form.width = w }

func (p *pickerModel) startCreate() tea.Cmd {
	p.creating = true
	p.form = newForm([]field{
		{key: "name", label: "Name", kind: fText},
		{key: "provider", label: "Provider", kind: fChoice},
		{key: "model", label: "Model", kind: fChoice},
		{key: "directory", label: "Directory", kind: fText, note: "optional"},
		{key: "create", label: "Create station", kind: fAction},
	})
	p.form.width = p.w
	c := p.client
	return func() tea.Msg {
		r, err := c.Providers()
		return opMsg{"providers", err, r}
	}
}

func (p *pickerModel) applyProviders() {
	var ids []string
	for _, pr := range p.providers.Providers {
		ids = append(ids, pr.ID)
	}
	sort.Strings(ids)
	p.form.set("provider", func(f *field) { f.choices = ids; f.value = firstOr(ids, "") })
	p.applyModels()
}

func (p *pickerModel) applyModels() {
	prov := p.form.get("provider").value
	var ms []string
	for _, pr := range p.providers.Providers {
		if pr.ID == prov {
			for id := range pr.Models {
				ms = append(ms, id)
			}
		}
	}
	sort.Strings(ms)
	def := p.providers.Default[prov]
	p.form.set("model", func(f *field) {
		f.choices = ms
		f.value = firstOr(ms, "")
		for _, m := range ms {
			if m == def {
				f.value = m
			}
		}
	})
}

func firstOr(s []string, d string) string {
	if len(s) > 0 {
		return s[0]
	}
	return d
}

func (p *pickerModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case stationsMsg:
		if msg.err != nil {
			p.err = msg.err.Error()
		} else {
			p.err = ""
			p.setStations(msg.list)
		}
		return p, nil
	case opMsg:
		switch msg.tag {
		case "metrics":
			if msg.err == nil {
				p.metrics = msg.val.(map[uint][]api.DataPoint)
			}
		case "tick":
			if g, _ := msg.val.(int); g == p.gen {
				return p, tea.Batch(p.fetchMetrics(), p.tick())
			}
		case "providers":
			if msg.err != nil {
				p.err = "loading providers: " + msg.err.Error()
			} else {
				p.providers = msg.val.(api.ProvidersResponse)
				p.applyProviders()
			}
		case "create":
			if msg.err != nil {
				p.err = msg.err.Error()
			} else {
				return p, func() tea.Msg { return openStationMsg{msg.val.(api.Station).ID} }
			}
		case "delete":
			if msg.err != nil {
				p.err = msg.err.Error()
			}
			return p, p.refresh()
		}
		return p, nil
	case tea.KeyMsg:
		if p.creating {
			return p.createKey(msg)
		}
		s := msg.String()
		if p.confirm {
			p.confirm = false
			if (s == "y" || s == "Y") && len(p.stations) > 0 {
				c, id := p.client, p.stations[p.cursor].ID
				return p, func() tea.Msg { return opMsg{"delete", c.DeleteStation(id), nil} }
			}
			return p, nil
		}
		switch s {
		case "up", "k":
			p.cursor = max(0, p.cursor-1)
		case "down", "j":
			p.cursor = min(len(p.stations)-1, p.cursor+1)
		case "enter":
			if len(p.stations) > 0 {
				id := p.stations[p.cursor].ID
				return p, func() tea.Msg { return openStationMsg{id} }
			}
		case "n":
			return p, p.startCreate()
		case "d":
			if len(p.stations) > 0 {
				p.confirm = true
			}
		case "r":
			return p, p.refresh()
		case "g":
			return p, func() tea.Msg { return navMsg{"settings"} }
		case "esc":
			if p.current != 0 {
				return p, func() tea.Msg { return backMsg{} }
			}
		}
	}
	return p, nil
}

func (p *pickerModel) createKey(k tea.KeyMsg) (screen, tea.Cmd) {
	if !p.form.editing && k.String() == "esc" {
		p.creating, p.err = false, ""
		return p, nil
	}
	var ev *formEvent
	var cmd tea.Cmd
	p.form, ev, cmd = p.form.Update(k)
	if ev == nil {
		return p, cmd
	}
	switch {
	case ev.key == "provider":
		p.applyModels()
	case ev.key == "create" && ev.kind == fAction:
		params := api.CreateStationParams{
			Name:       strings.TrimSpace(p.form.get("name").value),
			ProviderID: p.form.get("provider").value,
			ModelID:    p.form.get("model").value,
			Directory:  strings.TrimSpace(p.form.get("directory").value),
		}
		if params.Name == "" || params.ProviderID == "" || params.ModelID == "" {
			p.err = "name, provider and model are required"
			return p, nil
		}
		p.err = ""
		c := p.client
		return p, func() tea.Msg {
			s, err := c.CreateStation(params)
			return opMsg{"create", err, s}
		}
	}
	return p, cmd
}

func (p *pickerModel) View() string {
	s := p.st
	var b strings.Builder
	if p.creating {
		b.WriteString(s.Bold.Render("New station") + "\n\n")
		b.WriteString(p.form.View(s))
		b.WriteString("\n" + s.Muted.Render("↑/↓ move · enter edit/select · ←/→ change choice · esc cancel") + "\n")
	} else {
		b.WriteString(s.Bold.Render("Stations") + "\n\n")
		if len(p.stations) == 0 {
			b.WriteString(s.Muted.Render("No stations yet — press n to create one.") + "\n")
		}
		// Each row is just the station and its metrics (the data points set to
		// show on the top bar) — no status or model text.
		namew := 0
		for _, st := range p.stations {
			namew = max(namew, lipgloss.Width(st.Name))
		}
		for i, st := range p.stations {
			stripe := lipgloss.NewStyle().Foreground(lipgloss.Color(st.Color)).Render("▌")
			cur := "  "
			name := s.Text.Render(st.Name)
			if i == p.cursor {
				cur, name = s.Primary.Render("▸ "), s.Sel.Render(st.Name)
			}
			name += strings.Repeat(" ", namew-lipgloss.Width(st.Name))
			var ms []string
			for _, d := range p.metrics[st.ID] {
				label := d.Label
				if label == "" {
					label = d.Key
				}
				ms = append(ms, s.Muted.Render(label+" ")+s.Primary.Bold(true).Render(api.FormatValue(d.Value, d.Decimals)))
			}
			b.WriteString(cur + stripe + " " + name + "   " + strings.Join(ms, s.Border.Render("  │  ")) + "\n")
		}
		hint := "enter open · n new · d delete · r refresh · g settings"
		if p.current != 0 {
			hint += " · esc back"
		}
		b.WriteString("\n" + s.Muted.Render(hint) + "\n")
		if p.confirm {
			b.WriteString(s.Danger.Render("Delete this station? (y/N)") + "\n")
		}
	}
	if p.err != "" {
		b.WriteString("\n" + s.Danger.Render(p.err) + "\n")
	}
	return b.String()
}
