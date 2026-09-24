package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"superbadger-tui/internal/api"
	"superbadger-tui/internal/chat"
	"superbadger-tui/internal/ws"
)

// A fast token stream delivers many deltas a second; they're buffered and
// applied in one batch per tick (see useStationChat.ts FLUSH_INTERVAL_MS).
const (
	flushInterval = 150 * time.Millisecond
	pollInterval  = 15 * time.Second
)

// Messages owned by one chat instance; gen lets the root drop stragglers from
// a chat the user has since switched away from.
type (
	wsMsg struct {
		gen int
		m   ws.Msg
	}
	flushMsg   struct{ gen int }
	pollMsg    struct{ gen int }
	historyMsg struct {
		gen    int
		events []json.RawMessage
	}
	dpMsg struct {
		gen int
		dps []api.DataPoint
	}
	usageMsg struct {
		gen int
		u   api.TokenUsage
	}
	stationMsg struct {
		gen int
		s   api.Station
	}
	chatErrMsg struct {
		gen int
		err error
	}
	chatNoticeMsg struct {
		gen  int
		text string
	}
	// Asks the root to change screens / stations.
	deletedMsg struct{ id uint }
)

type queued struct{ id, text string }

type chatModel struct {
	gen     int
	client  *api.Client
	st      Styles
	station api.Station
	pos     [2]int // index, total (for the station position indicator)

	conn    *ws.Conn
	status  string
	state   *chat.State
	pending []chat.Event // buffered until the next flush

	outbox       []queued
	pendingLocal string
	seq          int

	vp        viewport.Model
	ta        textarea.Model
	stick     bool
	dirty     bool
	showTools bool
	showThink bool

	dps        []api.DataPoint
	usage      api.TokenUsage
	compacting bool

	menu     bool
	menuCur  int
	confirm  string // "reset"|"delete" awaiting y/n
	localErr string

	// question prompt state
	qCur    int
	qSel    map[int]bool
	qTyping bool
	qInput  textinput.Model

	w, h int
}

var genCounter int

func newChat(c *api.Client, st Styles, s api.Station, idx, total int) *chatModel {
	genCounter++
	ta := textarea.New()
	ta.Placeholder = "message  ·  enter sends  ·  alt+enter newline  ·  /settings"
	ta.ShowLineNumbers = false
	ta.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return "❯ "
		}
		return "  "
	})
	ta.SetHeight(3)
	ta.CharLimit = 0
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	ta.Focus()
	qi := textinput.New()
	qi.Prompt = "other: "
	m := &chatModel{
		gen: genCounter, client: c, st: st, station: s, pos: [2]int{idx, total},
		state: chat.New(), status: "connecting", vp: viewport.New(80, 10), ta: ta,
		stick: true, dirty: true, showTools: true, qSel: map[int]bool{}, qInput: qi,
	}
	m.applyStyles()
	m.conn = ws.Dial(c.WSURL(fmt.Sprintf("/stations/%d/ws", s.ID)))
	return m
}

func (m *chatModel) Init() tea.Cmd {
	return tea.Batch(m.waitFrame(), m.flushTick(), m.pollTick(), m.fetchHistory(), m.fetchTop(), textarea.Blink)
}

func (m *chatModel) Close() { m.conn.Close() }

func (m *chatModel) waitFrame() tea.Cmd {
	conn, gen := m.conn, m.gen
	return func() tea.Msg {
		msg, ok := <-conn.Out()
		if !ok {
			return nil
		}
		return wsMsg{gen, msg}
	}
}

func (m *chatModel) flushTick() tea.Cmd {
	gen := m.gen
	return tea.Tick(flushInterval, func(time.Time) tea.Msg { return flushMsg{gen} })
}

func (m *chatModel) pollTick() tea.Cmd {
	gen := m.gen
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return pollMsg{gen} })
}

func (m *chatModel) fetchHistory() tea.Cmd {
	c, id, gen := m.client, m.station.ID, m.gen
	return func() tea.Msg {
		ev, err := c.History(id)
		if err != nil || len(ev) == 0 {
			return nil // no history yet is not an error worth surfacing
		}
		return historyMsg{gen, ev}
	}
}

