package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tylerreagan/clasp/internal/cruft"
	"github.com/tylerreagan/clasp/internal/state"
)

func cruftFixture() *cruft.Report {
	return &cruft.Report{
		ScannedAt:  time.Now(),
		CutoffDays: 30,
		Worktrees: []cruft.Worktree{
			{Path: "/r/.claude/worktrees/test-cuts", Repo: "/r", Branch: "claude/test-cuts", HEAD: "abcdef1234567890", Merged: true, MergedKnown: true, DirtyKnown: true, DefaultBranch: "main", LastCommit: time.Now().Add(-72 * time.Hour), Size: 263 << 20, Removable: true},
			{Path: "/r/.claude/worktrees/agitated-feistel", Repo: "/r", Branch: "claude/agitated-feistel", HEAD: "1234567890abcdef", MergedKnown: true, DirtyKnown: true, DefaultBranch: "main", Size: 262 << 20, Why: "not merged into main"},
			{Path: "/r/.claude/worktrees/ios", Repo: "/r", Detached: true, HEAD: "deadbeefcafe", Merged: true, MergedKnown: true, Size: 247 << 20, Removable: true},
		},
		Projects: []cruft.ProjectDir{
			{Dir: "/c/projects/-gone", Name: "-gone", CWDs: []string{"/gone/place"}, Dead: true, DeadKnown: true, Size: 5 << 20, Transcripts: 2, Desktop: 2, Trashable: true},
			{Dir: "/c/projects/-live", Name: "-live", CWDs: []string{"/live/proj"}, DeadKnown: true, Size: 1 << 30, Transcripts: 3, Desktop: 3, OlderThanCutoff: 2, Why: "project folder still exists"},
		},
		DeadEntries: []string{"/gone/a", "/gone/b"},
		Memories:    []cruft.MemoryIssue{{Project: "-proj", File: "a.md", Kind: cruft.DeadLink, Detail: "[[x]]"}},
		Swept:       []cruft.SweptDir{{Name: "plans", Path: "/c/plans", Size: 1 << 20, Files: 3}},
		PluginCache: cruft.CacheSize{Total: 27 << 20, Stale: 5 << 20, StaleCopies: 2},
		Errors:      []string{"one problem"},
	}
}

func cruftModel(width int) Model {
	m := Model{
		st:     &state.State{Memory: []state.MemoryEntry{{Project: "-proj", File: "a.md", Name: "alpha"}, {Project: "-proj", File: "b.md", Name: "beta"}}},
		keys:   defaultKeys(),
		tab:    tabCruft,
		width:  width,
		height: 40,
		ready:  true,
		cruft:  cruftFixture(),
	}
	vw, vh := m.viewportDims()
	m.detail.Width, m.detail.Height = vw, vh
	m.refreshBindings()
	m.detail.SetContent(m.detailContent())
	return m
}

func cruftAt(t *testing.T, m Model, kind cruftKind, idx int) Model {
	for i, e := range m.cruftEntries() {
		if e.kind == kind && e.idx == idx {
			m.cursors[tabCruft] = i
			m.detail.SetContent(m.detailContent())
			return m
		}
	}
	t.Fatalf("no entry %v/%d", kind, idx)
	return m
}

func withSpies(t *testing.T) (removed, trashed *[]string) {
	r, tr := &[]string{}, &[]string{}
	oldR, oldT, oldS := removeWorktree, trashProjectDir, cruftScan
	removeWorktree = func(p string) error { *r = append(*r, p); return nil }
	trashProjectDir = func(p string) error { *tr = append(*tr, p); return nil }
	cruftScan = func(context.Context) *cruft.Report { return cruftFixture() }
	t.Cleanup(func() { removeWorktree, trashProjectDir, cruftScan = oldR, oldT, oldS })
	return r, tr
}

