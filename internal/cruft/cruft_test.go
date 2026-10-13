package cruft

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fix struct {
	t    *testing.T
	root string
	env  Env
}

func newFix(t *testing.T) *fix {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fix{t: t, root: root}
	f.env = Env{Home: filepath.Join(root, "home"), ClaudeDir: filepath.Join(root, "home", ".claude")}
	f.mkdir(f.env.ClaudeDir)
	// No live processes unless a test says so.
	old := processLive
	t.Cleanup(func() { processLive = old })
	processLive = func(int, string) bool { return false }
	return f
}

func (f *fix) mkdir(p string) {
	f.t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fix) write(p, content string) {
	f.t.Helper()
	f.mkdir(filepath.Dir(p))
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fix) git(dir string, args ...string) string {
	f.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo makes a repo with one commit on branch.
func (f *fix) repo(name, branch string) string {
	dir := filepath.Join(f.root, name)
	f.mkdir(dir)
	f.git(dir, "init", "-q", "-b", branch)
	f.write(filepath.Join(dir, "README"), "hi\n")
	f.git(dir, "add", ".")
	f.git(dir, "commit", "-q", "-m", "init")
	return dir
}

func (f *fix) commit(dir, file string) {
	f.write(filepath.Join(dir, file), file)
	f.git(dir, "add", ".")
	f.git(dir, "commit", "-q", "-m", file)
}

func (f *fix) claudeJSON(keys ...string) {
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%q:{}", k))
	}
	f.write(filepath.Join(f.env.Home, ".claude.json"), `{"oauthAccount":{"secret":"x"},"projects":{`+strings.Join(parts, ",")+`}}`)
}

func (f *fix) session(pid int, cwd string) {
	f.write(filepath.Join(f.env.ClaudeDir, "sessions", fmt.Sprintf("%d.json", pid)),
		fmt.Sprintf(`{"pid":%d,"cwd":%q,"procStart":"Mon Oct 12 20:11:22 2026"}`, pid, cwd))
}

func (f *fix) livePIDs(pids ...int) {
	processLive = func(pid int, _ string) bool {
		for _, p := range pids {
			if p == pid {
				return true
			}
		}
		return false
	}
}

func byBase(ws []Worktree) map[string]Worktree {
	m := map[string]Worktree{}
	for _, w := range ws {
		m[filepath.Base(w.Path)] = w
	}
	return m
}

func TestWorktreeClassification(t *testing.T) {
	f := newFix(t)
	repo := f.repo("repo", "main")
	add := func(name string, args ...string) string {
		p := filepath.Join(f.root, name)
		f.git(repo, append([]string{"worktree", "add", "-q"}, append(args, p)...)...)
		return p
	}
	add("merged", "-b", "feat/merged")
	un := add("unmerged", "-b", "unmerged")
	f.commit(un, "u.txt")
	add("det-in-main", "--detach")
	dn := add("det-not-main", "--detach")
	f.commit(dn, "d.txt")
	dirty := add("dirty", "-b", "dirty")
	f.write(filepath.Join(dirty, "README"), "changed\n")
	untracked := add("untracked", "-b", "untracked")
	f.write(filepath.Join(untracked, "new.txt"), "x")
	live := add("live", "-b", "live")
	gone := add("gone", "-b", "gone")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	f.mkdir(filepath.Join(live, "sub"))
	f.session(4242, filepath.Join(live, "sub"))
	f.livePIDs(4242)
	f.claudeJSON(repo)

	r := Scan(context.Background(), f.env)
	got := byBase(r.Worktrees)
	if len(got) != 8 {
		t.Fatalf("want 8 worktrees, got %d: %+v", len(got), r.Worktrees)
	}
	for name, want := range map[string]struct {
		removable bool
		why       string
	}{
		"merged":       {true, ""},
		"unmerged":     {false, "unmerged branch"},
		"det-in-main":  {true, ""},
		"det-not-main": {false, "detached HEAD not in main"},
		"dirty":        {false, "uncommitted changes"},
		"untracked":    {false, "uncommitted changes"},
		"live":         {false, "in use by PID 4242"},
		"gone":         {false, "missing on disk (git worktree prune)"},
	} {
		w := got[name]
		if w.Removable != want.removable || w.Why != want.why {
			t.Errorf("%s: removable=%v why=%q, want %v %q", name, w.Removable, w.Why, want.removable, want.why)
		}
	}
	m := got["merged"]
	if !m.MergedKnown || !m.Merged || !m.DirtyKnown || m.Dirty || m.Branch != "feat/merged" || m.Detached ||
		m.DefaultBranch != "main" || m.Repo != repo || m.Size == 0 || m.LastCommit.IsZero() || m.HEAD == "" {
		t.Errorf("merged fields wrong: %+v", m)
	}
	if d := got["det-in-main"]; !d.Detached || d.Branch != "" || !d.Merged {
		t.Errorf("detached fields wrong: %+v", d)
	}
	if got["gone"].Size != 0 {
		t.Errorf("prunable worktree should have no size")
	}
	if !got["untracked"].Dirty || !got["dirty"].Dirty {
		t.Errorf("dirty flags: %+v %+v", got["untracked"], got["dirty"])
	}
	if want := []int{4242}; !reflect.DeepEqual(got["live"].LivePIDs, want) {
		t.Errorf("live pids = %v", got["live"].LivePIDs)
	}
	for _, w := range r.Worktrees {
		if w.Path == repo {
			t.Errorf("main worktree must be skipped")
		}
	}
	if len(r.Sessions) != 1 || r.Sessions[0].PID != 4242 {
		t.Errorf("sessions = %v", r.Sessions)
	}
}

