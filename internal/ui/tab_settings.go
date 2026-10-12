package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tylerreagan/clasp/internal/settings"
)

// ── list ─────────────────────────────────────────────────────────────────────

// settingsRows builds the Settings list: two muted header rows, then one row
// per key. Set keys only, unless settingsAll. dataIdx indexes Snapshot.Items.
func (m Model) settingsRows() []rowItem {
	if m.st == nil || m.st.Settings == nil {
		return nil
	}
	snap := m.st.Settings

	set, warn := 0, 0
	for _, it := range snap.Items {
		if it.Set() {
			set++
		}
		if settingsWarns(it) {
			warn++
		}
	}

	proj := "project: none"
	if snap.Project != "" {
		proj = "project: " + filepath.Base(snap.Project)
		if snap.ProjectAuto {
			proj += " (auto)"
		}
	}
	counts := fmt.Sprintf("%d set · %d ▲ · a: all", set, warn)
	if m.settingsAll {
		counts = fmt.Sprintf("%d keys · a: set only", len(snap.Items))
	}
	rows := []rowItem{
		{isHeader: true, label: proj, dataIdx: -1},
		{isHeader: true, label: counts, dataIdx: -1},
	}

	// Prefix is cursor + indicator + space (3 columns), see renderList.
	labelW := m.listOuter() - 2 - 3
	for i, it := range snap.Items {
		if !m.settingsAll && !it.Set() {
			continue
		}
		rows = append(rows, rowItem{
			ind:     settingsIndicator(it),
			indW:    1,
			label:   settingsLabel(it.Key, sourceTag(it), labelW),
			dataIdx: i,
		})
	}
	return rows
}

// settingsWarns reports whether a set key shows the ▲ indicator.
func settingsWarns(it settings.Item) bool {
	return it.Set() && (it.Warnings > 0 || it.Doc.Removed != "")
}

func settingsIndicator(it settings.Item) string {
	switch {
	case !it.Set():
		return styleMuted.Render("·")
	case settingsWarns(it):
		return styleYellow.Render("▲")
	case !it.Documented:
		return styleYellow.Render("?")
	}
	return styleGreen.Render("●")
}

// sourceTag is the short right-aligned tag in the list (7 columns at most).
func sourceTag(it settings.Item) string {
	if !it.Set() {
		return "—"
	}
	src := it.Sources()
	switch {
	case len(src) == 0:
		return "ignored"
	case it.Winner != nil:
		return it.Winner.Kind.Label()
	case len(src) == 1:
		return src[0].Kind.Label()
	}
	return "merged"
}

// settingsLabel is the key truncated to leave room for tag, then padded so
// the tag ends flush with the right edge of width columns.
func settingsLabel(keyName, tag string, width int) string {
	tagW := utf8.RuneCountInString(tag)
	keyW := width - tagW - 1
	if keyW < 1 {
		return truncate(keyName, width)
	}
	k := truncate(keyName, keyW)
	pad := width - utf8.RuneCountInString(k) - tagW
	return k + strings.Repeat(" ", max(1, pad)) + tag
}

// toggleSettingsAll flips between set-only and all keys. The selected key
// stays selected when it is still listed; otherwise the cursor is clamped.
func (m *Model) toggleSettingsAll() {
	var selected *settings.Item
	if snap := m.settingsSnapshot(); snap != nil {
		if it, ok := m.settingsItem(m.cursors[tabSettings]); ok {
			selected = &it
		}
	}
	m.settingsAll = !m.settingsAll
	c := m.cursors[tabSettings]
	if selected != nil {
		n := 0
		for _, r := range m.settingsRows() {
			if r.isHeader {
				continue
			}
			if m.st.Settings.Items[r.dataIdx].Key == selected.Key {
				c = n
				break
			}
			n++
		}
	}
	m.cursors[tabSettings] = clamp(c, 0, m.lenFor(tabSettings)-1)
}

func (m Model) settingsSnapshot() *settings.Snapshot {
	if m.st == nil {
		return nil
	}
	return m.st.Settings
}

// settingsItem returns the item under the cursor (Nth selectable row).
func (m Model) settingsItem(cursor int) (settings.Item, bool) {
	snap := m.settingsSnapshot()
	if snap == nil {
		return settings.Item{}, false
	}
	sel := 0
	for _, r := range m.settingsRows() {
		if r.isHeader {
			continue
		}
		if sel == cursor {
			if r.dataIdx < 0 || r.dataIdx >= len(snap.Items) {
				return settings.Item{}, false
			}
			return snap.Items[r.dataIdx], true
		}
		sel++
	}
	return settings.Item{}, false
}

