package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/tylerreagan/clasp/internal/cruft"
	"github.com/tylerreagan/clasp/internal/inventory"
	"github.com/tylerreagan/clasp/internal/state"
)

type tab int

const (
	tabSessions tab = iota
	tabSkills
	tabPlugins
	tabMCP
	tabMemory
	tabSettings
	tabCruft
	tabCount
)

var tabNames = [tabCount]string{"Sessions", "Skills", "Plugins", "MCP", "Memory", "Settings", "Cruft"}

// group clusters tabs for [ / ] navigation; h/l/tab cycle within a group.
type group int

const (
	groupInventory group = iota
	groupConfig
	groupCleanup
	groupCount
)

var groupNames = [groupCount]string{"Inventory", "Config", "Cleanup"}

// groupTabs lists each group's tabs in display order.
var groupTabs = [groupCount][]tab{
	groupInventory: {tabSessions, tabSkills, tabPlugins, tabMCP, tabMemory},
	groupConfig:    {tabSettings},
	groupCleanup:   {tabCruft},
}

// groupOf maps a tab back to its group.
func groupOf(t tab) group {
	for g, ts := range groupTabs {
		for _, x := range ts {
			if x == t {
				return group(g)
			}
		}
	}
	return groupInventory
}

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

type deleteResultMsg struct {
	name string
	err  error
}

type clearStatusMsg struct{}

// deleteTarget identifies a memory entry awaiting delete confirmation. The
// name is carried for the prompt and the success message; project+file locate
// the .md file on disk.
type deleteTarget struct {
	project string
	file    string
	name    string
}

// trashTarget is an inventory item whose leftover copies await trash
// confirmation; n and size cover distinct paths.
type trashTarget struct {
	kind inventory.Kind
	name string
	n    int
	dirs string // the directories that will move, e.g. "design~g1", so a skill row names its plugin
	size int64
}

// trashResultMsg reports a TrashLeftovers run.
type trashResultMsg struct {
	moved   []string
	refused map[string]string
	err     error
}

// trashLeftovers is swapped by tests so they never touch the filesystem.
var trashLeftovers = state.TrashLeftovers

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
	// tail is a pre-styled, right-aligned run (flags and marks) that is
	// tailW cells wide; the label is truncated and padded to leave room.
	tail  string
	tailW int
}

type Model struct {
	st        *state.State
	loadErr   error
	tab       tab
	lastTab   [groupCount]tab // last-used tab per group; zero/invalid entries fall back to the group's first tab
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

	// Settings tab.
	project         string         // "" = follow the latest session
	settingsAll     bool           // a: list every catalog key, not just set ones
	srcFilter       [tabCount]int  // s: per-tab source filter (Skills, Plugins, MCP)
	picker          *projectPicker // p: project picker modal; nil when closed
	projects        chan<- string  // resolved project → watcher; nil in tests
	lastSentProject string

	// pendingDelete is non-nil while a memory delete awaits y/n confirmation.
	// Like showHelp it takes modal priority: the next key either confirms (y)
	// or cancels (anything else). Delete is irreversible, so it never fires on
	// a single keypress the way the plugin toggle does.
	pendingDelete *deleteTarget

	// pendingTrash is the sibling modal for moving an inventory item's
	// leftover copies to the Trash; same priority and y/anything-else rules.
	pendingTrash *trashTarget

	// Cruft tab: the last scan (nil until one finishes), whether one is in
	// flight, a generation that drops stale results, and the removal awaiting
	// y/n.
	cruft         *cruft.Report
	cruftScanning bool
	cruftGen      int
	pendingCruft  *cruftTarget
}

