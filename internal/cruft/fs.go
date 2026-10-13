package cruft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tylerreagan/clasp/internal/inventory"
)

// processLive decides whether a recorded session is still running. Tests
// replace it.
var processLive = inventory.ProcessLive

// EncodeProjectPath is Claude Code's encoding of a project path into a
// ~/.claude/projects dir name: every non-alphanumeric rune becomes '-'.
func EncodeProjectPath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// LiveSessions returns the running sessions recorded in ClaudeDir/sessions.
// A session is live when its process exists and started at the recorded
// procStart; an unknown start time counts as live. Read problems are ignored
// here; guards that act on the result use CheckedLiveSessions.
func LiveSessions(env Env) []LiveSession {
	out, _ := CheckedLiveSessions(env)
	return out
}

// CheckedLiveSessions is LiveSessions for write guards: it also reports an
// error when it cannot be sure the list is complete, so a guard can refuse
// instead of acting. That is the case when the sessions dir exists but cannot
// be listed, or when a <pid>.json file of a live PID cannot be read or parsed
// (for example while Claude Code is rewriting it). A missing dir means none.
func CheckedLiveSessions(env Env) ([]LiveSession, error) {
	dir := filepath.Join(env.ClaudeDir, "sessions")
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("can't read running sessions")
	}
	seen := map[int]bool{}
	var out []LiveSession
	var unsure error
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		namePID, _ := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		var s struct {
			PID       int    `json:"pid"`
			CWD       string `json:"cwd"`
			ProcStart string `json:"procStart"`
		}
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if !e.Type().IsRegular() || rerr != nil || json.Unmarshal(data, &s) != nil || s.PID <= 0 {
			if namePID > 0 && processLive(namePID, "") {
				unsure = fmt.Errorf("can't read the session file of running PID %d", namePID)
			}
			continue
		}
		if seen[s.PID] || !processLive(s.PID, s.ProcStart) {
			continue
		}
		seen[s.PID] = true
		out = append(out, LiveSession{PID: s.PID, CWD: s.CWD})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, unsure
}

// dirSize sums the sizes of the files under root and counts them, never
// following symlinks (a symlink counts as the link itself). It stops early
// with ctx's error when cancelled.
func dirSize(ctx context.Context, root string) (size int64, files int, err error) {
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if werr != nil {
			return nil // unreadable entry: skip, keep summing
		}
		if d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			size += info.Size()
			files++
		}
		return nil
	})
	return size, files, err
}

// within reports whether cwd is path or inside it, comparing whole path
// elements. Symlink-resolved forms are tried too, so /var and /private/var
// spellings of one dir match.
func within(cwd, path string) bool {
	if cwd == "" || path == "" {
		return false
	}
	if inside(filepath.Clean(cwd), filepath.Clean(path)) {
		return true
	}
	return inside(resolve(cwd), resolve(path))
}

func inside(cwd, path string) bool {
	return cwd == path || strings.HasPrefix(cwd, strings.TrimSuffix(path, string(filepath.Separator))+string(filepath.Separator))
}

func resolve(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// pidsWithin lists live sessions whose cwd is inside path.
func pidsWithin(sessions []LiveSession, path string) []int {
	var pids []int
	for _, s := range sessions {
		if within(s.CWD, path) {
			pids = append(pids, s.PID)
		}
	}
	return pids
}
