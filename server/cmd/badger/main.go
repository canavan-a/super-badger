// badger is superbadger's admin CLI, meant to be run directly on the machine
// hosting the server (not exposed over HTTP) — right now just token
// management, since that's the one thing that needs a human with shell
// access rather than an already-authenticated app. It talks to the exact
// same SQLite file the server does (same SUPERBADGER_DB_PATH), so a token
// generated here is immediately valid — no server restart needed, since
// api.RequireAuth checks the DB on every request rather than caching tokens
// at startup.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/joho/godotenv"
	"gorm.io/gorm"

	"main/config"
	"main/database"
)

func main() {
	_ = godotenv.Load()

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "token":
		runToken(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  badger token generate [label]   mint a new API token and print it (shown once)
  badger token list               list tokens (label, last 4 chars, recency — never the full token)
  badger token revoke <id>        delete a token by its "badger token list" id`)
}

func runToken(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}
	cfg := config.Load()
	db, err := database.Connect(cfg.DBPath)
	if err != nil {
		fatal("connect db: %v", err)
	}

	switch args[0] {
	case "generate":
		label := ""
		if len(args) > 1 {
			label = args[1]
		}
		generateToken(db, label)
	case "list":
		listTokens(db)
	case "revoke":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: badger token revoke <id>")
			os.Exit(1)
		}
		revokeToken(db, args[1])
	default:
		usage()
		os.Exit(1)
	}
}

// tokenBytes is 32 random bytes (256 bits) hex-encoded to 64 characters —
// the "sb_" prefix just makes a leaked token/env-var grep-able as
// superbadger's, the way "sk-"/"ghp_" prefixes do for other services.
const tokenBytes = 32

func generateToken(db *gorm.DB, label string) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		fatal("generate token: %v", err)
	}
	token := "sb_" + hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(token))

	err := database.CreateAuthToken(db, &database.AuthToken{
		Label:     label,
		TokenHash: hex.EncodeToString(sum[:]),
		Last4:     token[len(token)-4:],
	})
	if err != nil {
		fatal("save token: %v", err)
	}

	fmt.Println(token)
	fmt.Fprintln(os.Stderr, "\nStore this somewhere safe — it won't be shown again.")
	fmt.Fprintln(os.Stderr, "Paste it into the app's Settings > Auth token field, or send it as")
	fmt.Fprintln(os.Stderr, `"Authorization: Bearer <token>" directly.`)
}

func listTokens(db *gorm.DB) {
	tokens, err := database.ListAuthTokens(db)
	if err != nil {
		fatal("list tokens: %v", err)
	}
	if len(tokens) == 0 {
		fmt.Println("no tokens yet — the API is open until you run 'badger token generate'")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tLABEL\t…LAST4\tCREATED\tLAST USED")
	for _, t := range tokens {
		lastUsed := "never"
		if t.LastUsedAt != nil {
			lastUsed = t.LastUsedAt.Format("2006-01-02 15:04")
		}
		label := t.Label
		if label == "" {
			label = "-"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", t.ID, label, t.Last4, t.CreatedAt.Format("2006-01-02 15:04"), lastUsed)
	}
	_ = w.Flush()
}

func revokeToken(db *gorm.DB, idArg string) {
	id, err := strconv.ParseUint(idArg, 10, 64)
	if err != nil {
		fatal("invalid id %q", idArg)
	}
	if err := database.DeleteAuthToken(db, uint(id)); err != nil {
		fatal("revoke token: %v", err)
	}
	fmt.Printf("revoked token %d\n", id)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
