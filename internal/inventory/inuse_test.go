package inventory

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// fakeProcs replaces the process probes for the test: alive maps PID to start
// time (zero start = alive but unreadable).
func fakeProcs(t *testing.T, alive map[int]time.Time) {
	t.Helper()
	oldAlive, oldStart := procAlive, procStartTime
	t.Cleanup(func() { procAlive, procStartTime = oldAlive, oldStart })
	procAlive = func(pid int) bool { _, ok := alive[pid]; return ok }
	procStartTime = func(pid int) (time.Time, bool) {
		st := alive[pid]
		return st, !st.IsZero()
	}
}

func fakeDesktop(t *testing.T, running, known bool) {
	t.Helper()
	old := desktopRunning
	t.Cleanup(func() { desktopRunning = old })
	desktopRunning = func() (bool, bool) { return running, known }
}

func writeInUse(f *fixture, dir string, pid int, procStart string) {
	f.write(filepath.Join(dir, ".in_use", fmt.Sprint(pid)), fmt.Sprintf(`{"pid":%d,"procStart":%q}`, pid, procStart))
}

func TestParseProcStart(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"Mon Oct 12 20:11:22 2026", time.Date(2026, 10, 12, 20, 11, 22, 0, time.UTC), true},
		{"Fri Oct  2 09:05:01 2026", time.Date(2026, 10, 2, 9, 5, 1, 0, time.UTC), true},
		{"Fri Oct 2 09:05:01 2026", time.Date(2026, 10, 2, 9, 5, 1, 0, time.UTC), true},
		{"", time.Time{}, false},
		{"yesterday", time.Time{}, false},
	}
	for _, tt := range tests {
		got, ok := parseProcStart(tt.in)
		if ok != tt.ok || (ok && !got.Equal(tt.want)) {
			t.Errorf("parseProcStart(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestLiveUsers(t *testing.T) {
	start := time.Date(2026, 10, 12, 20, 11, 22, 0, time.UTC)
	const startStr = "Mon Oct 12 20:11:22 2026"
	tests := []struct {
		name      string
		alive     map[int]time.Time
		pid       int
		procStart string
		want      []int
	}{
		{"alive, start matches", map[int]time.Time{10: start}, 10, startStr, []int{10}},
		{"alive, start within a second", map[int]time.Time{10: start.Add(900 * time.Millisecond)}, 10, startStr, []int{10}},
		{"alive, start differs (pid reused)", map[int]time.Time{10: start.Add(time.Hour)}, 10, startStr, nil},
		{"alive, start unreadable", map[int]time.Time{10: {}}, 10, startStr, []int{10}},
		{"alive, procStart empty", map[int]time.Time{10: start}, 10, "", []int{10}},
		{"alive, procStart unparsable", map[int]time.Time{10: start}, 10, "garbage", []int{10}},
		{"dead", map[int]time.Time{}, 10, startStr, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeProcs(t, tt.alive)
			f := newFixture(t)
			dir := filepath.Join(f.root, "copy")
			writeInUse(f, dir, tt.pid, tt.procStart)
			if got := LiveUsers(dir); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LiveUsers = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLiveUsersMissingAndMalformed(t *testing.T) {
	fakeProcs(t, map[int]time.Time{7: {}})
	f := newFixture(t)
	dir := filepath.Join(f.root, "copy")
	if got := LiveUsers(dir); got != nil {
		t.Errorf("no .in_use: got %v", got)
	}
	f.write(filepath.Join(dir, ".in_use", "bad"), "{not json")
	f.write(filepath.Join(dir, ".in_use", "zero"), `{"pid":0}`)
	if err := os.MkdirAll(filepath.Join(dir, ".in_use", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeInUse(f, dir, 7, "")
	writeInUse(f, filepath.Join(dir), 7, "") // same pid twice is listed once
	if got := LiveUsers(dir); !reflect.DeepEqual(got, []int{7}) {
		t.Errorf("got %v, want [7]", got)
	}
}

func TestProcessListHasDesktop(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want bool
	}{
		{"desktop", "/sbin/launchd\n/Applications/Claude.app/Contents/MacOS/Claude\n", true},
		{"cli inside the app", "/Applications/Claude.app/Contents/MacOS/claude\n/Users/x/Library/Application Support/Claude/claude-code/2.1.0/claude.app/Contents/MacOS/claude\n", false},
		{"helper", "/Applications/Claude.app/Contents/Frameworks/Claude Helper.app/Contents/MacOS/Claude Helper\n", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		if got := processListHasDesktop(tt.out); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
