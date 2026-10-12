package ui

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tylerreagan/clasp/internal/inventory"
	"github.com/tylerreagan/clasp/internal/state"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

func cliIn(origin, hash string, enabled bool) inventory.Install {
	return inventory.Install{Side: inventory.SideCLI, Origin: origin, Path: "/p/cli/" + origin, Version: "1.2.3", Enabled: enabled, Hash: hash}
}
func dskIn(origin, hash string, enabled bool) inventory.Install {
	return inventory.Install{Side: inventory.SideDSK, Origin: origin, Path: "/p/dsk/" + origin, Version: "1.2.3", Enabled: enabled, Hash: hash}
}

func invFixture(desktop bool) *inventory.Inventory {
	mp := cliIn(inventory.OriginMarketplace, "aaaaaaaa11112222", true)
	mp.PluginID, mp.Plugin = "tdd@market", "tdd"
	synced := cliIn(inventory.OriginSynced, "bbbbbbbb", true)
	synced.Plugin = "tdd"
	rpm := dskIn(inventory.OriginRPM, "bbbbbbbb", true)
	rpm.Plugin = "tdd"
	rpm.Desc = "Test-driven development"
	same := func(side inventory.Side) inventory.Install {
		return inventory.Install{Side: side, Origin: inventory.OriginPersonal, Path: "/p/same", Enabled: true, Hash: "cccc"}
	}
	return &inventory.Inventory{
		HasDesktop: desktop,
		Skills: []inventory.Item{
			{Kind: inventory.KindSkill, Name: "Zeta", Installs: []inventory.Install{same(inventory.SideCLI), same(inventory.SideDSK)}},
			{Kind: inventory.KindSkill, Name: "tdd", Installs: []inventory.Install{mp, synced, rpm}},
			{Kind: inventory.KindSkill, Name: "a-very-long-skill-name-that-overflows-the-column", Installs: []inventory.Install{cliIn(inventory.OriginPersonal, "x", true)}},
			{Kind: inventory.KindSkill, Name: "cli-only", Installs: []inventory.Install{cliIn(inventory.OriginPersonal, "y", false)}},
			{Kind: inventory.KindSkill, Name: "dsk-only", Installs: []inventory.Install{dskIn(inventory.OriginAnthropicSkills, "z", true)}},
			{Kind: inventory.KindSkill, Name: "alpha", Installs: []inventory.Install{same(inventory.SideCLI), same(inventory.SideDSK)}},
		},
		Plugins: []inventory.Item{
			{Kind: inventory.KindPlugin, Name: "Notion", Installs: []inventory.Install{mp}},
			{Kind: inventory.KindPlugin, Name: "design", Installs: []inventory.Install{rpm}},
			{Kind: inventory.KindPlugin, Name: "apple", Installs: []inventory.Install{cliIn(inventory.OriginPersonal, "q", true)}},
		},
		MCP: []inventory.Item{
			{Kind: inventory.KindMCP, Name: "linear", Installs: []inventory.Install{{Side: inventory.SideCLI, Origin: inventory.OriginAccount, Enabled: true}}},
			{Kind: inventory.KindMCP, Name: "local", Installs: []inventory.Install{{Side: inventory.SideCLI, Origin: inventory.OriginSettings, Enabled: true}}},
		},
		Errs: []error{errors.New("boom: could not read x")},
	}
}

