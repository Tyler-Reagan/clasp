package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func navModel(width int, start tab) Model {
	m := invModel(width, true)
	m.tab = start
	for g := group(0); g < groupCount; g++ {
		m.lastTab[g] = groupTabs[g][0]
	}
	m.lastTab[groupOf(start)] = start
	m.refreshBindings()
	m.detail.SetContent(m.detailContent())
	return m
}

func TestGroupKeys(t *testing.T) {
	m := navModel(100, tabSkills)
	steps := []struct {
		key  string
		want tab
	}{
		{"]", tabSettings},
		{"]", tabCruft},
		{"]", tabSkills}, // wraps, lands on last-used Inventory tab
		{"[", tabCruft},
		{"[", tabSettings},
		{"[", tabSkills},
	}
	for i, s := range steps {
		m, _ = key1(m, s.key)
		if m.tab != s.want {
			t.Fatalf("step %d (%s): tab = %v, want %v", i, s.key, tabNames[m.tab], tabNames[s.want])
		}
	}
}

func TestTabCycleStaysInGroup(t *testing.T) {
	m := navModel(100, tabSettings)
	m, _ = key1(m, "l")
	if m.tab != tabSettings {
		t.Errorf("l on Settings -> %v", tabNames[m.tab])
	}
	m, _ = key1(m, "h")
	if m.tab != tabSettings {
		t.Errorf("h on Settings -> %v", tabNames[m.tab])
	}
	m = navModel(100, tabMemory)
	m, _ = key1(m, "l")
	if m.tab != tabSessions {
		t.Errorf("l on Memory -> %v, want Sessions", tabNames[m.tab])
	}
	m, _ = key1(m, "h")
	if m.tab != tabMemory {
		t.Errorf("h on Sessions -> %v, want Memory", tabNames[m.tab])
	}
	for i := 0; i < 12; i++ {
		m, _ = key1(m, "l")
		if groupOf(m.tab) != groupInventory {
			t.Fatalf("l left the group: %v", tabNames[m.tab])
		}
	}
}

func TestGroupRemembersLastTab(t *testing.T) {
	m := navModel(100, tabSkills)
	m, _ = key1(m, "l") // Plugins
	m, _ = key1(m, "l") // MCP
	m, _ = key1(m, "]")
	m, _ = key1(m, "[")
	if m.tab != tabMCP {
		t.Errorf("back in Inventory landed on %v, want MCP", tabNames[m.tab])
	}
	if !m.keys.Toggle.Enabled() && m.tab == tabPlugins {
		t.Error("bindings stale")
	}
}

func TestGroupKeysExitZoom(t *testing.T) {
	for _, k := range []string{"]", "["} {
		m := navModel(100, tabSkills)
		m.focus = focusZoomedDetail
		m.refreshBindings()
		m, _ = key1(m, k)
		if m.focus != focusList {
			t.Errorf("%s did not exit zoom", k)
		}
		if groupOf(m.tab) == groupInventory {
			t.Errorf("%s stayed in Inventory (%v)", k, tabNames[m.tab])
		}
	}
	m := navModel(100, tabMemory)
	m.focus = focusZoomedDetail
	m, _ = key1(m, "l")
	if m.focus != focusList || m.tab != tabSessions {
		t.Errorf("zoom + l: focus=%v tab=%v", m.focus, tabNames[m.tab])
	}
}

func TestCruftTabEmpty(t *testing.T) {
	m := navModel(100, tabCruft)
	if n := m.listLen(); n != 0 {
		t.Errorf("cruft listLen = %d", n)
	}
	_ = m.detailContent()
	if v := plain(m.View()); !strings.Contains(v, "Cruft scan arrives") || !strings.Contains(v, "in the next step") {
		t.Error("empty-state message missing")
	}
}

func TestGroupedHeaderFits(t *testing.T) {
	for _, w := range []int{80, 100} {
		for tb := tab(0); tb < tabCount; tb++ {
			for _, zoom := range []bool{false, true} {
				m := navModel(w, tb)
				if zoom {
					m.focus = focusZoomedDetail
				}
				h := m.renderHeader()
				if got := lipgloss.Height(h); got != len(wordmarkLines) {
					t.Errorf("w=%d %v: header height %d", w, tabNames[tb], got)
				}
				for i, l := range strings.Split(h, "\n") {
					if lipgloss.Width(l) > w {
						t.Errorf("w=%d %v header line %d width %d", w, tabNames[tb], i, lipgloss.Width(l))
					}
				}
				for i, l := range strings.Split(m.View(), "\n") {
					if lipgloss.Width(l) > w {
						t.Errorf("w=%d %v view line %d width %d", w, tabNames[tb], i, lipgloss.Width(l))
					}
				}
			}
		}
	}
	lines := strings.Split(plain(navModel(80, tabSkills).renderHeader()), "\n")
	if !strings.Contains(lines[2], "Inventory") || !strings.Contains(lines[3], "› ") || !strings.Contains(lines[3], "Memory") {
		t.Errorf("header rows wrong:\n%s", strings.Join(lines, "\n"))
	}
	// A too-narrow terminal truncates rather than wraps.
	if h := navModel(40, tabSkills).renderHeader(); lipgloss.Height(h) != 4 {
		t.Errorf("narrow header height %d", lipgloss.Height(h))
	}
}
