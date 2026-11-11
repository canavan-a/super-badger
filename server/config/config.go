package config

import (
	"log"
	"os"
	"strings"
)

type Config struct {
	ListenAddr       string
	DBPath           string
	OpencodeBaseURL  string
	OpencodePassword string
	MullvadBin       string
	// StaticDir, when non-empty, enables serving the mobile app's built web
	// client (see server/static) on StaticListenAddr — a separate port from
	// ListenAddr, left disabled by default so existing API-only deployments
	// are unaffected.
	StaticDir        string
	StaticListenAddr string
}

func Load() Config {
	return Config{
		ListenAddr:      getEnv("SUPERBADGER_LISTEN_ADDR", ":8080"),
		DBPath:          getEnv("SUPERBADGER_DB_PATH", "superbadger.db"),
		OpencodeBaseURL: getEnv("SUPERBADGER_OPENCODE_URL", "http://127.0.0.1:4096"),
		// opencode 2 always requires HTTP Basic auth: this must match
		// OPENCODE_PASSWORD on the opencode process. Read from
		// SUPERBADGER_OPENCODE_PASSWORD_FILE when set (the NixOS module passes
		// a systemd credential), else SUPERBADGER_OPENCODE_PASSWORD.
		OpencodePassword: secret("SUPERBADGER_OPENCODE_PASSWORD"),
		MullvadBin:       getEnv("SUPERBADGER_MULLVAD_BIN", "mullvad"),
		StaticDir:        os.Getenv("SUPERBADGER_STATIC_DIR"),
		StaticListenAddr: getEnv("SUPERBADGER_STATIC_LISTEN_ADDR", ":8081"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// secret reads key's value from the file named by key_FILE when that is
// set, else from key itself.
func secret(key string) string {
	if path := os.Getenv(key + "_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("read %s_FILE: %v", key, err)
		}
		return strings.TrimSpace(string(b))
	}
	return os.Getenv(key)
}
