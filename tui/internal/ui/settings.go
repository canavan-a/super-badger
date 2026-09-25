package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"superbadger-tui/internal/api"
	"superbadger-tui/internal/config"
	"superbadger-tui/internal/notify"
)

// ---- global settings (server URL, token, theme, notifications) ----

type settingsModel struct {
	cfg     *config.Config
	client  *api.Client
	st      Styles
	form    form
	msg     string
	w, h    int
	desk    notify.Notifier // nil when unavailable
	deskWhy error
}

// deskNote is the hint beside the Desktop notifications switch.
func deskNote(d notify.Notifier, why error) string {
	if d != nil {
		return "via " + d.Name()
	}
	if why != nil {
		return "unavailable — " + why.Error()
	}
	return "unavailable on this machine"
}

func newSettings(cfg *config.Config, c *api.Client, st Styles, desk notify.Notifier, deskWhy error) *settingsModel {
	m := &settingsModel{cfg: cfg, client: c, st: st, desk: desk, deskWhy: deskWhy}
	m.form = newForm([]field{
		{kind: fHeader, label: "Connection"},
		{key: "server_url", label: "Server URL", kind: fText, value: cfg.ServerURL, note: "e.g. http://localhost:8080"},
		{key: "auth_token", label: "Auth token", kind: fText, value: cfg.AuthToken, secret: true, note: "badger token generate <label>"},
		{key: "test", label: "Test connection", kind: fAction},
		{kind: fHeader, label: "Appearance"},
		{key: "theme", label: "Theme", kind: fChoice, value: cfg.Theme, choices: config.ThemeNames, note: "/splash previews the title screen"},
		{kind: fHeader, label: "Notifications"},
		{key: "notifications", label: "Other-station alerts", kind: fToggle, on: cfg.Notifications},
		{key: "desktop", label: "Desktop notifications", kind: fToggle, on: cfg.DesktopNotifications, note: deskNote(desk, deskWhy)},
		{key: "desktest", label: "Send a test notification", kind: fAction},
		{key: "splash", label: "Launch art", kind: fToggle, on: cfg.Splash, note: "shown at launch"},
		{kind: fHeader, label: "Server"},
		{key: "metrics", label: "Metric sources…", kind: fAction},
		{key: "mullvad", label: "Mullvad VPN…", kind: fAction},
	})
	return m
}

func (m *settingsModel) Init() tea.Cmd { return nil }

func (m *settingsModel) resize(w, h int) { m.w, m.h = w, h; m.form.width = w }

func (m *settingsModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case deskTestDoneMsg:
		if msg.err != nil {
			m.msg = "✗ desktop notification failed: " + msg.err.Error()
		} else {
			m.msg = "✓ sent via " + msg.name + " — you should see it on your desktop now"
		}
	case opMsg:
		if msg.tag == "test" {
			if msg.err != nil || !msg.val.(bool) {
				m.msg = "✗ cannot reach the server"
			} else {
				m.msg = "✓ server is up"
			}
		}
	case tea.KeyMsg:
		if !m.form.editing && msg.String() == "esc" {
			return m, func() tea.Msg { return backMsg{} }
		}
		var ev *formEvent
		var cmd tea.Cmd
		m.form, ev, cmd = m.form.Update(msg)
		if ev == nil {
			return m, cmd
		}
		changed := true
		switch ev.key {
		case "server_url":
			m.cfg.ServerURL = strings.TrimRight(strings.TrimSpace(ev.value), "/")
			m.cfg.Touch("server_url")
		case "auth_token":
			m.cfg.AuthToken = strings.TrimSpace(ev.value)
			m.cfg.Touch("auth_token")
		case "theme":
			m.cfg.Theme = ev.value
			m.st = NewStyles(ev.value)
		case "splash":
			m.cfg.Splash = ev.on
			m.cfg.Touch("splash")
		case "notifications":
			m.cfg.Notifications = ev.on
		case "desktop":
			m.cfg.DesktopNotifications = ev.on
		case "desktest":
			d, why := m.desk, m.deskWhy
			return m, func() tea.Msg {
				if d == nil {
					if why == nil {
						why = fmt.Errorf("not available on this machine")
					}
					return deskTestDoneMsg{err: why}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*notify.SendTimeout)
				defer cancel()
				return deskTestDoneMsg{err: d.Notify(ctx, "superbadger", "Desktop notifications are working."), name: d.Name()}
			}
		case "test":
			c := api.New(m.cfg.ServerURL, m.cfg.AuthToken)
			return m, func() tea.Msg { return opMsg{"test", nil, c.Health()} }
		case "metrics":
			return m, func() tea.Msg { return navMsg{"metrics"} }
		case "mullvad":
			return m, func() tea.Msg { return navMsg{"mullvad"} }
		default:
			changed = false
		}
		if changed {
			if err := m.cfg.Save(); err != nil {
				m.msg = "could not save config: " + err.Error()
			} else {
				m.msg = "saved"
			}
			return m, func() tea.Msg { return settingsChangedMsg{} }
		}
		return m, cmd
	}
	return m, nil
}

