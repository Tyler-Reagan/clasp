package watcher

import (
	"log"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
	"github.com/tylerreagan/clasp/internal/state"
	"github.com/tylerreagan/clasp/internal/ui"
)

// Watch monitors ~/.claude subdirectories and sends RefreshMsg to p on any change.
func Watch(p *tea.Program) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("clasp watcher: %v", err)
		return
	}
	defer w.Close()

	dir := state.ClaudeDir()
	for _, sub := range []string{"sessions", "skills", "plugins"} {
		_ = w.Add(filepath.Join(dir, sub))
	}

	// watch existing memory directories (new projects require restart)
	memDirs, _ := filepath.Glob(filepath.Join(dir, "projects", "*", "memory"))
	for _, d := range memDirs {
		_ = w.Add(d)
	}
	// root settings file
	_ = w.Add(dir)

	for {
		select {
		case _, ok := <-w.Events:
			if !ok {
				return
			}
			p.Send(ui.RefreshMsg{})
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			log.Printf("clasp watcher: %v", err)
		}
	}
}
