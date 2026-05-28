package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/tylerreagan/clasp/internal/state"
)

type tab int

const (
	tabSessions tab = iota
	tabSkills
	tabPlugins
	tabMCP
	tabMemory
	tabCount
)

var tabNames = [tabCount]string{"Sessions", "Skills", "Plugins", "MCP", "Memory"}

// focus is the active interaction surface. The Update dispatcher routes keys
// based on focus after handling any modal overlays (e.g. ? help — wired in
// task #14) and globally-available keys.
type focus int

const (
	focusList         focus = iota // List + detail side-by-side (browse mode).
	focusZoomedDetail              // Detail full-screen (wired in task #13).
)

type RefreshMsg struct{}

type toggleResultMsg struct {
	id  string
	err error
}

type clearStatusMsg struct{}

// rowItem separates the styled indicator from the plain label so truncation
// operates only on plain bytes (no ANSI escape codes).
// isHeader marks non-selectable group header rows; dataIdx is an optional
// index into the backing slice (used by tabMemory to map cursor → entry).
type rowItem struct {
	ind      string
	indW     int
	label    string
	isHeader bool
	dataIdx  int
}

type Model struct {
	st        *state.State
	loadErr   error
	tab       tab
	focus     focus
	cursors   [tabCount]int
	width     int
	height    int
	detail    viewport.Model
	help      help.Model
	keys      keyMap
	vimG      vimG // tracks the gg two-press chord
	showHelp  bool // ? overlay active; takes modal priority over focus dispatch
	ready     bool
	statusMsg string // transient feedback shown in the status bar
}

func New() (*Model, error) {
	st, err := state.Load()
	h := help.New()
	// Pull help.Model away from its default adaptive-gray styles into the
	// warm palette: copper for key labels, muted parchment for descriptions
	// and separators. The hint bar and the ? overlay both inherit this.
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(colorCopper)
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(colorMutedParchment)
	h.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(colorMutedParchment)
	h.Styles.FullKey = lipgloss.NewStyle().Foreground(colorCopper)
	h.Styles.FullDesc = lipgloss.NewStyle().Foreground(colorMutedParchment)
	h.Styles.FullSeparator = lipgloss.NewStyle().Foreground(colorMutedParchment)

	m := &Model{
		st:      st,
		loadErr: err,
		focus:   focusList,
		help:    h,
		keys:    defaultKeys(),
	}
	// Zoom/UnZoom/Toggle enabled state depends on current focus and tab.
	m.refreshBindings()
	return m, nil
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		vw, vh := m.viewportDims()
		if !m.ready {
			m.detail = viewport.New(vw, vh)
			m.ready = true
		} else {
			m.detail.Width, m.detail.Height = vw, vh
		}
		m.help.Width = m.width
		m.detail.SetContent(m.detailContent())
		return m, nil

	case RefreshMsg:
		st, err := state.Load()
		m.st, m.loadErr = st, err
		m.detail.SetContent(m.detailContent())
		return m, nil

	case toggleResultMsg:
		if msg.err != nil {
			m.statusMsg = styleRed.Render("✗ " + msg.err.Error())
		} else {
			m.statusMsg = styleGreen.Render("✓ toggled " + pluginShortName(msg.id))
		}
		return m, clearStatusAfter(3 * time.Second)

	case clearStatusMsg:
		m.statusMsg = ""
		return m, nil

	case tea.KeyMsg:
		// Modal priority: the ? overlay swallows keys before focus dispatch.
		// ctrl+c still quits (universal kill); esc/?/q dismiss the overlay.
		if m.showHelp {
			return m.updateHelp(msg)
		}

		// Global keys: handled regardless of focus.
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Refresh):
			return m, func() tea.Msg { return RefreshMsg{} }
		case key.Matches(msg, m.keys.Help):
			m.showHelp = true
			return m, nil
		}

		// Focus-specific dispatch.
		switch m.focus {
		case focusList:
			return m.updateList(msg)
		case focusZoomedDetail:
			return m.updateZoom(msg)
		}
	}
	return m, nil
}

