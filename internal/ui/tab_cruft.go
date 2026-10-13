package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tylerreagan/clasp/internal/cruft"
	"github.com/tylerreagan/clasp/internal/inventory"
	"github.com/tylerreagan/clasp/internal/state"
)

// Seams: tests swap these so they never scan or touch the real machine.
var (
	cruftScan = func(ctx context.Context) *cruft.Report {
		return cruft.Scan(ctx, inventory.DefaultEnv())
	}
	removeWorktree   = state.RemoveWorktree
	trashProjectDir  = state.TrashProjectDir
	cruftScanTimeout = 2 * time.Minute
)

// maxLiveTranscriptRows caps the live transcript dirs listed before "… N more".
const maxLiveTranscriptRows = 10

// cruftScanMsg carries a finished scan; results whose gen is not the latest
// are dropped.
type cruftScanMsg struct {
	gen    int
	report *cruft.Report
}

// cruftActionMsg reports a worktree removal or project-dir trash.
type cruftActionMsg struct {
	what string
	err  error
}

type cruftKind int

const (
	ckWorktree cruftKind = iota
	ckProject
	ckDead
	ckMemory
	ckSwept
	ckCache
	ckDesktop
)

// cruftEntry is one selectable Cruft row: kind says which report slice idx
// indexes (ckCache and ckDesktop have no slice; idx is 0).
type cruftEntry struct {
	kind cruftKind
	idx  int
}

// cruftTarget is the removal awaiting y/n. path is exactly what will be removed.
type cruftTarget struct {
	kind cruftKind // ckWorktree or ckProject
	name string
	path string
	size int64
}

func cruftScanCmd(ctx context.Context, cancel context.CancelFunc, gen int) tea.Cmd {
	return func() tea.Msg {
		defer cancel()
		return cruftScanMsg{gen: gen, report: cruftScan(ctx)}
	}
}

// startCruftScan begins a scan and cancels any still in flight, so repeated
// r presses never stack full walks of the transcripts and worktrees.
func (m *Model) startCruftScan() tea.Cmd {
	if m.cruftCancel != nil {
		m.cruftCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), cruftScanTimeout)
	m.cruftCancel = cancel
	m.cruftGen++
	m.cruftScanning = true
	return cruftScanCmd(ctx, cancel, m.cruftGen)
}

// cruftKey identifies an entry across scans (kind plus its path or name), so
// the cursor stays on the same thing when a rescan adds or removes rows.
func (m Model) cruftKey(e cruftEntry) string {
	r := m.cruft
	if r == nil {
		return ""
	}
	switch e.kind {
	case ckWorktree:
		return "w" + r.Worktrees[e.idx].Path
	case ckProject:
		return "p" + r.Projects[e.idx].Dir
	case ckDead:
		return "d" + r.DeadEntries[e.idx]
	case ckMemory:
		is := r.Memories[e.idx]
		return fmt.Sprintf("m%s/%s/%d/%s", is.Project, is.File, is.Kind, is.Detail)
	case ckSwept:
		return "s" + r.Swept[e.idx].Path
	}
	return fmt.Sprint(e.kind)
}

// applyCruftReport swaps in a new report and keeps the cursor on the entry it
// was on, or clamps it when that entry is gone.
func (m *Model) applyCruftReport(r *cruft.Report) {
	var key string
	if e, ok := m.selectedCruft(); ok {
		key = m.cruftKey(e)
	}
	m.cruft, m.cruftScanning, m.cruftCancel = r, false, nil
	if key != "" {
		for i, e := range m.cruftEntries() {
			if m.cruftKey(e) == key {
				m.cursors[tabCruft] = i
				break
			}
		}
	}
	m.clampCursors()
}

// cruftSize is a compact size: 512K, 7.5M, 263M, 1.5G.
func cruftSize(n int64) string {
	const k = 1024
	f := float64(n)
	unit := "B"
	switch {
	case n >= k*k*k:
		f, unit = f/(k*k*k), "G"
	case n >= k*k:
		f, unit = f/(k*k), "M"
	case n >= k:
		f, unit = f/k, "K"
	}
	if f < 10 && unit != "B" && unit != "K" {
		return fmt.Sprintf("%.1f%s", f, unit)
	}
	return fmt.Sprintf("%.0f%s", f, unit)
}

func cruftAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return "just now"
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 90*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return fmt.Sprintf("%dmo ago", int(d.Hours()/24/30))
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func worktreeLabel(w cruft.Worktree) string {
	if w.Branch != "" && !w.Detached {
		return strings.TrimPrefix(w.Branch, "refs/heads/")
	}
	return "(det) " + filepath.Base(w.Path)
}

func projectDirLabel(p cruft.ProjectDir) string {
	if len(p.CWDs) > 0 {
		return filepath.Base(p.CWDs[0])
	}
	return p.Name
}

func cruftSizeTail(n int64) (string, int) {
	return styleMuted.Render(fmt.Sprintf(" %5s", cruftSize(n))), 6
}

func cruftHeader(label, tail string) rowItem {
	return rowItem{isHeader: true, label: label, tail: styleMuted.Render(tail), tailW: len([]rune(tail)), dataIdx: -1}
}

// cruftLayout builds the list rows and the flat entry slice together, so rows,
// detail and keys can never disagree: a selectable row's dataIdx indexes the
// returned entries, and the cursor (the Nth selectable row) is that index.
func (m Model) cruftLayout() ([]rowItem, []cruftEntry) {
	r := m.cruft
	if r == nil {
		label := "· press r to scan"
		if m.cruftScanning {
			label = "· scanning…"
		}
		return []rowItem{{isHeader: true, label: label, dataIdx: -1}}, nil
	}
	var rows []rowItem
	var ents []cruftEntry
	add := func(row rowItem, e cruftEntry) {
		row.dataIdx = len(ents)
		if row.ind == "" {
			row.ind, row.indW = " ", 1
		}
		rows = append(rows, row)
		ents = append(ents, e)
	}

	if len(r.Worktrees) > 0 {
		var total int64
		for _, w := range r.Worktrees {
			total += w.Size
		}
		rows = append(rows, cruftHeader("Worktrees", cruftSize(total)))
		for i, w := range r.Worktrees {
			dot := styleMuted.Render("●")
			switch {
			case w.Removable:
				dot = styleGreen.Render("●")
			case w.Dirty || (w.MergedKnown && !w.Merged):
				dot = styleYellow.Render("●")
			}
			tail, tw := cruftSizeTail(w.Size)
			add(rowItem{ind: dot, indW: 1, label: worktreeLabel(w), tail: tail, tailW: tw}, cruftEntry{ckWorktree, i})
		}
	}

	if len(r.Projects) > 0 {
		var total int64
		for _, p := range r.Projects {
			total += p.Size
		}
		rows = append(rows, cruftHeader("Transcripts", cruftSize(total)))
		live := 0
		hidden := 0
		for i, p := range r.Projects {
			if !p.Dead {
				live++
				if live > maxLiveTranscriptRows {
					hidden++
					continue
				}
			}
			tail := styleMuted.Render(fmt.Sprintf(" %5s", cruftSize(p.Size)))
			if p.Trashable {
				tail += " " + styleYellow.Render("⌫")
			} else {
				tail += "  "
			}
			add(rowItem{label: projectDirLabel(p), tail: tail, tailW: 8}, cruftEntry{ckProject, i})
		}
		if hidden > 0 {
			rows = append(rows, rowItem{isHeader: true, label: fmt.Sprintf("  … %d more", hidden), dataIdx: -1})
		}
	}

	if len(r.DeadEntries) > 0 {
		rows = append(rows, cruftHeader("Dead projects", fmt.Sprint(len(r.DeadEntries))))
		for i, p := range r.DeadEntries {
			add(rowItem{label: filepath.Base(p)}, cruftEntry{ckDead, i})
		}
	}

	if len(r.Memories) > 0 {
		rows = append(rows, cruftHeader("Memories", fmt.Sprint(len(r.Memories))))
		for i, is := range r.Memories {
			add(rowItem{label: is.File + " " + is.Kind.String()}, cruftEntry{ckMemory, i})
		}
	}

	if len(r.Swept) > 0 {
		var total int64
		for _, s := range r.Swept {
			total += s.Size
		}
		rows = append(rows, cruftHeader("Auto-swept", cruftSize(total)))
		for i, s := range r.Swept {
			tail, tw := cruftSizeTail(s.Size)
			add(rowItem{label: s.Name, tail: tail, tailW: tw}, cruftEntry{ckSwept, i})
		}
	}

	rows = append(rows, cruftHeader("Plugin cache", cruftSize(r.PluginCache.Total)))
	add(rowItem{label: "stale copies"}, cruftEntry{ckCache, 0})

	rows = append(rows, rowItem{isHeader: true, label: "Desktop storage", dataIdx: -1})
	add(rowItem{label: "Claude Desktop's own"}, cruftEntry{ckDesktop, 0})
	return rows, ents
}

