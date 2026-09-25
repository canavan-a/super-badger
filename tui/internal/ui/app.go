// Package ui is the bubbletea front end: a root model that owns the chat
// screen (the main view) and swaps in the picker / settings screens.
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"superbadger-tui/internal/api"
	"superbadger-tui/internal/config"
	"superbadger-tui/internal/notify"
	"superbadger-tui/internal/ws"
)

type (
	notifMsg struct {
		gen int
		m   ws.Msg
	}
)

type App struct {
	cfg    *config.Config
	client *api.Client
	st     Styles

	w, h     int
	stations []api.Station // ascending id, used for swapping
	chat     *chatModel
	sub      screen // non-nil while a non-chat screen is showing
	subName  string

	notif    *ws.Conn
	notifGen int
	toast    string
	toastAt  time.Time
	loadErr  string

	wantOpen   uint // station to open once the list has been (re)loaded
	recovering bool // landed on settings because the server was unreachable
	splash     bool // showing the launch art until a key is pressed
	splashN    int  // animation frame counter for the splash

	// Keys are ignored until this time. Terminals can deliver stray input at
	// startup (query replies, the Enter that launched us), and an "any key"
	// splash would otherwise be dismissed before it is ever seen.
	splashUntil time.Time

	// Desktop notifications. desk is nil when this machine can't show them
	// (deskWhy says why); deskFailed stops further attempts after a send fails,
	// until the settings change; deskWarned makes each problem announce itself
	// only once. focused/focusKnown track terminal focus (only terminals that
	// report it tell us), so we don't pop up about the screen you're looking at.
	desk       notify.Notifier
	deskWhy    error
	deskFailed bool
	deskWarned bool
	focused    bool
	focusKnown bool
	deskLast   map[string]time.Time
}

func New(cfg *config.Config) *App {
	a := &App{cfg: cfg, st: NewStyles(cfg.Theme), splash: cfg.Splash}
	a.client = api.New(cfg.ServerURL, cfg.AuthToken)
	if cfg.Notice != "" {
		a.toast, a.toastAt = cfg.Notice, time.Now()
	}
	a.desk, a.deskWhy = notify.Detect()
	a.deskLast = map[string]time.Time{}
	return a
}

// splashGrace is how long after launch keys are ignored on the splash.
const splashGrace = 400 * time.Millisecond

type splashTickMsg struct{}

func (a *App) splashTick() tea.Cmd {
	return tea.Tick(splashFrame, func(time.Time) tea.Msg { return splashTickMsg{} })
}

func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{a.loadStations(), a.startNotifications()}
	if a.splash {
		cmds = append(cmds, a.splashTick())
	}
	return tea.Batch(cmds...)
}

// configured reports whether a server URL has been set; until it has, the
// TUI never tries to connect and shows the welcome page instead.
func (a *App) configured() bool { return a.cfg.ServerURL != "" }

func (a *App) loadStations() tea.Cmd {
	if !a.configured() {
		return nil
	}
	c := a.client
	return func() tea.Msg {
		l, err := c.ListStations()
		return stationsMsg{l, err}
	}
}

func (a *App) startNotifications() tea.Cmd {
	if a.notif != nil {
		a.notif.Close()
		a.notif = nil
	}
	// The feed serves both the in-terminal toasts and the desktop
	// notifications, so it runs if either is on.
	if !(a.cfg.Notifications || a.cfg.DesktopNotifications) || !a.configured() {
		return nil
	}
	a.notifGen++
	a.notif = ws.Dial(a.client.WSURL("/notifications/ws"))
	return a.waitNotif()
}

func (a *App) waitNotif() tea.Cmd {
	conn, gen := a.notif, a.notifGen
	return func() tea.Msg {
		m, ok := <-conn.Out()
		if !ok {
			return nil
		}
		return notifMsg{gen, m}
	}
}