func TestNoDefaultBranchIsUnknown(t *testing.T) {
	f := newFix(t)
	repo := f.repo("repo", "trunk")
	f.git(repo, "worktree", "add", "-q", "-b", "x", filepath.Join(f.root, "wt"))
	ws, err := ScanRepo(context.Background(), f.env, repo)
	if err != nil || len(ws) != 1 {
		t.Fatalf("ws=%v err=%v", ws, err)
	}
	w := ws[0]
	if w.Removable || w.MergedKnown || w.Why != "no default branch found" {
		t.Errorf("got %+v", w)
	}
}

func TestOriginHeadDefaultBranch(t *testing.T) {
	f := newFix(t)
	repo := f.repo("repo", "develop")
	f.git(repo, "branch", "main") // would be a fallback; origin/HEAD must win
	f.git(repo, "update-ref", "refs/remotes/origin/develop", "HEAD")
	f.git(repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	f.git(repo, "worktree", "add", "-q", "-b", "x", filepath.Join(f.root, "wt"))
	ws, err := ScanRepo(context.Background(), f.env, repo)
	if err != nil || len(ws) != 1 || ws[0].DefaultBranch != "develop" || !ws[0].Removable {
		t.Fatalf("ws=%+v err=%v", ws, err)
	}
}

func TestGitFailureIsNeverRemovable(t *testing.T) {
	f := newFix(t)
	repo := f.repo("repo", "main")
	f.git(repo, "worktree", "add", "-q", "-b", "x", filepath.Join(f.root, "wt"))
	old := runGit
	t.Cleanup(func() { runGit = old })
	runGit = func(ctx context.Context, dir string, args ...string) (string, error) {
		if args[0] == "status" {
			return "", fmt.Errorf("boom")
		}
		return old(ctx, dir, args...)
	}
	ws, err := ScanRepo(context.Background(), f.env, repo)
	if err != nil || len(ws) != 1 {
		t.Fatalf("ws=%v err=%v", ws, err)
	}
	if ws[0].Removable || ws[0].DirtyKnown || ws[0].Why != "could not read git status" {
		t.Errorf("got %+v", ws[0])
	}
	runGit = func(ctx context.Context, dir string, args ...string) (string, error) {
		if args[0] == "merge-base" {
			return "", fmt.Errorf("boom")
		}
		return old(ctx, dir, args...)
	}
	ws, _ = ScanRepo(context.Background(), f.env, repo)
	if ws[0].Removable || ws[0].MergedKnown || ws[0].Why != "could not check merge state" {
		t.Errorf("got %+v", ws[0])
	}
}

func TestScanRepoNotARepo(t *testing.T) {
	f := newFix(t)
	if _, err := ScanRepo(context.Background(), f.env, f.root); err == nil {
		t.Error("want error for a non-git dir")
	}
}

func TestReposDedupedAcrossWorktreePaths(t *testing.T) {
	f := newFix(t)
	repo := f.repo("repo", "main")
	wt := filepath.Join(f.root, "wt")
	f.git(repo, "worktree", "add", "-q", "-b", "x", wt)
	plain := filepath.Join(f.root, "plain")
	f.mkdir(plain)
	f.claudeJSON(repo, wt, plain, filepath.Join(f.root, "missing"))
	r := Scan(context.Background(), f.env)
	if len(r.Worktrees) != 1 {
		t.Fatalf("want one worktree once, got %+v", r.Worktrees)
	}
	if want := []string{filepath.Join(f.root, "missing")}; !reflect.DeepEqual(r.DeadEntries, want) {
		t.Errorf("dead entries = %v", r.DeadEntries)
	}
	if len(r.Errors) != 0 {
		t.Errorf("non-git paths must be skipped silently: %v", r.Errors)
	}
}

func TestReposFromTranscriptCWDs(t *testing.T) {
	f := newFix(t)
	repo := f.repo("repo", "main")
	f.git(repo, "worktree", "add", "-q", "-b", "x", filepath.Join(f.root, "wt"))
	f.write(filepath.Join(f.env.ClaudeDir, "projects", "p", "a.jsonl"), fmt.Sprintf(`{"cwd":%q}`+"\n", repo))
	r := Scan(context.Background(), f.env)
	if len(r.Worktrees) != 1 {
		t.Fatalf("worktrees = %+v", r.Worktrees)
	}
}

func TestEncodeProjectPath(t *testing.T) {
	if got := EncodeProjectPath("/Users/a/.claude/worktrees/x_y"); got != "-Users-a--claude-worktrees-x-y" {
		t.Errorf("got %q", got)
	}
}

func TestLiveSessionsLiveness(t *testing.T) {
	f := newFix(t)
	f.session(1, "/a")
	f.session(2, "/b")
	f.write(filepath.Join(f.env.ClaudeDir, "sessions", "bad.json"), "{")
	f.livePIDs(2)
	got := LiveSessions(f.env)
	if !reflect.DeepEqual(got, []LiveSession{{PID: 2, CWD: "/b"}}) {
		t.Errorf("got %v", got)
	}
}

func TestWithin(t *testing.T) {
	for _, tt := range []struct {
		cwd, path string
		want      bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/c", "/a/b", true},
		{"/a/bc", "/a/b", false},
		{"/a", "/a/b", false},
		{"", "/a", false},
		{"/a/b/../b/c", "/a/b", true},
	} {
		if got := within(tt.cwd, tt.path); got != tt.want {
			t.Errorf("within(%q,%q)=%v", tt.cwd, tt.path, got)
		}
	}
}

func TestProjectDirs(t *testing.T) {
	f := newFix(t)
	proj := filepath.Join(f.env.ClaudeDir, "projects")
	alive := filepath.Join(f.root, "alive")
	f.mkdir(alive)
	line := func(cwd, entry string) string {
		return fmt.Sprintf(`{"type":"user","cwd":%q,"entrypoint":%q}`+"\n", cwd, entry)
	}
	gone := filepath.Join(f.root, "gone")
	// dead: all cwds missing; mixed entrypoints
	f.write(filepath.Join(proj, "dead", "a.jsonl"), line(gone, "claude-desktop"))
	f.write(filepath.Join(proj, "dead", "b.jsonl"), line(gone+"2", "cli"))
	f.write(filepath.Join(proj, "dead", "c.jsonl"), line(gone, "claude-vscode"))
	f.write(filepath.Join(proj, "dead", "sub", "nested.jsonl"), line(alive, "cli")) // not top-level: ignored
	// alive: one cwd exists
	f.write(filepath.Join(proj, "mixed", "a.jsonl"), line(gone, "cli"))
	f.write(filepath.Join(proj, "mixed", "b.jsonl"), line(alive, "cli"))
	// unknown: no cwd, and empty dir
	f.write(filepath.Join(proj, "nocwd", "a.jsonl"), `{"type":"summary"}`+"\n")
	f.mkdir(filepath.Join(proj, "empty"))
	// dead with memories
	f.write(filepath.Join(proj, "deadmem", "a.jsonl"), line(gone, "cli"))
	f.write(filepath.Join(proj, "deadmem", "memory", "MEMORY.md"), "x")
	// dead memory dir without .md files is not "memories"
	f.write(filepath.Join(proj, "deadnomd", "a.jsonl"), line(gone, "cli"))
	f.write(filepath.Join(proj, "deadnomd", "memory", "notes.txt"), "x")
	// dead but a live session's cwd encodes to it
	livecwd := filepath.Join(f.root, "livegone")
	f.write(filepath.Join(proj, EncodeProjectPath(livecwd), "a.jsonl"), line(livecwd, "cli"))
	f.session(77, livecwd)
	f.livePIDs(77)
	// old transcript
	old := filepath.Join(proj, "dead", "old.jsonl")
	f.write(old, line(gone, "claude-desktop"))
	past := time.Now().Add(-40 * 24 * time.Hour)
	os.Chtimes(old, past, past)
	// a stray file and a symlinked dir are not project dirs
	f.write(filepath.Join(proj, "stray.txt"), "x")
	os.Symlink(alive, filepath.Join(proj, "link"))

	r := Scan(context.Background(), f.env)
	by := map[string]ProjectDir{}
	for _, p := range r.Projects {
		by[p.Name] = p
	}
	if len(by) != 7 {
		t.Fatalf("projects = %d: %v", len(by), r.Projects)
	}
	d := by["dead"]
	if !d.Dead || !d.DeadKnown || !d.Trashable || d.Why != "" || d.Transcripts != 4 || d.Desktop != 2 || d.CLI != 1 || d.Other != 1 ||
		d.OlderThanCutoff != 1 || d.HasMemories || len(d.CWDs) != 2 || d.Size == 0 || d.Newest.IsZero() {
		t.Errorf("dead: %+v", d)
	}
	if m := by["mixed"]; m.Dead || !m.DeadKnown || m.Trashable || len(m.CWDs) != 2 {
		t.Errorf("mixed: %+v", m)
	}
	if n := by["nocwd"]; n.Dead || n.DeadKnown || n.Trashable || n.Other != 1 {
		t.Errorf("nocwd: %+v", n)
	}
	if e := by["empty"]; e.Dead || e.DeadKnown || e.Trashable {
		t.Errorf("empty: %+v", e)
	}
	if m := by["deadmem"]; !m.Dead || !m.HasMemories || m.Trashable || m.Why != "has memory files" {
		t.Errorf("deadmem: %+v", m)
	}
	if m := by["deadnomd"]; !m.Trashable || m.HasMemories {
		t.Errorf("deadnomd: %+v", m)
	}
	if l := by[EncodeProjectPath(livecwd)]; !l.Dead || l.Trashable || !reflect.DeepEqual(l.LivePIDs, []int{77}) || l.Why != "in use by PID 77" {
		t.Errorf("live: %+v", l)
	}
	// Order: dead dirs first.
	seenAlive := false
	for _, p := range r.Projects {
		if !p.Dead {
			seenAlive = true
		} else if seenAlive {
			t.Errorf("dead project %s sorted after a live one", p.Name)
		}
	}

	one, err := ScanProject(f.env, filepath.Join(proj, "dead"))
	if err != nil || !one.Trashable || one.Transcripts != 4 {
		t.Errorf("ScanProject: %+v %v", one, err)
	}
	if _, err := ScanProject(f.env, filepath.Join(proj, "link")); err == nil {
		t.Error("ScanProject must reject a symlink")
	}
	if _, err := ScanProject(f.env, filepath.Join(proj, "nope")); err == nil {
		t.Error("ScanProject must error for a missing dir")
	}
}

func TestHugeFirstLine(t *testing.T) {
	f := newFix(t)
	proj := filepath.Join(f.env.ClaudeDir, "projects", "big")
	gone := filepath.Join(f.root, "gone")
	huge := `{"type":"file-history-snapshot","blob":"` + strings.Repeat("x", 3<<20) + `"}` + "\n"
	f.write(filepath.Join(proj, "a.jsonl"), huge+fmt.Sprintf(`{"cwd":%q,"entrypoint":"cli"}`+"\n", gone))
	// A huge line carrying "cwd" is skipped, not decoded, and not an error.
	f.write(filepath.Join(proj, "b.jsonl"), `{"cwd":"/x","blob":"`+strings.Repeat("y", 2<<20)+`"}`+"\n")
	p, err := ScanProject(f.env, proj)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.CWDs, []string{gone}) || !p.Dead || p.CLI != 1 || p.Other != 1 {
		t.Errorf("got %+v", p)
	}
}

