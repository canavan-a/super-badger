package config

import (
	"os"
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
		// Matches OPENCODE_SERVER_PASSWORD on the opencode process itself
		// (HTTP Basic auth, any username) — empty means opencode was
		// launched unsecured, which is also fine for local dev.
		OpencodePassword: os.Getenv("SUPERBADGER_OPENCODE_PASSWORD"),
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