// New loads state and builds the model. projects, if non-nil, receives the
// resolved settings project dir whenever it changes so the watcher can follow
// it; nil is fine (tests).
func New(projects chan<- string) (*Model, error) {
	st, err := state.LoadFor("")
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

		projects: projects,
	}
	for g := group(0); g < groupCount; g++ {
		m.lastTab[g] = groupTabs[g][0]
	}
	m.lastTab[groupOf(m.tab)] = m.tab
	// Zoom/UnZoom/Toggle enabled state depends on current focus and tab.
	m.refreshBindings()
	m.publishProject()
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
		st, err := state.LoadFor(m.project)
		m.st, m.loadErr = st, err
		m.refreshBindings()
		// A delete (or any external change) can shrink a list out from under
		// its cursor; clamp every tab so each stays on a valid row.
		m.clampCursors()
		m.publishProject()
		m.detail.SetContent(m.detailContent())
		return m, nil

	case toggleResultMsg:
		if msg.err != nil {
			m.statusMsg = styleRed.Render("✗ " + msg.err.Error())
		} else {
			m.statusMsg = styleGreen.Render("✓ toggled " + pluginShortName(msg.id))
		}
		return m, clearStatusAfter(3 * time.Second)

	case deleteResultMsg:
		if msg.err != nil {
			m.statusMsg = styleRed.Render("✗ " + msg.err.Error())
			return m, clearStatusAfter(3 * time.Second)
		}
		m.statusMsg = styleGreen.Render("🗑 trashed " + msg.name)
		// Reload immediately so the row disappears without waiting on the watcher,
		// then clear the toast after a beat.
		return m, tea.Batch(
			func() tea.Msg { return RefreshMsg{} },
			clearStatusAfter(3*time.Second),
		)

	case trashResultMsg:
		switch {
		case msg.err != nil:
			m.statusMsg = styleRed.Render("✗ " + msg.err.Error())
		default:
			s := styleGreen.Render(fmt.Sprintf("moved %d to the Trash", len(msg.moved)))
			if len(msg.moved) == 0 {
				s = styleYellow.Render("nothing moved")
			}
			if k := len(msg.refused); k > 0 {
				paths := make([]string, 0, k)
				for p := range msg.refused {
					paths = append(paths, p)
				}
				sort.Strings(paths)
				s += styleYellow.Render(fmt.Sprintf(" · %d refused: %s", k, msg.refused[paths[0]]))
			}
			m.statusMsg = s
		}
		return m, tea.Batch(
			func() tea.Msg { return RefreshMsg{} },
			clearStatusAfter(4*time.Second),
		)

	case cruftScanMsg:
		if msg.gen != m.cruftGen {
			return m, nil
		}
		m.cruft, m.cruftScanning = msg.report, false
		m.clampCursors()
		m.detail.SetContent(m.detailContent())
		return m, nil

	case cruftActionMsg:
		if msg.err != nil {
			m.statusMsg = styleRed.Render("✗ " + msg.err.Error())
		} else {
			m.statusMsg = styleGreen.Render("✓ " + msg.what)
		}
		return m, tea.Batch(m.startCruftScan(), clearStatusAfter(4*time.Second))

	case clearStatusMsg:
		m.statusMsg = ""
		return m, nil

	case tea.KeyMsg:
		// Modal priority: the ? overlay swallows keys before focus dispatch.
		// ctrl+c still quits (universal kill); esc/?/q dismiss the overlay.
		if m.showHelp {
			return m.updateHelp(msg)
		}
		if m.picker != nil {
			return m.updatePicker(msg)
		}

		// A pending delete confirmation is also modal: the next key is the
		// y/n answer, captured before any normal dispatch.
		if m.pendingDelete != nil {
			return m.updateConfirmDelete(msg)
		}

		if m.pendingTrash != nil {
			return m.updateConfirmTrash(msg)
		}

		if m.pendingCruft != nil {
			return m.updateConfirmCruft(msg)
		}

		// Global keys: handled regardless of focus.
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Refresh):
			if m.tab == tabCruft {
				return m, tea.Batch(func() tea.Msg { return RefreshMsg{} }, m.startCruftScan())
			}
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

// updateConfirmDelete handles the y/n answer to a pending memory delete.
// Only an explicit y/Y proceeds; every other key (esc, n, or a stray press)
// cancels — the safe default for a destructive, irreversible action.
func (m Model) updateConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "y", "Y":
		t := *m.pendingDelete
		m.pendingDelete = nil
		return m, memoryDeleteCmd(t)
	default:
		m.pendingDelete = nil
		m.statusMsg = styleMuted.Render("· delete cancelled")
		return m, clearStatusAfter(2 * time.Second)
	}
}

