package watcher

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// fakeWatcher records Add/Remove calls. failAdds makes the first n Adds of a
// path fail, to simulate a file that has not been recreated yet.
type fakeWatcher struct {
	mu       sync.Mutex
	added    []string
	removed  []string
	failAdds map[string]int
}

func (f *fakeWatcher) Add(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAdds[name] > 0 {
		f.failAdds[name]--
		return errors.New("no such file")
	}
	f.added = append(f.added, name)
	return nil
}

func (f *fakeWatcher) Remove(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, name)
	return nil
}

func newTestLoop(w watcher, send func()) *loop {
	return &loop{
		w:          w,
		static:     map[string]bool{},
		reAdd:      map[string]bool{},
		debounce:   20 * time.Millisecond,
		maxWait:    100 * time.Millisecond,
		retryDelay: time.Millisecond,
		retries:    5,
		send:       send,
	}
}

func TestLoop_MaxWaitBoundsContinuousBurst(t *testing.T) {
	sends := make(chan struct{}, 10)
	l := newTestLoop(&fakeWatcher{}, func() { sends <- struct{}{} })

	events := make(chan fsnotify.Event)
	done := make(chan struct{})
	go func() { l.run(events, nil, nil); close(done) }()

	// events every 5ms for 300ms never leave a 20ms gap, so only maxWait
	// (100ms) can trigger a send while the burst continues
	start := time.Now()
	for time.Since(start) < 300*time.Millisecond {
		events <- fsnotify.Event{Name: "x", Op: fsnotify.Write}
		time.Sleep(5 * time.Millisecond)
	}
	close(events)
	<-done
	if n := len(sends); n < 2 {
		t.Fatalf("sends during a 300ms burst = %d, want ≥ 2 (maxWait 100ms)", n)
	}
}

func TestLoop_DebouncesEvents(t *testing.T) {
	sends := make(chan struct{}, 10)
	l := newTestLoop(&fakeWatcher{}, func() { sends <- struct{}{} })

	events := make(chan fsnotify.Event)
	done := make(chan struct{})
	go func() { l.run(events, nil, nil); close(done) }()

	for i := 0; i < 5; i++ {
		events <- fsnotify.Event{Name: "x", Op: fsnotify.Write}
		time.Sleep(2 * time.Millisecond)
	}

	select {
	case <-sends:
	case <-time.After(2 * time.Second):
		t.Fatal("no send after events")
	}
	select {
	case <-sends:
		t.Error("got a second send for one burst of events")
	case <-time.After(100 * time.Millisecond):
	}

	// A later event starts a new debounce window.
	events <- fsnotify.Event{Name: "x", Op: fsnotify.Write}
	select {
	case <-sends:
	case <-time.After(2 * time.Second):
		t.Fatal("no send for second burst")
	}

	close(events)
	<-done
}

func TestLoop_ReAddsReplacedFile(t *testing.T) {
	path := "/home/u/.claude.json"
	w := &fakeWatcher{failAdds: map[string]int{path: 2}}
	l := newTestLoop(w, func() {})
	l.reAdd[path] = true

	events := make(chan fsnotify.Event, 3)
	events <- fsnotify.Event{Name: path, Op: fsnotify.Rename}
	events <- fsnotify.Event{Name: "/other", Op: fsnotify.Remove} // not in reAdd
	events <- fsnotify.Event{Name: path, Op: fsnotify.Write}      // not a replace
	close(events)
	l.run(events, nil, nil)

	if len(w.added) != 1 || w.added[0] != path {
		t.Errorf("added = %v, want one re-add of %s after retries", w.added, path)
	}
}

func TestLoop_ProjectWatch(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	noClaude := t.TempDir()
	for _, p := range []string{a, b} {
		if err := os.Mkdir(filepath.Join(p, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	aDir, bDir := filepath.Join(a, ".claude"), filepath.Join(b, ".claude")

	w := &fakeWatcher{}
	l := newTestLoop(w, func() {})
	projects := make(chan string, 8)
	for _, p := range []string{a, a, b, noClaude, ""} {
		projects <- p
	}
	close(projects)
	events := make(chan fsnotify.Event)
	done := make(chan struct{})
	go func() { l.run(events, nil, projects); close(done) }()
	// projects is drained before the loop sees events closing
	time.Sleep(50 * time.Millisecond)
	close(events)
	<-done

	wantAdded := []string{aDir, bDir} // duplicate a ignored, noClaude skipped
	if len(w.added) != len(wantAdded) || w.added[0] != aDir || w.added[1] != bDir {
		t.Errorf("added = %v, want %v", w.added, wantAdded)
	}
	// a removed when switching to b, b removed when switching to noClaude.
	if len(w.removed) != 2 || w.removed[0] != aDir || w.removed[1] != bDir {
		t.Errorf("removed = %v, want [%s %s]", w.removed, aDir, bDir)
	}
}

func TestLoop_ProjectWatchKeepsStaticDir(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	w := &fakeWatcher{}
	l := newTestLoop(w, func() {})
	l.addStatic(dir)

	l.setProject(home)
	l.setProject("")

	if len(w.added) != 1 {
		t.Errorf("added = %v, want only the static watch", w.added)
	}
	if len(w.removed) != 0 {
		t.Errorf("removed = %v, want none: project dir is a static watch", w.removed)
	}
}
