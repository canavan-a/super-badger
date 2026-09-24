package ui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"superbadger-tui/internal/api"
	"superbadger-tui/internal/chat"
	"superbadger-tui/internal/config"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestPaintKeepsBackgroundAfterResets(t *testing.T) {
	s := NewStyles("burrow")
	out := s.Paint(s.Primary.Render("hi")+" plain\n"+s.Danger.Render("x"), 20, 3)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d", len(lines))
	}
	bg := sgr(48, s.T.Bg)
	for i, l := range lines {
		if lw := lipgloss.Width(l); lw != 20 {
			t.Errorf("line %d width %d, want 20", i, lw)
		}
		if !strings.HasPrefix(l, bg) {
			t.Errorf("line %d does not start with the page background", i)
		}
	}
	// The text after the first styled span must be re-painted, not left on
	// the terminal's own background.
	if !strings.Contains(lines[0], "\x1b[0m"+bg) {
		t.Error("background not restored after a reset")
	}
}

func testApp(w, h int) *App {
	cfg := config.Default()
	cfg.Splash = false
	a := New(&cfg)
	a.w, a.h = w, h
	return a
}

func TestFrameIsExactlyScreenSized(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {100, 34}, {132, 43}, {40, 12}} {
		a := testApp(sz[0], sz[1])
		out := ansi.ReplaceAllString(a.View(), "")
		lines := strings.Split(out, "\n")
		if len(lines) != sz[1] {
			t.Errorf("%v: %d lines", sz, len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w != sz[0] {
				t.Errorf("%v: line %d is %d wide: %q", sz, i, w, l)
			}
		}
		if !strings.HasPrefix(lines[0], "╭") || !strings.HasSuffix(lines[0], "╮") {
			t.Errorf("%v: top edge broken: %q", sz, lines[0])
		}
	}
}

func TestUserAndModelTurnsLookDifferent(t *testing.T) {
	st := NewStyles("burrow")
	m := newChat(api.New("http://127.0.0.1:1", ""), st, api.Station{ID: 1, Name: "alpha", Color: "#34C759", Reachable: true}, 0, 1)
	defer m.Close()
	m.resize(70, 30)
	m.state.AddLocalUserTurn("u1", "how do I reverse a string in go?\nkeep it short")
	for _, raw := range []string{
		`{"type":"message.updated","properties":{"info":{"id":"a1","role":"assistant"}}}`,
		`{"type":"message.part.updated","properties":{"part":{"id":"p1","messageID":"a1","type":"text","text":"Convert to runes, then swap:\n` + "```go" + `\nr := []rune(s)\n` + "```" + `"}}}`,
		`{"type":"message.part.updated","properties":{"part":{"id":"t1","messageID":"a1","type":"tool","tool":"read","state":{"status":"completed","title":"main.go","output":"package main"}}}}`,
	} {
		e, _ := chat.ParseEvent([]byte(raw))
		m.state.Apply(e)
	}
	raw := m.transcript()
	plain := ansi.ReplaceAllString(raw, "")
	t.Log("\n" + plain)

	you := strings.Index(plain, "❯ you")
	bot := strings.Index(plain, "◆ alpha")
	if you < 0 || bot < 0 {
		t.Fatalf("missing speaker labels:\n%s", plain)
	}
	// The user's block is a filled panel; the model's is not.
	if !strings.Contains(raw, bgTriple(st.T.Panel)) {
		t.Error("user block should carry the panel background")
	}
	for _, l := range strings.Split(raw, "\n") {
		if strings.Contains(ansi.ReplaceAllString(l, ""), "Convert to runes") && strings.Contains(l, bgTriple(st.T.Panel)) {
			t.Error("model text must not sit on the user panel")
		}
	}
	for i, l := range strings.Split(plain, "\n") {
		if w := lipgloss.Width(l); w > 70 {
			t.Errorf("line %d is %d wide (>70): %q", i, w, l)
		}
	}
}