// The window frame takes 2 rows (top+bottom border) and 2 columns; the last
// row inside it is the toast line.
func (a *App) contentHeight() int { return max(3, a.h-3) }
func (a *App) innerW() int        { return max(20, a.w-2) }

func (a *App) setSub(name string, s screen) tea.Cmd {
	a.sub, a.subName = s, name
	s.resize(a.innerW(), a.contentHeight())
	return s.Init()
}

func (a *App) index(id uint) int {
	for i, s := range a.stations {
		if s.ID == id {
			return i
		}
	}
	return -1
}

func (a *App) openStation(id uint) tea.Cmd {
	i := a.index(id)
	if i < 0 {
		return nil
	}
	if a.chat != nil {
		a.chat.Close()
	}
	a.sub, a.subName = nil, ""
	a.chat = newChat(a.client, a.st, a.stations[i], i, len(a.stations))
	a.chat.resize(a.innerW(), a.contentHeight())
	a.cfg.LastStationID = id
	_ = a.cfg.Save()
	return a.chat.Init()
}

// swap moves to the next/previous station by ascending id, wrapping — the
// same order the app's edge-swipe uses.
func (a *App) swap(dir int) tea.Cmd {
	if a.chat == nil || len(a.stations) < 2 {
		return nil
	}
	i := a.index(a.chat.station.ID)
	n := len(a.stations)
	return a.openStation(a.stations[(i+dir+n)%n].ID)
}

func (a *App) currentID() uint {
	if a.chat != nil {
		return a.chat.station.ID
	}
	return 0
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
		if a.splash && a.splashUntil.IsZero() {
			a.splashUntil = time.Now().Add(splashGrace)
		}
		if a.splash && (a.w < splashW || a.h < splashH) {
			a.splash = false // the art doesn't fit; don't gate on it
		}
		if a.chat != nil {
			a.chat.resize(a.innerW(), a.contentHeight())
		}
		if a.sub != nil {
			a.sub.resize(a.innerW(), a.contentHeight())
		}
		return a, nil

	case splashTickMsg:
		if !a.splash {
			return a, nil // dismissed: stop animating
		}
		a.splashN++
		return a, a.splashTick()

	case stationsMsg:
		return a, a.onStations(msg)

	case notifMsg:
		if msg.gen != a.notifGen {
			return a, nil
		}
		var cmd tea.Cmd
		if msg.m.Frame != nil {
			cmd = a.onNotification(msg.m.Frame)
		}
		return a, tea.Batch(a.waitNotif(), cmd)

	case tea.FocusMsg:
		a.focused, a.focusKnown = true, true
		return a, nil
	case tea.BlurMsg:
		a.focused, a.focusKnown = false, true
		return a, nil

	case deskResultMsg:
		if msg.err != nil {
			// Never fatal: say so once and stop trying until the settings change.
			a.deskFailed = true
			a.warnOnce("desktop notification failed: " + msg.err.Error() + " — turned off for now; check /settings")
		}
		return a, nil

	case deskTestDoneMsg:
		if msg.err == nil {
			a.deskFailed = false // it works: let real notifications through again
		}
		if a.sub != nil {
			var c tea.Cmd
			a.sub, c = a.sub.Update(msg)
			return a, c
		}
		return a, nil

	case openStationMsg:
		if a.index(msg.id) < 0 {
			// Just created: the list doesn't know it yet.
			a.wantOpen = msg.id
			return a, a.loadStations()
		}
		return a, a.openStation(msg.id)

	case backMsg:
		a.sub, a.subName = nil, ""
		if a.chat != nil {
			return a, a.chat.fetchTop() // pick up any settings edits
		}
		// No chat open (e.g. the server changed under it): reload, which
		// reopens the last station.
		return a, a.loadStations()

	case navMsg:
		return a, a.navigate(msg.to)

	case settingsChangedMsg:
		return a, a.onSettingsChanged()

	case deletedMsg:
		if a.chat != nil && a.chat.station.ID == msg.id {
			a.chat.Close()
			a.chat = nil
		}
		return a, tea.Batch(a.loadStations())

	case resetDoneMsg:
		if a.chat != nil && a.chat.gen == msg.gen {
			a.chat.afterReset(msg.s)
		}
		return a, nil

	case tea.KeyMsg:
		return a, a.key(msg)
	}

	// Everything else is routed by owner.
	var cmds []tea.Cmd
	if a.chat != nil && chatOwns(msg, a.chat.gen) {
		cmds = append(cmds, a.chat.Update(msg))
	} else if _, ok := msg.(tea.MouseMsg); ok && a.chat != nil && a.sub == nil {
		cmds = append(cmds, a.chat.Update(msg))
	}
	if a.sub != nil {
		switch msg.(type) {
		case opMsg, stationsMsg:
			var c tea.Cmd
			a.sub, c = a.sub.Update(msg)
			cmds = append(cmds, c)
		}
	}
	return a, tea.Batch(cmds...)
}

