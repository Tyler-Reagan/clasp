package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/tylerreagan/clasp/internal/inventory"
	"github.com/tylerreagan/clasp/internal/state"
)

var copyDay = time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)

func stale(v string) inventory.Copy {
	return inventory.Copy{Status: inventory.CopyStale, Side: inventory.SideCLI, Path: "/home/u/.claude/plugins/cache/m/p/" + v, Version: v, Size: 32 * 1024, ModTime: copyDay, Why: "older cache version; Claude Code sweeps it"}
}

func copiesModel(width int, desktop bool, plugins ...inventory.Item) Model {
	m := Model{
		st:     &state.State{Inventory: &inventory.Inventory{HasDesktop: desktop, Plugins: plugins}},
		keys:   defaultKeys(),
		tab:    tabPlugins,
		width:  width,
		height: 40,
		ready:  true,
	}
	vw, vh := m.viewportDims()
	m.detail.Width, m.detail.Height = vw, vh
	m.refreshBindings()
	m.detail.SetContent(m.detailContent())
	return m
}

func staleItem() inventory.Item {
	a, b := hashed("aaa"), hashed("bbb")
	it := inventory.Item{Kind: inventory.KindPlugin, Name: "a-plugin-with-a-really-long-name-that-has-to-truncate",
		Installs: []inventory.Install{cliIn(inventory.OriginMarketplace, a, true), dskIn(inventory.OriginRPM, b, true)}}
	it.Copies = []inventory.Copy{{Status: inventory.CopyActive, Side: inventory.SideCLI, Path: "/home/u/.claude/plugins/cache/m/p/9.9.9", Version: "9.9.9", Size: 48 * 1024, ModTime: copyDay}}
	for i := 0; i < 7; i++ {
		it.Copies = append(it.Copies, stale(string(rune('a'+i))))
	}
	return it
}

func hashed(h string) string { return h }

func leftoverItem(trashable bool) inventory.Item {
	return inventory.Item{Kind: inventory.KindPlugin, Name: "gone", Copies: []inventory.Copy{{
		Status: inventory.CopyLeftover, Side: inventory.SideCLI, Path: "/home/u/.claude/plugins/data/gone",
		Size: 16 * 1024, ModTime: copyDay, Trashable: trashable, Why: "uninstalled; nothing sweeps this",
	}}}
}

func TestStaleBadgeAndTruncation(t *testing.T) {
	for _, w := range []int{80, 100} {
		m := copiesModel(w, true, staleItem())
		lines := listLines(m)
		var row string
		for _, l := range lines {
			if strings.Contains(l, "a-plugin") {
				row = l
			}
		}
		if !strings.Contains(row, "≠ ◌7") || !strings.Contains(row, "…") {
			t.Errorf("w=%d row = %q", w, row)
		}
		lw := lipgloss.Width(lines[0])
		for _, l := range lines {
			if lipgloss.Width(l) > lw {
				t.Errorf("w=%d line wider than list (%d > %d): %q", w, lipgloss.Width(l), lw, l)
			}
		}
	}
}

func TestLeftoverRowAndMismatchedFilter(t *testing.T) {
	m := copiesModel(100, true, leftoverItem(true), staleItem())
	m.srcFilter[tabPlugins] = srcMismatched
	var row string
	for _, l := range listLines(m) {
		if strings.Contains(l, "gone") {
			row = l
		}
	}
	if !strings.Contains(row, "⌫") || !strings.HasSuffix(strings.TrimRight(row, " "), "·   ·") {
		t.Errorf("leftover row = %q", row)
	}
}