// bgTriple is the "48;2;r;g;b" fragment lipgloss emits for a background, which
// it may fold into a longer combined sequence.
func bgTriple(hex string) string {
	r, g, b := rgb(hex)
	_, _ = g, b // termenv may round a channel by one, so match on red only
	return fmt.Sprintf("48;2;%d;", r)
}

func TestMain(m *testing.M) {
	// No TTY under `go test`, so force color output to assert on it.
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}

func TestFrameWithHintIsExactWidth(t *testing.T) {
	a := testApp(100, 30)
	for _, hint := range []string{"short", strings.Repeat("x ", 80)} {
		out := ansi.ReplaceAllString(a.frame("body", "", hint), "")
		for i, l := range strings.Split(out, "\n") {
			if w := lipgloss.Width(l); w != 100 {
				t.Errorf("hint %.8q: line %d is %d wide", hint, i, w)
			}
		}
	}
}

func TestArrowsRollStationsOnlyWhenInputEmpty(t *testing.T) {
	a := testApp(100, 30)
	a.stations = []api.Station{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}, {ID: 3, Name: "c"}}
	a.chat = newChat(a.client, a.st, a.stations[0], 0, 3)
	defer func() { a.chat.Close() }()
	a.chat.resize(98, 26)

	press := func(s string) {
		var k tea.KeyMsg
		switch s {
		case "left":
			k = tea.KeyMsg{Type: tea.KeyLeft}
		case "right":
			k = tea.KeyMsg{Type: tea.KeyRight}
		}
		a.key(k)
	}
	press("left") // wraps 1 -> 3
	if a.chat.station.ID != 3 {
		t.Fatalf("left from first should wrap to last, got %d", a.chat.station.ID)
	}
	press("right") // 3 -> 1
	if a.chat.station.ID != 1 {
		t.Fatalf("right from last should wrap to first, got %d", a.chat.station.ID)
	}
	a.chat.ta.SetValue("draft")
	press("right")
	if a.chat.station.ID != 1 {
		t.Fatal("arrows must move the cursor, not the station, when there is text")
	}
}

func TestSplashHasNoNestedWindow(t *testing.T) {
	if splashH == 0 || splashW == 0 {
		t.Fatal("splash is empty")
	}
	plain := ansi.ReplaceAllString(strings.Join(splashRows, "\n"), "")
	for _, bad := range []string{"╭", "╮", "╰", "╯", "│", "● ● ●", "~ $"} {
		if strings.Contains(plain, bad) {
			t.Errorf("splash still contains %q", bad)
		}
	}
	if !strings.Contains(plain, "press any key to burrow in") {
		t.Error("prompt line missing")
	}
	for i, l := range splashRows {
		if w := lipgloss.Width(l); w != splashW {
			t.Errorf("row %d is %d wide, want %d", i, w, splashW)
		}
	}
	t.Logf("splash is %dx%d", splashW, splashH)
}

func TestSplashShowsFirstEvenWithoutAReachableServer(t *testing.T) {
	cfg := config.Default() // splash on
	cfg.ServerURL = "http://127.0.0.1:1"
	a := New(&cfg)
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	// The station load fails, which would normally drop us on Settings.
	a.Update(stationsMsg{err: fmt.Errorf("connection refused")})
	plain := ansi.ReplaceAllString(a.View(), "")
	if !strings.Contains(plain, "press any key to burrow in") {
		t.Fatalf("splash not shown while server is down:\n%s", plain)
	}
	if strings.Contains(plain, "Settings") {
		t.Fatal("settings showed through the splash")
	}
	// Any key dismisses it. A failed connection shows the welcome page with
	// the error; it must not throw the user into Settings.
	a.splashUntil = time.Time{} // past the launch grace period
	a.Update(tea.KeyMsg{Type: tea.KeySpace})
	after := ansi.ReplaceAllString(a.View(), "")
	if !strings.Contains(after, "can't reach http://127.0.0.1:1") || strings.Contains(after, "── Connection") {
		t.Fatalf("expected the welcome page, got:\n%s", after)
	}
}