// ── detail ───────────────────────────────────────────────────────────────────

func (m Model) settingsDetail(cursor int) string {
	snap := m.settingsSnapshot()
	if snap == nil {
		return styleMuted.Render("· settings not loaded")
	}
	it, ok := m.settingsItem(cursor)
	if !ok {
		return emptyState("no keys set in any file · a lists all keys")
	}
	w := m.detail.Width
	if w <= 0 {
		w = 60
	}
	fetched := snap.Fetched
	if fetched == "" {
		fetched = settings.Default().Fetched()
	}

	var out []string
	add := func(lines ...string) { out = append(out, lines...) }
	blank := func() {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
	}

	add(styleDetailTitle.Render(it.Key))
	add(settingsDescription(it, fetched, w)...)
	add("")
	add(kvStyled("Value", settingsValue(it), w, valueStyle(it))...)
	add(kvStyled("Set by", setBy(it, snap), w, styleVal)...)
	add("")

	add(layerRows(it, snap, w)...)
	if it.Doc.Scope != settings.ScopeGlobal {
		if snap.MDMPresent {
			add(styleYellow.Render("MDM profile present; Claude Code may skip managed files."))
		} else {
			add(styleMuted.Render("Not seen by clasp: server-managed settings, MDM profile."))
		}
	}

	add("", styleMuted.Render(strings.Repeat("─", w)))
	d := it.Doc
	if d.Type != "" {
		add(kvStyled("Type", d.Type, w, styleVal)...)
		for _, tv := range d.TypeValues {
			add(indentLines(tv, 14, w, styleMuted)...)
		}
	}
	if d.Default != "" {
		add(kvStyled("Default", d.Default, w, styleVal)...)
	}
	if d.Scope != "" {
		files := string(d.Scope)
		if d.ScopeNote != "" {
			files += " · " + d.ScopeNote
		}
		add(kvStyled("Files", files, w, styleVal)...)
	}
	if ov := overrides(d); ov != "" {
		add(kvStyled("Overrides", ov, w, styleVal)...)
	}

	var notes []string
	for _, n := range it.Notes {
		if strings.HasPrefix(n, "Removed in") {
			continue // shown at the top
		}
		notes = append(notes, n)
	}
	if len(notes) > 0 {
		blank()
		for _, n := range notes {
			for _, l := range wrapWords(n, w) {
				add(styleMuted.Render(l))
			}
		}
	}

	if len(it.Parts) > 0 {
		blank()
		add(styleSelected.Render(fmt.Sprintf("Entries (%d)", len(it.Parts))))
		add(entryRows(it, w)...)
	}

	if it.Documented {
		blank()
		add(styleMuted.Render(fmt.Sprintf("Docs: settings-reference#%s · catalog fetched %s", d.Anchor, fetched)))
	}
	return strings.Join(out, "\n")
}

func settingsDescription(it settings.Item, fetched string, w int) []string {
	var out []string
	wrap := func(text string, style lipgloss.Style) {
		for _, l := range wrapWords(text, w) {
			out = append(out, style.Render(l))
		}
	}
	switch {
	case !it.Documented:
		wrap("Not in official docs. It can be a typo, a removed key, or a key newer than the catalog ("+fetched+").", styleYellow)
		return out
	case it.Doc.Removed != "":
		wrap("Removed in "+it.Doc.Removed+". Claude Code ignores it.", styleYellow)
		return out
	}
	text := it.Doc.STE
	if text == "" {
		text = it.Doc.Summary
	}
	if text != "" {
		wrap(text, styleVal)
	}
	return out
}

// maxValueRunes caps the Value line; longer JSON is cut with an ellipsis.
const maxValueRunes = 200

func valueStyle(it settings.Item) lipgloss.Style {
	if settingsValueMuted(it) {
		return styleMuted
	}
	return styleVal
}

func settingsValueMuted(it settings.Item) bool {
	if !it.Set() {
		return true
	}
	return it.Winner == nil && (it.Doc.Merge == settings.MergeUndocumented || !hasValue(it))
}

func hasValue(it settings.Item) bool {
	return it.Winner != nil || it.Value != nil
}