func TestSymlinksNotFollowedInSizes(t *testing.T) {
	f := newFix(t)
	big := filepath.Join(f.root, "big")
	f.write(filepath.Join(big, "blob"), strings.Repeat("z", 1<<20))
	proj := filepath.Join(f.env.ClaudeDir, "projects", "p")
	f.write(filepath.Join(proj, "a.jsonl"), `{"cwd":"/nope"}`+"\n")
	os.Symlink(big, filepath.Join(proj, "linkdir"))
	os.Symlink(filepath.Join(big, "blob"), filepath.Join(proj, "linkfile"))
	p, _ := ScanProject(f.env, proj)
	if p.Size > 4096 {
		t.Errorf("size followed a symlink: %d", p.Size)
	}
}

func TestMemoryIssues(t *testing.T) {
	f := newFix(t)
	proj := filepath.Join(f.env.ClaudeDir, "projects")
	gone := filepath.Join(f.root, "gone")
	mem := filepath.Join(proj, "live", "memory")
	alive := filepath.Join(f.root, "alive")
	f.mkdir(alive)
	f.write(filepath.Join(proj, "live", "t.jsonl"), fmt.Sprintf(`{"cwd":%q}`+"\n", alive))
	f.write(filepath.Join(mem, "MEMORY.md"), "- [A](a.md)\n- [B](b.md)\n- [Missing](project_clasp.md)\n- [web](https://x.com/y.md)\n")
	f.write(filepath.Join(mem, "a.md"), "---\nname: alpha-name\n---\nsee [[b]] and [[alpha-name]] and [[nowhere]] and [[nowhere]]\n")
	f.write(filepath.Join(mem, "b.md"), "---\nname: \"beta\"\n---\nlinks [[alpha-name]] [[beta]] [[a]]\n")
	f.write(filepath.Join(mem, "c.md"), "orphan, [[a|alias]] [[gamma.md]]\n")

	// Dead project: every file reported as ProjectGone.
	dmem := filepath.Join(proj, "dead", "memory")
	f.write(filepath.Join(proj, "dead", "t.jsonl"), fmt.Sprintf(`{"cwd":%q}`+"\n", gone))
	f.write(filepath.Join(dmem, "MEMORY.md"), "- [x](x.md)\n")
	f.write(filepath.Join(dmem, "x.md"), "x")
	// No MEMORY.md: NotInIndex skipped.
	f.write(filepath.Join(proj, "noidx", "memory", "z.md"), "z")

	r := Scan(context.Background(), f.env)
	type k struct {
		project, file string
		kind          MemoryIssueKind
		detail        string
	}
	var got []k
	for _, m := range r.Memories {
		got = append(got, k{m.Project, m.File, m.Kind, m.Detail})
	}
	want := []k{
		{"dead", "x.md", ProjectGone, "project folder is gone"},
		{"live", "MEMORY.md", IndexMissingFile, "project_clasp.md"},
		{"live", "a.md", DeadLink, "nowhere"},
		{"live", "c.md", DeadLink, "gamma.md"},
		{"live", "c.md", NotInIndex, ""},
		{"noidx", "z.md", NotInIndex, "no MEMORY.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%v\nwant\n%v", got, want)
	}
	for kind, s := range map[MemoryIssueKind]string{DeadLink: "dead link", NotInIndex: "not in index", IndexMissingFile: "index points at missing file", ProjectGone: "project gone"} {
		if kind.String() != s {
			t.Errorf("String(%d)=%q", kind, kind.String())
		}
	}
}