func TestSplashShowsForAFreshUnconfiguredInstall(t *testing.T) {
	// No config file at all: defaults, nothing configured.
	cfg, err := config.LoadFrom(t.TempDir() + "/none.json")
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	if !strings.Contains(ansi.ReplaceAllString(a.View(), ""), "press any key to burrow in") {
		t.Fatal("splash missing on a fresh install")
	}
}

func TestUnconfiguredServerShowsWelcomeAndNeverOpensSettings(t *testing.T) {
	cfg, _ := config.LoadFrom(t.TempDir() + "/none.json")
	if cfg.ServerURL != "" {
		t.Fatalf("there must be no default server, got %q", cfg.ServerURL)
	}
	cfg.Splash = false
	a := New(cfg)
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if a.loadStations() != nil {
		t.Fatal("must not try to connect when no server is configured")
	}
	view := ansi.ReplaceAllString(a.View(), "")
	if !strings.Contains(view, "No server is configured yet") || strings.Contains(view, "── Connection") {
		t.Fatalf("expected the welcome page:\n%s", view)
	}
	// g opens settings on request.
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if !strings.Contains(ansi.ReplaceAllString(a.View(), ""), "── Connection") {
		t.Fatal("g should open settings")
	}
}

func TestSlashSettingsOpensSettings(t *testing.T) {
	a := testApp(100, 30)
	a.cfg.ServerURL = "http://127.0.0.1:1"
	a.stations = []api.Station{{ID: 1, Name: "a", Reachable: true}}
	a.chat = newChat(a.client, a.st, a.stations[0], 0, 1)
	defer a.chat.Close()
	a.chat.resize(98, 26)

	for _, tc := range []struct{ typed, want string }{
		{"/settings", "── Connection"},
		{"/SETTINGS", "── Connection"},
		{"/stations", "Stations"},
		{"/station", "settings"},
	} {
		a.sub, a.subName = nil, ""
		a.chat.ta.SetValue(tc.typed)
		cmd := a.key(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatalf("%s: no command produced", tc.typed)
		}
		a.Update(cmd())
		if !strings.Contains(ansi.ReplaceAllString(a.View(), ""), tc.want) {
			t.Errorf("%s did not open the expected screen (%q)", tc.typed, tc.want)
		}
		if len(a.chat.outbox) != 0 || a.chat.ta.Value() != "" {
			t.Errorf("%s must not be sent to the model and the box should clear", tc.typed)
		}
	}
	// An unknown slash-command is ordinary text and is queued for the model.
	a.sub, a.subName = nil, ""
	a.chat.ta.SetValue("/compact-please")
	a.key(tea.KeyMsg{Type: tea.KeyEnter})
	if len(a.chat.outbox) != 1 {
		t.Fatal("unknown /text should be sent as a normal message")
	}
}

func TestSplashKeepsTheBadger(t *testing.T) {
	// The badger is drawn in colored half-blocks; only page-colored ones are
	// background. A wrong "blank" test once cropped the whole picture away.
	pixels := 0
	for y := 0; y < splashGap; y++ {
		for _, c := range splashGrid[y] {
			if !c.blankish {
				pixels++
			}
		}
	}
	if pixels < 250 {
		t.Fatalf("only %d picture cells survived; the badger is missing", pixels)
	}
	if splashH < 20 {
		t.Fatalf("splash is only %d rows tall; the picture was cropped", splashH)
	}
}

func plainRow(s string) string { return ansi.ReplaceAllString(s, "") }

