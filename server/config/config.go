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
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