func settingsValue(it settings.Item) string {
	switch {
	case !it.Set():
		if it.Doc.Default != "" {
			return "unset · default: " + it.Doc.Default
		}
		return "unset"
	case it.Doc.Merge == settings.MergeUndocumented:
		return "see layers"
	case !hasValue(it):
		return "not applied · see layers"
	case len(it.Parts) > 0:
		// list and map keys: the Entries block shows each value with its source
		return fmt.Sprintf("%d entries · see Entries", len(it.Parts))
	}
	s := settings.FormatValue(it.Value)
	if utf8.RuneCountInString(s) > maxValueRunes {
		s = string([]rune(s)[:maxValueRunes-1]) + "…"
	}
	return s
}

func setBy(it settings.Item, snap *settings.Snapshot) string {
	src := it.Sources()
	switch {
	case !it.Set() || len(src) == 0:
		return "—"
	case it.Winner != nil:
		return it.Winner.Kind.Label() + " · " + layerPath(*it.Winner, snap)
	case len(src) == 1:
		return src[0].Kind.Label() + " · " + layerPath(*src[0], snap)
	}
	return fmt.Sprintf("merged from %d files", len(src))
}

func overrides(d settings.Doc) string {
	all := append(append([]string{}, d.OverrideFlags...), d.OverrideEnv...)
	return strings.Join(all, ", ")
}

