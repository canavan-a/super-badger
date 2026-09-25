// Command superbadger is a terminal chat client for a superbadger server.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"superbadger-tui/internal/config"
	"superbadger-tui/internal/ui"
)

func main() {
	server := flag.String("server", "", "server URL (overrides the saved setting for this run)")
	token := flag.String("token", "", "auth token (overrides the saved setting for this run)")
	noSplash := flag.Bool("no-splash", false, "skip the launch art")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: superbadger [--server URL] [--token TOKEN] [--no-splash]")
		flag.PrintDefaults()
	}
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "superbadger: reading config:", err)
		os.Exit(1)
	}
	// Flags apply to this run only; they are never written to the config file.
	cfg.Override(*server, *token, *noSplash)

	p := tea.NewProgram(ui.New(cfg), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "superbadger:", err)
		os.Exit(1)
	}
}
