package watcher

import (
	"log"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
	"github.com/tylerreagan/clasp/internal/inventory"
	"github.com/tylerreagan/clasp/internal/settings"
	"github.com/tylerreagan/clasp/internal/state"
	"github.com/tylerreagan/clasp/internal/ui"
)

const (
	debounce   = 200 * time.Millisecond
	maxWait    = time.Second // longest a refresh waits during a continuous burst
	retryDelay = 25 * time.Millisecond
	retries    = 10
)

// Start monitors Claude Code's config files and sends ui.RefreshMsg to p after
// any change. It returns immediately; the event loop runs in its own goroutine.
// Values received on projects (may be nil) select the project whose .claude
// directory is watched; "" means no project.
func Start(p *tea.Program, projects <-chan string) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("clasp watcher: %v", err)
		return
	}

	dir := state.ClaudeDir()
	env := settings.DefaultEnv()
	l := &loop{
		w:          w,
		static:     map[string]bool{},
		reAdd:      map[string]bool{},
		debounce:   debounce,
		maxWait:    maxWait,
		retryDelay: retryDelay,
		retries:    retries,
		send:       func() { p.Send(ui.RefreshMsg{}) },
	}

	for _, sub := range []string{"sessions", "skills", "plugins"} {
		l.addStatic(filepath.Join(dir, sub))
	}
	// watch existing memory directories (new projects require restart)
	memDirs, _ := filepath.Glob(filepath.Join(dir, "projects", "*", "memory"))
	for _, d := range memDirs {
		l.addStatic(d)
	}
	// root settings file
	l.addStatic(dir)

	// ~/.claude.json is replaced atomically and rewritten constantly. Watch the
	// file itself (not $HOME) and re-add it whenever the old inode goes away.
	claudeJSON := filepath.Join(env.Home, ".claude.json")
	l.addStatic(claudeJSON)
	l.reAdd[claudeJSON] = true

	if env.ManagedDir != "" {
		l.addStatic(env.ManagedDir)
		l.addStatic(filepath.Join(env.ManagedDir, "managed-settings.d"))
	}

	// Inventory sources. Accounts and plugin dirs that appear later need a
	// restart, like memory dirs. Never watch DesktopDir itself: fsnotify is
	// not recursive and that dir changes constantly.
	inv := inventory.DefaultEnv()
	addDirs := func(pattern string) {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && fi.IsDir() {
				l.addStatic(m)
			}
		}
	}
	l.addStatic(filepath.Join(dir, "plugins", "synced"))
	addDirs(filepath.Join(dir, "plugins", "synced", "*_*"))
	if inv.DesktopDir != "" {
		desktopCfg := filepath.Join(inv.DesktopDir, "claude_desktop_config.json")
		l.addStatic(desktopCfg)
		l.reAdd[desktopCfg] = true
		skillsPlugin := filepath.Join(inv.DesktopDir, "local-agent-mode-sessions", "skills-plugin", "*", "*")
		addDirs(skillsPlugin)
		skillDirs, _ := filepath.Glob(skillsPlugin)
		for _, d := range skillDirs {
			l.addStatic(filepath.Join(d, "skills"))
		}
		addDirs(filepath.Join(inv.DesktopDir, "local-agent-mode-sessions", "*", "*", "rpm"))
	}

	go func() {
		defer w.Close()
		l.run(w.Events, w.Errors, projects)
	}()
}

// watcher is the subset of *fsnotify.Watcher the loop needs.
type watcher interface {
	Add(name string) error
	Remove(name string) error
}

// loop owns the watch set and coalesces events into single refreshes.
type loop struct {
	w          watcher
	static     map[string]bool // fixed watches; the project watch never removes these
	reAdd      map[string]bool // files re-added after Remove/Rename
	debounce   time.Duration
	maxWait    time.Duration
	retryDelay time.Duration
	retries    int
	send       func()

	project string // last project received
	watched string // project .claude dir currently watched, "" if none
}

// addStatic adds a fixed watch. Paths that don't exist are skipped silently.
func (l *loop) addStatic(path string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	if l.w.Add(path) == nil {
		l.static[filepath.Clean(path)] = true
	}
}

func (l *loop) run(events <-chan fsnotify.Event, errs <-chan error, projects <-chan string) {
	var timer *time.Timer
	var fire <-chan time.Time
	var first time.Time // start of the current burst
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Has(fsnotify.Remove|fsnotify.Rename) && l.reAdd[filepath.Clean(ev.Name)] {
				l.readd(ev.Name)
			}
			switch {
			case timer == nil:
				timer = time.NewTimer(l.debounce)
				first = time.Now()
			case fire == nil:
				// previous burst already fired and its tick was consumed
				timer.Reset(l.debounce)
				first = time.Now()
			case time.Since(first) < l.maxWait:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(l.debounce)
			}
			// past maxWait the pending timer is left to fire, so a file that
			// changes constantly (~/.claude.json) cannot starve refreshes
			fire = timer.C
		case <-fire:
			fire = nil
			l.send()
		case err, ok := <-errs:
			if !ok {
				return
			}
			log.Printf("clasp watcher: %v", err)
		case proj, ok := <-projects:
			if !ok {
				projects = nil
				continue
			}
			l.setProject(proj)
		}
	}
}

// readd re-watches a file that was replaced, retrying briefly because the
// writer may not have created the new file yet.
func (l *loop) readd(path string) {
	var err error
	for i := 0; i < l.retries; i++ {
		if err = l.w.Add(path); err == nil {
			return
		}
		time.Sleep(l.retryDelay)
	}
	log.Printf("clasp watcher: re-add %s: %v", path, err)
}

// setProject moves the project watch to <proj>/.claude, if that exists.
func (l *loop) setProject(proj string) {
	if proj == l.project {
		return
	}
	l.project = proj

	if l.watched != "" {
		_ = l.w.Remove(l.watched)
		l.watched = ""
	}
	if proj == "" {
		return
	}
	d := filepath.Clean(filepath.Join(proj, ".claude"))
	if l.static[d] {
		return // already watched for good (project is $HOME)
	}
	if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
		return
	}
	if err := l.w.Add(d); err != nil {
		log.Printf("clasp watcher: %v", err)
		return
	}
	l.watched = d
}