// fetchTop refreshes everything the top bar shows: station, data points, usage.
func (m *chatModel) fetchTop() tea.Cmd {
	c, id, gen := m.client, m.station.ID, m.gen
	return tea.Batch(
		func() tea.Msg {
			if s, err := c.GetStation(id); err == nil {
				return stationMsg{gen, s}
			}
			return nil
		},
		func() tea.Msg {
			if d, err := c.DataPoints(id); err == nil {
				return dpMsg{gen, d}
			}
			return nil
		},
		func() tea.Msg {
			if u, err := c.Usage(id); err == nil {
				return usageMsg{gen, u}
			}
			return nil
		},
	)
}

func (m *chatModel) resize(w, h int) {
	m.w, m.h = w, h
	m.ta.SetWidth(max(20, w-4))
	m.vp.Width = w
	m.dirty = true
}

// blocked reports whether a permission/question prompt owns the keyboard.
func (m *chatModel) blocked() bool {
	return m.state.PendingPermission != nil || m.state.PendingQuestion != nil
}

func (m *chatModel) overlay() bool { return m.menu || m.confirm != "" }

func (m *chatModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case wsMsg:
		if msg.m.Frame != nil {
			if e, err := chat.ParseEvent(msg.m.Frame); err == nil {
				m.pending = append(m.pending, e)
			}
		} else {
			m.status = msg.m.Status
			if m.status == "open" {
				return tea.Batch(m.waitFrame(), m.pump())
			}
		}
		return m.waitFrame()

	case flushMsg:
		cmd := m.flush()
		return tea.Batch(m.flushTick(), cmd)

	case pollMsg:
		return tea.Batch(m.pollTick(), m.fetchTop())

	case historyMsg:
		m.state.Seed(msg.events)
		m.dirty = true
	case dpMsg:
		sort.SliceStable(msg.dps, func(i, j int) bool {
			if msg.dps[i].Order != msg.dps[j].Order {
				return msg.dps[i].Order < msg.dps[j].Order
			}
			return msg.dps[i].Key < msg.dps[j].Key
		})
		m.dps = msg.dps
	case usageMsg:
		m.usage = msg.u
	case stationMsg:
		m.station = msg.s
	case chatErrMsg:
		m.localErr = msg.err.Error()
		m.compacting = false
	case chatNoticeMsg:
		m.state.Notice = msg.text
		m.compacting = false
		m.dirty = true

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		m.stick = m.vp.AtBottom()
		return cmd

	case tea.KeyMsg:
		return m.key(msg)
	}
	return nil
}

// flush applies buffered events in one batch, reconciling the optimistic
// local user turn against opencode's echo, and pumps the outbox when a reply
// finishes.
func (m *chatModel) flush() tea.Cmd {
	if len(m.pending) == 0 {
		return nil
	}
	wasBusy := m.state.Busy
	for _, e := range m.pending {
		if m.pendingLocal != "" && e.Type == "message.updated" {
			if info, _ := e.Properties["info"].(map[string]any); info != nil && info["role"] == "user" {
				id, _ := info["id"].(string)
				m.state.ReconcileLocalUserTurn(m.pendingLocal, id)
				m.pendingLocal = ""
			}
		}
		m.state.Apply(e)
	}
	m.pending = nil
	m.dirty = true
	if wasBusy && !m.state.Busy {
		// Usage isn't pushed by any event, so refetch when a reply completes.
		return tea.Batch(m.fetchTop(), m.pump())
	}
	return m.pump()
}

// pump sends the head of the outbox once nothing is in flight. This station
// only ever runs one prompt at a time, so later messages wait their turn.
func (m *chatModel) pump() tea.Cmd {
	if m.state.Busy || len(m.outbox) == 0 || m.status != "open" {
		return nil
	}
	head := m.outbox[0]
	if !m.conn.Send(ws.Prompt(head.text)) {
		return nil
	}
	m.outbox = m.outbox[1:]
	m.pendingLocal = "local-" + head.id
	m.state.AddLocalUserTurn(m.pendingLocal, head.text)
	m.state.Busy = true
	m.state.Error = ""
	m.stick = true
	m.dirty = true
	return nil
}

func (m *chatModel) send(text string) tea.Cmd {
	m.seq++
	m.outbox = append(m.outbox, queued{fmt.Sprintf("%d-%d", time.Now().UnixNano(), m.seq), text})
	m.stick, m.dirty = true, true
	return m.pump()
}

