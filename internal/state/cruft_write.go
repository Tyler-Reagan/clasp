package state

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tylerreagan/clasp/internal/cruft"
	"github.com/tylerreagan/clasp/internal/inventory"
)

// Seams for RemoveWorktree and TrashProjectDir. Tests replace them; the
// defaults read the real machine, run the real git and move into the real
// ~/.Trash. Moves reuse trashMove from trash.go.
var (
	// cruftEnv is where sessions and ~/.claude/projects live.
	cruftEnv = inventory.DefaultEnv
	// scanRepo re-lists a repo's linked worktrees with fresh guards.
	scanRepo = cruft.ScanRepo
	// scanProject re-inspects one transcript dir from scratch.
	scanProject = cruft.ScanProject
	// liveSessions lists running sessions, probed again right before acting.
	// It fails closed: an error means the list may be incomplete.
	liveSessions = cruft.CheckedLiveSessions
	// gitRemove runs `git worktree remove` without --force.
	gitRemove = runGitWorktreeRemove
)

// worktreeScanTimeout bounds the fresh re-scan of a repo's worktrees.
const worktreeScanTimeout = 30 * time.Second

// RemoveWorktree runs git worktree remove (never --force) on a merged, clean
// worktree no live session uses, after a fresh re-check. It never deletes
// branches and never touches the main worktree. Errors are short sentences
// meant to be shown to the user as they are.
func RemoveWorktree(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("refused: not an absolute path")
	}
	path = filepath.Clean(path)
	if why := realDir(path); why != "" {
		return errors.New("refused: " + why)
	}
	// A linked worktree has a .git file; only the main worktree has a .git
	// dir. Refuse it before git is even asked.
	if fi, err := os.Lstat(filepath.Join(path, ".git")); err == nil && fi.IsDir() {
		return errors.New("refused: it is the main worktree")
	}

	env := cruftEnv()
	ctx, cancel := context.WithTimeout(context.Background(), worktreeScanTimeout)
	defer cancel()
	wts, err := scanRepo(ctx, env, path)
	if err != nil {
		return errors.New("refused: can't list the repository's worktrees")
	}
	var w *cruft.Worktree
	for i := range wts {
		if filepath.Clean(wts[i].Repo) == path || samePath(wts[i].Repo, path) {
			return errors.New("refused: it is the main worktree")
		}
		if filepath.Clean(wts[i].Path) == path || samePath(wts[i].Path, path) {
			w = &wts[i]
		}
	}
	if w == nil {
		return errors.New("refused: not a worktree of its repository")
	}
	if samePath(w.Repo, w.Path) {
		return errors.New("refused: it is the main worktree")
	}
	if !w.Removable {
		why := w.Why
		if why == "" {
			why = "not removable"
		}
		return errors.New("refused: " + why)
	}
	// Belt and braces: the fields Removable is built from must agree.
	switch {
	case !w.MergedKnown || !w.Merged:
		return errors.New("refused: unmerged branch")
	case !w.DirtyKnown || w.Dirty:
		return errors.New("refused: uncommitted changes")
	}

	// Live sessions, probed again right now: one may have started since the
	// scan above.
	sessions, err := liveSessions(env)
	if err != nil {
		return errors.New("refused: " + err.Error())
	}
	var pids []int
	for _, s := range sessions {
		if sessionInside(s.CWD, path) || sessionInside(s.CWD, w.Path) {
			pids = append(pids, s.PID)
		}
	}
	if len(pids) > 0 {
		return errors.New("refused: " + inventory.InUseBy(pids))
	}

	if err := gitRemove(ctx, w.Repo, w.Path); err != nil {
		return err
	}
	return nil
}

// TrashProjectDir moves a dead transcript dir under ~/.claude/projects to the
// Trash after a fresh re-check. It never deletes. Errors are short sentences
// meant to be shown to the user as they are.
func TrashProjectDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("refused: not an absolute path")
	}
	dir = filepath.Clean(dir)
	env := cruftEnv()
	root := filepath.Join(filepath.Clean(env.ClaudeDir), "projects")
	base := filepath.Base(dir)
	if filepath.Dir(dir) != root || base == "" || base == "." || base == ".." || base == string(filepath.Separator) {
		return errors.New("refused: not a transcript folder directly under " + root)
	}
	if why := realDir(dir); why != "" {
		return errors.New("refused: " + why)
	}
	// The projects root must itself be a real dir: a link there would point
	// the move somewhere else entirely.
	if fi, err := os.Lstat(root); err != nil || !fi.IsDir() {
		return errors.New("refused: " + root + " is not a real directory")
	}

	p, err := scanProject(env, dir)
	if err != nil {
		return errors.New("refused: can't read it")
	}
	if !p.Trashable {
		why := p.Why
		if why == "" {
			why = "not trashable"
		}
		return errors.New("refused: " + why)
	}
	switch {
	case !p.DeadKnown:
		return errors.New("refused: no working directory recorded in its transcripts")
	case !p.Dead:
		return errors.New("refused: project folder still exists")
	case p.HasMemories:
		return errors.New("refused: has memory files")
	}

	// Live sessions, probed again right before the move.
	sessions, err := liveSessions(env)
	if err != nil {
		return errors.New("refused: " + err.Error())
	}
	var pids []int
	for _, s := range sessions {
		if s.CWD == "" {
			continue
		}
		if cruft.EncodeProjectPath(s.CWD) == base || containsPath(p.CWDs, filepath.Clean(s.CWD)) {
			pids = append(pids, s.PID)
		}
	}
	if len(pids) > 0 {
		return errors.New("refused: " + inventory.InUseBy(pids))
	}

	if err := trashMove(dir); err != nil {
		return errors.New("move to the Trash failed: " + err.Error())
	}
	return nil
}

// realDir returns "" when path is an existing directory and not a symlink,
// or the reason it is not.
func realDir(path string) string {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "it does not exist"
	case err != nil:
		return "can't read it"
	case fi.Mode()&os.ModeSymlink != 0:
		return "it is a symlink"
	case !fi.IsDir():
		return "it is not a directory"
	}
	return ""
}

// samePath reports whether a and b name the same path, comparing cleaned and
// then symlink-resolved forms (so /var and /private/var spellings match).
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	return ea == nil && eb == nil && ra == rb
}

// sessionInside reports whether cwd is dir or inside it, on whole path
// elements, in cleaned or symlink-resolved form.
func sessionInside(cwd, dir string) bool {
	if cwd == "" || dir == "" {
		return false
	}
	if within(filepath.Clean(cwd), filepath.Clean(dir)) {
		return true
	}
	rc, ec := filepath.EvalSymlinks(cwd)
	rd, ed := filepath.EvalSymlinks(dir)
	return ec == nil && ed == nil && within(rc, rd)
}

func containsPath(list []string, s string) bool {
	for _, x := range list {
		if filepath.Clean(x) == s {
			return true
		}
	}
	return false
}

// worktreeRemoveArgs is the only git command line RemoveWorktree runs. It
// never carries --force or -f: git then refuses dirty or locked worktrees by
// itself, a second guard behind the scan.
func worktreeRemoveArgs(repo, path string) []string {
	return []string{"-C", repo, "worktree", "remove", path}
}

func runGitWorktreeRemove(ctx context.Context, repo, path string) error {
	cmd := exec.CommandContext(ctx, "git", worktreeRemoveArgs(repo, path)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if _, err := cmd.Output(); err != nil {
		msg := err.Error()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if line := firstLine(string(ee.Stderr)); line != "" {
				msg = line
			}
		}
		return errors.New("git worktree remove failed: " + msg)
	}
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}
