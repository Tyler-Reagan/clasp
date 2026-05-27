package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tylerreagan/clasp/internal/ui"
	"github.com/tylerreagan/clasp/internal/watcher"
)

func main() {
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