func (m *chatModel) key(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	m.localErr = ""

	if m.confirm != "" {
		switch s {
		case "y", "Y", "enter":
			what := m.confirm
			m.confirm = ""
			return m.runAction(what)
		default:
			m.confirm = ""
		}
		return nil
	}
	if m.menu {
		return m.menuKey(s)
	}
	if p := m.state.PendingPermission; p != nil {
		switch s {
		case "o", "y", "1":
			m.conn.Send(ws.PermissionReply(p.ID, "once"))
			m.state.PendingPermission = nil
		case "a", "2":
			m.conn.Send(ws.PermissionReply(p.ID, "always"))
			m.state.PendingPermission = nil
		case "r", "n", "3", "esc":
			m.conn.Send(ws.PermissionReply(p.ID, "reject"))
			m.state.PendingPermission = nil
		}
		m.dirty = true
		return nil
	}
	if q := m.state.PendingQuestion; q != nil {
		return m.questionKey(q, k)
	}

	switch s {
	case "esc":
		// Deliberately does nothing here: stopping a reply is the explicit
		// /stop command, so a stray Esc can't cancel work in progress.
		return nil
	case "ctrl+k":
		m.menu, m.menuCur = true, 0
		return nil
	case "ctrl+o":
		m.showTools = !m.showTools
		m.dirty = true
		return nil
	case "ctrl+r":
		m.showThink = !m.showThink
		m.dirty = true
		return nil
	case "alt+c":
		if m.station.HasAction("compact") {
			return m.runAction("compact")
		}
	case "alt+r":
		if m.station.HasAction("reset") {
			m.confirm = "reset"
			return nil
		}
	case "alt+d":
		if m.station.HasAction("delete") {
			m.confirm = "delete"
			return nil
		}
	case "enter":
		text := strings.TrimSpace(m.ta.Value())
		if text == "" {
			return nil
		}
		if cmd, ok := m.localCommand(strings.ToLower(text)); ok {
			m.ta.Reset()
			return cmd
		}
		if to, ok := slashCommands[strings.ToLower(text)]; ok {
			// A known /command is run locally, not sent to the model.
			m.ta.Reset()
			return func() tea.Msg { return navMsg{to} }
		}
		m.ta.Reset()
		return m.send(text)
	case "pgup", "pgdown", "ctrl+u", "ctrl+d", "home", "end":
		var cmd tea.Cmd
		switch s {
		case "ctrl+u":
			m.vp.HalfViewUp()
		case "ctrl+d":
			m.vp.HalfViewDown()
		case "home":
			m.vp.GotoTop()
		case "end":
			m.vp.GotoBottom()
		default:
			m.vp, cmd = m.vp.Update(k)
		}
		m.stick = m.vp.AtBottom()
		return cmd
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(k)
	return cmd
}

var menuItems = []struct{ key, label string }{
	{"compact", "Compact context"},
	{"abort", "Abort current reply"},
	{"reset", "Reset session (clears history)"},
	{"delete", "Delete station"},
}

func (m *chatModel) menuKey(s string) tea.Cmd {
	switch s {
	case "esc", "ctrl+k", "q":
		m.menu = false
	case "up", "k":
		m.menuCur = (m.menuCur + len(menuItems) - 1) % len(menuItems)
	case "down", "j":
		m.menuCur = (m.menuCur + 1) % len(menuItems)
	case "enter":
		m.menu = false
		what := menuItems[m.menuCur].key
		if what == "reset" || what == "delete" {
			m.confirm = what
			return nil
		}
		return m.runAction(what)
	}
	return nil
}

func (m *chatModel) runAction(what string) tea.Cmd {
	c, id, gen := m.client, m.station.ID, m.gen
	fail := func(err error) tea.Msg { return chatErrMsg{gen, err} }
	switch what {
	case "abort":
		return func() tea.Msg {
			if err := c.AbortStation(id); err != nil {
				return fail(err)
			}
			return nil
		}
	case "compact":
		m.compacting = true
		return func() tea.Msg {
			if err := c.CompactStation(id); err != nil {
				return fail(err)
			}
			return chatNoticeMsg{gen, "Conversation compacted"}
		}
	case "reset":
		return func() tea.Msg {
			s, err := c.ResetStation(id)
			if err != nil {
				return fail(err)
			}
			return resetDoneMsg{gen, s}
		}
	case "delete":
		return func() tea.Msg {
			if err := c.DeleteStation(id); err != nil {
				return fail(err)
			}
			return deletedMsg{id}
		}
	}
	return nil
}

type resetDoneMsg struct {
	gen int
	s   api.Station
}

// afterReset drops the old transcript and re-dials so the server resolves the
// station's new session on connect.
func (m *chatModel) afterReset(s api.Station) {
	m.station = s
	m.state = chat.New()
	m.pending, m.outbox, m.pendingLocal = nil, nil, ""
	m.usage = api.TokenUsage{}
	m.dirty, m.stick = true, true
	m.conn.Reconnect()
}

func (m *chatModel) questionKey(q *chat.PendingQuestion, k tea.KeyMsg) tea.Cmd {
	s := k.String()
	if m.qTyping {
		switch s {
		case "enter":
			if v := strings.TrimSpace(m.qInput.Value()); v != "" {
				m.answerQuestion(q, []string{v})
			}
			return nil
		case "esc":
			m.qTyping = false
			m.qInput.Blur()
			return nil
		}
		var cmd tea.Cmd
		m.qInput, cmd = m.qInput.Update(k)
		return cmd
	}
	switch s {
	case "up", "k":
		m.qCur = max(0, m.qCur-1)
	case "down", "j":
		m.qCur = min(len(q.Options)-1, m.qCur+1)
	case " ":
		if q.Multiple {
			m.qSel[m.qCur] = !m.qSel[m.qCur]
		}
	case "enter":
		var picked []string
		if q.Multiple {
			for i, o := range q.Options {
				if m.qSel[i] {
					picked = append(picked, o.Label)
				}
			}
		}
		if len(picked) == 0 && m.qCur < len(q.Options) {
			picked = []string{q.Options[m.qCur].Label}
		}
		if len(picked) > 0 {
			m.answerQuestion(q, picked)
		}
	case "c", "tab":
		if q.Custom {
			m.qTyping = true
			return m.qInput.Focus()
		}
	case "esc":
		m.conn.Send(ws.QuestionReject(q.ID))
		m.resetQuestion()
	}
	return nil
}

func (m *chatModel) answerQuestion(q *chat.PendingQuestion, answers []string) {
	m.conn.Send(ws.QuestionReply(q.ID, [][]string{answers}))
	m.resetQuestion()
}

func (m *chatModel) resetQuestion() {
	m.state.PendingQuestion = nil
	m.qCur, m.qSel, m.qTyping = 0, map[int]bool{}, false
	m.qInput.SetValue("")
	m.qInput.Blur()
	m.dirty = true
}

// ---- rendering ----
//
// Visual language (see superbadger.ans): a navy page, dusty-blue chrome, and
// one cyan accent. Your messages are raised cyan-railed panels; the model's
// are flat, blue-railed text — so who said what is obvious at a glance.

func (m *chatModel) applyStyles() {
	s := m.st
	fg := func(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }
	for _, st := range []*textarea.Style{&m.ta.FocusedStyle, &m.ta.BlurredStyle} {
		st.Base = lipgloss.NewStyle()
		st.CursorLine = lipgloss.NewStyle()
		st.CursorLineNumber = lipgloss.NewStyle()
		st.EndOfBuffer = lipgloss.NewStyle()
		st.Prompt = fg(s.T.Accent).Bold(true)
		st.Text = fg(s.T.Text)
		st.Placeholder = fg(s.T.Muted)
	}
	m.ta.Cursor.Style = fg(s.T.Accent)
	m.qInput.PromptStyle = fg(s.T.Accent)
	m.qInput.TextStyle = fg(s.T.Text)
}

func (m *chatModel) statusLabel() (string, lipgloss.Style) {
	switch {
	case !m.station.Reachable:
		return "unreachable", m.st.Danger
	case m.status != "open":
		return "reconnecting…", m.st.Muted
	case m.station.Status == "active":
		return "active", m.st.Success
	case m.station.Status == "error":
		return "error", m.st.Danger
	}
	return "idle", m.st.Soft
}

func (m *chatModel) hint() string {
	if m.blocked() || m.overlay() {
		return ""
	}
	return "^s stations · ←/→ swap · /stop · /show thinking · ^k actions · ^e station · ^g settings · ^o tools · ^q quit"
}

func (m *chatModel) topBar() string {
	s := m.st
	name := chip(m.station.Name, s.T.Bg, m.station.Color)
	txt, style := m.statusLabel()
	parts := []string{name + "  " + style.Render("● "+txt)}

	metric := func(label, value string) string {
		return s.Muted.Render(label+" ") + s.Primary.Bold(true).Render(value)
	}
	if m.station.HasAction("tokens") {
		if ctx := m.usage.Context(); ctx > 0 {
			parts = append(parts, metric("ctx", api.FormatTokens(ctx)))
		}
	}
	for _, d := range m.dps {
		if !d.ShowOnTopBar {
			continue
		}
		label := d.Label
		if label == "" {
			label = d.Key
		}
		parts = append(parts, metric(label, api.FormatValue(d.Value, d.Decimals)))
	}
	left := " " + strings.Join(parts, s.Border.Render("  │  "))
	right := s.Soft.Render(fmt.Sprintf("‹ %d/%d › ", m.pos[0]+1, m.pos[1]))
	gap := max(1, m.w-lipgloss.Width(left)-lipgloss.Width(right))
	line1 := left + strings.Repeat(" ", gap) + right

	var acts []string
	if m.station.HasAction("compact") {
		if m.compacting {
			acts = append(acts, "compacting…")
		} else {
			acts = append(acts, "compact ⌥c")
		}
	}
	if m.station.HasAction("reset") {
		acts = append(acts, "reset ⌥r")
	}
	// Second row: directory + enabled action buttons. No model/provider here
	// (wasted space), and the row is dropped entirely when there's nothing.
	var row2 []string
	if m.station.Directory != "" {
		row2 = append(row2, s.Muted.Render(m.station.Directory))
	}
	if len(acts) > 0 {
		row2 = append(row2, s.Soft.Render(strings.Join(acts, "  ")))
	}
	if m.station.HasAction("delete") {
		row2 = append(row2, s.Danger.Render("delete ⌥d"))
	}
	out := []string{line1}
	if len(row2) > 0 {
		out = append(out, truncate(" "+strings.Join(row2, "   "), m.w))
	}
	if !m.station.Reachable {
		out = append(out, " "+s.Danger.Render("the model behind this station isn't responding — is it running?"))
	}
	out = append(out, s.Border.Render(strings.Repeat("─", m.w)))
	return strings.Join(out, "\n")
}

func truncate(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

// panelLine renders one row of a raised panel: rail + text on the panel
// background, padded to the full width. Each segment carries its own
// background because a nested reset would otherwise punch holes in the fill.
func (m *chatModel) panelLine(rail, text string) string {
	t := m.st.T
	seg := func(txt, fg string) string {
		return lipgloss.NewStyle().Background(lipgloss.Color(t.Panel)).Foreground(lipgloss.Color(fg)).Render(txt)
	}
	pad := max(0, m.w-1-lipgloss.Width(rail)-lipgloss.Width(text))
	return " " + seg(rail, t.Accent) + seg(text+strings.Repeat(" ", pad), t.Text)
}

func (m *chatModel) transcript() string {
	tw := max(10, m.w-6)
	wrap := lipgloss.NewStyle().Width(tw)
	var b strings.Builder
	for _, t := range m.state.Turns {
		if len(t.PartOrder) == 0 {
			continue
		}
		if t.Role == "assistant" {
			b.WriteString(m.assistantTurn(t, wrap) + "\n\n")
		} else {
			b.WriteString(m.userBlock(t.Text(), wrap, false) + "\n\n")
		}
	}
	for _, q := range m.outbox {
		b.WriteString(m.userBlock(q.text, wrap, true) + "\n\n")
	}
	if m.state.Busy {
		b.WriteString(" " + m.st.Primary.Render("◆ ") + m.st.Muted.Render("working…  /stop to abort") + "\n")
	}
	if b.Len() == 0 {
		b.WriteString("\n " + m.st.Muted.Render("nothing here yet — say something."))
	}
	return b.String()
}

// userBlock is "my" text: a full-width raised panel with a cyan rail.
func (m *chatModel) userBlock(text string, wrap lipgloss.Style, queued bool) string {
	label := "❯ you"
	if queued {
		label = "❯ you · queued"
	}
	blank := m.panelLine("▎", "")
	lines := []string{blank, m.panelLine("▎ ", lipgloss.NewStyle().Bold(true).Render(label))}
	for _, l := range strings.Split(wrap.Render(text), "\n") {
		lines = append(lines, m.panelLine("▎   ", strings.TrimRight(l, " ")))
	}
	return strings.Join(append(lines, blank), "\n")
}

// assistantTurn is the model's text: no panel, a dim blue rail, its own name
// as the header.
func (m *chatModel) assistantTurn(t *chat.Turn, wrap lipgloss.Style) string {
	s := m.st
	rail := s.Border.Render("▏") + " "
	head := " " + s.Soft.Bold(true).Render("◆ "+m.station.Name)
	out := []string{head}
	for _, id := range t.PartOrder {
		r := m.renderPart(t.Parts[id], wrap)
		if r == "" {
			continue
		}
		for _, l := range strings.Split(r, "\n") {
			out = append(out, " "+rail+l)
		}
	}
	return strings.Join(out, "\n")
}

func (m *chatModel) renderPart(p *chat.Part, wrap lipgloss.Style) string {
	if p == nil {
		return ""
	}
	tw := max(10, m.w-6)
	switch p.Kind {
	case chat.KindText:
		return m.renderText(p.Text, wrap)
	case chat.KindReasoning:
		if !m.showThink {
			return m.st.Muted.Render("· thinking  (ctrl+r to show)")
		}
		return m.st.Muted.Italic(true).Render(wrap.Render(p.Text))
	case chat.KindTool:
		icon := map[string]string{"pending": "○", "running": "◔", "completed": "●", "error": "✗"}[p.Status]
		st := m.st.Soft
		if p.Status == "error" {
			st = m.st.Danger
		}
		head := st.Render(icon+" "+p.Tool) + " " + m.st.Muted.Render(p.Title)
		out := truncate(head, tw)
		if !m.showTools {
			return out
		}
		body := p.Output
		if p.Error != "" {
			body = p.Error
		}
		if body != "" {
			lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
			if len(lines) > 8 {
				lines = append(lines[:8], fmt.Sprintf("… %d more lines", len(lines)-8))
			}
			for i, l := range lines {
				lines[i] = "  " + m.st.Muted.Render(truncate(l, tw-2))
			}
			out += "\n" + strings.Join(lines, "\n")
		}
		return out
	case chat.KindStepFinish:
		if p.Tokens > 0 {
			return m.st.Muted.Render(fmt.Sprintf("╌ step done · %s tokens", api.FormatTokens(p.Tokens)))
		}
		return ""
	case chat.KindFile:
		return m.st.Soft.Render("◇ " + p.Text)
	case chat.KindSubtask:
		return m.st.Soft.Render("⇢ " + p.Text)
	case chat.KindNotice:
		return m.st.Muted.Render(p.Text)
	}
	return ""
}

// renderText does just enough markdown for a terminal: fenced code blocks
// get the theme's code background; everything else is wrapped plain text.
func (m *chatModel) renderText(text string, wrap lipgloss.Style) string {
	var out, plain []string
	inCode := false
	flush := func() {
		if len(plain) > 0 {
			out = append(out, wrap.Render(strings.Join(plain, "\n")))
			plain = nil
		}
	}
	tw := max(10, m.w-6)
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			flush()
			inCode = !inCode
			continue
		}
		if inCode {
			l := truncate(" "+line, tw)
			out = append(out, m.st.Code.Render(l+strings.Repeat(" ", max(0, tw-lipgloss.Width(l)))))
		} else {
			plain = append(plain, line)
		}
	}
	flush()
	return strings.Join(out, "\n")
}