func TestSplashCursorBlinks(t *testing.T) {
	last := len(splashGrid) - 1
	on := plainRow(splashFrameRows(0)[last])
	off := plainRow(splashFrameRows(blinkFrames)[last])
	if !strings.Contains(on, "burrow in _") {
		t.Fatalf("cursor should show on frame 0: %q", on)
	}
	if strings.Contains(off, "_") || !strings.Contains(off, "burrow in") {
		t.Fatalf("cursor should be hidden (text kept) half a period later: %q", off)
	}
	if lipgloss.Width(on) != lipgloss.Width(off) {
		t.Fatal("blinking must not change the row width")
	}
	if plainRow(splashFrameRows(2 * blinkFrames)[last]) != on {
		t.Fatal("cursor should come back after a full period")
	}
}

func TestSplashGlimmerSweepsOnlyTheWordmark(t *testing.T) {
	base := splashFrameRows(0)
	changedWord, changedEye, changedOther := false, false, false
	for n := 0; n < (splashW+glimmerGap)/glimmerStep; n++ {
		fr := splashFrameRows(n)
		for y, row := range fr {
			if plainRow(row) != plainRow(splashRows[y]) && y != len(fr)-1 {
				t.Fatalf("frame %d row %d: glimmer changed the glyphs, only colors may change", n, y)
			}
			if row == splashRows[y] {
				continue
			}
			switch {
			case splashWordRow(y) || y == len(fr)-1: // wordmark, or the blinking prompt
				changedWord = true
			case rowHasEye(splashGrid[y]):
				changedEye = true
			default:
				changedOther = true
			}
		}
	}
	if !changedWord {
		t.Fatal("no frame brightened any wordmark cell")
	}
	if !changedEye {
		t.Fatal("the badger's eye never glimmered")
	}
	if changedOther {
		t.Fatal("the badger's body and the margins must never glimmer")
	}
	// The band moves: consecutive frames during a sweep must differ.
	moved := 0
	for n := 0; n < splashW/glimmerStep; n++ {
		if strings.Join(splashFrameRows(n), "") != strings.Join(splashFrameRows(n+1), "") {
			moved++
		}
	}
	if moved < 10 {
		t.Fatalf("the glimmer barely moves: only %d of the sweep's frames differ", moved)
	}
	_ = base
}

func TestSplashGlimmerBrightensTowardWhite(t *testing.T) {
	if got := glimmerColor("100;100;100", 1); got == "100;100;100" {
		t.Fatal("full-strength glimmer left the color unchanged")
	}
	if got := glimmerColor("0;0;0", 0.5); got != "127;127;127" {
		t.Fatalf("unexpected blend %q", got)
	}
}

func TestSplashTicksAdvanceAndStopWhenDismissed(t *testing.T) {
	cfg := config.Default()
	a := New(&cfg)
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if _, cmd := a.Update(splashTickMsg{}); cmd == nil || a.splashN != 1 {
		t.Fatalf("tick should advance the frame and re-arm (n=%d)", a.splashN)
	}
	a.splashUntil = time.Time{}
	a.Update(tea.KeyMsg{Type: tea.KeySpace}) // dismiss
	if _, cmd := a.Update(splashTickMsg{}); cmd != nil {
		t.Fatal("ticking must stop once the splash is dismissed")
	}
}

func TestBadgerEyeIsFoundAndOnlyItGlints(t *testing.T) {
	eye, y0, y1 := 0, 99, -1
	for y, row := range splashGrid {
		if splashWordRow(y) {
			continue
		}
		for _, c := range row {
			if splashEye(c) {
				eye++
				y0, y1 = min(y0, y), max(y1, y)
			}
		}
	}
	if eye < 8 || eye > 40 {
		t.Fatalf("expected a small eye cluster, found %d cells", eye)
	}
	if y0 < 0 || y1 >= splashGap {
		t.Fatalf("eye should be in the badger rows (0-%d), got rows %d-%d", splashGap-1, y0, y1)
	}
	// Find a frame where the band is over the eye and check it is brighter,
	// while every non-eye cell in the same rows is byte-identical.
	bright := false
	for n := 0; n < (splashW+glimmerGap)/glimmerStep; n++ {
		fr := splashFrameRows(n)
		for y := y0; y <= y1; y++ {
			if fr[y] == splashRows[y] || splashWordRow(y) {
				continue
			}
			bright = true
			for x, c := range splashGrid[y] {
				if splashEye(c) {
					continue
				}
				if !strings.Contains(fr[y], c.esc+c.r) {
					t.Fatalf("frame %d: non-eye cell (%d,%d) was altered", n, x, y)
				}
			}
		}
	}
	if !bright {
		t.Fatal("the eye never brightened")
	}
}

