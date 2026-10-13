package state

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tylerreagan/clasp/internal/cruft"
	"github.com/tylerreagan/clasp/internal/inventory"
)

// cruftFixture is a fake machine under t.TempDir: a temp home and ClaudeDir,
// a real git repo, a temp Trash, and spies on gitRemove and trashMove. Live
// sessions come from the fixture, never from the real ~/.claude/sessions.
type cruftFixture struct {
	t           *testing.T
	root        string
	env         inventory.Env
	repo        string
	trash       string
	sessions    []cruft.LiveSession
	sessionsErr error
	gitCalls    [][2]string // repo, path
	moves       []string
}

func newCruftFixture(t *testing.T) *cruftFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// Keep the user's git config (signing, hooks, templates) out of it.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &cruftFixture{
		t:    t,
		root: root,
		env: inventory.Env{
			Home:      filepath.Join(root, "home"),
			ClaudeDir: filepath.Join(root, "home", ".claude"),
		},
		repo:  filepath.Join(root, "repo"),
		trash: filepath.Join(root, "Trash"),
	}
	for _, d := range []string{f.env.ClaudeDir, filepath.Join(f.env.ClaudeDir, "projects"), f.trash, f.repo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	saved := struct {
		env     func() inventory.Env
		scanR   func(context.Context, cruft.Env, string) ([]cruft.Worktree, error)
		scanP   func(cruft.Env, string) (cruft.ProjectDir, error)
		live    func(cruft.Env) ([]cruft.LiveSession, error)
		gitRm   func(context.Context, string, string) error
		trashMv func(string) error
	}{cruftEnv, scanRepo, scanProject, liveSessions, gitRemove, trashMove}
	t.Cleanup(func() {
		cruftEnv, scanRepo, scanProject = saved.env, saved.scanR, saved.scanP
		liveSessions, gitRemove, trashMove = saved.live, saved.gitRm, saved.trashMv
	})
	cruftEnv = func() inventory.Env { return f.env }
	liveSessions = func(cruft.Env) ([]cruft.LiveSession, error) { return f.sessions, f.sessionsErr }
	gitRemove = func(ctx context.Context, repo, path string) error {
		f.gitCalls = append(f.gitCalls, [2]string{repo, path})
		return runGitWorktreeRemove(ctx, repo, path)
	}
	trashMove = func(p string) error {
		f.moves = append(f.moves, p)
		return os.Rename(p, uniqueTrashName(f.trash, filepath.Base(p)))
	}

	f.git(f.repo, "init", "-q", "-b", "main")
	f.write(filepath.Join(f.repo, "a.txt"), "a\n")
	f.git(f.repo, "add", "a.txt")
	f.git(f.repo, "commit", "-q", "-m", "init")
	return f
}

