package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlagOverridesAreNeverPersisted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"server_url":"http://real:8080","auth_token":"real","theme":"dark"}`), 0o600)

	c, err := LoadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	c.Override("http://flag:1", "flagtok", true) // --server, --token, --no-splash
	if c.ServerURL != "http://flag:1" || c.AuthToken != "flagtok" || c.Splash {
		t.Fatalf("overrides not applied: %+v", c)
	}
	c.Theme = "slate" // an unrelated settings change triggers a save
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	got, _ := LoadFrom(p)
	if got.ServerURL != "http://real:8080" || got.AuthToken != "real" || !got.Splash {
		t.Fatalf("a flag leaked into the config file: %+v", got)
	}
	if got.Theme != "slate" {
		t.Fatalf("the real change was lost: %q", got.Theme)
	}
}

func TestEditingAnOverriddenFieldPersistsIt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	c, _ := LoadFrom(p)
	c.Override("", "", true) // --no-splash for this run
	c.Splash = false         // the user switches "Launch art" off on purpose
	c.Touch("splash")
	c.Save()
	if got, _ := LoadFrom(p); got.Splash {
		t.Fatal("explicit setting was not saved")
	}
}

func TestLaunchArtIsOnByDefault(t *testing.T) {
	c, _ := LoadFrom(filepath.Join(t.TempDir(), "none.json"))
	if !c.Splash {
		t.Fatal("launch art should default to on")
	}
}

func TestOldPersistedNoSplashIsIgnored(t *testing.T) {
	// Earlier builds could persist a --no-splash flag by accident. The
	// setting is now the positive "splash", so that stale value must not
	// switch the art off.
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"no_splash":true,"theme":"dark"}`), 0o600)
	c, _ := LoadFrom(p)
	if !c.Splash {
		t.Fatal("stale no_splash turned the launch art off")
	}
}

func TestNoDefaultServer(t *testing.T) {
	c, _ := LoadFrom(filepath.Join(t.TempDir(), "none.json"))
	if c.ServerURL != "" {
		t.Fatalf("unexpected default server %q", c.ServerURL)
	}
}

func TestSaveKeepsSettingsFromOtherVersions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"server_url":"http://s:1","future_setting":{"a":[1,2,3]},"theme":"dark"}`), 0o600)
	c, _ := LoadFrom(p)
	c.Theme = "ember"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), `"future_setting"`) || !strings.Contains(string(raw), `[`) {
		t.Fatalf("a key this version doesn't know was dropped on save:\n%s", raw)
	}
	if got, _ := LoadFrom(p); got.Theme != "ember" || got.ServerURL != "http://s:1" {
		t.Fatalf("known settings wrong after save: %+v", got)
	}
}

func TestSaveIsAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "c.json")
	c, _ := LoadFrom(p)
	c.AuthToken = "secret"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("config holds a token; mode is %v, want 0600", st.Mode().Perm())
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Fatalf("a temp file was left behind: %v", ents)
	}
	// Saving again replaces in place.
	c.Theme = "slate"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if ents, _ = os.ReadDir(filepath.Dir(p)); len(ents) != 1 {
		t.Fatalf("temp files accumulating: %v", ents)
	}
}

func TestCorruptConfigIsKeptNotLost(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"server_url": "http://precious:1", oops`), 0o600)
	c, err := LoadFrom(p)
	if err != nil {
		t.Fatalf("a corrupt file must not stop the app: %v", err)
	}
	if c.Notice == "" {
		t.Fatal("the user should be told what happened")
	}
	kept, err := os.ReadFile(p + ".corrupt")
	if err != nil || !strings.Contains(string(kept), "precious") {
		t.Fatalf("the unreadable file was not preserved: %v %q", err, kept)
	}
	// Saving afterwards must not clobber that backup.
	c.Theme = "dark"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if kept2, _ := os.ReadFile(p + ".corrupt"); string(kept2) != string(kept) {
		t.Fatal("the backup was overwritten by a later save")
	}
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "superbadger-config-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", dir)
	os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestSavingAPathlessConfigFromATestNeverWrites(t *testing.T) {
	// Even with the config dir sandboxed, a config with no explicit path is
	// the "real user config" case and must not be written by a test binary.
	p, _ := DefaultPath()
	c := Default()
	c.ServerURL = "http://should-not-be-saved"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatalf("Save wrote the default config path %s from a test", p)
	}
}