// updateHelp handles keys while the ? overlay is shown. Modal: the overlay
// fully captures input until dismissed. ctrl+c stays as an unconditional quit.
func (m Model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "?", "q":
		m.showHelp = false
		return m, nil
	}
	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// gg chord runs ahead of the main dispatch so single-g doesn't fire
	// "go to top" on its own. Any non-g key cancels the chord (gContinue)
	// and falls through to the regular handler.
	switch m.vimG.step(msg.String()) {
	case gWait:
		return m, nil
	case gTop:
		m.cursors[m.tab] = 0
		m.detail.GotoTop()
		m.detail.SetContent(m.detailContent())
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.NextTab):
		m.tab = (m.tab + 1) % tabCount
		m.cursors[m.tab] = clamp(m.cursors[m.tab], 0, m.listLen()-1)
		m.refreshBindings()
		m.detail.GotoTop()
		m.detail.SetContent(m.detailContent())
		return m, func() tea.Msg { return RefreshMsg{} }
	case key.Matches(msg, m.keys.PrevTab):
		m.tab = (m.tab - 1 + tabCount) % tabCount
		m.cursors[m.tab] = clamp(m.cursors[m.tab], 0, m.listLen()-1)
		m.refreshBindings()
		m.detail.GotoTop()
		m.detail.SetContent(m.detailContent())
		return m, func() tea.Msg { return RefreshMsg{} }
	case key.Matches(msg, m.keys.Up):
		if m.cursors[m.tab] > 0 {
			m.cursors[m.tab]--
			m.detail.GotoTop()
			m.detail.SetContent(m.detailContent())
		}
		return m, nil
	case key.Matches(msg, m.keys.Down):
		if m.cursors[m.tab] < m.listLen()-1 {
			m.cursors[m.tab]++
			m.detail.GotoTop()
			m.detail.SetContent(m.detailContent())
		}
		return m, nil
	case key.Matches(msg, m.keys.End):
		m.cursors[m.tab] = max(0, m.listLen()-1)
		m.detail.GotoTop()
		m.detail.SetContent(m.detailContent())
		return m, nil
	case key.Matches(msg, m.keys.Toggle):
		if m.tab == tabPlugins && m.st != nil {
			c := m.cursors[m.tab]
			if c < len(m.st.Plugins) {
				return m, pluginToggleCmd(m.st.Plugins[c].ID)
			}
		}
		return m, nil
	case key.Matches(msg, m.keys.ScrollDown):
		m.detail.HalfPageDown()
		return m, nil
	case key.Matches(msg, m.keys.ScrollUp):
		m.detail.HalfPageUp()
		return m, nil
	case key.Matches(msg, m.keys.ScrollPageDown):
		m.detail.PageDown()
		return m, nil
	case key.Matches(msg, m.keys.ScrollPageUp):
		m.detail.PageUp()
		return m, nil
	case key.Matches(msg, m.keys.Zoom):
		m.focus = focusZoomedDetail
		m.refreshBindings()
		m.resizeDetail()
		m.detail.GotoTop()
		m.detail.SetContent(m.detailContent())
		return m, nil
	}
	return m, nil
}

func (m Model) updateZoom(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// gg chord (top-of-detail in zoom mode).
	switch m.vimG.step(msg.String()) {
	case gWait:
		return m, nil
	case gTop:
		m.detail.GotoTop()
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.UnZoom):
		m.focus = focusList
		m.refreshBindings()
		m.resizeDetail()
		m.detail.GotoTop()
		m.detail.SetContent(m.detailContent())
		return m, nil
	case key.Matches(msg, m.keys.NextTab), key.Matches(msg, m.keys.PrevTab):
		// Forgiving Tab: exit zoom AND switch tab, landing in browse on the new tab.
		m.focus = focusList
		if key.Matches(msg, m.keys.NextTab) {
			m.tab = (m.tab + 1) % tabCount
		} else {
			m.tab = (m.tab - 1 + tabCount) % tabCount
		}
		m.cursors[m.tab] = clamp(m.cursors[m.tab], 0, m.listLen()-1)
		m.refreshBindings()
		m.resizeDetail()
		m.detail.GotoTop()
		m.detail.SetContent(m.detailContent())
		return m, func() tea.Msg { return RefreshMsg{} }
	case key.Matches(msg, m.keys.Up):
		m.detail.LineUp(1)
		return m, nil
	case key.Matches(msg, m.keys.Down):
		m.detail.LineDown(1)
		return m, nil
	case key.Matches(msg, m.keys.End):
		m.detail.GotoBottom()
		return m, nil
	case key.Matches(msg, m.keys.ScrollDown):
		m.detail.HalfPageDown()
		return m, nil
	case key.Matches(msg, m.keys.ScrollUp):
		m.detail.HalfPageUp()
		return m, nil
	case key.Matches(msg, m.keys.ScrollPageDown):
		m.detail.PageDown()
		return m, nil
	case key.Matches(msg, m.keys.ScrollPageUp):
		m.detail.PageUp()
		return m, nil
	}
	return m, nil
}