func (m *settingsModel) View() string {
	return m.st.Bold.Render("Settings") + "\n\n" + m.form.View(m.st) + "\n" +
		m.st.Muted.Render(m.msg) + "\n" +
		m.st.Muted.Render("↑/↓ move · enter edit/toggle · ←/→ change choice · esc back") + "\n"
}

// ---- metric sources ----

type metricsModel struct {
	client  *api.Client
	st      Styles
	sources []api.MetricSource
	cursor  int
	err     string
	confirm bool

	editing bool
	editID  uint // 0 = new
	form    form
	w, h    int
}

func newMetrics(c *api.Client, st Styles) *metricsModel { return &metricsModel{client: c, st: st} }

func (m *metricsModel) Init() tea.Cmd {
	c := m.client
	return func() tea.Msg {
		l, err := c.ListMetricSources()
		return opMsg{"list", err, l}
	}
}

func (m *metricsModel) resize(w, h int) { m.w, m.h = w, h; m.form.width = w }

func (m *metricsModel) edit(src *api.MetricSource) {
	m.editing = true
	f := []field{
		{key: "name", label: "Name", kind: fText},
		{key: "url", label: "URL", kind: fText},
		{key: "api_key", label: "API key", kind: fText, secret: true, note: "optional"},
		{key: "poll", label: "Poll (seconds)", kind: fText, value: "30"},
		{key: "enabled", label: "Enabled", kind: fToggle, on: true},
		{key: "save", label: "Save", kind: fAction},
	}
	m.editID = 0
	if src != nil {
		m.editID = src.ID
		f[0].value, f[1].value = src.Name, src.URL
		f[3].value, f[4].on = strconv.Itoa(src.PollIntervalSeconds), src.Enabled
		if src.APIKey != nil {
			f[2].value = *src.APIKey
		}
	}
	m.form = newForm(f)
	m.form.width = m.w
}

func (m *metricsModel) params() (api.MetricSourceParams, error) {
	poll, err := strconv.Atoi(strings.TrimSpace(m.form.get("poll").value))
	if err != nil || poll < 1 {
		return api.MetricSourceParams{}, fmt.Errorf("poll interval must be a positive number of seconds")
	}
	p := api.MetricSourceParams{
		Name:                strings.TrimSpace(m.form.get("name").value),
		URL:                 strings.TrimSpace(m.form.get("url").value),
		APIKey:              m.form.get("api_key").value,
		PollIntervalSeconds: poll,
		Enabled:             m.form.get("enabled").on,
	}
	if p.Name == "" || p.URL == "" {
		return p, fmt.Errorf("name and URL are required")
	}
	return p, nil
}

func (m *metricsModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case opMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.err = ""
		if msg.tag == "list" {
			m.sources = msg.val.([]api.MetricSource)
			m.cursor = min(m.cursor, max(0, len(m.sources)-1))
		} else {
			m.editing = false
			return m, m.Init()
		}
	case tea.KeyMsg:
		s := msg.String()
		if m.editing {
			if !m.form.editing && s == "esc" {
				m.editing, m.err = false, ""
				return m, nil
			}
			var ev *formEvent
			var cmd tea.Cmd
			m.form, ev, cmd = m.form.Update(msg)
			if ev != nil && ev.key == "save" {
				p, err := m.params()
				if err != nil {
					m.err = err.Error()
					return m, nil
				}
				c, id := m.client, m.editID
				return m, func() tea.Msg {
					if id == 0 {
						_, err := c.CreateMetricSource(p)
						return opMsg{"save", err, nil}
					}
					return opMsg{"save", c.UpdateMetricSource(id, p), nil}
				}
			}
			return m, cmd
		}
		if m.confirm {
			m.confirm = false
			if s == "y" || s == "Y" {
				c, id := m.client, m.sources[m.cursor].ID
				return m, func() tea.Msg { return opMsg{"delete", c.DeleteMetricSource(id), nil} }
			}
			return m, nil
		}
		switch s {
		case "esc":
			return m, func() tea.Msg { return navMsg{"settings"} }
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			m.cursor = min(len(m.sources)-1, m.cursor+1)
		case "n":
			m.edit(nil)
		case "enter":
			if len(m.sources) > 0 {
				m.edit(&m.sources[m.cursor])
			}
		case "d":
			m.confirm = len(m.sources) > 0
		case " ":
			if len(m.sources) > 0 {
				src := m.sources[m.cursor]
				key := ""
				if src.APIKey != nil {
					key = *src.APIKey
				}
				p := api.MetricSourceParams{Name: src.Name, URL: src.URL, APIKey: key,
					PollIntervalSeconds: src.PollIntervalSeconds, Enabled: !src.Enabled}
				c := m.client
				return m, func() tea.Msg { return opMsg{"save", c.UpdateMetricSource(src.ID, p), nil} }
			}
		}
	}
	return m, nil
}