func TestCruftEnterStartsScanAndStaleGenIgnored(t *testing.T) {
	withSpies(t)
	m := invModel(100, false)
	m.cruft = nil
	m.setTab(tabMemory)
	m, c := key1(m, "]") // Inventory -> Config
	m, c = key1(m, "]")  // -> Cleanup
	if m.tab != tabCruft || !m.cruftScanning || m.cruftGen != 1 {
		t.Fatalf("tab=%v scanning=%v gen=%d", m.tab, m.cruftScanning, m.cruftGen)
	}
	var found bool
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch v := c().(type) {
		case tea.BatchMsg:
			for _, cc := range v {
				walk(cc)
			}
		case cruftScanMsg:
			found = v.gen == 1 && v.report != nil
			n, _ := m.Update(v)
			m = n.(Model)
		}
	}
	walk(c)
	if !found || m.cruft == nil || m.cruftScanning {
		t.Fatalf("scan result not applied: found=%v", found)
	}
	// Stale gen is ignored.
	n, _ := m.Update(cruftScanMsg{gen: 0, report: &cruft.Report{}})
	if n.(Model).cruft == m.cruft && len(n.(Model).cruft.Worktrees) == 0 {
		t.Fatal("stale scan replaced report")
	}
	m.cruftGen = 5
	m.cruftScanning = true
	n, _ = m.Update(cruftScanMsg{gen: 4, report: &cruft.Report{}})
	if !n.(Model).cruftScanning || len(n.(Model).cruft.Worktrees) == 0 {
		t.Fatal("stale scan applied")
	}
}

func TestCruftRowsAndWidths(t *testing.T) {
	for _, w := range []int{80, 100} {
		m := cruftModel(w)
		out := plain(m.View())
		for _, l := range strings.Split(out, "\n") {
			if lipgloss.Width(l) > w {
				t.Errorf("w=%d line width %d: %q", w, lipgloss.Width(l), l)
			}
		}
		for _, want := range []string{"Worktrees", "claude/test-cuts", "(det) ios", "Transcripts", "Dead projects", "Memories", "Auto-swept", "Plugin cache", "Desktop storage", "scan "} {
			if !strings.Contains(out, want) {
				t.Errorf("w=%d missing %q", w, want)
			}
		}
		ref := invModel(w, false)
		if got, want := strings.Count(m.renderHeader(), "\n"), strings.Count(ref.renderHeader(), "\n"); got != want {
			t.Errorf("w=%d header lines %d != %d", w, got, want)
		}
	}
}

func TestCruftRemoveWorktreeFlow(t *testing.T) {
	removed, _ := withSpies(t)
	m := cruftAt(t, cruftModel(100), ckWorktree, 0)
	m, _ = key1(m, "x")
	if m.pendingCruft == nil || !strings.Contains(plain(m.renderStatus()), "remove worktree claude/test-cuts (263M)? y/N") {
		t.Fatalf("no prompt: %q", plain(m.renderStatus()))
	}
	if !strings.Contains(plain(m.detail.View()), "/r/.claude/worktrees/test-cuts") {
		t.Errorf("detail lacks exact path: %q", plain(m.detail.View()))
	}
	m, c := key1(m, "n")
	if m.pendingCruft != nil || c == nil || len(*removed) != 0 {
		t.Fatal("n should cancel without calling")
	}
	m, _ = key1(m, "x")
	m, c = key1(m, "y")
	msg := c()
	if len(*removed) != 1 || (*removed)[0] != "/r/.claude/worktrees/test-cuts" {
		t.Fatalf("removed = %v", *removed)
	}
	gen := m.cruftGen
	n, c := m.Update(msg)
	m = n.(Model)
	if m.cruftGen != gen+1 || !m.cruftScanning || !strings.Contains(plain(m.statusMsg), "removed worktree") {
		t.Fatalf("no rescan/status: gen=%d status=%q", m.cruftGen, m.statusMsg)
	}
	if c == nil {
		t.Fatal("expected cmds")
	}
	// failure path
	n, _ = m.Update(cruftActionMsg{err: errors.New("it broke")})
	if !strings.Contains(plain(n.(Model).statusMsg), "it broke") {
		t.Fatal("error not shown")
	}
}

