package cruft

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// gitTimeout bounds each git call.
var gitTimeout = 5 * time.Second

// runGit runs git -C dir args... and returns stdout. Tests replace it. Failure
// (including timeout) is returned as is; callers treat it as "unknown".
var runGit = func(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// Keep status read-only (no index refresh) and never prompt.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	return string(out), err
}

// ScanRepo lists the linked worktrees of the repo containing repo (any path
// inside it), with the same fields and guards as Scan. The write path calls it
// for a fresh re-check right before removing something.
func ScanRepo(ctx context.Context, env Env, repo string) ([]Worktree, error) {
	if _, err := commonDir(ctx, repo); err != nil {
		return nil, err
	}
	main, entries, err := listWorktrees(ctx, repo)
	if err != nil {
		return nil, err
	}
	return inspectWorktrees(ctx, main, entries, LiveSessions(env)), nil
}

// commonDir resolves the repo's shared .git dir, which identifies the repo
// whichever worktree path was used.
func commonDir(ctx context.Context, path string) (string, error) {
	out, err := runGit(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	d := strings.TrimSpace(out)
	if d == "" {
		return "", errors.New("empty git common dir")
	}
	return filepath.Clean(d), nil
}

// wtEntry is one record of `git worktree list --porcelain`.
type wtEntry struct {
	path, head, branch string
	detached, prunable bool
	prunableWhy        string
}

// listWorktrees returns the main worktree's path and the linked worktrees.
func listWorktrees(ctx context.Context, dir string) (string, []wtEntry, error) {
	out, err := runGit(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", nil, err
	}
	var all []wtEntry
	var cur *wtEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			cur = nil
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		if key == "worktree" {
			all = append(all, wtEntry{path: val})
			cur = &all[len(all)-1]
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "HEAD":
			cur.head = val
		case "branch":
			cur.branch = strings.TrimPrefix(val, "refs/heads/")
		case "detached":
			cur.detached = true
		case "prunable":
			cur.prunable = true
			cur.prunableWhy = val
		}
	}
	if len(all) == 0 {
		return "", nil, errors.New("git worktree list returned nothing")
	}
	return all[0].path, all[1:], nil
}

// scanWorktrees finds the repos behind the candidate paths and inspects every
// linked worktree in them.
func scanWorktrees(ctx context.Context, cands []string, sessions []LiveSession, errf func(string, error)) []Worktree {
	sort.Strings(cands)
	seen := map[string]bool{}
	var out []Worktree
	for _, c := range cands {
		if ctx.Err() != nil {
			break
		}
		if fi, err := os.Stat(c); err != nil || !fi.IsDir() {
			continue
		}
		cd, err := commonDir(ctx, c)
		if err != nil || seen[cd] {
			continue // not a git repo (or already done): skip silently
		}
		seen[cd] = true
		main, entries, err := listWorktrees(ctx, c)
		if err != nil {
			errf("listing worktrees of "+c, err)
			continue
		}
		out = append(out, inspectWorktrees(ctx, main, entries, sessions)...)
	}
	sortWorktrees(out)
	return out
}

func sortWorktrees(w []Worktree) {
	sort.Slice(w, func(i, j int) bool {
		if w[i].Repo != w[j].Repo {
			return w[i].Repo < w[j].Repo
		}
		return w[i].Path < w[j].Path
	})
}

func inspectWorktrees(ctx context.Context, main string, entries []wtEntry, sessions []LiveSession) []Worktree {
	var def string
	var defDone bool
	var out []Worktree
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		if !defDone {
			def = defaultBranch(ctx, main)
			defDone = true
		}
		out = append(out, inspect(ctx, main, e, def, sessions))
	}
	sortWorktrees(out)
	return out
}

// defaultBranch finds the local branch worktrees are compared against:
// origin/HEAD's target, else main, else master. "" when none exists locally.
func defaultBranch(ctx context.Context, repo string) string {
	var cands []string
	if out, err := runGit(ctx, repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if b := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); b != "" {
			cands = append(cands, b)
		}
	}
	cands = append(cands, "main", "master")
	for _, b := range cands {
		if _, err := runGit(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return b
		}
	}
	return ""
}

func inspect(ctx context.Context, repo string, e wtEntry, def string, sessions []LiveSession) Worktree {
	w := Worktree{
		Path: e.path, Repo: repo, Branch: e.branch, HEAD: e.head,
		Detached: e.detached, DefaultBranch: def,
	}
	fi, err := os.Lstat(e.path)
	missing := e.prunable || errors.Is(err, os.ErrNotExist)
	if missing {
		w.Why = "missing on disk (git worktree prune)"
		return w
	}
	w.LivePIDs = pidsWithin(sessions, e.path)

	// Merge state.
	if def != "" && e.head != "" {
		_, merr := runGit(ctx, repo, "merge-base", "--is-ancestor", e.head, "refs/heads/"+def)
		var ee *exec.ExitError
		switch {
		case merr == nil:
			w.Merged, w.MergedKnown = true, true
		case errors.As(merr, &ee) && ee.ExitCode() == 1:
			w.MergedKnown = true
		}
	}
	// Dirty state.
	if out, derr := runGit(ctx, e.path, "status", "--porcelain"); derr == nil {
		w.DirtyKnown = true
		w.Dirty = strings.TrimSpace(out) != ""
	}
	if out, lerr := runGit(ctx, e.path, "log", "-1", "--format=%ct"); lerr == nil {
		if sec, perr := strconv.ParseInt(strings.TrimSpace(out), 10, 64); perr == nil {
			w.LastCommit = time.Unix(sec, 0)
		}
	}
	w.Size, _, _ = dirSize(ctx, e.path)

	switch {
	case err != nil:
		w.Why = "cannot read worktree: " + err.Error()
	case fi.Mode()&os.ModeSymlink != 0:
		w.Why = "worktree path is a symlink"
	case def == "":
		w.Why = "no default branch found"
	case !w.MergedKnown:
		w.Why = "could not check merge state"
	case !w.Merged && w.Detached:
		w.Why = "detached HEAD not in " + def
	case !w.Merged:
		w.Why = "unmerged branch"
	case !w.DirtyKnown:
		w.Why = "could not read git status"
	case w.Dirty:
		w.Why = "uncommitted changes"
	case len(w.LivePIDs) > 0:
		w.Why = "in use by " + pidList(w.LivePIDs)
	default:
		w.Removable = true
	}
	return w
}

func pidList(pids []int) string {
	if len(pids) == 1 {
		return fmt.Sprintf("PID %d", pids[0])
	}
	s := make([]string, len(pids))
	for i, p := range pids {
		s[i] = strconv.Itoa(p)
	}
	return "PIDs " + strings.Join(s, ", ")
}