// chatOwns reports whether msg is addressed to the chat with this gen.
func chatOwns(msg tea.Msg, gen int) bool {
	switch m := msg.(type) {
	case wsMsg:
		return m.gen == gen
	case flushMsg:
		return m.gen == gen
	case pollMsg:
		return m.gen == gen
	case historyMsg:
		return m.gen == gen
	case dpMsg:
		return m.gen == gen
	case usageMsg:
		return m.gen == gen
	case stationMsg:
		return m.gen == gen
	case chatErrMsg:
		return m.gen == gen
	case chatNoticeMsg:
		return m.gen == gen
	}
	return false
}

func (a *App) key(k tea.KeyMsg) tea.Cmd {
	if a.splash {
		quit := k.String() == "ctrl+q" || k.String() == "ctrl+c"
		if !quit && time.Now().Before(a.splashUntil) {
			return nil // too soon after launch to be a deliberate key press
		}
		a.splash = false // "press any key to burrow in"
		if !quit {
			return nil
		}
	}
	if k.String() == "ctrl+q" {
		a.shutdown()
		return tea.Quit
	}
	if a.sub != nil {
		var c tea.Cmd
		a.sub, c = a.sub.Update(k)
		if k.String() == "ctrl+c" {
			a.shutdown()
			return tea.Quit
		}
		return c
	}
	if a.chat == nil {
		switch k.String() {
		case "ctrl+c":
			a.shutdown()
			return tea.Quit
		case "g", "/":
			return a.navigate("settings")
		case "r", "enter":
			return a.loadStations()
		}
		return nil
	}
	c := a.chat
	if k.String() == "ctrl+c" {
		// Quits the TUI, never the reply: a reply already running on the
		// server carries on, and /stop is what cancels one.
		a.shutdown()
		return tea.Quit
	}
	if !c.blocked() && !c.overlay() {
		switch k.String() {
		case "ctrl+s":
			return a.setSub("picker", newPicker(a.client, a.st, a.stations, a.currentID()))
		case "ctrl+n":
			return a.swap(1)
		case "left", "right":
			// With nothing typed, the arrows roll through the stations.
			if c.ta.Value() == "" {
				if k.String() == "left" {
					return a.swap(-1)
				}
				return a.swap(1)
			}
		case "ctrl+p":
			return a.swap(-1)
		case "ctrl+e":
			return a.setSub("stationsettings", newStationSettings(a.client, a.st, c.station))
		case "ctrl+g":
			return a.navigate("settings")
		}
	}
	return c.Update(k)
}

func (a *App) shutdown() {
	if a.chat != nil {
		a.chat.Close()
	}
	if a.notif != nil {
		a.notif.Close()
	}
}

