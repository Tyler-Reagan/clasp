package inventory

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"time"
)

// Process probes. Tests replace them; the real ones live in proc_*.go.
var (
	// procAlive reports whether a process with this PID exists.
	procAlive = osProcAlive
	// procStartTime reports when the process started; ok is false when that
	// cannot be read.
	procStartTime = osProcStartTime
)

// startTolerance absorbs rounding between the start time Claude Code records
// and the one the OS reports. A false "not in use" is the unsafe error, so it
// is wide enough for a second of skew.
const startTolerance = time.Second

// parseProcStart parses the UTC ctime string Claude Code writes as procStart,
// e.g. "Mon Oct 12 20:11:22 2026" or, for a one-digit day, "Fri Oct  2 ...".
func parseProcStart(s string) (time.Time, bool) {
	t, err := time.Parse(time.ANSIC, s)
	return t, err == nil
}

// ProcessLive reports whether pid is a live process that started at procStart
// (the UTC ctime string Claude Code records). If procStart is missing or
// unparsable, or the OS will not say when the process started, an existing
// process still counts: not knowing never frees anything.
func ProcessLive(pid int, procStart string) bool {
	if pid <= 0 || !procAlive(pid) {
		return false
	}
	recorded, parsed := parseProcStart(procStart)
	actual, known := procStartTime(pid)
	if parsed && known {
		if d := actual.Sub(recorded); d < -startTolerance || d > startTolerance {
			return false
		}
	}
	return true
}

// LiveUsers returns the PIDs of live sessions using the plugin copy at dir,
// from dir/.in_use/<pid> files holding {"pid","procStart"}. A PID is live when
// the process exists and started at procStart. If procStart is missing or
// unparsable, or the OS will not say when the process started, an existing
// process still counts: not knowing never frees a copy. A missing .in_use dir
// means no users.
func LiveUsers(dir string) []int {
	ents, _ := readDir(filepath.Join(dir, ".in_use"))
	var pids []int
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		data, err := readFile(filepath.Join(dir, ".in_use", e.Name()))
		if err != nil {
			continue
		}
		var u struct {
			PID       int    `json:"pid"`
			ProcStart string `json:"procStart"`
		}
		if json.Unmarshal(data, &u) != nil || !ProcessLive(u.PID, u.ProcStart) {
			continue
		}
		pids = append(pids, u.PID)
	}
	sort.Ints(pids)
	out := pids[:0]
	for i, p := range pids {
		if i == 0 || p != pids[i-1] {
			out = append(out, p)
		}
	}
	return out
}