func TestSettingsSweptAndCache(t *testing.T) {
	f := newFix(t)
	if r := Scan(context.Background(), f.env); r.CutoffDays != 30 || len(r.Swept) != 0 || r.PluginCache.Total != 0 {
		t.Errorf("defaults: %+v", r)
	}
	f.write(filepath.Join(f.env.ClaudeDir, "settings.json"), `{"cleanupPeriodDays": 7, "env":{"K":"V"}}`)
	f.write(filepath.Join(f.env.ClaudeDir, "plans", "p.md"), "12345")
	f.write(filepath.Join(f.env.ClaudeDir, "plans", "q.md"), "123")
	f.write(filepath.Join(f.env.ClaudeDir, "tasks", "t", "x"), "1")
	f.write(filepath.Join(f.env.ClaudeDir, "plugins", "cache", "m", "p", "1.0", "f"), "abcdef")
	r := Scan(context.Background(), f.env)
	if r.CutoffDays != 7 {
		t.Errorf("cutoff = %d", r.CutoffDays)
	}
	names := map[string]SweptDir{}
	for _, s := range r.Swept {
		names[s.Name] = s
	}
	if len(names) != 2 || names["plans"].Size != 8 || names["plans"].Files != 2 || names["tasks"].Files != 1 {
		t.Errorf("swept = %+v", r.Swept)
	}
	if r.PluginCache.Total != 6 {
		t.Errorf("cache = %+v", r.PluginCache)
	}
	f.write(filepath.Join(f.env.ClaudeDir, "settings.json"), `not json`)
	if Scan(context.Background(), f.env).CutoffDays != 30 {
		t.Error("bad settings should fall back to 30")
	}
}