func (a *App) navigate(to string) tea.Cmd {
	switch to {
	case "settings":
		// Opened with no station yet: once the connection details work,
		// carry straight on into the first station.
		a.recovering = a.chat == nil
		return a.setSub("settings", newSettings(a.cfg, a.client, a.st, a.desk, a.deskWhy))
	case "picker":
		return a.setSub("picker", newPicker(a.client, a.st, a.stations, a.currentID()))
	case "stationsettings":
		if a.chat == nil {
			return nil
		}
		return a.setSub("stationsettings", newStationSettings(a.client, a.st, a.chat.station))
	case "quit":
		a.shutdown()
		return tea.Quit
	case "splash":
		// Replay the title screen (handy for previewing a theme).
		if a.w < splashW || a.h < splashH {
			a.toast, a.toastAt = "terminal too small for the title screen", time.Now()
			return nil
		}
		a.splash, a.splashN, a.splashUntil = true, 0, time.Time{}
		return a.splashTick()
	case "metrics":
		return a.setSub("metrics", newMetrics(a.client, a.st))
	case "mullvad":
		return a.setSub("mullvad", newMullvad(a.client, a.st))
	}
	return nil
}

// onStations handles the station list arriving: on first load it opens the
// last-used station (or the picker if there's none / the server is down).
func (a *App) onStations(m stationsMsg) tea.Cmd {
	if m.err != nil {
		// Never yank the user into Settings over a failed connection: the
		// welcome page shows the error and how to fix it.
		a.loadErr = m.err.Error()
		return nil
	}
	a.loadErr = ""
	a.stations = sortedByID(m.list)
	if p, ok := a.sub.(*pickerModel); ok {
		p.setStations(m.list)
		p.err = ""
	}
	if a.wantOpen != 0 {
		id := a.wantOpen
		a.wantOpen = 0
		return a.openStation(id)
	}
	if a.chat != nil {
		if i := a.index(a.chat.station.ID); i >= 0 {
			a.chat.pos = [2]int{i, len(a.stations)}
			return nil
		}
		// The open station was deleted (e.g. from the picker).
		a.chat.Close()
		a.chat = nil
		if a.sub == nil && len(a.stations) > 0 {
			return a.openStation(a.stations[0].ID)
		}
	}
	if a.chat != nil {
		return nil
	}
	if len(a.stations) == 0 {
		if _, ok := a.sub.(*pickerModel); ok {
			return nil
		}
		return a.setSub("picker", newPicker(a.client, a.st, a.stations, 0))
	}
	if a.sub != nil && !(a.subName == "settings" && a.recovering) {
		return nil // don't yank the user out of a screen
	}
	a.recovering = false
	id := a.stations[0].ID
	if i := a.index(a.cfg.LastStationID); i >= 0 {
		id = a.stations[i].ID
	}
	return a.openStation(id)
}

// onSettingsChanged rebuilds the client/theme after the user edits settings
// and reconnects everything against the (possibly new) server.
func (a *App) onSettingsChanged() tea.Cmd {
	a.deskFailed, a.deskWarned = false, false // changed something: give it another chance
	a.st = NewStyles(a.cfg.Theme)
	a.client = api.New(a.cfg.ServerURL, a.cfg.AuthToken)
	cmds := []tea.Cmd{a.startNotifications(), a.loadStations()}
	if a.chat != nil {
		a.chat.st = a.st
		a.chat.applyStyles()
		if a.chat.client.BaseURL != a.client.BaseURL || a.chat.client.Token != a.client.Token {
			id := a.chat.station.ID
			a.chat.Close()
			a.chat = nil
			a.pendingOpen(id)
		}
	}
	if a.sub != nil {
		switch s := a.sub.(type) {
		case *settingsModel:
			s.st, s.client = a.st, a.client
		}
	}
	return tea.Batch(cmds...)
}

// pendingOpen remembers which station to reopen once the reconnected server
// answers with its station list (onStations picks LastStationID up).
func (a *App) pendingOpen(id uint) { a.cfg.LastStationID = id }

// deskResultMsg reports how a desktop notification send went.
type deskResultMsg struct{ err error }