func TestCruftRemoveRefusals(t *testing.T) {
	removed, _ := withSpies(t)
	m := cruftAt(t, cruftModel(100), ckWorktree, 1)
	m, _ = key1(m, "x")
	if m.pendingCruft != nil || !strings.Contains(plain(m.statusMsg), "not merged into main") {
		t.Fatalf("status = %q", m.statusMsg)
	}
	m = cruftAt(t, m, ckProject, 0)
	m, _ = key1(m, "x")
	if m.pendingCruft != nil || !strings.Contains(plain(m.statusMsg), "select one") {
		t.Fatalf("status = %q", m.statusMsg)
	}
	if len(*removed) != 0 {
		t.Fatal("spy called")
	}
}

func TestCruftTrashFlow(t *testing.T) {
	_, trashed := withSpies(t)
	m := cruftAt(t, cruftModel(100), ckProject, 0)
	m, _ = key1(m, "t")
	if m.pendingCruft == nil || !strings.Contains(plain(m.renderStatus()), "trash transcripts place (5.0M)? y/N") {
		t.Fatalf("no prompt: %q", plain(m.renderStatus()))
	}
	m, c := key1(m, "y")
	c()
	if len(*trashed) != 1 || (*trashed)[0] != "/c/projects/-gone" {
		t.Fatalf("trashed = %v", *trashed)
	}
	m = cruftAt(t, cruftModel(100), ckProject, 1)
	m, _ = key1(m, "t")
	if m.pendingCruft != nil || !strings.Contains(plain(m.statusMsg), "project folder still exists") {
		t.Fatalf("status = %q", m.statusMsg)
	}
}

func TestCruftRefreshKey(t *testing.T) {
	withSpies(t)
	m := cruftModel(100)
	m, c := key1(m, "r")
	if !m.cruftScanning || m.cruftGen != 1 {
		t.Fatal("r on Cruft did not start a scan")
	}
	var sawScan bool
	for _, cc := range c().(tea.BatchMsg) {
		if _, ok := cc().(cruftScanMsg); ok {
			sawScan = true
		}
	}
	if !sawScan {
		t.Fatal("no scan msg")
	}
	o := invModel(100, false)
	o, c = key1(o, "r")
	if o.cruftScanning {
		t.Fatal("r elsewhere started a scan")
	}
	if _, ok := c().(RefreshMsg); !ok {
		t.Fatal("r elsewhere should be RefreshMsg")
	}
}

func TestMemoryTabBadge(t *testing.T) {
	m := cruftModel(100)
	m.tab = tabMemory
	lines := strings.Join(listLines(m), "\n")
	var a, b string
	for _, l := range strings.Split(lines, "\n") {
		if strings.Contains(l, "alpha") {
			a = l
		}
		if strings.Contains(l, "beta") {
			b = l
		}
	}
	if !strings.Contains(a, "⚠") || strings.Contains(b, "⚠") {
		t.Fatalf("badges wrong: %q / %q", a, b)
	}
	if !strings.Contains(plain(m.detailContent()), "dead link") {
		t.Fatal("memory detail lacks issue")
	}
	m.cruft = nil
	if strings.Contains(strings.Join(listLines(m), ""), "⚠") {
		t.Fatal("badge without report")
	}
}

func TestCruftScanningDoesNotBlock(t *testing.T) {
	withSpies(t)
	m := cruftModel(100)
	m.cruft = nil
	m, _ = key1(m, "r") // cmd never executed: scan stays in flight
	if !strings.Contains(plain(m.View()), "scanning…") {
		t.Fatal("no scanning row")
	}
	m, _ = key1(m, "j")
	m, _ = key1(m, "k")
	m, _ = key1(m, "]")
	if m.tab != tabInventoryFirst() {
		t.Fatalf("tab did not move: %v", m.tab)
	}
}

func tabInventoryFirst() tab { return tabSessions }

func TestCruftScanningWithOldReport(t *testing.T) {
	m := cruftModel(100)
	m.cruftScanning = true
	if !strings.Contains(plain(m.renderStatus()), "scanning…") {
		t.Fatal("status lacks scanning")
	}
	if !strings.Contains(plain(m.View()), "Worktrees") {
		t.Fatal("old report hidden")
	}
}