// refreshBindings updates per-binding Enabled state based on the current
// focus and tab. Called whenever those change so help.Model only shows
// the keys that actually fire in the current context.
func (m *Model) refreshBindings() {
	m.keys.Zoom.SetEnabled(m.focus == focusList)
	m.keys.UnZoom.SetEnabled(m.focus == focusZoomedDetail)
	m.keys.Toggle.SetEnabled(m.focus == focusList && m.tab == tabPlugins)
}

// resizeDetail re-applies viewport dimensions after a focus change.
// Zoom mode gives the detail viewport the full terminal width.
func (m *Model) resizeDetail() {
	vw, vh := m.viewportDims()
	m.detail.Width, m.detail.Height = vw, vh
}

func (m Model) View() string {
	if !m.ready {
		return stylePrimary.Bold(true).Render("clasp") + " " +
			styleMuted.Render("· reading ~/.claude…")
	}
	if m.showHelp {
		return m.renderHelp()
	}
	sep := styleMuted.Render(strings.Repeat("─", m.width))

	var body string
	if m.focus == focusZoomedDetail {
		body = m.renderDetail()
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.renderList(), m.renderDetail())
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(),
		sep,
		body,
		m.renderStatus(),
	)
}

// emptyState renders a uniform empty-list message — a muted leading glyph
// plus the supplied label. Used per-tab when there's nothing to show.
func emptyState(msg string) string {
	return styleMuted.Render("· " + msg)
}

// renderHelp is the ? overlay — a centered box listing context-aware
// keybindings. Replaces the main view entirely while showHelp is true.
func (m Model) renderHelp() string {
	full := m.help
	full.ShowAll = true
	keys := contextKeys{keys: m.keys, focus: m.focus}

	body := full.View(keys)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Padding(1, 2).
		Render(
			styleDetailTitle.Render("Keybindings") + "  " +
				styleMuted.Render(m.helpContextLabel()) + "\n\n" +
				body + "\n\n" +
				styleMuted.Render("? / esc / q to close"),
		)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) helpContextLabel() string {
	if m.focus == focusZoomedDetail {
		return "(zoom mode)"
	}
	return "(browse mode)"
}

// ── rendering ────────────────────────────────────────────────────────────────

// wordmarkLines is the static 4-row pixel-art "clasp" wordmark. The styling
// is loosely modeled on the Claude Code brand wordmark — chunky block letters
// in the warm-spectrum copper accent. Each letter is 4 cells wide; the whole
// wordmark is 24 cells wide.
var wordmarkLines = []string{
	"████ █    ████ ████ ████",
	"█    █    █  █ █    █  █",
	"█    █    ████  ███ ████",
	"████ ████ █  █ ████ █   ",
}

func renderWordmark() string {
	style := stylePrimary.Bold(true)
	out := make([]string, len(wordmarkLines))
	for i, l := range wordmarkLines {
		out[i] = style.Render(l)
	}
	return strings.Join(out, "\n")
}

func (m Model) renderHeader() string {
	var tabs []string
	for i, name := range tabNames {
		if tab(i) == m.tab {
			label := name
			// Zoom mode bracket marker — signals that tabs are still reachable
			// (Tab/h/l forgivingly exits zoom and switches) without losing the
			// visual cue of which collection you're zoomed into.
			if m.focus == focusZoomedDetail {
				label = "[" + name + "]"
			}
			tabs = append(tabs, styleTabActive.Render(label))
		} else {
			tabs = append(tabs, styleTabInactive.Render(name))
		}
	}

	wordmark := renderWordmark()
	tabRow := strings.Join(tabs, "")

	wordmarkW := lipgloss.Width(wordmark)
	wordmarkH := lipgloss.Height(wordmark)
	padW := max(0, m.width-wordmarkW-lipgloss.Width(tabRow))

	// Right column: pad the tab row to the right edge and bottom-align it
	// against the wordmark so tabs sit on the baseline of the letterforms.
	rightCol := lipgloss.PlaceVertical(
		wordmarkH,
		lipgloss.Bottom,
		strings.Repeat(" ", padW)+tabRow,
	)

	return lipgloss.JoinHorizontal(lipgloss.Top, wordmark, rightCol)
}