// deskTestDoneMsg is the result of Settings' "send a test notification".
type deskTestDoneMsg struct {
	err  error
	name string // the tool used, when there was one
}

// desktopDedupe is how long an identical notification is suppressed for.
const desktopDedupe = 3 * time.Second

// warnOnce shows a problem as a toast the first time only; the rest stay quiet.
func (a *App) warnOnce(text string) {
	if a.deskWarned {
		return
	}
	a.deskWarned = true
	a.toast, a.toastAt = text, time.Now()
}

// onNotification handles one event from the notifications feed: an in-terminal
// toast (for other stations) and/or a native desktop notification.
func (a *App) onNotification(raw json.RawMessage) tea.Cmd {
	var n struct {
		Type        string  `json:"type"`
		StationID   uint    `json:"station_id"`
		StationName string  `json:"station_name"`
		Key         string  `json:"key"`
		Label       string  `json:"label"`
		Decimals    int     `json:"decimals"`
		Value       float64 `json:"value"`
		Direction   string  `json:"direction"`
	}
	if json.Unmarshal(raw, &n) != nil {
		return nil
	}
	what, title, body := "", "", ""
	switch n.Type {
	case "agent_idle":
		what, title, body = "finished", n.StationName+" finished", "The agent is done and waiting for you."
	case "permission_requested":
		what, title, body = "needs permission", n.StationName+" needs permission", "Open superbadger to allow or reject."
	case "question_requested":
		what, title, body = "has a question", n.StationName+" has a question", "Open superbadger to answer."
	case "datapoint_threshold":
		name := n.Label
		if name == "" {
			name = n.Key
		}
		val := api.FormatValue(n.Value, n.Decimals)
		what = fmt.Sprintf("%s is %s threshold (%s)", name, n.Direction, val)
		title, body = n.StationName+": "+name+" alert", fmt.Sprintf("%s is %s its threshold (%s).", name, n.Direction, val)
	default:
		return nil
	}

	current := n.StationID == a.currentID()
	if a.cfg.Notifications && !current {
		a.toast = fmt.Sprintf("● %s %s", n.StationName, what)
		a.toastAt = time.Now()
		_, _ = os.Stderr.WriteString("\a") // terminal bell
	}
	return a.desktopNotify(n.Type+"/"+strconv.Itoa(int(n.StationID)), title, body, current)
}

// desktopNotify sends a native notification if the setting is on and it makes
// sense right now. It never blocks the UI (the send runs as a command) and
// never fails loudly: an unavailable or failing notifier is reported once.
func (a *App) desktopNotify(key, title, body string, current bool) tea.Cmd {
	if !a.cfg.DesktopNotifications || a.deskFailed {
		return nil
	}
	if a.desk == nil {
		reason := "not available on this machine"
		if a.deskWhy != nil {
			reason = a.deskWhy.Error()
		}
		a.warnOnce("desktop notifications unavailable: " + reason + " (turn them off in /settings)")
		return nil
	}
	// You're looking at that station in a focused terminal: no popup needed.
	// (Focus is only known if the terminal reports it; if not, we notify.)
	if current && a.focusKnown && a.focused {
		return nil
	}
	now := time.Now()
	if last, ok := a.deskLast[key]; ok && now.Sub(last) < desktopDedupe {
		return nil
	}
	a.deskLast[key] = now
	d := a.desk
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*notify.SendTimeout)
		defer cancel()
		return deskResultMsg{d.Notify(ctx, title, body)}
	}
}

func (a *App) View() string {
	if a.w == 0 {
		return ""
	}
	if a.splash {
		return a.splashView()
	}
	body, hint := "", ""
	switch {
	case a.sub != nil:
		body = a.sub.View()
	case a.chat != nil:
		body = a.chat.View()
		hint = a.chat.hint()
	default:
		body, hint = a.welcome(), "g settings · r retry · ctrl+q quit"
	}
	foot := ""
	if a.toast != "" && time.Since(a.toastAt) < 8*time.Second {
		foot = a.st.Primary.Render("● " + strings.TrimPrefix(a.toast, "● "))
	}
	return a.st.Paint(a.frame(body, foot, hint), a.w, a.h)
}

