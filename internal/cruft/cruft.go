// Package cruft finds what is safe to clean up under ~/.claude and in the
// repos Claude Code has worked in: merged worktrees, transcript dirs of
// deleted projects, memory problems, and the sizes of what Claude Code sweeps
// itself. It is read-only: nothing here writes, and nothing touches the
// network. Scan is slow (git calls and size walks), so callers run it in the
// background and never from inventory.Load.
package cruft

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tylerreagan/clasp/internal/inventory"
)

// Env is where the scan looks; Desktop's support dir is never read.
type Env = inventory.Env

// defaultCutoffDays is Claude Code's cleanupPeriodDays default.
const defaultCutoffDays = 30

// Report is one scan's findings.
type Report struct {
	ScannedAt   time.Time
	CutoffDays  int           // effective cleanupPeriodDays (default 30)
	Worktrees   []Worktree    // sorted by repo then path
	Projects    []ProjectDir  // sorted: dead first, then size desc
	DeadEntries []string      // ~/.claude.json project keys whose path is gone, sorted
	Memories    []MemoryIssue // sorted by project, file
	Swept       []SweptDir    // dirs Claude Code sweeps itself (only those that exist)
	PluginCache CacheSize
	Sessions    []LiveSession // live sessions used for guards
	Errors      []string      // non-fatal problems, human readable
}

// Worktree is a linked git worktree of a repo Claude Code has worked in.
type Worktree struct {
	Path, Repo, Branch, HEAD                         string
	Detached, Merged, MergedKnown, Dirty, DirtyKnown bool
	DefaultBranch                                    string
	LastCommit                                       time.Time
	Size                                             int64
	LivePIDs                                         []int // live sessions whose cwd is inside Path
	Removable                                        bool
	Why                                              string // reason when not removable
}

// ProjectDir is one ~/.claude/projects/<dir> transcript folder.
type ProjectDir struct {
	Dir, Name string   // full path under ClaudeDir/projects, base name
	CWDs      []string // distinct cwd values recorded in its transcripts
	// Dead is DeadKnown and every CWD missing. DeadKnown is false when no cwd
	// was recorded: such a dir is unknown, never dead.
	Dead, DeadKnown                                   bool
	Size                                              int64
	Newest                                            time.Time
	Transcripts, Desktop, CLI, Other, OlderThanCutoff int
	HasMemories                                       bool
	LivePIDs                                          []int
	Trashable                                         bool
	Why                                               string
}

// MemoryIssueKind says what is wrong with a memory file or index.
type MemoryIssueKind int

const (
	DeadLink         MemoryIssueKind = iota // [[x]] matches no name: or file stem
	NotInIndex                              // file not mentioned in MEMORY.md
	IndexMissingFile                        // MEMORY.md links a file that is not there
	ProjectGone                             // the memory's project folder is gone
)

func (k MemoryIssueKind) String() string {
	switch k {
	case DeadLink:
		return "dead link"
	case NotInIndex:
		return "not in index"
	case IndexMissingFile:
		return "index points at missing file"
	case ProjectGone:
		return "project gone"
	}
	return "unknown"
}

// MemoryIssue is one problem in a project's memory dir. Project is the encoded
// dir name; File is the memory file ("MEMORY.md" for IndexMissingFile).
type MemoryIssue struct {
	Project, File string
	Kind          MemoryIssueKind
	Detail        string
}

// SweptDir is a ~/.claude dir Claude Code deletes old entries from itself.
type SweptDir struct {
	Name, Path string
	Size       int64
	Files      int
}

// CacheSize is the plugin cache: all of it, and the part that is stale.
type CacheSize struct {
	Total, Stale int64
	StaleCopies  int
}

// LiveSession is a running Claude Code session.
type LiveSession struct {
	PID int
	CWD string
}