func TestShineIsSubtle(t *testing.T) {
	// The brightest point is well short of white, and the band fades out
	// smoothly over its full width — no hot core, no flash.
	peak := shine(0, glimmerHalf)
	if peak <= 0.3 || peak > 0.85 {
		t.Fatalf("peak %.2f: should be a visible but gentle lift", peak)
	}
	if got := glimmerColor("58;78;128", peak); got == "255;255;255" {
		t.Fatal("the shimmer must never reach pure white")
	}
	prev := peak
	for d := 1.0; d < glimmerHalf; d++ {
		cur := shine(d, glimmerHalf)
		if cur >= prev {
			t.Fatalf("falloff should be monotonic: shine(%v)=%.2f after %.2f", d, cur, prev)
		}
		prev = cur
	}
	if shine(glimmerHalf+1, glimmerHalf) != 0 {
		t.Fatal("no light should reach past the band")
	}
}

func TestSplashIgnoresStrayKeysRightAfterLaunch(t *testing.T) {
	cfg := config.Default()
	a := New(&cfg)
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) // e.g. a terminal reply
	if !a.splash {
		t.Fatal("a key in the first moments must not dismiss the splash")
	}
	// ...but quitting must always work, even during the grace period.
	if cmd := a.key(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Fatal("ctrl+c should quit during the grace period")
	}
	a.splash = true
	a.splashUntil = time.Now().Add(-time.Millisecond)
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if a.splash {
		t.Fatal("after the grace period any key should dismiss the splash")
	}
}

// ---- chat commands ----

func busyChat(t *testing.T) (*App, *chatModel) {
	t.Helper()
	a := testApp(100, 30)
	a.cfg.ServerURL = "http://127.0.0.1:1"
	a.client = api.New(a.cfg.ServerURL, "")
	a.stations = []api.Station{{ID: 1, Name: "a", Reachable: true}}
	a.chat = newChat(a.client, a.st, a.stations[0], 0, 1)
	t.Cleanup(a.chat.Close)
	a.chat.resize(98, 26)
	a.chat.state.Busy = true
	return a, a.chat
}