func (m Model) cruftRows() []rowItem {
	rows, _ := m.cruftLayout()
	return rows
}

func (m Model) cruftEntries() []cruftEntry {
	_, ents := m.cruftLayout()
	return ents
}

// selectedCruft returns the entry under the cursor.
func (m Model) selectedCruft() (cruftEntry, bool) {
	ents := m.cruftEntries()
	c := m.cursors[tabCruft]
	if c < 0 || c >= len(ents) {
		return cruftEntry{}, false
	}
	return ents[c], true
}

func (m *Model) setCruftStatus(s string, d time.Duration) tea.Cmd {
	m.statusMsg = s
	return clearStatusAfter(d)
}

// removeCruftSelected handles x: confirm a worktree removal.
func (m Model) removeCruftSelected() (Model, tea.Cmd) {
	e, ok := m.selectedCruft()
	if !ok {
		return m, nil
	}
	if e.kind != ckWorktree {
		return m, m.setCruftStatus(styleYellow.Render("x removes worktrees — select one"), 3*time.Second)
	}
	w := m.cruft.Worktrees[e.idx]
	if !w.Removable {
		why := w.Why
		if why == "" {
			why = "not removable"
		}
		return m, m.setCruftStatus(styleYellow.Render("not removable: "+why), 3*time.Second)
	}
	m.pendingCruft = &cruftTarget{kind: ckWorktree, name: worktreeLabel(w), path: w.Path, size: w.Size}
	m.detail.GotoTop()
	m.detail.SetContent(m.detailContent())
	return m, nil
}

// trashCruftSelected handles t: confirm trashing a dead project's transcripts.
func (m Model) trashCruftSelected() (Model, tea.Cmd) {
	e, ok := m.selectedCruft()
	if !ok {
		return m, nil
	}
	if e.kind != ckProject {
		return m, m.setCruftStatus(styleYellow.Render("t trashes transcript folders — select one"), 3*time.Second)
	}
	p := m.cruft.Projects[e.idx]
	if !p.Trashable {
		why := p.Why
		if why == "" {
			why = "not trashable"
		}
		return m, m.setCruftStatus(styleYellow.Render("not trashable: "+why), 3*time.Second)
	}
	m.pendingCruft = &cruftTarget{kind: ckProject, name: projectDirLabel(p), path: p.Dir, size: p.Size}
	m.detail.GotoTop()
	m.detail.SetContent(m.detailContent())
	return m, nil
}

// updateConfirmCruft handles the y/n answer; anything but y cancels.
func (m Model) updateConfirmCruft(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "y", "Y":
		t := *m.pendingCruft
		m.pendingCruft = nil
		m.detail.SetContent(m.detailContent())
		return m, func() tea.Msg {
			if t.kind == ckWorktree {
				return cruftActionMsg{what: "removed worktree " + t.name, err: removeWorktree(t.path)}
			}
			return cruftActionMsg{what: "moved " + t.name + " transcripts to the Trash", err: trashProjectDir(t.path)}
		}
	default:
		m.pendingCruft = nil
		m.detail.SetContent(m.detailContent())
		return m, m.setCruftStatus(styleMuted.Render("· cancelled"), 2*time.Second)
	}
}

