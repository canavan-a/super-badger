// Package notify sends native desktop notifications by shelling out to the
// platform's own tool, so there are no extra dependencies:
//
//	Linux  notify-send (libnotify), which talks to the desktop's notification daemon
//	macOS  osascript ("display notification")
//
// Anywhere else, or when the tool or the desktop session is missing, Detect
// returns an *Unavailable that says why in words a person can act on. Callers
// are expected to treat that (and any send error) as normal, never fatal: the
// TUI carries on and says so once.
package notify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode"
)

// Notifier sends one notification.
type Notifier interface {
	// Name is the tool used, e.g. "notify-send".
	Name() string
	Notify(ctx context.Context, title, body string) error
}

// Unavailable means desktop notifications can't be sent here, and why.
type Unavailable struct{ Reason string }

func (u *Unavailable) Error() string { return u.Reason }

// IsUnavailable reports whether err is (or wraps) an *Unavailable.
func IsUnavailable(err error) bool {
	var u *Unavailable
	return errors.As(err, &u)
}

// SendTimeout bounds a single send; a wedged notification daemon must not hang
// anything.
var SendTimeout = 3 * time.Second

const (
	maxTitle = 90
	maxBody  = 240
)

// env is everything Detect and Notify touch outside the process, so tests can
// stand in for a machine with (or without) any of it.
type env struct {
	goos     string
	lookPath func(string) (string, error)
	getenv   func(string) string
	run      func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func runCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

var systemEnv = env{goos: runtime.GOOS, lookPath: exec.LookPath, getenv: os.Getenv, run: runCmd}

// Detect returns the notifier for this machine, or an *Unavailable.
func Detect() (Notifier, error) { return detect(systemEnv) }

func detect(e env) (Notifier, error) {
	switch e.goos {
	case "linux", "freebsd", "openbsd", "netbsd":
		if _, err := e.lookPath("notify-send"); err != nil {
			return nil, &Unavailable{"notify-send was not found — install libnotify " +
				"(Debian/Ubuntu: libnotify-bin, Arch/Fedora: libnotify, NixOS: pkgs.libnotify)"}
		}
		// notify-send talks to the desktop over the session D-Bus; without one
		// (SSH, a bare TTY, a container) every send would fail.
		if e.getenv("DBUS_SESSION_BUS_ADDRESS") == "" && e.getenv("XDG_RUNTIME_DIR") == "" {
			return nil, &Unavailable{"no desktop session was found to send notifications to " +
				"(are you on SSH or a bare console?)"}
		}
		return &linuxSend{e}, nil
	case "darwin":
		if _, err := e.lookPath("osascript"); err != nil {
			return nil, &Unavailable{"osascript was not found"}
		}
		return &macSend{e}, nil
	}
	return nil, &Unavailable{fmt.Sprintf("desktop notifications aren't supported on %s yet", e.goos)}
}

// Clean makes text safe and short enough for a notification: control
// characters and newlines become spaces, runs of spaces collapse, and anything
// over max runes is cut with an ellipsis.
func Clean(s string, max int) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteRune(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	if r := []rune(out); len(r) > max {
		out = string(r[:max-1]) + "…"
	}
	return out
}

func prepare(title, body string) (string, string) {
	title, body = Clean(title, maxTitle), Clean(body, maxBody)
	if title == "" {
		title = "superbadger"
	}
	return title, body
}

func failure(name string, err error, out []byte) error {
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("%s: %w (%s)", name, err, Clean(msg, 160))
	}
	return fmt.Errorf("%s: %w", name, err)
}

// ---- Linux ----

type linuxSend struct{ e env }

func (linuxSend) Name() string { return "notify-send" }

func (n *linuxSend) Notify(ctx context.Context, title, body string) error {
	title, body = prepare(title, body)
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()
	// Every value is its own argument (no shell), and "--" ends option parsing
	// so a title starting with "-" can't be read as a flag.
	out, err := n.e.run(ctx, "notify-send",
		"--app-name=superbadger", "--urgency=normal", "--expire-time=8000", "--", title, body)
	if err != nil {
		return failure("notify-send", err, out)
	}
	return nil
}

// ---- macOS ----

type macSend struct{ e env }

func (macSend) Name() string { return "osascript" }

// appleString quotes s as an AppleScript string literal.
func appleString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func (n *macSend) Notify(ctx context.Context, title, body string) error {
	title, body = prepare(title, body)
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()
	script := "display notification " + appleString(body) + " with title " + appleString(title)
	out, err := n.e.run(ctx, "osascript", "-e", script)
	if err != nil {
		return failure("osascript", err, out)
	}
	return nil
}