// layerPath is the short form of a layer file: relative for project and local
// files, ~ for the home directory, absolute otherwise.
func layerPath(l settings.Layer, snap *settings.Snapshot) string {
	if (l.Kind == settings.LayerProject || l.Kind == settings.LayerLocal) && snap.Project != "" {
		if rel, err := filepath.Rel(snap.Project, l.Path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return shortPath(l.Path)
}

// layerRows renders the "Layers, highest first" block.
func layerRows(it settings.Item, snap *settings.Snapshot, w int) []string {
	const labelW = 10 // fits "--settings"
	type row struct {
		label, path, status string // status is styled; statusW is its width
		statusW             int
		muted               bool
	}
	global := it.Doc.Scope == settings.ScopeGlobal

	var rows []row
	sessionRow := func() {
		rows = append(rows, row{label: "--settings", path: "session flag", muted: true,
			status: styleMuted.Render("not visible to clasp"), statusW: len("not visible to clasp")})
	}
	if !global && !hasManaged(snap) {
		sessionRow()
	}
	for i := range snap.Layers {
		l := snap.Layers[i]
		if global != (l.Kind == settings.LayerGlobal) {
			continue
		}
		status, plain := layerStatus(it, l)
		rows = append(rows, row{label: l.Kind.Label(), path: layerPath(l, snap), status: status, statusW: utf8.RuneCountInString(plain)})
		if !global && l.Kind == settings.LayerManaged && lastManaged(snap, i) {
			sessionRow()
		}
	}

	pathW := 0
	for _, r := range rows {
		pathW = max(pathW, utf8.RuneCountInString(r.path))
	}
	// Leave room for the label, gaps, and a typical status; very long paths
	// are cut from the left so the file name stays visible. A status wider
	// than the reserve drops to its own line.
	statusW := 0
	for _, r := range rows {
		statusW = max(statusW, r.statusW)
	}
	statusW = min(statusW, 20)
	if limit := w - 2 - labelW - 1 - 2 - statusW; pathW > limit {
		pathW = max(12, limit)
	}

	out := []string{styleSelected.Render("Layers, highest first")}
	for _, r := range rows {
		path := truncateLeft(r.path, pathW)
		pad := strings.Repeat(" ", pathW-utf8.RuneCountInString(path))
		labelStyle := styleVal
		pathStyle := styleMuted
		if r.muted {
			labelStyle = styleMuted
		}
		head := "  " + labelStyle.Render(fmt.Sprintf("%-*s", labelW, r.label)) + " " + pathStyle.Render(path) + pad
		if lipgloss.Width(head)+2+r.statusW <= w {
			out = append(out, head+"  "+r.status)
		} else {
			out = append(out, head, strings.Repeat(" ", 2+labelW+1)+r.status)
		}
	}
	return out
}

func hasManaged(snap *settings.Snapshot) bool {
	for _, l := range snap.Layers {
		if l.Kind == settings.LayerManaged {
			return true
		}
	}
	return false
}

// lastManaged reports whether layer i is the final managed layer.
func lastManaged(snap *settings.Snapshot, i int) bool {
	return i+1 >= len(snap.Layers) || snap.Layers[i+1].Kind != settings.LayerManaged
}

// layerStatus returns the styled status for one layer and its plain text
// (for width math).
func layerStatus(it settings.Item, l settings.Layer) (styled, plain string) {
	if l.Err != nil {
		p := "invalid JSON"
		if !strings.HasPrefix(l.Err.Error(), "parse ") {
			p = "unreadable"
		}
		return styleRed.Render(p), p
	}
	for _, c := range it.Contribs {
		if c.Layer == nil || c.Layer.Kind != l.Kind || c.Layer.Path != l.Path {
			continue
		}
		switch c.Status {
		case settings.StatusWins:
			return styleGreen.Render("✓ wins"), "✓ wins"
		case settings.StatusOverridden:
			return styleMuted.Render("overridden"), "overridden"
		case settings.StatusMerged:
			p := fmt.Sprintf("%d entries", entryCount(c.Value))
			return styleVal.Render(p), p
		case settings.StatusIgnored:
			p := "ignored here"
			if c.Reason != "" {
				p += " (" + c.Reason + ")"
			}
			return styleYellow.Render(p), p
		case settings.StatusListed:
			p := truncate(settings.FormatValue(c.Value), 40)
			return styleVal.Render(p), p
		}
	}
	p := "not set"
	switch {
	case l.Note != "":
		p = l.Note
	case !l.Present:
		p = "no file"
	}
	return styleMuted.Render(p), p
}

func entryCount(v any) int {
	switch x := v.(type) {
	case []any:
		return len(x)
	case map[string]any:
		return len(x)
	}
	return 1
}

// entryRows renders list entries and map members with the layer each came
// from and any check result.
func entryRows(it settings.Item, w int) []string {
	const labelW = 8
	var out []string
	for _, p := range it.Parts {
		label := "?"
		if p.Layer != nil {
			label = p.Layer.Kind.Label()
		}
		val := entryText(p)

		var plains, styled []string
		if p.Missing != "" {
			plains = append(plains, "▲ path missing")
			styled = append(styled, styleYellow.Render("▲ path missing"))
		}
		if p.Inactive {
			plains = append(plains, "inactive until trusted")
			styled = append(styled, styleYellow.Render("inactive until trusted"))
		}
		if p.Shadowed {
			word := "duplicate"
			if p.Name != "" {
				word = "overridden"
			}
			plains = append(plains, word)
			styled = append(styled, styleMuted.Render(word))
		}
		status := strings.Join(styled, "  ")
		statusW := utf8.RuneCountInString(strings.Join(plains, "  "))

		valW := max(10, w-2-labelW-1)
		chunks := hardWrap(val, valW)
		indent := strings.Repeat(" ", 2+labelW+1)
		for i, c := range chunks {
			prefix := indent
			if i == 0 {
				prefix = "  " + styleMuted.Render(fmt.Sprintf("%-*s", labelW, label)) + " "
			}
			line := prefix + styleVal.Render(c)
			if i == len(chunks)-1 && status != "" {
				if len(indent)+utf8.RuneCountInString(c)+2+statusW <= w {
					line += "  " + status
				} else {
					out = append(out, line)
					line = indent + status
				}
			}
			out = append(out, line)
		}
	}
	return out
}

func entryText(p settings.Part) string {
	var v string
	if s, ok := p.Value.(string); ok {
		v = s
	} else {
		v = settings.FormatValue(p.Value)
	}
	if p.Name != "" {
		return p.Name + " = " + v
	}
	return v
}

// ── text helpers ─────────────────────────────────────────────────────────────

// kvStyled is kv with wrapping and a caller-chosen value style. Long unbroken
// words are split so nothing runs past the pane.
func kvStyled(k, v string, width int, style lipgloss.Style) []string {
	const keyColW = 14 // matches kv/kvWrap: "%-12s" + two spaces
	keyStr := styleKey.Render(fmt.Sprintf("%-12s", k+":")) + "  "
	chunks := hardWrap(v, max(10, width-keyColW))
	out := make([]string, len(chunks))
	for i, c := range chunks {
		if i == 0 {
			out[i] = keyStr + style.Render(c)
		} else {
			out[i] = strings.Repeat(" ", keyColW) + style.Render(c)
		}
	}
	return out
}

// indentLines wraps s under an indent of n spaces.
func indentLines(s string, n, width int, style lipgloss.Style) []string {
	chunks := hardWrap(s, max(10, width-n))
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = strings.Repeat(" ", n) + style.Render(c)
	}
	return out
}

// hardWrap wraps at word boundaries like wrapWords and also splits any word
// longer than width.
func hardWrap(s string, width int) []string {
	var out []string
	for _, l := range wrapWords(s, width) {
		r := []rune(l)
		if width <= 0 || len(r) <= width {
			out = append(out, l)
			continue
		}
		for len(r) > width {
			out = append(out, string(r[:width]))
			r = r[width:]
		}
		out = append(out, string(r))
	}
	return out
}

// truncateLeft keeps the tail of s, marking the cut with a leading ellipsis.
func truncateLeft(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	return "…" + string(r[len(r)-width+1:])
}

// ── project picker ───────────────────────────────────────────────────────────

type pickerOption struct {
	project string // "" = follow the latest session
	label   string
	tag     string
}

type projectPicker struct {
	opts   []pickerOption
	cursor int
}

// newProjectPicker lists: follow-latest (auto), the working directories of
// active sessions (newest first, de-duplicated), then known projects not
// already listed.
func newProjectPicker(m Model) *projectPicker {
	p := &projectPicker{}
	snap := m.settingsSnapshot()

	latest := "none"
	if snap != nil && snap.ProjectAuto && snap.Project != "" {
		latest = shortPath(snap.Project)
	}

	type sess struct {
		cwd string
		at  int64
	}
	var sessions []sess
	if m.st != nil {
		for _, s := range m.st.Sessions {
			if s.CWD != "" {
				sessions = append(sessions, sess{filepath.Clean(s.CWD), s.StartedAt})
			}
		}
	}
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].at > sessions[j].at })
	if (snap == nil || !snap.ProjectAuto) && len(sessions) > 0 {
		latest = shortPath(sessions[0].cwd)
	}

	p.opts = append(p.opts, pickerOption{project: "", label: "Follow latest session (auto)", tag: latest})
	seen := map[string]bool{}
	for _, s := range sessions {
		if seen[s.cwd] {
			continue
		}
		seen[s.cwd] = true
		p.opts = append(p.opts, pickerOption{project: s.cwd, label: shortPath(s.cwd), tag: "session"})
	}
	if snap != nil {
		for _, k := range snap.KnownProjects {
			k = filepath.Clean(k)
			if seen[k] {
				continue
			}
			seen[k] = true
			p.opts = append(p.opts, pickerOption{project: k, label: shortPath(k), tag: "known"})
		}
	}

	if m.project != "" {
		want := filepath.Clean(m.project)
		for i, o := range p.opts {
			if o.project == want {
				p.cursor = i
			}
		}
	}
	return p
}

