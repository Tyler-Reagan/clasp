package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tylerreagan/clasp/internal/inventory"
	"github.com/tylerreagan/clasp/internal/state"
)

// Source filter states, cycled by the s key on Skills, Plugins and MCP.
const (
	srcAll = iota
	srcCLI
	srcDSK
	srcMismatched
	srcCount
)

var srcNames = [srcCount]string{"all", "CLI only", "Desktop only", "mismatched"}

const loadNote = "Columns show files on disk. Desktop's Code tab also reads ~/.claude and adds account-synced plugins; which copy loads is not documented."

// invTab reports whether t is one of the three inventory-driven tabs.
func invTab(t tab) bool { return t == tabSkills || t == tabPlugins || t == tabMCP }

func (m Model) hasDesktop() bool {
	return m.st != nil && m.st.Inventory != nil && m.st.Inventory.HasDesktop
}

// invAll is the unfiltered, unsorted item slice for an inventory tab.
func (m Model) invAll(t tab) []inventory.Item {
	if m.st == nil || m.st.Inventory == nil {
		return nil
	}
	switch t {
	case tabSkills:
		return m.st.Inventory.Skills
	case tabPlugins:
		return m.st.Inventory.Plugins
	case tabMCP:
		return m.st.Inventory.MCP
	}
	return nil
}

