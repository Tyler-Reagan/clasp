package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tylerreagan/clasp/internal/state"
)

type tab int

const (
	tabSessions tab = iota
	tabSkills
	tabPlugins
	tabMemory
	tabCount
)

var tabNames = [tabCount]string{"Sessions", "Skills", "Plugins", "Memory"}

type RefreshMsg struct{}

type toggleResultMsg struct {
	id  string
	err error
}

type clearStatusMsg struct{}

// rowItem separates the styled indicator from the plain label so truncation
// operates only on plain bytes (no ANSI escape codes).
type rowItem struct {
	ind   string // already-styled indicator rune, e.g. styleGreen.Render("●")
	indW  int    // visual width of ind (typically 1)
	label string // plain text; safe to truncate with len()
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
	lw := m.listWidth()
	contentW := lw - 2 // border steals 2
	bodyH := m.panelH()

	start := max(0, cursor-bodyH+1)
	end := min(start+bodyH, len(rows))

	var lines []string
	for i := start; i < end; i++ {
		r := rows[i]
		// 1 left-pad + indicator + 1 space + label
		prefix := " " + r.ind + " "
		prefixVisW := 1 + r.indW + 1
		label := truncate(r.label, contentW-prefixVisW)
		line := prefix + label
		if i == cursor {
			lines = append(lines, styleSelected.Width(contentW).Render(line))
		} else {
			lines = append(lines, styleNormal.Width(contentW).Render(line))
		}
	}
	if len(lines) == 0 {
		lines = []string{styleMuted.Render(" (none)")}
	}
	return styleBorder.Width(lw).Height(bodyH).Render(strings.Join(lines, "\n"))
}

func (m Model) renderDetail() string {
	dw, dh := m.detailOuterDims()
	// 1-char padding on each side inside the border
	padded := lipgloss.NewStyle().Padding(0, 1).Render(m.detail.View())
	return styleBorder.Width(dw).Height(dh).Render(padded)
}

func (m Model) renderStatus() string {
	var right string
	if m.statusMsg != "" {
		right = m.statusMsg
	} else if m.loadErr != nil {
		right = styleYellow.Render("⚠ " + m.loadErr.Error())
	}

	hint := styleKey.Render("↑↓/jk") + styleMuted.Render(" nav  ") +
		styleKey.Render("tab/hl") + styleMuted.Render(" switch  ") +
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
	case tabMemory:
		rows := make([]rowItem, len(m.st.Memory))
		for i, e := range m.st.Memory {
			name := e.Name
			if name == "" {
				name = strings.TrimSuffix(e.File, ".md")
			}
			t := e.Type
			if t == "" {
				t = "·"
			}
			rows[i] = rowItem{ind: styleMuted.Render(t[:1]), indW: 1, label: name}
		}
		return rows
	}
	return nil
}

func (m Model) listLen() int { return len(m.listRows()) }

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
		return strings.Join([]string{
			styleDetailTitle.Render(sk.Name),
			"",
			kv("Description", sk.Description),
			kv("Version", sk.Version),
		}, "\n")

	case tabPlugins:
		if c >= len(m.st.Plugins) {
			return styleMuted.Render("no plugins installed")
		}
		p := m.st.Plugins[c]
		statusStr := styleRed.Render("disabled")
		if p.Enabled {
			statusStr = styleGreen.Render("enabled")
		}
		return strings.Join([]string{
			styleDetailTitle.Render(p.ID),
			"",
			kv("Version", p.Version()),
			"Status:  " + statusStr,
			kv("Scope", p.Scope()),
			kv("Installed", p.InstalledAt()),
		}, "\n")

	case tabMemory:
		if c >= len(m.st.Memory) {
			return styleMuted.Render("no memory entries")
		}
		e := m.st.Memory[c]
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
			"",
		}, "\n")
		return header + styleMuted.Render(e.Body)
	}
	return ""
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (m Model) listWidth() int { return 32 }

// panelH is the inner content height for both panels (header + sep + status + 2 borders = 5 rows).
func (m Model) panelH() int { return m.height - 5 }

// detailOuterDims returns the Width/Height args for styleBorder around the detail pane.
func (m Model) detailOuterDims() (w, h int) {
	return m.width - m.listWidth(), m.panelH()
}

// viewportDims returns the viewport width/height: inner content minus padding on each side.
func (m Model) viewportDims() (w, h int) {
	outerW, outerH := m.detailOuterDims()
	return outerW - 2 - 2, outerH - 2 // -2 border, -2 horizontal padding
}

func kv(k, v string) string {
	return styleKey.Render(fmt.Sprintf("%-12s", k+":")) + "  " + styleVal.Render(v)
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
// The encoding is: path separator "/" replaced by "-", e.g.
// /Users/foo/Projects/bar → -Users-foo-Projects-bar
// We strip the home prefix and show the remainder.
func projectLabel(encoded string) string {
	home, _ := os.UserHomeDir()
	// encode the home dir the same way (replace / with -)
	encodedHome := strings.ReplaceAll(strings.TrimPrefix(home, "/"), "/", "-")
	label := strings.TrimPrefix(encoded, "-"+encodedHome)
	label = strings.TrimPrefix(label, "-")
	if label == "" {
		return encoded
	}
	return strings.ReplaceAll(label, "-", "/")
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

// truncate expects a plain string (no ANSI codes). len() == visual width for ASCII/short labels.
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