func (m *chatModel) bottom() string {
	s := m.st
	var lines []string
	if m.state.Notice != "" {
		lines = append(lines, " "+s.Soft.Render(m.state.Notice))
	}
	if e := m.state.Error; e != "" {
		lines = append(lines, " "+s.Danger.Render("error: "+e))
	}
	if m.localErr != "" {
		lines = append(lines, " "+s.Danger.Render(m.localErr))
	}
	iw := max(10, m.w-4)
	wrap := lipgloss.NewStyle().Width(iw)
	var panel []string
	color := s.T.Accent
	switch {
	case m.confirm != "":
		color = s.T.Danger
		panel = append(panel, s.Danger.Render(fmt.Sprintf("%s this station? (y/N)", strings.Title(m.confirm)))) //nolint:staticcheck
	case m.menu:
		for i, it := range menuItems {
			cur, line := "  ", it.label
			if i == m.menuCur {
				cur, line = s.Primary.Render("▸ "), s.Sel.Render(it.label)
			}
			panel = append(panel, cur+line)
		}
		panel = append(panel, s.Muted.Render("enter select · esc close"))
	case m.state.PendingPermission != nil:
		color = s.T.Danger
		p := m.state.PendingPermission
		panel = append(panel,
			s.Danger.Bold(true).Render("permission requested  ")+s.Bold.Render(p.Permission),
			s.Muted.Render(strings.Join(p.Patterns, ", ")),
			"",
			s.Primary.Render("[o]")+" allow once   "+s.Primary.Render("[a]")+" always allow   "+s.Danger.Render("[r]")+" reject")
	case m.state.PendingQuestion != nil:
		q := m.state.PendingQuestion
		if q.Header != "" {
			panel = append(panel, s.Primary.Bold(true).Render(q.Header))
		}
		panel = append(panel, wrap.Render(q.Question), "")
		for i, o := range q.Options {
			cur, mark := "  ", ""
			if q.Multiple {
				mark = "[ ] "
				if m.qSel[i] {
					mark = "[x] "
				}
			}
			line := mark + o.Label
			if i == m.qCur {
				cur, line = s.Primary.Render("▸ "), s.Sel.Render(line)
			}
			if o.Description != "" {
				line += "  " + s.Muted.Render(o.Description)
			}
			panel = append(panel, cur+line)
		}
		hint := "↑/↓ move · enter answer · esc reject"
		if q.Multiple {
			hint = "space toggle · " + hint
		}
		if q.Custom {
			hint += " · c other"
		}
		if m.qTyping {
			panel = append(panel, m.qInput.View())
			hint = "enter send · esc back"
		}
		panel = append(panel, "", s.Muted.Render(hint))
	default:
		lines = append(lines, box(m.ta.View(), m.w, s.T.Accent))
		return strings.Join(lines, "\n")
	}
	lines = append(lines, box(strings.Join(panel, "\n"), m.w, color))
	return strings.Join(lines, "\n")
}

