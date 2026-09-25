package notify

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type call struct {
	name string
	args []string
}

// fakeEnv is a machine with the given tools and environment variables; runs
// are recorded, and answered with out/err.
func fakeEnv(goos string, have []string, vars map[string]string, out string, runErr error) (env, *[]call) {
	var calls []call
	return env{
		goos: goos,
		lookPath: func(name string) (string, error) {
			for _, h := range have {
				if h == name {
					return "/usr/bin/" + name, nil
				}
			}
			return "", exec.ErrNotFound
		},
		getenv: func(k string) string { return vars[k] },
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, call{name, args})
			return []byte(out), runErr
		},
	}, &calls
}

var desktop = map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus", "XDG_RUNTIME_DIR": "/run/user/1000"}

func TestLinuxDetectAndSend(t *testing.T) {
	e, calls := fakeEnv("linux", []string{"notify-send"}, desktop, "", nil)
	n, err := detect(e)
	if err != nil || n.Name() != "notify-send" {
		t.Fatalf("detect: %v, %v", n, err)
	}
	if err := n.Notify(context.Background(), "alpha finished", "The agent is done."); err != nil {
		t.Fatal(err)
	}
	want := []string{"--app-name=superbadger", "--urgency=normal", "--expire-time=8000", "--", "alpha finished", "The agent is done."}
	if len(*calls) != 1 || (*calls)[0].name != "notify-send" || strings.Join((*calls)[0].args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("ran %+v, want notify-send %v", *calls, want)
	}
}

func TestLinuxMissingNotifySendIsAnActionableError(t *testing.T) {
	e, _ := fakeEnv("linux", nil, desktop, "", nil)
	n, err := detect(e)
	if n != nil || !IsUnavailable(err) {
		t.Fatalf("want Unavailable, got %v, %v", n, err)
	}
	for _, hint := range []string{"notify-send", "libnotify", "NixOS"} {
		if !strings.Contains(err.Error(), hint) {
			t.Errorf("the message should mention %q so the user knows what to install: %s", hint, err)
		}
	}
}

func TestLinuxWithoutADesktopSession(t *testing.T) {
	e, _ := fakeEnv("linux", []string{"notify-send"}, map[string]string{}, "", nil)
	_, err := detect(e)
	if !IsUnavailable(err) || !strings.Contains(err.Error(), "session") {
		t.Fatalf("expected a no-session error, got %v", err)
	}
}

func TestArgumentsAreNeverInterpretedByAShell(t *testing.T) {
	e, calls := fakeEnv("linux", []string{"notify-send"}, desktop, "", nil)
	n, _ := detect(e)
	nasty := `$(touch /tmp/pwned); rm -rf ~ ; "quoted" 'single' ` + "`backtick`"
	if err := n.Notify(context.Background(), "-u critical "+nasty, nasty); err != nil {
		t.Fatal(err)
	}
	args := (*calls)[0].args
	// exactly the fixed flags, "--", then title and body as one argument each
	if len(args) != 6 || args[3] != "--" {
		t.Fatalf("unexpected argument list: %q", args)
	}
	if !strings.HasPrefix(args[4], "-u critical ") {
		t.Fatalf("a title beginning with '-' must arrive as data after '--': %q", args[4])
	}
	if !strings.Contains(args[5], "$(touch /tmp/pwned)") {
		t.Fatalf("the text must arrive verbatim, not be mangled or executed: %q", args[5])
	}
}

func TestSendFailureIncludesWhatTheToolSaid(t *testing.T) {
	e, _ := fakeEnv("linux", []string{"notify-send"}, desktop, "Cannot autolaunch D-Bus without X11 $DISPLAY", errors.New("exit status 1"))
	n, _ := detect(e)
	err := n.Notify(context.Background(), "t", "b")
	if err == nil || !strings.Contains(err.Error(), "D-Bus") || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("the error should carry the tool's own explanation: %v", err)
	}
	if IsUnavailable(err) {
		t.Fatal("a failed send is not 'unavailable'")
	}
}