func (m *metricsModel) View() string {
	s := m.st
	var b strings.Builder
	if m.editing {
		title := "New metric source"
		if m.editID != 0 {
			title = "Edit metric source"
		}
		b.WriteString(s.Bold.Render(title) + "\n\n" + m.form.View(s))
		b.WriteString("\n" + s.Muted.Render("enter edit/select · esc cancel") + "\n")
	} else {
		b.WriteString(s.Bold.Render("Metric sources") + "\n\n")
		if len(m.sources) == 0 {
			b.WriteString(s.Muted.Render("None configured — press n to add one.") + "\n")
		}
		for i, src := range m.sources {
			cur, name := "  ", src.Name
			if i == m.cursor {
				cur, name = s.Primary.Render("▸ "), s.Sel.Render(src.Name)
			}
			on := s.Success.Render("on ")
			if !src.Enabled {
				on = s.Muted.Render("off")
			}
			b.WriteString(fmt.Sprintf("%s%s  %s  %s\n", cur, on, name, s.Muted.Render(fmt.Sprintf("%s · every %ds", src.URL, src.PollIntervalSeconds))))
		}
		b.WriteString("\n" + s.Muted.Render("enter edit · n new · space enable/disable · d delete · esc back") + "\n")
		if m.confirm {
			b.WriteString(s.Danger.Render("Delete this source? (y/N)") + "\n")
		}
	}
	if m.err != "" {
		b.WriteString("\n" + s.Danger.Render(m.err) + "\n")
	}
	return b.String()
}

// ---- Mullvad VPN ----

type mullvadModel struct {
	client *api.Client
	st     Styles
	output string
	err    string
	busy   bool

	locating bool
	form     form
	w, h     int
}

func newMullvad(c *api.Client, st Styles) *mullvadModel { return &mullvadModel{client: c, st: st} }

func (m *mullvadModel) Init() tea.Cmd { return m.run("status", m.client.MullvadStatus) }

func (m *mullvadModel) resize(w, h int) { m.w, m.h = w, h; m.form.width = w }

func (m *mullvadModel) run(tag string, fn func() (api.MullvadOutput, error)) tea.Cmd {
	m.busy = true
	return func() tea.Msg {
		o, err := fn()
		return opMsg{tag, err, o.Output}
	}
}

func (m *mullvadModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case opMsg:
		m.busy = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.output = strings.TrimSpace(msg.val.(string))
		if msg.tag != "status" {
			return m, m.run("status", m.client.MullvadStatus)
		}
	case tea.KeyMsg:
		s := msg.String()
		c := m.client
		if m.locating {
			if !m.form.editing && s == "esc" {
				m.locating = false
				return m, nil
			}
			var ev *formEvent
			var cmd tea.Cmd
			m.form, ev, cmd = m.form.Update(msg)
			if ev != nil && ev.key == "apply" {
				country := strings.TrimSpace(m.form.get("country").value)
				if country == "" {
					m.err = "country is required (e.g. se)"
					return m, nil
				}
				city, host := strings.TrimSpace(m.form.get("city").value), strings.TrimSpace(m.form.get("host").value)
				m.locating = false
				return m, m.run("loc", func() (api.MullvadOutput, error) { return c.MullvadSetLocation(country, city, host) })
			}
			return m, cmd
		}
		switch s {
		case "esc":
			return m, func() tea.Msg { return navMsg{"settings"} }
		case "c":
			return m, m.run("connect", c.MullvadConnect)
		case "d":
			return m, m.run("disconnect", c.MullvadDisconnect)
		case "r":
			return m, m.run("status", c.MullvadStatus)
		case "a":
			return m, m.run("lan", func() (api.MullvadOutput, error) { return c.MullvadSetLan(true) })
		case "b":
			return m, m.run("lan", func() (api.MullvadOutput, error) { return c.MullvadSetLan(false) })
		case "l":
			m.locating = true
			m.form = newForm([]field{
				{key: "country", label: "Country", kind: fText, note: "code, e.g. se"},
				{key: "city", label: "City", kind: fText, note: "optional, e.g. got"},
				{key: "host", label: "Hostname", kind: fText, note: "optional"},
				{key: "apply", label: "Apply", kind: fAction},
			})
			m.form.width = m.w
		}
	}
	return m, nil
}

func (m *mullvadModel) View() string {
	s := m.st
	var b strings.Builder
	b.WriteString(s.Bold.Render("Mullvad VPN") + "\n\n")
	if m.locating {
		b.WriteString(m.form.View(s) + "\n" + s.Muted.Render("enter edit/select · esc cancel") + "\n")
	} else {
		if m.busy {
			b.WriteString(s.Muted.Render("working…") + "\n")
		}
		b.WriteString(m.output + "\n\n")
		b.WriteString(s.Muted.Render("c connect · d disconnect · l location · a allow LAN · b block LAN · r refresh · esc back") + "\n")
	}
	if m.err != "" {
		b.WriteString("\n" + s.Danger.Render(m.err) + "\n")
	}
	return b.String()
}
