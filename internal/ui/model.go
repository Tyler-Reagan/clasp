package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

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
	cursors   [tabCount]int
	width     int
	height    int
	detail    viewport.Model
	ready     bool
	statusMsg string // transient feedback shown in the status bar
}

func New() (*Model, error) {
	st, err := state.Load()
	return &Model{st: st, loadErr: err}, nil
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
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab", "l":
			m.tab = (m.tab + 1) % tabCount
			m.cursors[m.tab] = clamp(m.cursors[m.tab], 0, m.listLen()-1)
			m.detail.GotoTop()
			m.detail.SetContent(m.detailContent())
			return m, nil
		case "shift+tab", "h":
			m.tab = (m.tab - 1 + tabCount) % tabCount
			m.cursors[m.tab] = clamp(m.cursors[m.tab], 0, m.listLen()-1)
			m.detail.GotoTop()
			m.detail.SetContent(m.detailContent())
			return m, nil
		case "up", "k":
			if m.cursors[m.tab] > 0 {
				m.cursors[m.tab]--
				m.detail.GotoTop()
				m.detail.SetContent(m.detailContent())
			}
			return m, nil
		case "down", "j":
			if m.cursors[m.tab] < m.listLen()-1 {
				m.cursors[m.tab]++
				m.detail.GotoTop()
				m.detail.SetContent(m.detailContent())
			}
			return m, nil
		case "r":
			return m, func() tea.Msg { return RefreshMsg{} }
		case " ":
			if m.tab == tabPlugins && m.st != nil {
				c := m.cursors[m.tab]
				if c < len(m.st.Plugins) {
					id := m.st.Plugins[c].ID
					return m, pluginToggleCmd(id)
				}
			}
		}
		// pass remaining keys (PgUp/PgDn/etc.) to detail viewport
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) View() string {
	if !m.ready {
		return "loading..."
	}
	sep := styleMuted.Render(strings.Repeat("─", m.width))
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(),
		sep,
		lipgloss.JoinHorizontal(lipgloss.Top,
			m.renderList(),
			m.renderDetail(),
		),
		m.renderStatus(),
	)
}

// ── rendering ────────────────────────────────────────────────────────────────

func (m Model) renderHeader() string {
	var tabs []string
	for i, name := range tabNames {
		if tab(i) == m.tab {
			tabs = append(tabs, styleTabActive.Render(name))
		} else {
			tabs = append(tabs, styleTabInactive.Render(name))
		}
	}
	title := styleTitle.Render("clasp")
	tabRow := strings.Join(tabs, "")
	pad := max(0, m.width-lipgloss.Width(title)-lipgloss.Width(tabRow))
	return title + strings.Repeat(" ", pad) + tabRow
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
			line = stylePrimary.Render("▶") + r.ind + " " + styleSelected.Render(label)
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
	outerW, outerH := m.detailOuterDims()
	innerW := outerW - 2 // border adds 2
	innerH := outerH     // Height() is inner in lipgloss v1.x; outer = innerH+2 but outerH already accounts for that — see panelH comment
	padded := lipgloss.NewStyle().Padding(0, 1).Render(m.detail.View())
	return styleBorder.Width(innerW).Height(innerH).Render(padded)
}

func (m Model) renderStatus() string {
	var right string
	if m.statusMsg != "" {
		right = m.statusMsg
	} else if m.loadErr != nil {
		right = styleYellow.Render("warn: " + m.loadErr.Error())
	}

	hint := styleKey.Render("jk") + styleMuted.Render(" nav  ") +
		styleKey.Render("hl/tab") + styleMuted.Render(" switch  ") +
		styleKey.Render("PgUp/Dn") + styleMuted.Render(" scroll  ") +
		styleKey.Render("r") + styleMuted.Render(" refresh  ") +
		styleKey.Render("q") + styleMuted.Render(" quit")
	if m.tab == tabPlugins {
		hint += "  " + styleKey.Render("space") + styleMuted.Render(" toggle")
	}

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
		return styleRed.Render("could not load ~/.claude state")
	}
	c := m.cursors[m.tab]
	switch m.tab {
	case tabSessions:
		if c >= len(m.st.Sessions) {
			return styleMuted.Render("no active sessions")
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
			return styleMuted.Render("no skills installed")
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
			return styleMuted.Render("no plugins installed")
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
		lines = append(lines, pluginSection("Skills", p.SkillNames))
		lines = append(lines, pluginSection("MCP Servers", mcpNames(p.MCPServers)))
		lines = append(lines, pluginSection("Commands", p.Commands))
		lines = append(lines, pluginSection("Agents", p.Agents))
		lines = append(lines, pluginSection("Hooks", p.HookEvents))
		claudeMD := "(none)"
		if p.HasClaudeMD {
			claudeMD = styleGreen.Render("yes")
		}
		lines = append(lines, kv("CLAUDE.md", claudeMD))
		return strings.Join(lines, "\n")

	case tabMCP:
		if c >= len(m.st.MCPServers) {
			return styleMuted.Render("no MCP servers configured")
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
			return styleMuted.Render("no memory entries")
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

// panelH is the INNER content height for both panel boxes.
// Accounting for all fixed rows: 1 header + 1 sep + 2 border rows (top+bottom) + 1 status = 5.
// lipgloss v1.x Height() is inner; outer = inner+2. So panelH+2 = m.height-3 outer height.
// Total: 1+1+(m.height-3)+1 = m.height. ✓
func (m Model) panelH() int { return max(1, m.height-5) }

// detailOuterDims returns the outer (including border) width and the INNER height for the detail pane.
func (m Model) detailOuterDims() (w, h int) {
	return m.width - m.listOuter(), m.panelH()
}

// viewportDims returns the viewport width/height.
// Detail outer width = m.width - listOuter.
// Detail inner width = outerW - 2 (border).
// Viewport width = inner - 2 (Padding(0,1) left+right).
// Viewport height = panelH - 2 (border top+bottom already in outer; but Height() is inner so
// viewport fits within the inner space minus the Padding(0,0) height — no vertical padding, so
// viewport height = panelH - 2 to leave room for the border rows that lipgloss adds on top).
//
// Wait: styleBorder.Height(panelH) → inner=panelH, outer=panelH+2.
// viewport fills the inner area. No vertical padding inside.
// But the padded view uses Padding(0,1) (no vertical pad) wrapping the viewport view.
// So viewport height should equal the inner height = panelH.
// We set it to panelH-2 to leave a comfortable 2-row buffer so glamour content
// doesn't push the border out.
func (m Model) viewportDims() (w, h int) {
	outerW, outerH := m.detailOuterDims()
	return max(1, outerW-4), max(1, outerH-2)
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