func (m Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := *m.picker
	switch {
	case msg.String() == "ctrl+c":
		return m, tea.Quit
	case msg.String() == "esc", msg.String() == "q", key.Matches(msg, m.keys.Project):
		m.picker = nil
		return m, nil
	case key.Matches(msg, m.keys.Up):
		p.cursor = max(0, p.cursor-1)
	case key.Matches(msg, m.keys.Down):
		p.cursor = min(len(p.opts)-1, p.cursor+1)
	case msg.String() == "enter":
		if p.cursor < 0 || p.cursor >= len(p.opts) {
			m.picker = nil
			return m, nil
		}
		m.project = p.opts[p.cursor].project
		m.picker = nil
		m.cursors[tabSettings] = 0
		m.detail.GotoTop()
		return m, func() tea.Msg { return RefreshMsg{} }
	}
	m.picker = &p
	return m, nil
}

func (m Model) renderPicker() string {
	p := m.picker
	boxW := min(72, max(30, m.width-8))
	innerW := boxW - 6 // border (2) + padding (2*2)
	visible := clamp(m.height-12, 3, 12)

	start := 0
	if p.cursor >= visible {
		start = p.cursor - visible + 1
	}
	end := min(len(p.opts), start+visible)

	lines := []string{styleDetailTitle.Render("Project"), ""}
	for i := start; i < end; i++ {
		o := p.opts[i]
		tagW := utf8.RuneCountInString(o.tag)
		labelW := max(8, innerW-2-tagW-2)
		label := truncate(o.label, labelW)
		pad := strings.Repeat(" ", max(2, innerW-2-utf8.RuneCountInString(label)-tagW))
		tag := truncate(o.tag, max(1, innerW-2-utf8.RuneCountInString(label)-2))
		if i == p.cursor {
			lines = append(lines, stylePrimary.Render("▌")+" "+styleSelected.Render(label)+pad+styleMuted.Render(tag))
		} else {
			lines = append(lines, "  "+styleVal.Render(label)+pad+styleMuted.Render(tag))
		}
	}
	if len(p.opts) > visible {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  %d–%d of %d", start+1, end, len(p.opts))))
	}
	lines = append(lines, "", styleMuted.Render("j/k move · enter select · esc close"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Padding(1, 2).
		Width(boxW - 2).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