func (m Model) renderList() string {
	rows := m.listRows()
	cursor := m.cursors[m.tab]
	listInner := m.listOuter() - 2
	bodyH := m.panelH()

	// row prefix: cursor-indicator (1) + type-indicator (1) + space (1) = 3 chars
	const prefixW = 3
	labelW := listInner - prefixW

	// For tabs with headers, map cursor (Nth selectable) to a visual row index.
	cursorVisIdx := cursor // default: cursor == visual index (no headers)
	if m.tab == tabMemory {
		sel := 0
		for i, r := range rows {
			if r.isHeader {
				continue
			}
			if sel == cursor {
				cursorVisIdx = i
				break
			}
			sel++
		}
	}

	start := max(0, cursorVisIdx-bodyH+1)
	end := min(start+bodyH, len(rows))

	var lines []string
	for i := start; i < end; i++ {
		r := rows[i]
		var line string
		if r.isHeader {
			// Project group header: muted, no cursor indicator, indented label.
			label := truncate(r.label, listInner-2)
			line = styleMuted.Render(" " + label)
		} else if i == cursorVisIdx {
			label := truncate(r.label, labelW)
			line = stylePrimary.Render("▌") + r.ind + " " + styleSelected.Render(label)
		} else {
			label := truncate(r.label, labelW)
			line = " " + r.ind + " " + label
		}
		lines = append(lines, styleNormal.Width(listInner).Render(line))
	}
	if len(lines) == 0 {
		lines = []string{styleMuted.Render(" (none)")}
	}
	return styleBorder.Width(listInner).Height(bodyH).Render(strings.Join(lines, "\n"))
}

func (m Model) renderDetail() string {
	outerW, innerH := m.detailOuterDims()
	innerW := outerW - 2 // lipgloss border adds 2 columns
	padded := lipgloss.NewStyle().Padding(0, 1).Render(m.detail.View())
	border := styleBorder
	// In zoom mode the detail pane is the user's entire focus — tint its
	// border copper to reinforce that, distinguishing it from the muted
	// steel border that the same panel wears in browse mode.
	if m.focus == focusZoomedDetail {
		border = border.BorderForeground(colorCopper)
	}
	return border.Width(innerW).Height(innerH).Render(padded)
}

func (m Model) renderStatus() string {
	var right string
	if m.statusMsg != "" {
		right = m.statusMsg
	} else if m.loadErr != nil {
		right = styleRed.Render("⚠ " + m.loadErr.Error())
	}

	hint := m.help.View(contextKeys{keys: m.keys, focus: m.focus})
	pad := max(0, m.width-2-lipgloss.Width(hint)-lipgloss.Width(right))
	bar := hint + strings.Repeat(" ", pad) + right

	return lipgloss.NewStyle().
		Background(colorBorder).
		Width(m.width).
		Padding(0, 1).
		Render(bar)
}

// ── list rows ─────────────────────────────────────────────────────────────────

func (m Model) listRows() []rowItem {
	if m.st == nil {
		return nil
	}
	switch m.tab {
	case tabSessions:
		rows := make([]rowItem, len(m.st.Sessions))
		for i, s := range m.st.Sessions {
			dot := styleGreen.Render("●")
			if s.Status != "busy" && s.Status != "idle" {
				dot = styleYellow.Render("●")
			}
			rows[i] = rowItem{ind: dot, indW: 1, label: fmt.Sprintf("%d  %s", s.PID, shortPath(s.CWD))}
		}
		return rows
	case tabSkills:
		rows := make([]rowItem, len(m.st.Skills))
		for i, s := range m.st.Skills {
			rows[i] = rowItem{ind: styleMuted.Render("·"), indW: 1, label: s.Name}
		}
		return rows
	case tabPlugins:
		rows := make([]rowItem, len(m.st.Plugins))
		for i, p := range m.st.Plugins {
			dot := styleRed.Render("○")
			if p.Enabled {
				dot = styleGreen.Render("●")
			}
			rows[i] = rowItem{ind: dot, indW: 1, label: pluginShortName(p.ID)}
		}
		return rows
	case tabMCP:
		rows := make([]rowItem, len(m.st.MCPServers))
		for i, srv := range m.st.MCPServers {
			rows[i] = rowItem{ind: stylePrimary.Render("◈"), indW: 1, label: srv.Name}
		}
		return rows
	case tabMemory:
		var rows []rowItem
		lastProj := ""
		for i, e := range m.st.Memory {
			proj := projectLabel(e.Project)
			if proj != lastProj {
				rows = append(rows, rowItem{isHeader: true, label: proj, dataIdx: -1})
				lastProj = proj
			}
			name := e.Name
			if name == "" {
				name = strings.TrimSuffix(e.File, ".md")
			}
			t := e.Type
			if t == "" {
				t = "·"
			}
			rows = append(rows, rowItem{
				ind:     styleMuted.Render(string([]rune(t)[0])),
				indW:    1,
				label:   name,
				dataIdx: i,
			})
		}
		return rows
	}
	return nil
}

