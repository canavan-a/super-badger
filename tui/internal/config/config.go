// Package config holds the TUI's own persisted settings — the same fields the
// app keeps in AsyncStorage under superbadger.settings.v1 (app/src/settings.ts),
// stored as JSON under the user's config dir.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// There is deliberately no default server: until one is set the TUI shows a
// welcome page rather than guessing a URL and failing.

var ThemeNames = []string{"burrow", "dark", "slate", "ember", "light", "sepia"}

type Config struct {
	ServerURL     string `json:"server_url"`
	AuthToken     string `json:"auth_token"`
	Theme         string `json:"theme"`
	LastStationID uint   `json:"last_station_id"`
	Notifications bool   `json:"notifications"`
	Splash        bool   `json:"splash"` // launch art; default on
	path          string

	// Command-line flags override values for one run only. Save writes the
	// on-disk values back for any field still overridden, so a flag is never
	// persisted by an unrelated settings change.
	// Notice is set when loading had to recover from something (e.g. an
	// unreadable file); the UI shows it once.
	Notice string `json:"-"`

	overridden map[string]bool
	onDisk     struct {
		ServerURL, AuthToken string
		Splash               bool
	}
}

// Override applies flag values for this run. Zero values mean "not given".
func (c *Config) Override(server, token string, noSplash bool) {
	c.onDisk.ServerURL, c.onDisk.AuthToken, c.onDisk.Splash = c.ServerURL, c.AuthToken, c.Splash
	c.overridden = map[string]bool{}
	if server != "" {
		c.ServerURL, c.overridden["server_url"] = server, true
	}
	if token != "" {
		c.AuthToken, c.overridden["auth_token"] = token, true
	}
	if noSplash {
		c.Splash, c.overridden["splash"] = false, true
	}
}

// Touch marks a field as edited by the user, so it is saved as-is even if a
// flag had overridden it for this run.
func (c *Config) Touch(field string) { delete(c.overridden, field) }

func Default() Config {
	return Config{Theme: "burrow", Notifications: true, Splash: true}
}

// DefaultPath is where the config lives unless a path is given explicitly.
func DefaultPath() (string, error) { return defaultPath() }

func defaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "superbadger", "config.json"), nil
}

// Load reads the config file, returning defaults if it doesn't exist yet.
func Load() (*Config, error) {
	p, err := defaultPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(p)
}

func LoadFrom(p string) (*Config, error) {
	c := Default()
	c.path = p
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		// Never crash on, or overwrite, a file we can't read: keep it beside
		// the config as *.corrupt and start from defaults.
		bak := p + ".corrupt"
		if rerr := os.Rename(p, bak); rerr != nil {
			return nil, fmt.Errorf("config %s is unreadable (%v) and could not be set aside: %w", p, err, rerr)
		}
		c = Default()
		c.path = p
		c.Notice = "config was unreadable — kept as " + filepath.Base(bak)
		return &c, nil
	}
	if c.Theme == "" {
		c.Theme = "burrow"
	}
	c.path = p
	return &c, nil
}

func (c *Config) Save() error {
	if c.path == "" {
		// A test binary must never write the real user config. Tests once did
		// (they build configs with no path, which means "the default"), and
		// every test run overwrote the developer's own server URL and token.
		// Tests that want to save give the config an explicit temp path.
		if testing.Testing() {
			return nil
		}
		p, err := defaultPath()
		if err != nil {
			return err
		}
		c.path = p
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	out := *c
	if c.overridden["server_url"] {
		out.ServerURL = c.onDisk.ServerURL
	}
	if c.overridden["auth_token"] {
		out.AuthToken = c.onDisk.AuthToken
	}
	if c.overridden["splash"] {
		out.Splash = c.onDisk.Splash
	}
	known, err := json.Marshal(out)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(known, &fields)

	// Start from what is on disk so settings written by a different version
	// of the app (unknown keys) survive our save instead of being dropped.
	merged := map[string]json.RawMessage{}
	if old, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(old, &merged) // unreadable: just overwrite with ours
	}
	for k, v := range fields {
		merged[k] = v
	}
	b, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(c.path, b)
}

// writeAtomic replaces path in one step (temp file + rename), so a crash or
// full disk mid-save can never leave a truncated config. The file can hold
// the auth token, so it stays owner-only.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