func TestEscAndCtrlCNeverCancelAReply(t *testing.T) {
	a, c := busyChat(t)
	if cmd := c.key(tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil {
		t.Fatal("Esc must not start an abort")
	}
	if !c.state.Busy {
		t.Fatal("Esc must leave the reply running")
	}
	// ctrl+c quits the TUI (it does not abort the server-side reply).
	cmd := a.key(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("ctrl+c should produce a quit, not an abort request; got %T", cmd())
	}
}

func TestStopCommandAbortsAndDropsQueuedMessages(t *testing.T) {
	a, c := busyChat(t)
	c.outbox = []queued{{"1", "follow-up one"}, {"2", "follow-up two"}}
	c.ta.SetValue("/stop")
	cmd := a.key(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("/stop while busy should issue the abort")
	}
	if len(c.outbox) != 0 {
		t.Fatal("/stop should discard messages queued behind the cancelled reply")
	}
	if c.ta.Value() != "" {
		t.Fatal("the input should clear")
	}
	if c.state.Notice == "" {
		t.Fatal("the user should see that it's stopping")
	}
	// It must not have been sent to the model as a prompt.
	for _, q := range c.outbox {
		if q.text == "/stop" {
			t.Fatal("/stop was queued as a message")
		}
	}
}

func TestStopWhenIdleSaysSoAndDoesNothing(t *testing.T) {
	a, c := busyChat(t)
	c.state.Busy = false
	c.ta.SetValue("/stop")
	if cmd := a.key(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatal("nothing to stop, so no request should be made")
	}
	if !strings.Contains(c.state.Notice, "nothing to stop") {
		t.Fatalf("notice = %q", c.state.Notice)
	}
}

func TestShowTogglesThinking(t *testing.T) {
	a, c := busyChat(t)
	c.showThink = false
	for i, want := range []bool{true, false, true} {
		c.ta.SetValue("/show")
		a.key(tea.KeyMsg{Type: tea.KeyEnter})
		if c.showThink != want {
			t.Fatalf("toggle %d: showThink=%v, want %v", i, c.showThink, want)
		}
		if len(c.outbox) != 0 {
			t.Fatal("/show must not be sent to the model")
		}
	}
	// Case-insensitive, like the other commands.
	c.ta.SetValue("/SHOW")
	a.key(tea.KeyMsg{Type: tea.KeyEnter})
	if c.showThink {
		t.Fatal("/SHOW should toggle too")
	}
}

func TestSplashCommandReplaysTheTitleScreen(t *testing.T) {
	a, c := busyChat(t)
	c.state.Busy = false
	a.splash = false
	c.ta.SetValue("/splash")
	cmd := a.key(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("/splash should navigate")
	}
	_, next := a.Update(cmd())
	if !a.splash || next == nil {
		t.Fatal("the title screen should be showing again and animating")
	}
	if a.splashN != 0 {
		t.Fatal("it should restart from the first frame")
	}
	// Too small to fit: say so rather than showing a clipped picture.
	a.splash = false
	a.w, a.h = 50, 12
	if a.navigate("splash") != nil || a.splash {
		t.Fatal("a too-small terminal should not enter the splash")
	}
	if a.toast == "" {
		t.Fatal("and should explain why")
	}
}

// ---- themed title screen ----

func TestBurrowSplashIsTheOriginalArt(t *testing.T) {
	set := splashFor(themes["burrow"])
	for y := range splashRows {
		if set.rows[y] != splashRows[y] {
			t.Fatalf("burrow row %d was altered", y)
		}
	}
}

func TestEveryThemeRecolorsTheSplashWithoutChangingTheShape(t *testing.T) {
	burrow := splashFor(themes["burrow"])
	for name, th := range themes {
		set := splashFor(th)
		if len(set.rows) != len(burrow.rows) {
			t.Fatalf("%s: row count changed", name)
		}
		for y := range set.rows {
			if plainRow(set.rows[y]) != plainRow(burrow.rows[y]) {
				t.Fatalf("%s row %d: recoloring changed the glyphs", name, y)
			}
			if lipgloss.Width(set.rows[y]) != splashW && lipgloss.Width(set.rows[y]) != lipgloss.Width(burrow.rows[y]) {
				t.Fatalf("%s row %d changed width", name, y)
			}
		}
		if name != "burrow" && strings.Join(set.rows, "") == strings.Join(burrow.rows, "") {
			t.Errorf("%s: the splash still has the burrow colors", name)
		}
	}
}

func TestSplashPageAndAccentMapToTheTheme(t *testing.T) {
	for name, th := range themes {
		if name == "burrow" {
			continue
		}
		set := splashFor(th)
		tr := themeRamp(th)
		// The art's page color must become exactly the theme's page color.
		if got := remapColor(artRamp[0], tr, hexVec(th.Accent)); got.str() != tr[0].str() {
			t.Errorf("%s: page maps to %s, want %s", name, got.str(), tr[0].str())
		}
		// Cyan (the eye) must become the theme's accent.
		acc := remapColor(artAccent, tr, hexVec(th.Accent))
		if want := hexVec(th.Accent); absv(acc, want) > 2 {
			t.Errorf("%s: cyan maps to %s, want the accent %s", name, acc.str(), want.str())
		}
		// Light strokes must become the theme's text color.
		if got := remapColor(artRamp[4], tr, hexVec(th.Accent)); absv(got, hexVec(th.Text)) > 2 {
			t.Errorf("%s: strokes map to %s, want text %s", name, got.str(), hexVec(th.Text).str())
		}
		// The eye is still findable after recoloring.
		found := false
		for y := 0; y < splashGap && !found; y++ {
			for _, c := range set.grid[y] {
				if splashEye(c) {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("%s: the eye was lost", name)
		}
	}
}

func absv(a, b vec) float64 {
	return abs(a[0]-b[0]) + abs(a[1]-b[1]) + abs(a[2]-b[2])
}

func TestShadesKeepTheirOrderInsideATheme(t *testing.T) {
	// Darker art tones stay darker on a dark theme, and flip on a light one.
	dark, light := themeRamp(themes["slate"]), themeRamp(themes["light"])
	for i := 0; i+1 < len(artRamp); i++ {
		d0 := vlum(remapColor(artRamp[i], dark, hexVec(themes["slate"].Accent)))
		d1 := vlum(remapColor(artRamp[i+1], dark, hexVec(themes["slate"].Accent)))
		if d1 <= d0 {
			t.Errorf("slate: tone %d is not lighter than tone %d (%.0f vs %.0f)", i+1, i, d1, d0)
		}
		l0 := vlum(remapColor(artRamp[i], light, hexVec(themes["light"].Accent)))
		l1 := vlum(remapColor(artRamp[i+1], light, hexVec(themes["light"].Accent)))
		if l1 >= l0 {
			t.Errorf("light: tone %d should be darker than tone %d (%.0f vs %.0f)", i+1, i, l1, l0)
		}
	}
}

func TestSplashViewUsesTheChosenThemesBackground(t *testing.T) {
	cfg := config.Default()
	cfg.Theme = "ember"
	a := New(&cfg)
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	view := a.View()
	if !strings.Contains(view, "48;2;5;4;3") { // ember page #050403
		t.Fatal("the title screen should be painted on the ember page color")
	}
	if strings.Contains(view, "48;2;16;20;34") {
		t.Fatal("the burrow navy leaked into the ember title screen")
	}
	// Switching theme changes it.
	cfg2 := config.Default()
	cfg2.Theme = "light"
	b := New(&cfg2)
	b.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if !strings.Contains(b.View(), "48;2;255;255;255") {
		t.Fatal("the light theme's title screen should be on a white page")
	}
}

func TestGlimmerDarkensOnLightThemesAndBrightensOnDark(t *testing.T) {
	if splashFor(themes["light"]).to != "0;0;0" || splashFor(themes["sepia"]).to != "0;0;0" {
		t.Fatal("light themes should glimmer toward black")
	}
	if splashFor(themes["ember"]).to != "255;255;255" {
		t.Fatal("dark themes should glimmer toward white")
	}
	if got := glimmerColorTo("100;100;100", 0.5, "0;0;0"); got != "50;50;50" {
		t.Fatalf("toward black: %s", got)
	}
	// Animation still runs on a themed set and only changes colors.
	set := splashFor(themes["light"])
	changed := false
	for n := 0; n < 30; n++ {
		fr := set.frame(n)
		for y := range fr {
			if y != len(fr)-1 && plainRow(fr[y]) != plainRow(set.rows[y]) {
				t.Fatalf("frame %d row %d: glyphs changed", n, y)
			}
			if fr[y] != set.rows[y] {
				changed = true
			}
		}
	}
	if !changed {
		t.Fatal("the themed splash never animates")
	}
}