// updateConfirmTrash handles the y/n answer to a pending leftover trash.
func (m Model) updateConfirmTrash(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "y", "Y":
		t := *m.pendingTrash
		m.pendingTrash = nil
		return m, func() tea.Msg {
			moved, refused, err := trashLeftovers(t.kind, t.name)
			return trashResultMsg{moved: moved, refused: refused, err: err}
		}
	default:
		m.pendingTrash = nil
		m.statusMsg = styleMuted.Render("· trash cancelled")
		return m, clearStatusAfter(2 * time.Second)
	}
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
		m.setTab(m.cycleTab(+1))
		return m.afterTabSwitch()
	case key.Matches(msg, m.keys.PrevTab):
		m.setTab(m.cycleTab(-1))
		return m.afterTabSwitch()
	case key.Matches(msg, m.keys.NextGroup):
		m.setTab(m.groupLanding((groupOf(m.tab) + 1) % groupCount))
		return m.afterTabSwitch()
	case key.Matches(msg, m.keys.PrevGroup):
		m.setTab(m.groupLanding((groupOf(m.tab) - 1 + groupCount) % groupCount))
		return m.afterTabSwitch()
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
			return m.toggleSelectedPlugin()
		}
		return m, nil
	case key.Matches(msg, m.keys.Source):
		m.cycleSource()
		m.detail.GotoTop()
		m.detail.SetContent(m.detailContent())
		return m, nil
	case key.Matches(msg, m.keys.Trash):
		if m.tab == tabCruft {
			return m.trashCruftSelected()
		}
		if !invTab(m.tab) {
			return m, nil
		}
		return m.trashSelected()
	case key.Matches(msg, m.keys.Remove):
		if m.tab != tabCruft {
			return m, nil
		}
		return m.removeCruftSelected()
	case key.Matches(msg, m.keys.ShowAll):
		m.toggleSettingsAll()
		m.detail.GotoTop()
		m.detail.SetContent(m.detailContent())
		return m, nil
	case key.Matches(msg, m.keys.Project):
		m.picker = newProjectPicker(m)
		return m, nil
	case key.Matches(msg, m.keys.Delete):
		if m.tab != tabMemory || m.st == nil {
			return m, nil
		}
		idx := m.selectedMemoryIdx()
		if idx < 0 || idx >= len(m.st.Memory) {
			return m, nil
		}
		e := m.st.Memory[idx]
		// MEMORY.md surfaces as a row but is the index, not a memory — refuse it.
		if e.File == "MEMORY.md" {
			m.statusMsg = styleRed.Render("✗ MEMORY.md is the index — not deletable")
			return m, clearStatusAfter(3 * time.Second)
		}
		name := e.Name
		if name == "" {
			name = strings.TrimSuffix(e.File, ".md")
		}
		m.pendingDelete = &deleteTarget{project: e.Project, file: e.File, name: name}
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
	case key.Matches(msg, m.keys.NextTab), key.Matches(msg, m.keys.PrevTab),
		key.Matches(msg, m.keys.NextGroup), key.Matches(msg, m.keys.PrevGroup):
		// Forgiving switch: exit zoom AND switch tab/group, landing in browse
		// mode on the new tab.
		m.focus = focusList
		switch {
		case key.Matches(msg, m.keys.NextTab):
			m.setTab(m.cycleTab(+1))
		case key.Matches(msg, m.keys.PrevTab):
			m.setTab(m.cycleTab(-1))
		case key.Matches(msg, m.keys.NextGroup):
			m.setTab(m.groupLanding((groupOf(m.tab) + 1) % groupCount))
		default:
			m.setTab(m.groupLanding((groupOf(m.tab) - 1 + groupCount) % groupCount))
		}
		m.resizeDetail()
		return m.afterTabSwitch()
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

// setTab makes t current and remembers it as its group's last-used tab.
func (m *Model) setTab(t tab) {
	m.tab = t
	m.lastTab[groupOf(t)] = t
}