func TestCopiesDetail(t *testing.T) {
	m := copiesModel(100, true, leftoverItem(true))
	d := plain(m.invDetail(tabPlugins))
	for _, want := range []string{"On-disk copies", "leftover", "16K", "2026-03-09", "uninstalled; nothing sweeps this", "t  move 1 leftover to the Trash (asks first)"} {
		if !strings.Contains(d, want) {
			t.Errorf("detail missing %q:\n%s", want, d)
		}
	}
	m = copiesModel(100, true, leftoverItem(false))
	d = plain(m.invDetail(tabPlugins))
	if !strings.Contains(d, "nothing here can be moved to the Trash") || strings.Contains(d, "t  move") {
		t.Errorf("non-trashable detail:\n%s", d)
	}
	m = copiesModel(100, true, staleItem())
	d = plain(m.invDetail(tabPlugins))
	if strings.Contains(d, "Trash") || strings.Index(d, "active") > strings.Index(d, "stale") {
		t.Errorf("stale detail:\n%s", d)
	}
}

func TestCopySummaryWithoutDesktop(t *testing.T) {
	m := copiesModel(100, false, staleItem(), leftoverItem(true))
	if got := plain(m.invSummary(tabPlugins)); got != "7 stale · 1 leftover" {
		t.Errorf("summary = %q", got)
	}
	if got := plain(copiesModel(100, false, inventory.Item{Name: "x"}).invSummary(tabPlugins)); got != "" {
		t.Errorf("empty summary = %q", got)
	}
	if !strings.Contains(plain(m.renderStatus()), "7 stale · 1 leftover") {
		t.Error("status bar missing copy summary")
	}
}

func TestTrashNothingTrashable(t *testing.T) {
	m := copiesModel(100, true, leftoverItem(false))
	m, cmd := key1(m, "t")
	if m.pendingTrash != nil || cmd == nil || !strings.Contains(plain(m.statusMsg), "nothing trashable here") {
		t.Errorf("pending=%v status=%q", m.pendingTrash, m.statusMsg)
	}
}

func TestTrashConfirmCancelAndYes(t *testing.T) {
	m := copiesModel(100, true, leftoverItem(true))
	m, _ = key1(m, "t")
	if m.pendingTrash == nil {
		t.Fatal("no pending trash")
	}
	if got := plain(m.renderStatus()); !strings.Contains(got, "trash 1 leftover (16K)? y/N") {
		t.Errorf("status = %q", got)
	}
	n, _ := key1(m, "n")
	if n.pendingTrash != nil {
		t.Error("n did not cancel")
	}

	var gotKind inventory.Kind
	var gotName string
	old := trashLeftovers
	defer func() { trashLeftovers = old }()
	trashLeftovers = func(k inventory.Kind, name string) ([]string, map[string]string, error) {
		gotKind, gotName = k, name
		return []string{"/a"}, map[string]string{"/b": "outside root"}, nil
	}
	y, cmd := key1(m, "y")
	if y.pendingTrash != nil || cmd == nil {
		t.Fatalf("pending=%v cmd=%v", y.pendingTrash, cmd != nil)
	}
	msg := cmd()
	if gotKind != inventory.KindPlugin || gotName != "gone" {
		t.Errorf("called with %v %q", gotKind, gotName)
	}
	r, rc := y.Update(msg)
	got := plain(r.(Model).statusMsg)
	if rc == nil || got != "moved 1 to the Trash · 1 refused: outside root" {
		t.Errorf("status = %q", got)
	}

	trashLeftovers = func(inventory.Kind, string) ([]string, map[string]string, error) {
		return nil, nil, errors.New("not found")
	}
	_, cmd = key1(m, "y")
	r, _ = y.Update(cmd())
	if !strings.Contains(plain(r.(Model).statusMsg), "not found") {
		t.Errorf("err status = %q", r.(Model).statusMsg)
	}
}

// A status message used to push the hint bar onto a second line: help.Model
// stops truncating once its "…" no longer fits.
func TestStatusBarStaysOneLineWithMessage(t *testing.T) {
	for _, w := range []int{80, 100} {
		m := copiesModel(w, true, leftoverItem(false))
		m.statusMsg = styleYellow.Render("not toggleable here (no Claude Code marketplace install)")
		bar := m.renderStatus()
		if strings.Contains(bar, "\n") {
			t.Fatalf("width %d: status bar wrapped:\n%s", w, bar)
		}
		if got := lipgloss.Width(bar); got != w {
			t.Fatalf("width %d: status bar is %d wide", w, got)
		}
	}
}