// invItems is the tab's items after the source filter, sorted by name without
// regard to case. The cursor on an inventory tab indexes this slice.
func (m Model) invItems(t tab) []inventory.Item {
	all := m.invAll(t)
	f := m.srcFilter[t]
	if !m.hasDesktop() {
		f = srcAll
	}
	out := make([]inventory.Item, 0, len(all))
	for _, it := range all {
		switch f {
		case srcCLI:
			if !it.OnlyCLI() {
				continue
			}
		case srcDSK:
			if !it.OnlyDSK() {
				continue
			}
		case srcMismatched:
			if !it.Mismatched() {
				continue
			}
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// selectedItem is the inventory item under the cursor, or nil.
func (m Model) selectedItem() *inventory.Item {
	if !invTab(m.tab) {
		return nil
	}
	items := m.invItems(m.tab)
	c := m.cursors[m.tab]
	if c < 0 || c >= len(items) {
		return nil
	}
	return &items[c]
}

// cycleSource advances the current tab's source filter and puts the cursor
// back on the first row.
func (m *Model) cycleSource() {
	m.srcFilter[m.tab] = (m.srcFilter[m.tab] + 1) % srcCount
	m.cursors[m.tab] = clamp(0, 0, m.lenFor(m.tab)-1)
}

// ── list ─────────────────────────────────────────────────────────────────────

// invMark is the 3-column mark for one side.
func invMark(it inventory.Item, side inventory.Side) string {
	glyph := styleMuted.Render("·")
	switch {
	case it.Account():
		glyph = styleCoral.Render("◇")
	case it.OnSide(side):
		glyph = styleMuted.Render("○")
		for _, in := range it.Installs {
			if in.Side == side && in.Enabled {
				glyph = styleGreen.Render("●")
				break
			}
		}
	}
	return "  " + glyph
}

// invFlags is the plain-text flag run ("≠", "×2", "◌3", "⌫") for an item.
func invFlags(it inventory.Item) string {
	var f []string
	if it.Differs() {
		f = append(f, "≠")
	}
	if it.Twice(inventory.SideCLI) || it.Twice(inventory.SideDSK) {
		f = append(f, "×2")
	}
	if n := it.StaleCount(); n > 0 {
		f = append(f, fmt.Sprintf("◌%d", n))
	}
	if len(it.Leftovers()) > 0 {
		f = append(f, "⌫")
	}
	return strings.Join(f, " ")
}

// invRows builds the list rows: with a Desktop store, a filter header and a
// NAME / CLI DSK header, then one row per item. dataIdx indexes invItems.
func (m Model) invRows(t tab) []rowItem {
	items := m.invItems(t)
	var rows []rowItem
	desk := m.hasDesktop()
	if desk {
		rows = append(rows,
			rowItem{isHeader: true, label: "filter: " + srcNames[m.srcFilter[t]] + "  s▾", dataIdx: -1},
			rowItem{isHeader: true, label: "NAME", tail: styleMuted.Render("CLI DSK") + " ", tailW: 8, dataIdx: -1},
		)
		if len(items) == 0 {
			rows = append(rows, rowItem{isHeader: true, label: "· no matches", dataIdx: -1})
		}
	}
	for i, it := range items {
		r := rowItem{label: it.Name, dataIdx: i}
		flags := invFlags(it)
		if desk {
			r.tail = invMark(it, inventory.SideCLI) + " " + invMark(it, inventory.SideDSK) + " "
			r.tailW = 8
			if flags != "" {
				r.tail = " " + styleYellow.Render(flags) + r.tail
				r.tailW += 1 + lipgloss.Width(flags)
			}
			r.ind, r.indW = " ", 1
		} else {
			m1 := invMark(it, inventory.SideCLI)
			r.ind, r.indW = strings.TrimLeft(m1, " "), 1
			if flags != "" {
				r.tail = " " + styleYellow.Render(flags)
				r.tailW = 1 + lipgloss.Width(flags)
			}
		}
		rows = append(rows, r)
	}
	return rows
}

// invSummary is the status-bar line for an inventory tab. The Desktop part
// ("in both · differ · mismatched") needs a Desktop store; the copy part
// ("N stale", "M leftover") does not, and a zero count is omitted.
func (m Model) invSummary(t tab) string {
	if !invTab(t) {
		return ""
	}
	var parts []string
	if m.hasDesktop() {
		both, differ, mism := 0, 0, 0
		for _, it := range m.invAll(t) {
			if it.OnSide(inventory.SideCLI) && it.OnSide(inventory.SideDSK) {
				both++
			}
			if it.Differs() {
				differ++
			}
			if it.Mismatched() {
				mism++
			}
		}
		parts = append(parts, fmt.Sprintf("%d in both · %d differ · %d mismatched", both, differ, mism))
	}
	stale, left := 0, 0
	for _, it := range m.invAll(t) {
		stale += it.StaleCount()
		if len(it.Leftovers()) > 0 {
			left++
		}
	}
	if stale > 0 {
		parts = append(parts, fmt.Sprintf("%d stale", stale))
	}
	if left > 0 {
		parts = append(parts, fmt.Sprintf("%d leftover", left))
	}
	if len(parts) == 0 {
		return ""
	}
	return styleMuted.Render(strings.Join(parts, " · "))
}

// ── toggle ───────────────────────────────────────────────────────────────────

// toggleSelectedPlugin toggles the selected plugin's Claude Code marketplace
// install, the only write path clasp has.
func (m Model) toggleSelectedPlugin() (Model, tea.Cmd) {
	if it := m.selectedItem(); it != nil {
		for _, in := range it.Installs {
			if in.Side == inventory.SideCLI && in.Origin == inventory.OriginMarketplace && in.PluginID != "" {
				return m, pluginToggleCmd(in.PluginID)
			}
		}
	}
	m.statusMsg = styleYellow.Render("not toggleable here (no Claude Code marketplace install)")
	return m, clearStatusAfter(3 * time.Second)
}

// ── detail ───────────────────────────────────────────────────────────────────

func originLabel(in inventory.Install) string {
	switch in.Origin {
	case inventory.OriginPersonal:
		return "personal"
	case inventory.OriginMarketplace:
		return "marketplace"
	case inventory.OriginSynced:
		return "synced from claude.ai"
	case inventory.OriginAnthropicSkills:
		return "anthropic-skills"
	case inventory.OriginRPM:
		return "Desktop plugin"
	case inventory.OriginDesktopConfig:
		return "claude_desktop_config.json"
	case inventory.OriginSettings:
		return "settings"
	case inventory.OriginProject:
		return "project"
	case inventory.OriginAccount:
		return "claude.ai account"
	}
	return in.Origin
}

// indentKV renders "    key      value" with the value wrapped under itself.
func indentKV(k, v string, width int) string {
	const indent, keyW = 4, 9
	keyStr := strings.Repeat(" ", indent) + styleKey.Render(fmt.Sprintf("%-*s", keyW, k))
	valW := width - indent - keyW
	if valW < 8 {
		return keyStr + styleVal.Render(v)
	}
	chunks := wrapWords(v, valW)
	pad := strings.Repeat(" ", indent+keyW)
	for i, c := range chunks {
		if i == 0 {
			chunks[i] = keyStr + styleVal.Render(c)
		} else {
			chunks[i] = pad + styleVal.Render(c)
		}
	}
	return strings.Join(chunks, "\n")
}

func installLines(in inventory.Install, width int) []string {
	st := styleGreen.Render("enabled")
	if !in.Enabled {
		st = styleMuted.Render("disabled")
	}
	lines := []string{"  " + styleVal.Render(originLabel(in)) + styleMuted.Render(" · ") + st}
	if in.Plugin != "" {
		lines = append(lines, indentKV("plugin", in.Plugin, width))
	}
	if in.PluginID != "" {
		lines = append(lines, indentKV("id", in.PluginID, width))
	}
	if in.Type != "" {
		lines = append(lines, indentKV("type", in.Type, width))
	}
	if in.Target != "" {
		lines = append(lines, indentKV("target", in.Target, width))
	}
	if in.Account != "" {
		lines = append(lines, indentKV("account", in.Account, width))
	}
	if in.Path != "" {
		lines = append(lines, indentKV("path", shortPath(in.Path), width))
	}
	if in.Version != "" {
		lines = append(lines, indentKV("version", in.Version, width))
	}
	if in.Hash != "" {
		lines = append(lines, indentKV("hash", in.Hash[:min(8, len(in.Hash))], width))
	}
	return lines
}

func (m Model) invDetail(t tab) string {
	it := m.selectedItem()
	if it == nil {
		if m.hasDesktop() && m.srcFilter[t] != srcAll && len(m.invAll(t)) > 0 {
			return emptyState("nothing matches this filter")
		}
		switch t {
		case tabSkills:
			return emptyState("no skills installed")
		case tabPlugins:
			return emptyState("no plugins installed")
		}
		return emptyState("no MCP servers configured")
	}
	w := m.detail.Width
	desk := m.hasDesktop()
	lines := []string{styleDetailTitle.Render(it.Name)}
	if desk {
		status := ""
		switch {
		case len(it.Installs) == 0:
			status = "not installed; only copies left on disk"
		case it.Account():
			status = "account (claude.ai)"
		case it.OnlyCLI():
			status = "Claude Code only"
		case it.OnlyDSK():
			status = "Desktop only"
		default:
			status = "on both"
		}
		lines = append(lines, styleMuted.Render(status))
	}
	var flags []string
	if it.Differs() {
		flags = append(flags, "≠ content differs between installs")
	}
	if it.Twice(inventory.SideCLI) {
		flags = append(flags, "×2 loaded twice on Claude Code")
	}
	if it.Twice(inventory.SideDSK) {
		flags = append(flags, "×2 loaded twice on Desktop")
	}
	if it.EnabledSplit() {
		flags = append(flags, "enabled on one side only")
	}
	for _, f := range flags {
		lines = append(lines, styleYellow.Render(f))
	}
	lines = append(lines, "")

	sides := []struct {
		side  inventory.Side
		label string
	}{{inventory.SideCLI, "Claude Code"}, {inventory.SideDSK, "Claude Desktop"}}
	for _, s := range sides {
		if s.side == inventory.SideDSK && !desk {
			continue
		}
		lines = append(lines, styleDetailTitle.Render(s.label))
		found := false
		for _, in := range it.Installs {
			if in.Side != s.side {
				continue
			}
			found = true
			lines = append(lines, installLines(in, w)...)
		}
		if !found {
			lines = append(lines, "  "+styleMuted.Render("· not found"))
		}
		lines = append(lines, "")
	}

	for _, in := range it.Installs {
		if in.Desc != "" {
			lines = append(lines, kvWrap("Description", in.Desc, w), "")
			break
		}
	}

	if t == tabSkills {
		for _, sk := range m.st.Skills {
			if sk.Name != it.Name {
				continue
			}
			if sk.BodyPreview != "" {
				lines = append(lines, kvWrap("Preview", sk.BodyPreview, w))
			}
			if sk.LineCount > 0 {
				lines = append(lines, kv("Lines", fmt.Sprintf("%d", sk.LineCount)))
			}
			lines = append(lines, "")
			break
		}
	}

	if t == tabPlugins {
		if p := m.matchingPlugin(it); p != nil {
			sections := []sectionBlock{
				{"Skills", p.SkillNames},
				{"MCP Servers", mcpNames(p.MCPServers)},
				{"Commands", p.Commands},
				{"Agents", p.Agents},
				{"Hooks", p.HookEvents},
			}
			lines = append(lines, renderPluginSections(sections, w), "")
		}
	}

	lines = append(lines, copyLines(it, w)...)

	if desk {
		lines = append(lines, styleMuted.Render(strings.Join(wrapWords(loadNote, w), "\n")))
	}
	if m.st.Inventory != nil && len(m.st.Inventory.Errs) > 0 {
		errs := m.st.Inventory.Errs
		lines = append(lines, "", styleMuted.Render(fmt.Sprintf("%d read errors", len(errs))))
		for _, e := range errs {
			lines = append(lines, styleMuted.Render(strings.Join(wrapWords("· "+e.Error(), w), "\n")))
		}
	}
	return strings.Join(lines, "\n")
}

// humanSize renders a byte count as 512B, 32K, 1.5M.
func humanSize(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%dB", n)
	case n < k*k:
		return fmt.Sprintf("%dK", (n+k/2)/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1fM", float64(n)/(k*k))
	}
	return fmt.Sprintf("%.1fG", float64(n)/(k*k*k))
}

// distinctSize sums copy sizes once per path: several items can share one
// Copy.Path, but here the slice is a single item's, so duplicates only arise
// if a path repeats within it.
func distinctSize(cs []inventory.Copy) (n int, size int64) {
	seen := map[string]bool{}
	for _, c := range cs {
		if seen[c.Path] {
			continue
		}
		seen[c.Path] = true
		n++
		size += c.Size
	}
	return n, size
}

// copyLines is the "On-disk copies" detail section, ending with the t hint.
func copyLines(it *inventory.Item, w int) []string {
	if len(it.Copies) == 0 {
		return nil
	}
	lines := []string{styleDetailTitle.Render("On-disk copies")}
	for _, c := range it.Copies {
		st := styleGreen.Render(c.Status.String())
		if c.Status != inventory.CopyActive {
			st = styleYellow.Render(c.Status.String())
		}
		date := "—"
		if !c.ModTime.IsZero() {
			date = c.ModTime.Format("2006-01-02")
		}
		lines = append(lines,
			"  "+st+styleMuted.Render(" · ")+styleVal.Render(humanSize(c.Size))+styleMuted.Render(" · ")+styleVal.Render(date),
			indentKV("path", shortPath(c.Path), w))
		if c.Why != "" {
			lines = append(lines, indentKV("why", c.Why, w))
		}
	}
	lines = append(lines, "")
	if n, _ := distinctSize(it.TrashableLeftovers()); n > 0 {
		noun := "leftover"
		if n != 1 {
			noun = "leftovers"
		}
		lines = append(lines, styleYellow.Render(fmt.Sprintf("t  move %d %s to the Trash (asks first)", n, noun)), "")
	} else if len(it.Leftovers()) > 0 {
		lines = append(lines, styleMuted.Render("nothing here can be moved to the Trash"), "")
	}
	return lines
}

// kindFor maps an inventory tab to its item kind.
func kindFor(t tab) inventory.Kind {
	switch t {
	case tabPlugins:
		return inventory.KindPlugin
	case tabMCP:
		return inventory.KindMCP
	}
	return inventory.KindSkill
}

// trashSelected starts the trash confirmation for the selected item's
// trashable leftovers, or says there is nothing to do.
func (m Model) trashSelected() (Model, tea.Cmd) {
	it := m.selectedItem()
	if it == nil || !invTab(m.tab) {
		return m, nil
	}
	n, size := distinctSize(it.TrashableLeftovers())
	if n == 0 {
		m.statusMsg = styleYellow.Render("nothing trashable here")
		return m, clearStatusAfter(3 * time.Second)
	}
	m.pendingTrash = &trashTarget{kind: kindFor(m.tab), name: it.Name, n: n, size: size}
	return m, nil
}

// matchingPlugin finds the state.Plugin behind an item's Claude Code
// marketplace install, for the skills/commands/agents/hooks lists.
func (m Model) matchingPlugin(it *inventory.Item) *state.Plugin {
	for _, in := range it.Installs {
		if in.Origin != inventory.OriginMarketplace || in.PluginID == "" {
			continue
		}
		for i := range m.st.Plugins {
			if m.st.Plugins[i].ID == in.PluginID {
				return &m.st.Plugins[i]
			}
		}
	}
	return nil
}