func (m Model) listLen() int {
	rows := m.listRows()
	if m.tab != tabMemory {
		return len(rows)
	}
	// For memory, cursor only lands on selectable (non-header) rows.
	n := 0
	for _, r := range rows {
		if !r.isHeader {
			n++
		}
	}
	return n
}

// ── detail content ────────────────────────────────────────────────────────────

func (m Model) detailContent() string {
	if m.st == nil {
		return styleRed.Render("⚠ could not load ~/.claude state")
	}
	c := m.cursors[m.tab]
	switch m.tab {
	case tabSessions:
		if c >= len(m.st.Sessions) {
			return emptyState("no active sessions")
		}
		s := m.st.Sessions[c]
		return strings.Join([]string{
			styleDetailTitle.Render("Session"),
			"",
			kv("PID", fmt.Sprintf("%d", s.PID)),
			kv("Session ID", s.SessionID),
			kv("CWD", shortPath(s.CWD)),
			kv("Status", s.Status),
			kv("Version", s.Version),
		}, "\n")

	case tabSkills:
		if c >= len(m.st.Skills) {
			return emptyState("no skills installed")
		}
		sk := m.st.Skills[c]
		lines := []string{styleDetailTitle.Render(sk.Name), ""}
		if sk.Description != "" {
			lines = append(lines, kvWrap("Description", sk.Description, m.detail.Width))
		}
		if sk.BodyPreview != "" {
			lines = append(lines, kvWrap("Preview", sk.BodyPreview, m.detail.Width))
		}
		lines = append(lines, "")
		if sk.SourcePath != "" {
			lines = append(lines, kv("Path", shortPath(sk.SourcePath)))
		}
		if sk.LineCount > 0 {
			lines = append(lines, kv("Lines", fmt.Sprintf("%d", sk.LineCount)))
		}
		if !sk.ModTime.IsZero() {
			lines = append(lines, kv("Modified", sk.ModTime.Format("2006-01-02 15:04")))
		}
		return strings.Join(lines, "\n")

	case tabPlugins:
		if c >= len(m.st.Plugins) {
			return emptyState("no plugins installed")
		}
		p := m.st.Plugins[c]
		statusStr := styleRed.Render("disabled")
		if p.Enabled {
			statusStr = styleGreen.Render("enabled")
		}
		title := pluginShortName(p.ID)
		if p.DisplayName != "" && p.DisplayName != title {
			title = p.DisplayName
		}
		lines := []string{styleDetailTitle.Render(title), ""}
		if p.Description != "" {
			lines = append(lines, kvWrap("Description", p.Description, m.detail.Width))
		}
		if p.Author != "" {
			lines = append(lines, kv("Author", p.Author))
		}
		lines = append(lines,
			kv("ID", p.ID),
			kv("Version", p.Version()),
			fmt.Sprintf("%-12s", "Status:")+"  "+statusStr,
			kv("Scope", p.Scope()),
			kv("Installed", p.InstalledAt()),
		)
		lines = append(lines, "", styleMuted.Render(strings.Repeat("─", m.detail.Width)))
		sections := []sectionBlock{
			{"Skills", p.SkillNames},
			{"MCP Servers", mcpNames(p.MCPServers)},
			{"Commands", p.Commands},
			{"Agents", p.Agents},
			{"Hooks", p.HookEvents},
		}
		lines = append(lines, renderPluginSections(sections, m.detail.Width))
		claudeMD := "(none)"
		if p.HasClaudeMD {
			claudeMD = styleGreen.Render("yes")
		}
		lines = append(lines, kv("CLAUDE.md", claudeMD))
		return strings.Join(lines, "\n")

	case tabMCP:
		if c >= len(m.st.MCPServers) {
			return emptyState("no MCP servers configured")
		}
		srv := m.st.MCPServers[c]
		lines := []string{
			styleDetailTitle.Render(srv.Name),
			"",
			kv("Type", srv.Type),
		}
		if srv.URL != "" {
			lines = append(lines, kv("URL", srv.URL))
		}
		if srv.Plugin != "" {
			lines = append(lines, kv("Plugin", pluginShortName(srv.Plugin)))
		}
		return strings.Join(lines, "\n")

	case tabMemory:
		// Map cursor (Nth selectable row) to the memory entry via dataIdx.
		memIdx := -1
		sel := 0
		for _, row := range m.listRows() {
			if row.isHeader {
				continue
			}
			if sel == c {
				memIdx = row.dataIdx
				break
			}
			sel++
		}
		if memIdx < 0 || memIdx >= len(m.st.Memory) {
			return emptyState("no memory entries")
		}
		e := m.st.Memory[memIdx]
		name := e.Name
		if name == "" {
			name = strings.TrimSuffix(e.File, ".md")
		}
		header := strings.Join([]string{
			styleDetailTitle.Render(name),
			"",
			kv("Type", e.Type),
			kv("File", e.File),
			kv("Project", projectLabel(e.Project)),
		}, "\n")
		if e.Body == "" {
			return header
		}
		return header + "\n" + styleMuted.Render(strings.Repeat("─", m.detail.Width)) + "\n" + renderMarkdown(e.Body, m.detail.Width)
	}
	return ""
}

