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