// cycleTab returns the next (dir=+1) or previous (dir=-1) tab within the
// current group, wrapping. A one-tab group returns the current tab.
func (m Model) cycleTab(dir int) tab {
	ts := groupTabs[groupOf(m.tab)]
	for i, t := range ts {
		if t == m.tab {
			return ts[(i+dir+len(ts))%len(ts)]
		}
	}
	return m.tab
}

// groupLanding is the tab a group switch lands on: the group's last-used tab,
// or its first tab when none was recorded.
func (m Model) groupLanding(g group) tab {
	for _, t := range groupTabs[g] {
		if t == m.lastTab[g] {
			return t
		}
	}
	return groupTabs[g][0]
}

// afterTabSwitch is the shared tail of every tab or group switch: clamp the
// new tab's cursor, refresh bindings and detail, and request a fresh load.
func (m Model) afterTabSwitch() (tea.Model, tea.Cmd) {
	m.cursors[m.tab] = clamp(m.cursors[m.tab], 0, m.listLen()-1)
	m.refreshBindings()
	m.detail.GotoTop()
	m.detail.SetContent(m.detailContent())
	cmds := []tea.Cmd{func() tea.Msg { return RefreshMsg{} }}
	if m.tab == tabCruft && m.cruft == nil && !m.cruftScanning {
		cmds = append(cmds, m.startCruftScan())
		m.detail.SetContent(m.detailContent())
	}
	return m, tea.Batch(cmds...)
}