func (f *cruftFixture) git(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (f *cruftFixture) write(path, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// worktree adds a linked worktree on a new branch at main's HEAD (merged).
func (f *cruftFixture) worktree(name string) string {
	f.t.Helper()
	p := filepath.Join(f.root, "wt-"+name)
	f.git(f.repo, "worktree", "add", "-q", "-b", name, p)
	return p
}

func (f *cruftFixture) noGit() {
	f.t.Helper()
	if len(f.gitCalls) > 0 {
		f.t.Fatalf("gitRemove was called on a refusal: %v", f.gitCalls)
	}
}

func (f *cruftFixture) noMove() {
	f.t.Helper()
	if len(f.moves) > 0 {
		f.t.Fatalf("trashMove was called on a refusal: %v", f.moves)
	}
}

func wantRefused(t *testing.T, err error, sub string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil, want refusal containing %q", sub)
	}
	if !strings.Contains(err.Error(), sub) {
		t.Fatalf("got %q, want it to contain %q", err.Error(), sub)
	}
}

func TestRemoveWorktreeMergedClean(t *testing.T) {
	f := newCruftFixture(t)
	wt := f.worktree("done")
	if err := RemoveWorktree(wt); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Lstat(wt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree dir still there: %v", err)
	}
	if list := f.git(f.repo, "worktree", "list", "--porcelain"); strings.Contains(list, wt) {
		t.Fatalf("git still lists the worktree:\n%s", list)
	}
	f.git(f.repo, "rev-parse", "--verify", "--quiet", "refs/heads/done") // branch kept
	if len(f.gitCalls) != 1 || f.gitCalls[0] != [2]string{f.repo, wt} {
		t.Fatalf("gitRemove calls = %v", f.gitCalls)
	}
}

func TestRemoveWorktreeRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *cruftFixture) string
		want  string
	}{
		{"unmerged", func(f *cruftFixture) string {
			wt := f.worktree("feat")
			f.write(filepath.Join(wt, "b.txt"), "b\n")
			f.git(wt, "add", "b.txt")
			f.git(wt, "commit", "-q", "-m", "b")
			return wt
		}, "refused: unmerged branch"},
		{"dirty tracked", func(f *cruftFixture) string {
			wt := f.worktree("dirty")
			f.write(filepath.Join(wt, "a.txt"), "changed\n")
			return wt
		}, "refused: uncommitted changes"},
		{"untracked only", func(f *cruftFixture) string {
			wt := f.worktree("untracked")
			f.write(filepath.Join(wt, "new.txt"), "n\n")
			return wt
		}, "refused: uncommitted changes"},
		{"live session inside", func(f *cruftFixture) string {
			wt := f.worktree("live")
			sub := filepath.Join(wt, "sub")
			os.MkdirAll(sub, 0o755)
			f.sessions = []cruft.LiveSession{{PID: 4242, CWD: sub}}
			return wt
		}, "refused: in use by PID 4242"},
		{"symlink", func(f *cruftFixture) string {
			wt := f.worktree("linked")
			link := filepath.Join(f.root, "link")
			if err := os.Symlink(wt, link); err != nil {
				t.Fatal(err)
			}
			return link
		}, "refused: it is a symlink"},
		{"relative", func(f *cruftFixture) string {
			f.worktree("rel")
			return "wt-rel"
		}, "refused: not an absolute path"},
		{"main worktree", func(f *cruftFixture) string {
			return f.repo
		}, "refused: it is the main worktree"},
		{"dir inside the repo", func(f *cruftFixture) string {
			d := filepath.Join(f.repo, "sub")
			os.MkdirAll(d, 0o755)
			return d
		}, "refused: not a worktree of its repository"},
		{"not a repo", func(f *cruftFixture) string {
			d := filepath.Join(f.root, "plain")
			os.MkdirAll(d, 0o755)
			return d
		}, "refused: can't list the repository's worktrees"},
		{"nonexistent", func(f *cruftFixture) string {
			return filepath.Join(f.root, "nope")
		}, "refused: it does not exist"},
		{"scan disagrees", func(f *cruftFixture) string {
			wt := f.worktree("disagree")
			scanRepo = func(ctx context.Context, env cruft.Env, repo string) ([]cruft.Worktree, error) {
				ws, err := cruft.ScanRepo(ctx, env, repo)
				for i := range ws {
					ws[i].Removable, ws[i].Why = false, "changed since the scan"
				}
				return ws, err
			}
			return wt
		}, "refused: changed since the scan"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCruftFixture(t)
			p := tc.setup(f)
			err := RemoveWorktree(p)
			wantRefused(t, err, tc.want)
			f.noGit()
		})
	}
}

// When the scan is wrong and calls a dirty tree removable, git itself must
// still refuse, because --force is never passed.
func TestRemoveWorktreeGitRefusesDirtyAsSecondGuard(t *testing.T) {
	f := newCruftFixture(t)
	wt := f.worktree("lying")
	f.write(filepath.Join(wt, "a.txt"), "changed\n")
	scanRepo = func(ctx context.Context, env cruft.Env, repo string) ([]cruft.Worktree, error) {
		ws, err := cruft.ScanRepo(ctx, env, repo)
		for i := range ws {
			ws[i].Removable, ws[i].Why = true, ""
			ws[i].Dirty, ws[i].DirtyKnown = false, true
		}
		return ws, err
	}
	err := RemoveWorktree(wt)
	wantRefused(t, err, "git worktree remove failed: ")
	if strings.Contains(err.Error(), "\n") {
		t.Fatalf("error is not one line: %q", err)
	}
	if _, serr := os.Stat(filepath.Join(wt, "a.txt")); serr != nil {
		t.Fatalf("dirty worktree was touched: %v", serr)
	}
}