func TestClaudeJSONOnlyProjectsDecoded(t *testing.T) {
	f := newFix(t)
	f.write(filepath.Join(f.env.Home, ".claude.json"), `{"oauthAccount":{"emailAddress":"x"},"projects":{"/b":{"allowedTools":[]},"/a":{}}}`)
	keys, err := claudeJSONProjects(f.env)
	if err != nil || !reflect.DeepEqual(keys, []string{"/a", "/b"}) {
		t.Errorf("keys=%v err=%v", keys, err)
	}
	f.write(filepath.Join(f.env.Home, ".claude.json"), `{`)
	r := Scan(context.Background(), f.env)
	if len(r.Errors) != 1 {
		t.Errorf("want one non-fatal error, got %v", r.Errors)
	}
}

func TestScanCancelStopsPromptly(t *testing.T) {
	f := newFix(t)
	repo := f.repo("repo", "main")
	for i := 0; i < 3; i++ {
		f.git(repo, "worktree", "add", "-q", "-b", fmt.Sprint("b", i), filepath.Join(f.root, fmt.Sprint("wt", i)))
	}
	f.claudeJSON(repo)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	r := Scan(ctx, f.env)
	if time.Since(start) > 2*time.Second {
		t.Error("cancelled scan took too long")
	}
	if len(r.Worktrees) != 0 {
		t.Errorf("cancelled scan returned worktrees: %v", r.Worktrees)
	}
	found := false
	for _, e := range r.Errors {
		found = found || strings.Contains(e, "cancelled")
	}
	if !found {
		t.Errorf("errors = %v", r.Errors)
	}
}

func TestCancelDuringScan(t *testing.T) {
	f := newFix(t)
	repo := f.repo("repo", "main")
	f.git(repo, "worktree", "add", "-q", "-b", "b", filepath.Join(f.root, "wt"))
	f.claudeJSON(repo)
	ctx, cancel := context.WithCancel(context.Background())
	old := runGit
	t.Cleanup(func() { runGit = old })
	calls := 0
	runGit = func(c context.Context, dir string, args ...string) (string, error) {
		calls++
		if calls == 3 {
			cancel()
		}
		return old(c, dir, args...)
	}
	r := Scan(ctx, f.env)
	if calls > 8 {
		t.Errorf("scan kept calling git after cancel: %d calls", calls)
	}
	if len(r.Errors) == 0 {
		t.Error("want a cancellation note")
	}
}