func invModel(width int, desktop bool) Model {
	m := Model{
		st:     &state.State{Inventory: invFixture(desktop), Plugins: []state.Plugin{{ID: "tdd@market", SkillNames: []string{"red-green"}}}},
		keys:   defaultKeys(),
		tab:    tabSkills,
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

func key1(m Model, s string) (Model, tea.Cmd) {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	if s == " " {
		msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	}
	n, c := m.Update(msg)
	return n.(Model), c
}

func listLines(m Model) []string {
	lines := strings.Split(plain(m.renderList()), "\n")
	var out []string
	for _, l := range lines[1 : len(lines)-1] { // drop the top and bottom border
		out = append(out, strings.Trim(l, "│"))
	}
	return out
}

func TestInventoryHeaderAndColumnAlignment(t *testing.T) {
	for _, w := range []int{80, 100} {
		m := invModel(w, true)
		lines := listLines(m)
		if !strings.Contains(lines[0], "filter: all") || !strings.Contains(lines[0], "s▾") {
			t.Errorf("w=%d filter header = %q", w, lines[0])
		}
		hdr := lines[1]
		if !strings.HasPrefix(strings.TrimSpace(hdr), "NAME") || !strings.HasSuffix(strings.TrimRight(hdr, " "), "CLI DSK") {
			t.Fatalf("w=%d header = %q", w, hdr)
		}
		hw := lipgloss.Width(hdr)
		cliEnd := strings.Index(hdr, "CLI") + 3
		dskEnd := strings.Index(hdr, "DSK") + 3
		for _, l := range lines[2:8] {
			if lipgloss.Width(l) != hw {
				t.Errorf("w=%d row width %d != header %d: %q", w, lipgloss.Width(l), hw, l)
			}
			r := []rune(l)
			if !strings.ContainsRune("●○·◇", r[cliEnd-1]) || !strings.ContainsRune("●○·◇", r[dskEnd-1]) {
				t.Errorf("w=%d marks not under CLI/DSK: %q", w, l)
			}
		}
	}
}

func TestInventoryRowsSortedMarksAndTruncation(t *testing.T) {
	m := invModel(100, true)
	lines := listLines(m)
	body := strings.Join(lines, "\n")
	if !strings.Contains(body, "…") {
		t.Errorf("long name not truncated:\n%s", body)
	}
	// case-insensitive: alpha ... tdd ... Zeta last
	order := []string{"a-very", "alpha", "cli-only", "dsk-only", "tdd", "Zeta"}
	last := -1
	for _, n := range order {
		i := strings.Index(body, n)
		if i < 0 || i < last {
			t.Fatalf("row order wrong at %q:\n%s", n, body)
		}
		last = i
	}
	for _, l := range lines {
		if strings.Contains(l, "cli-only") && !strings.HasSuffix(strings.TrimRight(l, " "), "○   ·") {
			t.Errorf("disabled CLI-only row = %q", l)
		}
		if strings.Contains(l, "dsk-only") && !strings.HasSuffix(strings.TrimRight(l, " "), "·   ●") {
			t.Errorf("DSK-only row = %q", l)
		}
		if strings.Contains(l, "tdd") && !strings.Contains(l, "≠ ×2") {
			t.Errorf("tdd row missing flags: %q", l)
		}
	}
}

func TestInventoryPluginSortIgnoresCase(t *testing.T) {
	m := invModel(100, true)
	m.tab = tabPlugins
	items := m.invItems(tabPlugins)
	if items[0].Name != "apple" || items[1].Name != "design" || items[2].Name != "Notion" {
		t.Errorf("order = %v", []string{items[0].Name, items[1].Name, items[2].Name})
	}
}

func TestInventorySourceFilterCycle(t *testing.T) {
	m := invModel(100, true)
	want := []struct {
		label string
		rows  int
	}{{"CLI only", 2}, {"Desktop only", 1}, {"mismatched", 4}, {"all", 6}}
	for _, w := range want {
		m, _ = key1(m, "s")
		if got := listLines(m)[0]; !strings.Contains(got, "filter: "+w.label) {
			t.Errorf("header = %q, want %q", got, w.label)
		}
		if n := m.lenFor(tabSkills); n != w.rows {
			t.Errorf("%s: %d rows, want %d", w.label, n, w.rows)
		}
	}
}

func TestInventoryCursorSkipsHeadersAndClamps(t *testing.T) {
	m := invModel(100, true)
	if m.selectedItem().Name != "a-very-long-skill-name-that-overflows-the-column" {
		t.Errorf("first selection = %q", m.selectedItem().Name)
	}
	var marked string
	for _, l := range listLines(m) {
		if strings.Contains(l, "▌") {
			marked = l
		}
	}
	if strings.Contains(marked, "NAME") || strings.Contains(marked, "filter") {
		t.Errorf("cursor on header: %q", marked)
	}
	m, _ = key1(m, "G")
	if m.cursors[tabSkills] != 5 {
		t.Fatalf("G cursor = %d", m.cursors[tabSkills])
	}
	m, _ = key1(m, "s") // CLI only: 2 rows
	if m.cursors[tabSkills] != 0 || m.selectedItem() == nil {
		t.Errorf("cursor after filter = %d", m.cursors[tabSkills])
	}
	m.cursors[tabSkills] = 99
	m.clampCursors()
	if m.cursors[tabSkills] != 1 {
		t.Errorf("clamp = %d, want 1", m.cursors[tabSkills])
	}
}

func TestInventoryEmptyFilter(t *testing.T) {
	m := invModel(100, true)
	m.tab = tabMCP
	m.refreshBindings()
	m, _ = key1(m, "s") // CLI only -> both MCP items qualify
	m, _ = key1(m, "s") // Desktop only -> none
	if m.selectedItem() != nil {
		t.Fatal("selection on an empty list")
	}
	if got := plain(m.invDetail(tabMCP)); !strings.Contains(got, "nothing matches this filter") {
		t.Errorf("detail = %q", got)
	}
}

func TestInventoryAccountItem(t *testing.T) {
	m := invModel(100, true)
	m.tab = tabMCP
	m.refreshBindings()
	var row string
	for _, l := range listLines(m) {
		if strings.Contains(l, "linear") {
			row = l
		}
	}
	if !strings.HasSuffix(strings.TrimRight(row, " "), "◇   ◇") {
		t.Errorf("account row = %q", row)
	}
	if m.invItems(tabMCP)[0].Mismatched() {
		t.Error("account item counted as mismatched")
	}
	m.srcFilter[tabMCP] = srcMismatched
	for _, it := range m.invItems(tabMCP) {
		if it.Name == "linear" {
			t.Error("account item in mismatched filter")
		}
	}
	m.srcFilter[tabMCP] = srcAll
	if got := plain(m.invDetail(tabMCP)); !strings.Contains(got, "account (claude.ai)") {
		t.Errorf("detail missing account status: %s", got)
	}
}

func TestInventoryNoDesktop(t *testing.T) {
	m := invModel(100, false)
	body := strings.Join(listLines(m), "\n")
	if strings.Contains(body, "CLI") || strings.Contains(body, "DSK") || strings.Contains(body, "filter:") {
		t.Errorf("desktop chrome without Desktop:\n%s", body)
	}
	if m.keys.Source.Enabled() {
		t.Error("s should be disabled without Desktop")
	}
	m, _ = key1(m, "s")
	if m.srcFilter[tabSkills] != srcAll {
		t.Error("s changed the filter without Desktop")
	}
	if got := plain(m.invDetail(tabSkills)); strings.Contains(got, "Claude Desktop") || strings.Contains(got, "Columns show") {
		t.Errorf("detail mentions Desktop:\n%s", got)
	}
	if m.invSummary(tabSkills) != "" {
		t.Error("summary without Desktop")
	}
}

func TestInventorySourceKeyScope(t *testing.T) {
	m := invModel(100, true)
	if !m.keys.Source.Enabled() {
		t.Fatal("s should be enabled on Skills")
	}
	m.tab = tabSettings
	m.refreshBindings()
	if m.keys.Source.Enabled() {
		t.Error("s enabled on Settings")
	}
	m.tab = tabSkills
	m.focus = focusZoomedDetail
	m.refreshBindings()
	if m.keys.Source.Enabled() {
		t.Error("s enabled in zoom")
	}
}

func TestInventoryDetail(t *testing.T) {
	m := invModel(100, true)
	m.cursors[tabSkills] = 4 // tdd
	got := plain(m.invDetail(tabSkills))
	for _, want := range []string{
		"tdd", "on both", "≠ content differs between installs", "×2 loaded twice on Claude Code",
		"Claude Code", "Claude Desktop", "marketplace", "id       tdd@market", "synced from claude.ai", "Desktop plugin",
		"hash     aaaaaaaa", "version  1.2.3", "Test-driven development", "Columns show files on disk.",
		"1 read errors", "boom",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("detail missing %q:\n%s", want, got)
		}
	}
	m.cursors[tabSkills] = 1 // alpha is on both; cli-only is index 2
	m.cursors[tabSkills] = 2
	got = plain(m.invDetail(tabSkills))
	if !strings.Contains(got, "Claude Code only") || !strings.Contains(got, "· not found") || !strings.Contains(got, "disabled") {
		t.Errorf("cli-only detail:\n%s", got)
	}
	m.cursors[tabSkills] = 3
	got = plain(m.invDetail(tabSkills))
	if !strings.Contains(got, "Desktop only") || !strings.Contains(got, "anthropic-skills") {
		t.Errorf("dsk-only detail:\n%s", got)
	}
}

func TestInventoryPluginDetailKeepsContents(t *testing.T) {
	m := invModel(100, true)
	m.tab = tabPlugins
	m.cursors[tabPlugins] = 2 // Notion -> tdd@market
	got := plain(m.invDetail(tabPlugins))
	if !strings.Contains(got, "red-green") {
		t.Errorf("plugin contents missing:\n%s", got)
	}
}

func TestInventoryStatusSummary(t *testing.T) {
	m := invModel(100, true)
	if got := plain(m.invSummary(tabSkills)); got != "3 in both · 1 differ · 4 mismatched" {
		t.Errorf("summary = %q", got)
	}
	if !strings.Contains(plain(m.renderStatus()), "4 mismatched") {
		t.Error("status bar missing summary")
	}
}

func TestInventoryToggle(t *testing.T) {
	m := invModel(100, true)
	m.tab = tabPlugins
	m.refreshBindings()
	m.cursors[tabPlugins] = 1 // design: Desktop only
	m, cmd := key1(m, " ")
	if cmd == nil || !strings.Contains(plain(m.statusMsg), "not toggleable here") {
		t.Errorf("status = %q", m.statusMsg)
	}
	// only the clear-status tick, never a toggle
	m.cursors[tabPlugins] = 2 // Notion: marketplace install
	m.statusMsg = ""
	m, cmd = key1(m, " ")
	if cmd == nil || m.statusMsg != "" {
		t.Errorf("marketplace toggle: cmd=%v status=%q", cmd != nil, m.statusMsg)
	}
}