// ── layout helpers ────────────────────────────────────────────────────────────

// listOuter is the total column width of the list panel including its border.
func (m Model) listOuter() int { return 32 }

// panelH is the inner content height (excluding border) for both panel boxes.
//
// Vertical stack: header(4) + separator(1) + panel outer(panelH+2) + status(1) = m.height
//   → panel outer = m.height - 6
//   → panel inner = panel outer - 2 = m.height - 8
//
// The header is 4 rows because it carries the pixel-art wordmark. lipgloss v1
// convention: styleBorder.Height(n) sets inner height to n; outer = n+2.
func (m Model) panelH() int { return max(1, m.height-8) }

// detailOuterDims returns the OUTER width and the INNER height of the detail pane.
// (Asymmetric because the width minus listOuter is what's available; the height comes
// from panelH which is already inner. Callers compute innerW = outerW - 2 when needed.)
//
// In zoom mode the detail pane fills the full terminal width — no list panel.
func (m Model) detailOuterDims() (outerW, innerH int) {
	if m.focus == focusZoomedDetail {
		return m.width, m.panelH()
	}
	return m.width - m.listOuter(), m.panelH()
}

// viewportDims returns the viewport's content area inside the detail pane.
//
//	outerW  = m.width - listOuter
//	innerW  = outerW - 2                  (border)
//	vpW     = innerW - 2 = outerW - 4     (Padding(0, 1) left+right)
//	vpH     = innerH - 2                  (defensive: see note below)
//
// The vpH = innerH - 2 buffer is empirical, not derivational: glamour-rendered markdown
// on the Memory tab can produce a trailing line that pushes the border off-screen when
// the viewport is sized to exactly innerH. Two-row buffer absorbs it.
func (m Model) viewportDims() (w, h int) {
	outerW, innerH := m.detailOuterDims()
	return max(1, outerW-4), max(1, innerH-2)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func kv(k, v string) string {
	return styleKey.Render(fmt.Sprintf("%-12s", k+":")) + "  " + styleVal.Render(v)
}

// kvWrap is like kv but wraps long values onto continuation lines.
func kvWrap(k, v string, width int) string {
	const keyColW = 14 // "%-12s" (12) + "  " (2)
	valW := width - keyColW
	keyStr := styleKey.Render(fmt.Sprintf("%-12s", k+":")) + "  "
	if valW <= 0 || width <= 0 {
		return keyStr + styleVal.Render(v)
	}
	chunks := wrapWords(v, valW)
	indent := strings.Repeat(" ", keyColW)
	lines := make([]string, len(chunks))
	for i, chunk := range chunks {
		if i == 0 {
			lines[i] = keyStr + styleVal.Render(chunk)
		} else {
			lines[i] = indent + styleVal.Render(chunk)
		}
	}
	return strings.Join(lines, "\n")
}

// wrapWords splits s into lines of at most width runes, breaking at word boundaries.
func wrapWords(s string, width int) []string {
	if width <= 0 || s == "" {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := ""
	for _, w := range words {
		wLen := len([]rune(w))
		if current == "" {
			current = w
		} else if len([]rune(current))+1+wLen <= width {
			current += " " + w
		} else {
			lines = append(lines, current)
			current = w
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func shortPath(p string) string {
	home, _ := os.UserHomeDir()
	return strings.Replace(p, home, "~", 1)
}

func pluginShortName(id string) string {
	name, _, _ := strings.Cut(id, "@")
	return name
}

// projectLabel converts an encoded project dir name to something readable.
func projectLabel(encoded string) string {
	home, _ := os.UserHomeDir()
	encodedHome := strings.ReplaceAll(strings.TrimPrefix(home, "/"), "/", "-")
	label := strings.TrimPrefix(encoded, "-"+encodedHome)
	label = strings.TrimPrefix(label, "-")
	if label == "" {
		return encoded
	}
	return strings.ReplaceAll(label, "-", "/")
}

// renderMarkdown renders body as markdown with word-wrap at width.
// Uses a zero-margin dark style so the body text aligns flush with the header kv lines above it.
// WithAutoStyle() is intentionally avoided — it queries the terminal for background color,
// which blocks inside a bubbletea raw-mode TUI.
func renderMarkdown(body string, width int) string {
	if width <= 0 {
		return body
	}
	style := glamourstyles.DarkStyleConfig
	var zero uint
	style.Document.Margin = &zero
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return body
	}
	out, err := r.Render(body)
	if err != nil {
		return body
	}
	return strings.TrimSpace(out)
}

// sectionBlock is one labelled content group inside the plugin detail
// (Skills, MCP Servers, Commands, etc.). renderPluginSections distributes
// these across columns based on available width.
type sectionBlock struct {
	label string
	items []string
}

// renderPluginSections lays out the plugin-detail sections in 1, 2, or 3
// columns depending on innerW (see columnsForWidth). Multi-column layouts
// pad each column to a uniform width with two-space inter-column gutters
// so lipgloss.JoinHorizontal aligns the section labels across rows.
func renderPluginSections(sections []sectionBlock, innerW int) string {
	cols := columnsForWidth(innerW)
	if len(cols) == 1 {
		parts := make([]string, len(sections))
		for i, s := range sections {
			parts[i] = pluginSection(s.label, s.items)
		}
		return strings.Join(parts, "\n")
	}

	const gutter = 2
	colWidth := (innerW - (len(cols)-1)*gutter) / len(cols)
	if colWidth < 1 {
		colWidth = 1
	}
	colStyle := lipgloss.NewStyle().Width(colWidth)

	rendered := make([]string, len(cols))
	for ci, indices := range cols {
		parts := make([]string, 0, len(indices))
		for _, si := range indices {
			parts = append(parts, pluginSection(sections[si].label, sections[si].items))
		}
		rendered[ci] = colStyle.Render(strings.Join(parts, "\n"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}

// pluginSection renders a labelled content group with a (none) fallback.
func pluginSection(label string, items []string) string {
	key := styleKey.Render(fmt.Sprintf("%-12s", label+":")) + "  "
	if len(items) == 0 {
		return key + styleMuted.Render("(none)")
	}
	lines := make([]string, len(items))
	indent := strings.Repeat(" ", 14)
	for i, item := range items {
		if i == 0 {
			lines[i] = key + styleVal.Render(item)
		} else {
			lines[i] = indent + styleVal.Render(item)
		}
	}
	return strings.Join(lines, "\n")
}

// mcpNames extracts just the server names from a []MCPServer slice.
func mcpNames(servers []state.MCPServer) []string {
	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = s.Name
	}
	return names
}

func pluginToggleCmd(id string) tea.Cmd {
	return func() tea.Msg {
		err := state.TogglePlugin(id)
		return toggleResultMsg{id: id, err: err}
	}
}

func clearStatusAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return clearStatusMsg{} })
}

// truncate expects a plain string (no ANSI codes).
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