func (m *chatModel) View() string {
	top, bottom := m.topBar(), m.bottom()
	m.vp.Width = m.w
	m.vp.Height = max(1, m.h-lipgloss.Height(top)-lipgloss.Height(bottom))
	if m.dirty {
		m.vp.SetContent(m.transcript())
		m.dirty = false
		if m.stick {
			m.vp.GotoBottom()
		}
	}
	return top + "\n" + m.vp.View() + "\n" + bottom
}

// slashCommands are typed into the message box and handled locally. Anything
// else starting with "/" is sent to the model like normal text.
// localCommand runs the /commands that act on this chat itself. ok is false
// for anything else, which falls through to the navigation commands and then
// to being sent to the model as ordinary text.
func (m *chatModel) localCommand(text string) (cmd tea.Cmd, ok bool) {
	switch text {
	case "/show":
		m.showThink = !m.showThink
		m.dirty = true
		if m.showThink {
			m.state.Notice = "thinking shown"
		} else {
			m.state.Notice = "thinking hidden"
		}
		return nil, true
	case "/stop":
		queued := len(m.outbox)
		m.outbox = nil // follow-ups typed ahead of the reply you just cancelled
		m.dirty = true
		switch {
		case m.state.Busy:
			m.state.Notice = "stopping…"
			return m.runAction("abort"), true
		case queued > 0:
			m.state.Notice = "cleared the queued messages"
		default:
			m.state.Notice = "nothing to stop"
		}
		return nil, true
	}
	return nil, false
}

var slashCommands = map[string]string{
	"/splash":   "splash",
	"/settings": "settings",
	"/stations": "picker",
	"/station":  "stationsettings",
	"/quit":     "quit",
	"/exit":     "quit",
}
