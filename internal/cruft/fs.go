package cruft

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
// procStart; an unknown start time counts as live.
func LiveSessions(env Env) []LiveSession {
	dir := filepath.Join(env.ClaudeDir, "sessions")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	seen := map[int]bool{}
	var out []LiveSession
	for _, e := range ents {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s struct {
			PID       int    `json:"pid"`
			CWD       string `json:"cwd"`
			ProcStart string `json:"procStart"`
		}
		if json.Unmarshal(data, &s) != nil || s.PID <= 0 || seen[s.PID] || !processLive(s.PID, s.ProcStart) {
			continue
		}
		seen[s.PID] = true
		out = append(out, LiveSession{PID: s.PID, CWD: s.CWD})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
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