// refreshBindings updates per-binding Enabled state based on the current
// focus and tab. Called whenever those change so help.Model only shows
// the keys that actually fire in the current context.
func (m *Model) refreshBindings() {
	m.keys.Zoom.SetEnabled(m.focus == focusList)
	m.keys.UnZoom.SetEnabled(m.focus == focusZoomedDetail)
	m.keys.Toggle.SetEnabled(m.focus == focusList && m.tab == tabPlugins)
	settings := m.focus == focusList && m.tab == tabSettings
	m.keys.ShowAll.SetEnabled(settings)
	m.keys.Project.SetEnabled(settings)
	m.keys.Delete.SetEnabled(m.focus == focusList && m.tab == tabMemory)
	cruftTab := m.focus == focusList && m.tab == tabCruft
	m.keys.Trash.SetEnabled(m.focus == focusList && invTab(m.tab) || cruftTab)
	if m.tab == tabCruft {
		m.keys.Trash.SetHelp("t", "trash transcripts")
	} else {
		m.keys.Trash.SetHelp("t", "trash leftovers")
	}
	m.keys.Remove.SetEnabled(cruftTab)
	m.keys.Source.SetEnabled(m.focus == focusList && invTab(m.tab) && m.hasDesktop())
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
	if m.picker != nil {
		return m.renderPicker()
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
	cur := groupOf(m.tab)

	// Line 3: the groups. Line 4: a chevron then the current group's tabs.
	var groups []string
	for g := group(0); g < groupCount; g++ {
		if g == cur {
			groups = append(groups, styleTabActive.Render(groupNames[g]))
		} else {
			groups = append(groups, styleTabInactive.Render(groupNames[g]))
		}
	}
	var tabs []string
	for _, t := range groupTabs[cur] {
		name := tabNames[t]
		if t == m.tab {
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
	wordmarkW := lipgloss.Width(wordmark)
	wordmarkH := lipgloss.Height(wordmark)
	avail := max(0, m.width-wordmarkW)

	// rightAlign pads row to avail cells, truncating (never wrapping) when it
	// would overflow.
	rightAlign := func(row string) string {
		if lipgloss.Width(row) > avail {
			row = ansi.Truncate(row, avail, "")
		}
		return strings.Repeat(" ", max(0, avail-lipgloss.Width(row))) + row
	}
	groupRow := rightAlign(strings.Join(groups, ""))
	tabRow := rightAlign(styleMuted.Render("›") + strings.Join(tabs, ""))

	// Rows 3 and 4 of the 4-line wordmark; the first two stay empty.
	rows := make([]string, wordmarkH)
	rows[wordmarkH-2] = groupRow
	rows[wordmarkH-1] = tabRow
	rightCol := strings.Join(rows, "\n")

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
	cursorVisIdx := visualIndex(rows, cursor)

	start := max(0, cursorVisIdx-bodyH+1)
	end := min(start+bodyH, len(rows))

	var lines []string
	for i := start; i < end; i++ {
		r := rows[i]
		var line string
		if r.isHeader && r.tailW > 0 {
			w := listInner - 1 - r.tailW
			line = styleMuted.Render(" "+padRight(truncate(r.label, w), w)) + r.tail
		} else if r.isHeader {
			// Project group header: muted, no cursor indicator, indented label.
			label := truncate(r.label, listInner-2)
			line = styleMuted.Render(" " + label)
		} else if i == cursorVisIdx {
			label := fitLabel(r, labelW)
			line = stylePrimary.Render("▌") + r.ind + " " + styleSelected.Render(label) + r.tail
		} else {
			label := fitLabel(r, labelW)
			line = " " + r.ind + " " + label + r.tail
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
	if m.pendingDelete != nil {
		// Persistent (no clear-tick) until the y/n answer arrives. y is copper
		// to read as the active choice; N is capitalized to signal the default.
		right = styleYellow.Render("trash '"+m.pendingDelete.name+"'? ") +
			stylePrimary.Render("y") + styleMuted.Render("/N")
	} else if m.pendingTrash != nil {
		t := m.pendingTrash
		right = styleYellow.Render(fmt.Sprintf("trash %s (%s)? ", t.dirs, humanSize(t.size))) +
			stylePrimary.Render("y") + styleMuted.Render("/N")
	} else if m.pendingCruft != nil {
		right = m.cruftPrompt()
	} else if m.statusMsg != "" {
		right = m.statusMsg
	} else if m.loadErr != nil {
		right = styleRed.Render("⚠ " + m.loadErr.Error())
	} else if m.tab == tabCruft && m.cruftScanning {
		right = styleMuted.Render("scanning…")
	} else {
		right = m.invSummary(m.tab)
	}

	// help.Model truncates with "…" at Width; reserve the bar's padding and
	// the right-hand message so a long hint never wraps onto a second line.
	h := m.help
	h.Width = max(0, m.width-3-lipgloss.Width(right)) // one space before right
	hint := h.View(contextKeys{keys: m.keys, focus: m.focus})
	// help.Model (bubbles v1.0.0) adds every remaining binding once its "…"
	// no longer fits, so cut the hint to the reserved width as well.
	hint = ansi.Truncate(hint, h.Width, "…")
	pad := max(0, m.width-2-lipgloss.Width(hint)-lipgloss.Width(right))
	bar := hint + strings.Repeat(" ", pad) + right

	return lipgloss.NewStyle().
		Background(colorBorder).
		Width(m.width).
		Padding(0, 1).
		Render(bar)
}

// ── list rows ─────────────────────────────────────────────────────────────────

func (m Model) listRows() []rowItem { return m.rowsFor(m.tab) }

func (m Model) rowsFor(t tab) []rowItem {
	if t == tabCruft {
		return m.cruftRows()
	}
	if m.st == nil {
		return nil
	}
	switch t {
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
	case tabSkills, tabPlugins, tabMCP:
		return m.invRows(t)
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
			row := rowItem{
				ind:     styleMuted.Render(string([]rune(t)[0])),
				indW:    1,
				label:   name,
				dataIdx: i,
			}
			if len(m.memoryIssuesFor(e.Project, e.File)) > 0 {
				row.tail, row.tailW = " "+styleYellow.Render("⚠"), 2
			}
			rows = append(rows, row)
		}
		return rows
	case tabSettings:
		return m.settingsRows()
	}
	return nil
}

func (m Model) listLen() int { return m.lenFor(m.tab) }

// lenFor counts selectable rows: the cursor only lands on non-header rows.
func (m Model) lenFor(t tab) int {
	n := 0
	for _, r := range m.rowsFor(t) {
		if !r.isHeader {
			n++
		}
	}
	return n
}

// clampCursors pulls every tab's cursor back inside its list after a reload
// shrinks it.
func (m *Model) clampCursors() {
	for t := tab(0); t < tabCount; t++ {
		m.cursors[t] = clamp(m.cursors[t], 0, m.lenFor(t)-1)
	}
}

// visualIndex maps a cursor (the Nth selectable row) to its index in rows.
// A cursor past the end maps to itself.
func visualIndex(rows []rowItem, cursor int) int {
	sel := 0
	for i, r := range rows {
		if r.isHeader {
			continue
		}
		if sel == cursor {
			return i
		}
		sel++
	}
	return cursor
}

// publishProject tells the watcher which project the Settings tab resolved,
// once per change. The send never blocks; when the channel is full the value
// stays unsent and the next load retries.
func (m *Model) publishProject() {
	if m.projects == nil || m.st == nil || m.st.Settings == nil {
		return
	}
	p := m.st.Settings.Project
	if p == m.lastSentProject {
		return
	}
	select {
	case m.projects <- p:
		m.lastSentProject = p
	default:
	}
}

// selectedMemoryIdx maps the Memory-tab cursor (the Nth selectable row) to an
// index into m.st.Memory, skipping the project group headers. Returns -1 when
// the current tab isn't Memory or nothing resolves.
func (m Model) selectedMemoryIdx() int {
	if m.tab != tabMemory {
		return -1
	}
	c := m.cursors[tabMemory]
	sel := 0
	for _, row := range m.listRows() {
		if row.isHeader {
			continue
		}
		if sel == c {
			return row.dataIdx
		}
		sel++
	}
	return -1
}

// ── detail content ────────────────────────────────────────────────────────────

func (m Model) detailContent() string {
	if m.tab == tabCruft {
		return m.cruftDetail()
	}
	if m.st == nil {
		return styleRed.Render("⚠ could not load ~/.claude state")
	}
	c := m.cursors[m.tab]
	switch m.tab {
	case tabSettings:
		return m.settingsDetail(c)
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
			kvWrap("CWD", shortPath(s.CWD), m.detail.Width),
			kv("Status", s.Status),
			kv("Version", s.Version),
		}, "\n")

	case tabSkills, tabPlugins, tabMCP:
		return m.invDetail(m.tab)

	case tabMemory:
		memIdx := m.selectedMemoryIdx()
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
		if is := m.memoryIssuesFor(e.Project, e.File); len(is) > 0 {
			lines := []string{header, "", styleYellow.Render("⚠ Cruft scan found")}
			for _, i := range is {
				txt := "· " + i.Kind.String()
				if i.Detail != "" {
					txt += ": " + i.Detail
				}
				lines = append(lines, noteLines(txt, m.detail.Width)...)
			}
			header = strings.Join(lines, "\n")
		}
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
//
//	→ panel outer = m.height - 6
//	→ panel inner = panel outer - 2 = m.height - 8
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
		if wLen > width {
			// a single word wider than the line (usually a path): flush the
			// current line, then break the word so nothing is clipped
			if current != "" {
				lines = append(lines, current)
			}
			pieces := breakWord(w, width)
			lines = append(lines, pieces[:len(pieces)-1]...)
			current = pieces[len(pieces)-1]
			continue
		}
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

// breakWord splits w into pieces of at most width runes, preferring to cut
// just after a "/" so paths break at directory boundaries.
func breakWord(w string, width int) []string {
	r := []rune(w)
	var out []string
	for len(r) > width {
		cut := width
		for i := width - 1; i > 0; i-- {
			if r[i] == '/' {
				cut = i + 1
				break
			}
		}
		out = append(out, string(r[:cut]))
		r = r[cut:]
	}
	return append(out, string(r))
}

// fitLabel truncates a row label to width, leaving room for its tail and
// padding so the tail ends flush right.
func fitLabel(r rowItem, width int) string {
	if r.tailW == 0 {
		return truncate(r.label, width)
	}
	w := max(1, width-r.tailW)
	return padRight(truncate(r.label, w), w)
}

func padRight(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
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

func memoryDeleteCmd(t deleteTarget) tea.Cmd {
	return func() tea.Msg {
		err := state.DeleteMemory(t.project, t.file)
		return deleteResultMsg{name: t.name, err: err}
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