func TestASlowNotificationDaemonCannotHangUs(t *testing.T) {
	old := SendTimeout
	SendTimeout = 50 * time.Millisecond
	defer func() { SendTimeout = old }()
	e, _ := fakeEnv("linux", []string{"notify-send"}, desktop, "", nil)
	e.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		<-ctx.Done() // a daemon that never answers
		return nil, ctx.Err()
	}
	n, _ := detect(e)
	start := time.Now()
	err := n.Notify(context.Background(), "t", "b")
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("expected a prompt timeout error, got %v after %v", err, time.Since(start))
	}
}

func TestMacUsesOsascriptWithEscaping(t *testing.T) {
	e, calls := fakeEnv("darwin", []string{"osascript"}, nil, "", nil)
	n, err := detect(e)
	if err != nil || n.Name() != "osascript" {
		t.Fatalf("%v %v", n, err)
	}
	if err := n.Notify(context.Background(), `say "hi"`, `back\slash "and" quotes`+"\nnewline"); err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	if c.name != "osascript" || len(c.args) != 2 || c.args[0] != "-e" {
		t.Fatalf("ran %+v", c)
	}
	want := `display notification "back\\slash \"and\" quotes newline" with title "say \"hi\""`
	if c.args[1] != want {
		t.Fatalf("script:\n got %s\nwant %s", c.args[1], want)
	}
}

func TestOtherPlatformsSayWhyNot(t *testing.T) {
	for _, goos := range []string{"windows", "plan9", "js"} {
		e, _ := fakeEnv(goos, nil, nil, "", nil)
		_, err := detect(e)
		if !IsUnavailable(err) || !strings.Contains(err.Error(), goos) {
			t.Errorf("%s: %v", goos, err)
		}
	}
}

func TestCleanMakesTextSafeAndShort(t *testing.T) {
	if got := Clean("a\n\tb\x00c   d", 50); got != "a b c d" {
		t.Errorf("control characters/whitespace: %q", got)
	}
	long := strings.Repeat("x", 500)
	if got := Clean(long, 20); len([]rune(got)) != 20 || !strings.HasSuffix(got, "…") {
		t.Errorf("truncation: %q", got)
	}
	if got := Clean("héllo wörld ✓", 50); got != "héllo wörld ✓" {
		t.Errorf("unicode must survive: %q", got)
	}
	t1, _ := prepare("  \n ", "body")
	if t1 != "superbadger" {
		t.Errorf("an empty title should fall back to the app name, got %q", t1)
	}
}

// TestDetectOnThisMachine only reports what Detect makes of the machine the
// tests run on (it sends nothing), so a failing build on some odd platform
// says what it saw.
func TestDetectOnThisMachine(t *testing.T) {
	n, err := Detect()
	switch {
	case n != nil:
		t.Logf("this machine can show desktop notifications via %s", n.Name())
	case IsUnavailable(err):
		t.Logf("this machine can't show desktop notifications: %v", err)
	default:
		t.Fatalf("Detect must return either a notifier or an *Unavailable, got %v", err)
	}
}

// TestRealDesktopNotification really pops a notification up, so it runs only
// when asked:  SUPERBADGER_TEST_DESKTOP=1 go test ./internal/notify -run Real -v
func TestRealDesktopNotification(t *testing.T) {
	if os.Getenv("SUPERBADGER_TEST_DESKTOP") != "1" {
		t.Skip("set SUPERBADGER_TEST_DESKTOP=1 to send a real desktop notification")
	}
	n, err := Detect()
	if err != nil {
		t.Skipf("no notifier here: %v", err)
	}
	if err := n.Notify(context.Background(), "superbadger test", "If you can read this, desktop notifications work."); err != nil {
		t.Fatal(err)
	}
}