func (m Model) cruftPrompt() string {
	t := m.pendingCruft
	verb := "remove worktree"
	if t.kind == ckProject {
		verb = "trash transcripts"
	}
	return styleYellow.Render(fmt.Sprintf("%s %s (%s)? ", verb, t.name, cruftSize(t.size))) +
		stylePrimary.Render("y") + styleMuted.Render("/N")
}

// markMemoryIssues indexes the last report's memory issues by project and file.
func (m Model) memoryIssuesFor(project, file string) []cruft.MemoryIssue {
	if m.cruft == nil {
		return nil
	}
	var out []cruft.MemoryIssue
	for _, is := range m.cruft.Memories {
		if is.Project == project && is.File == file {
			out = append(out, is)
		}
	}
	return out
}

// ── detail ───────────────────────────────────────────────────────────────────

func noteLines(s string, w int) []string {
	var out []string
	for _, l := range wrapWords(s, w) {
		out = append(out, styleMuted.Render(l))
	}
	return out
}

func (m Model) cruftDetail() string {
	w := m.detail.Width
	r := m.cruft
	if r == nil {
		if m.cruftScanning {
			return emptyState("scanning ~/.claude and your repos…")
		}
		return emptyState("press r to scan")
	}
	lines := []string{styleMuted.Render(fmt.Sprintf("scan %s · %d errors", r.ScannedAt.Format("15:04"), len(r.Errors))), ""}
	if t := m.pendingCruft; t != nil {
		lines = append(lines, styleYellow.Render("Will remove exactly:"), kvWrap("Path", shortPath(t.path), w), "")
	}
	e, ok := m.selectedCruft()
	if ok {
		lines = append(lines, m.cruftEntryDetail(e, w)...)
	}
	if len(r.Errors) > 0 {
		lines = append(lines, "", styleMuted.Render("Scan errors"))
		for _, er := range r.Errors {
			lines = append(lines, noteLines("· "+er, w)...)
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) cruftEntryDetail(e cruftEntry, w int) []string {
	r := m.cruft
	var l []string
	switch e.kind {
	case ckWorktree:
		wt := r.Worktrees[e.idx]
		l = append(l, styleDetailTitle.Render(worktreeLabel(wt)), "",
			kvWrap("Path", shortPath(wt.Path), w),
			kvWrap("Repo", shortPath(wt.Repo), w))
		if wt.Detached {
			l = append(l, kv("Branch", "detached at "+shortHEAD(wt.HEAD)))
		} else {
			l = append(l, kv("Branch", strings.TrimPrefix(wt.Branch, "refs/heads/")+" @ "+shortHEAD(wt.HEAD)))
		}
		def := wt.DefaultBranch
		if def == "" {
			def = "default branch"
		}
		merged := "unknown"
		if wt.MergedKnown {
			merged = yesNo(wt.Merged)
		}
		dirty := "unknown"
		if wt.DirtyKnown {
			dirty = yesNo(wt.Dirty)
		}
		last := "unknown"
		if !wt.LastCommit.IsZero() {
			last = wt.LastCommit.Format("2006-01-02") + " (" + cruftAgo(wt.LastCommit) + ")"
		}
		pids := "none"
		if len(wt.LivePIDs) > 0 {
			pids = strings.Trim(fmt.Sprint(wt.LivePIDs), "[]")
		}
		l = append(l,
			kvWrap("Merged", merged+" (into "+def+")", w),
			kv("Uncommitted", dirty),
			kvWrap("Last commit", last, w),
			kv("Size", cruftSize(wt.Size)),
			kv("Live PIDs", pids), "")
		if wt.Removable {
			l = append(l, styleVal.Render("x  remove with git worktree remove"), styleMuted.Render("   (asks first; branch kept)"))
		} else {
			l = append(l, noteLines("Not removable: "+wt.Why, w)...)
		}
		l = append(l, "")
		l = append(l, noteLines("Never touches a worktree with uncommitted changes.", w)...)
		if strings.Contains(wt.Path, "/.claude/worktrees/") {
			l = append(l, noteLines("Made by Claude Desktop: its session checks the branch out again when resumed.", w)...)
		}

	case ckProject:
		p := r.Projects[e.idx]
		l = append(l, styleDetailTitle.Render(projectDirLabel(p)), "")
		if len(p.CWDs) == 0 {
			l = append(l, kv("Project", "(none recorded)"))
		}
		for i, c := range p.CWDs {
			k := ""
			if i == 0 {
				k = "Project"
			}
			l = append(l, kvWrap(k, shortPath(c), w))
		}
		status := "alive"
		switch {
		case !p.DeadKnown:
			status = "unknown"
		case p.Dead:
			status = "dead (folder gone)"
		}
		l = append(l,
			kv("Status", status),
			kvWrap("Transcripts", fmt.Sprintf("%d (%d Desktop, %d CLI, %d other)", p.Transcripts, p.Desktop, p.CLI, p.Other), w),
			kv("Old", fmt.Sprintf("%d older than %d days", p.OlderThanCutoff, r.CutoffDays)),
			kv("Size", cruftSize(p.Size)))
		if !p.Newest.IsZero() {
			l = append(l, kvWrap("Newest", p.Newest.Format("2006-01-02")+" ("+cruftAgo(p.Newest)+")", w))
		}
		l = append(l, kv("Memories", yesNo(p.HasMemories)), kvWrap("Folder", shortPath(p.Dir), w), "")
		if p.Trashable {
			l = append(l, styleVal.Render("t  move this folder to the Trash"), styleMuted.Render("   (asks first)"))
		} else {
			l = append(l, noteLines("Not trashable: "+p.Why, w)...)
		}
		if !p.Dead && p.OlderThanCutoff > 0 && p.Desktop > 0 {
			l = append(l, "")
			l = append(l, noteLines(fmt.Sprintf("Claude Code keeps Desktop transcripts forever unless desktopSessionCleanupPeriodDays is set; CLI transcripts go after %d days.", r.CutoffDays), w)...)
		}

	case ckDead:
		p := r.DeadEntries[e.idx]
		l = append(l, styleDetailTitle.Render(filepath.Base(p)), "",
			kvWrap("Path", shortPath(p), w), "")
		l = append(l, noteLines("Listed in ~/.claude.json projects; clasp never edits that file.", w)...)

	case ckMemory:
		is := r.Memories[e.idx]
		l = append(l, styleDetailTitle.Render(is.File), "",
			kvWrap("Project", projectLabel(is.Project), w),
			kv("File", is.File),
			kvWrap("Issue", is.Kind.String(), w))
		if is.Detail != "" {
			l = append(l, kvWrap("Detail", is.Detail, w))
		}
		l = append(l, "")
		l = append(l, noteLines("Delete or fix it on the Memory tab (d deletes).", w)...)

	case ckSwept:
		s := r.Swept[e.idx]
		l = append(l, styleDetailTitle.Render(s.Name), "",
			kvWrap("Path", shortPath(s.Path), w),
			kv("Size", cruftSize(s.Size)),
			kv("Files", fmt.Sprint(s.Files)), "")
		l = append(l, noteLines(fmt.Sprintf("Claude Code deletes these after %d days (cleanupPeriodDays).", r.CutoffDays), w)...)

	case ckCache:
		c := r.PluginCache
		l = append(l, styleDetailTitle.Render("Plugin cache"), "",
			kv("Total", cruftSize(c.Total)),
			kv("Stale", fmt.Sprintf("%s in %d copies", cruftSize(c.Stale), c.StaleCopies)), "")
		l = append(l, noteLines("See the Plugins tab: stale copies show Claude Code's deletion date.", w)...)

	case ckDesktop:
		l = append(l, styleDetailTitle.Render("Desktop storage"), "")
		l = append(l, noteLines("Settings › This computer › Storage in Claude Desktop reports worktrees, history, caches and Claude Code versions, and cleans inactive worktrees. clasp doesn't measure Desktop's folders.", w)...)
	}
	return l
}

func shortHEAD(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