func TestWorktreeRemoveArgsNeverForce(t *testing.T) {
	args := worktreeRemoveArgs("/r", "/w")
	want := []string{"-C", "/r", "worktree", "remove", "/w"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for _, a := range args {
		if a == "--force" || a == "-f" || strings.HasPrefix(a, "--force") {
			t.Fatalf("args carry %q: %v", a, args)
		}
	}
}

// project writes a transcript dir under projects named after cwd (or name)
// whose transcript records cwd ("" records none).
func (f *cruftFixture) project(name, cwd string) string {
	f.t.Helper()
	dir := filepath.Join(f.env.ClaudeDir, "projects", name)
	line := `{"type":"user"}`
	if cwd != "" {
		line = `{"cwd":"` + cwd + `","entrypoint":"cli"}`
	}
	f.write(filepath.Join(dir, "s1.jsonl"), line+"\n")
	return dir
}

func TestTrashProjectDirDead(t *testing.T) {
	f := newCruftFixture(t)
	gone := filepath.Join(f.root, "gone")
	dir := f.project(cruft.EncodeProjectPath(gone), gone)
	if err := TrashProjectDir(dir); err != nil {
		t.Fatalf("TrashProjectDir: %v", err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dir still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.trash, filepath.Base(dir), "s1.jsonl")); err != nil {
		t.Fatalf("not in the Trash: %v", err)
	}
}

// An incomplete session list must refuse both writes, never act.
func TestUnreadableSessionsRefuse(t *testing.T) {
	f := newCruftFixture(t)
	f.sessionsErr = errors.New("can't read the session file of running PID 7")
	wt := f.worktree("done")
	if err := RemoveWorktree(wt); err == nil || !strings.Contains(err.Error(), "PID 7") {
		t.Fatalf("RemoveWorktree err = %v", err)
	}
	gone := filepath.Join(f.root, "gone")
	dir := f.project(cruft.EncodeProjectPath(gone), gone)
	if err := TrashProjectDir(dir); err == nil || !strings.Contains(err.Error(), "PID 7") {
		t.Fatalf("TrashProjectDir err = %v", err)
	}
	if len(f.gitCalls) != 0 || len(f.moves) != 0 {
		t.Fatalf("acted: git %v moves %v", f.gitCalls, f.moves)
	}
}

func TestTrashProjectDirRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *cruftFixture) string
		want  string
	}{
		{"alive", func(f *cruftFixture) string {
			return f.project(cruft.EncodeProjectPath(f.repo), f.repo)
		}, "refused: project folder still exists"},
		{"unknown", func(f *cruftFixture) string {
			return f.project("-unknown", "")
		}, "refused: no working directory recorded in its transcripts"},
		{"has memories", func(f *cruftFixture) string {
			gone := filepath.Join(f.root, "gone")
			dir := f.project(cruft.EncodeProjectPath(gone), gone)
			f.write(filepath.Join(dir, "memory", "MEMORY.md"), "- x\n")
			return dir
		}, "refused: has memory files"},
		{"live by encoded name", func(f *cruftFixture) string {
			gone := filepath.Join(f.root, "gone")
			dir := f.project(cruft.EncodeProjectPath(gone), gone)
			// A different spelling that encodes to the same dir name.
			f.sessions = []cruft.LiveSession{{PID: 77, CWD: strings.ReplaceAll(gone, "/", ".")}}
			return dir
		}, "refused: in use by PID 77"},
		{"live by cwd", func(f *cruftFixture) string {
			gone := filepath.Join(f.root, "gone")
			dir := f.project("-renamed", gone)
			f.sessions = []cruft.LiveSession{{PID: 78, CWD: gone}}
			return dir
		}, "refused: in use by PID 78"},
		{"symlink", func(f *cruftFixture) string {
			gone := filepath.Join(f.root, "gone")
			real := f.project(cruft.EncodeProjectPath(gone), gone)
			link := filepath.Join(f.env.ClaudeDir, "projects", "-link")
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			return link
		}, "refused: it is a symlink"},
		{"dotdot escape", func(f *cruftFixture) string {
			os.MkdirAll(filepath.Join(f.env.ClaudeDir, "x"), 0o755)
			return filepath.Join(f.env.ClaudeDir, "projects") + "/../x"
		}, "refused: not a transcript folder directly under"},
		{"depth 2", func(f *cruftFixture) string {
			gone := filepath.Join(f.root, "gone")
			f.project("a", gone)
			d := filepath.Join(f.env.ClaudeDir, "projects", "a", "b")
			f.write(filepath.Join(d, "s.jsonl"), `{"cwd":"`+gone+`"}`+"\n")
			return d
		}, "refused: not a transcript folder directly under"},
		{"projects root", func(f *cruftFixture) string {
			return filepath.Join(f.env.ClaudeDir, "projects")
		}, "refused: not a transcript folder directly under"},
		{"outside ClaudeDir", func(f *cruftFixture) string {
			gone := filepath.Join(f.root, "gone")
			d := filepath.Join(f.root, "elsewhere", "projects", "-x")
			f.write(filepath.Join(d, "s.jsonl"), `{"cwd":"`+gone+`"}`+"\n")
			return d
		}, "refused: not a transcript folder directly under"},
		{"relative", func(f *cruftFixture) string {
			return "projects/-x"
		}, "refused: not an absolute path"},
		{"nonexistent", func(f *cruftFixture) string {
			return filepath.Join(f.env.ClaudeDir, "projects", "-nope")
		}, "refused: it does not exist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCruftFixture(t)
			p := tc.setup(f)
			err := TrashProjectDir(p)
			wantRefused(t, err, tc.want)
			f.noMove()
		})
	}
}