// frame wraps the screen in the rounded "terminal window" from the
// superbadger.ans art: a title in the top edge, side rails, and a bottom edge
// that carries the key hints.
func (a *App) frame(body, foot, hint string) string {
	iw, ih := a.innerW(), a.contentHeight()+1 // +1: toast row
	edge := a.st.Border
	title := a.st.Primary.Bold(true).Render("superbadger")
	// ╭───────────── superbadger ─────────────╮   (the border simply continues)
	fill := iw - 2 - lipgloss.Width(title) // the two spaces around the title
	left := max(1, fill/2)
	right := max(1, fill-left)
	top := edge.Render("╭"+strings.Repeat("─", left)+" ") + title + edge.Render(" "+strings.Repeat("─", right)+"╮")

	lines := strings.Split(body, "\n")
	if len(lines) > ih-1 {
		lines = lines[:ih-1]
	}
	for len(lines) < ih-1 {
		lines = append(lines, "")
	}
	lines = append(lines, foot)
	rail := edge.Render("│")
	clip := lipgloss.NewStyle().Width(iw).MaxWidth(iw)
	out := []string{top}
	for _, l := range lines {
		out = append(out, rail+clip.Render(l)+rail)
	}
	bot := ""
	if hint != "" {
		h := lipgloss.NewStyle().MaxWidth(iw - 5).Render(hint)
		bot = edge.Render("╰─ ") + a.st.Muted.Render(h) + edge.Render(" "+strings.Repeat("─", max(0, iw-lipgloss.Width(h)-3))+"╯")
	} else {
		bot = edge.Render("╰" + strings.Repeat("─", iw) + "╯")
	}
	return strings.Join(append(out, bot), "\n")
}

// splashView centers the logo on the page background — no window around it —
// and any key dismisses it. It doesn't depend on the server: it's shown first
// even when the server is unconfigured or down.
func (a *App) splashView() string {
	pad := strings.Repeat(" ", max(0, (a.w-splashW)/2))
	top := max(0, (a.h-splashH)/2)
	lines := make([]string, 0, a.h)
	for i := 0; i < top; i++ {
		lines = append(lines, "")
	}
	for _, l := range splashFor(a.st.T).frame(a.splashN) {
		lines = append(lines, pad+l)
	}
	return a.st.Paint(strings.Join(lines, "\n"), a.w, a.h)
}

// welcome is the base page shown when there is no station to chat with yet:
// no server configured, the server unreachable, or still connecting.
func (a *App) welcome() string {
	s := a.st
	title := s.Primary.Bold(true).Render("welcome to superbadger")
	switch {
	case !a.configured():
		return "\n " + title + "\n\n" +
			" " + s.Text.Render("No server is configured yet.") + "\n" +
			" " + s.Muted.Render("Press ") + s.Primary.Render("g") + s.Muted.Render(" (or ") + s.Primary.Render("/") + s.Muted.Render(") to set one,") + "\n" +
			" " + s.Muted.Render("or start with ") + s.Soft.Render("superbadger --server http://host:8080") + "\n"
	case a.loadErr != "":
		return "\n " + title + "\n\n" +
			" " + s.Danger.Render("can't reach "+a.cfg.ServerURL) + "\n" +
			" " + s.Muted.Render(a.loadErr) + "\n\n" +
			" " + s.Primary.Render("r") + s.Muted.Render(" retry  ·  ") + s.Primary.Render("g") + s.Muted.Render(" settings") + "\n"
	}
	return "\n " + title + "\n\n " + s.Muted.Render("connecting to "+a.cfg.ServerURL+"…") + "\n"
}
