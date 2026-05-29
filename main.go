package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tylerreagan/clasp/internal/ui"
	"github.com/tylerreagan/clasp/internal/watcher"
)

// Build information, injected at release time via -ldflags by GoReleaser.
// Defaults are used for `go build` / `go run` from source.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	for _, arg := range os.Args[1:] {
		switch arg {
		case "-v", "--version", "version":
			fmt.Printf("clasp %s (commit %s, built %s)\n", version, commit, date)
			return
		case "-h", "--help", "help":
			fmt.Println("clasp — a read-only terminal UI companion for Claude Code.")
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Println("  clasp            Launch the TUI")
			fmt.Println("  clasp --version  Print version and exit")
			fmt.Println("  clasp --help     Show this help")
			fmt.Println()
			fmt.Println("Once running, press ? for the in-app keybinding overlay.")
			return
		}
	}

	m, err := ui.New()
	if err != nil {
		// non-fatal: model loads with partial state and shows a warning
		fmt.Fprintf(os.Stderr, "clasp: warning: %v\n", err)
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	go watcher.Watch(p)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "clasp: %v\n", err)
		os.Exit(1)
	}
}