// Scan runs every scanner. It stops early when ctx is cancelled, returning
// what it has and noting the cancellation in Errors.
func Scan(ctx context.Context, env Env) *Report {
	r := &Report{ScannedAt: time.Now(), CutoffDays: cutoffDays(env)}
	errf := func(msg string, err error) {
		if err != nil {
			msg += ": " + err.Error()
		}
		r.Errors = append(r.Errors, msg)
	}
	r.Sessions = LiveSessions(env)

	r.Projects = scanProjects(ctx, env, r.Sessions, r.CutoffDays, errf)
	var existingCWDs []string
	for _, p := range r.Projects {
		for _, c := range p.CWDs {
			if !pathGone(c) {
				existingCWDs = append(existingCWDs, c)
			}
		}
	}
	keys, err := claudeJSONProjects(env)
	if err != nil {
		errf("reading ~/.claude.json projects", err)
	}
	var cands []string
	for _, k := range keys {
		if pathGone(k) {
			r.DeadEntries = append(r.DeadEntries, k)
		} else {
			cands = append(cands, k)
		}
	}
	sort.Strings(r.DeadEntries)
	r.Memories = scanMemories(ctx, env, r.Projects, errf)

	if ctx.Err() == nil {
		r.Worktrees = scanWorktrees(ctx, append(cands, existingCWDs...), r.Sessions, errf)
	}
	for _, name := range sweptNames {
		if ctx.Err() != nil {
			break
		}
		p := filepath.Join(env.ClaudeDir, name)
		if fi, err := os.Lstat(p); err != nil || !fi.IsDir() {
			continue
		}
		size, files, _ := dirSize(ctx, p)
		r.Swept = append(r.Swept, SweptDir{Name: name, Path: p, Size: size, Files: files})
	}
	if ctx.Err() == nil {
		r.PluginCache = pluginCache(ctx, env)
	}
	if err := ctx.Err(); err != nil {
		errf("scan cancelled", err)
	}
	return r
}

var sweptNames = []string{"file-history", "plans", "shell-snapshots", "session-env", "tasks", "uploads"}

// pluginCache sizes the plugin cache and its stale copies.
func pluginCache(ctx context.Context, env Env) CacheSize {
	var c CacheSize
	c.Total, _, _ = dirSize(ctx, filepath.Join(env.ClaudeDir, "plugins", "cache"))
	seen := map[string]bool{}
	cliOnly := env
	cliOnly.DesktopDir = "" // the CLI plugin cache is all that is sized here
	inv := inventory.Load(cliOnly)
	var items []inventory.Item
	items = append(items, inv.Skills...)
	items = append(items, inv.Plugins...)
	items = append(items, inv.MCP...)
	for _, it := range items {
		for _, cp := range it.Copies {
			if cp.Status != inventory.CopyStale || seen[cp.Path] {
				continue
			}
			seen[cp.Path] = true
			c.Stale += cp.Size
			c.StaleCopies++
		}
	}
	return c
}

// cutoffDays reads cleanupPeriodDays from settings.json, ignoring errors.
func cutoffDays(env Env) int {
	data, err := os.ReadFile(filepath.Join(env.ClaudeDir, "settings.json"))
	if err != nil {
		return defaultCutoffDays
	}
	var s struct {
		Days *int `json:"cleanupPeriodDays"`
	}
	if json.Unmarshal(data, &s) != nil || s.Days == nil || *s.Days <= 0 {
		return defaultCutoffDays
	}
	return *s.Days
}

// claudeJSONProjects returns the keys of the projects map in ~/.claude.json,
// sorted. The file also holds oauthAccount and secrets, so nothing else is
// decoded. A missing file is not an error.
func claudeJSONProjects(env Env) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(env.Home, ".claude.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Projects map[string]json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(f.Projects))
	for k := range f.Projects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// pathGone reports whether path definitely does not exist. Any other error
// (permissions, I/O) counts as "still there": uncertainty never marks things dead.
func pathGone(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}
