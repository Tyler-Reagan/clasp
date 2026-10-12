package ui

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tylerreagan/clasp/internal/settings"
	"github.com/tylerreagan/clasp/internal/state"
)

const testProject = "/work/clasp"

// settingsFixture builds a snapshot by hand: a set replace key, a list key
// with a missing path and a duplicate, an undocumented key, an ignored key,
// an unset key and a removed key.
func settingsFixture() *settings.Snapshot {
	layers := []settings.Layer{
		{Kind: settings.LayerManaged, Path: "/Library/ClaudeCode/managed-settings.json"},
		{Kind: settings.LayerLocal, Path: testProject + "/.claude/settings.local.json", Present: true},
		{Kind: settings.LayerProject, Path: testProject + "/.claude/settings.json"},
		{Kind: settings.LayerUser, Path: "/home/u/.claude/settings.json", Present: true},
		{Kind: settings.LayerGlobal, Path: "/home/u/.claude.json", Present: true},
	}
	local, user := &layers[1], &layers[3]
	allow := []any{"Read(//gone/x)", "Bash(go *)", "Bash(go *)"}
	return &settings.Snapshot{
		Project:       testProject,
		ProjectAuto:   true,
		Layers:        layers,
		KnownProjects: []string{"/work/other", testProject},
		Fetched:       "2026-10-12",
		Items: []settings.Item{
			{
				Key: "model", Documented: true,
				Doc: settings.Doc{
					Key: "model", Anchor: "model", Scope: settings.ScopeAny,
					STE: "Choose the model that Claude Code starts with.", Type: "string",
					Default: "unset", OverrideFlags: []string{"--model"}, OverrideEnv: []string{"ANTHROPIC_MODEL"},
				},
				Value: "opus", Winner: user,
				Contribs: []settings.Contribution{{Layer: user, Value: "opus", Status: settings.StatusWins}},
			},
			{
				Key: "permissions.allow", Documented: true,
				Doc: settings.Doc{
					Key: "permissions.allow", Anchor: "permissions-allow", Merge: settings.MergeList,
					STE: "List the tool uses that need no prompt.",
				},
				Value:    allow[:2],
				Contribs: []settings.Contribution{{Layer: local, Value: allow, Status: settings.StatusMerged}},
				Parts: []settings.Part{
					{Value: allow[0], Layer: local, Missing: "/gone/x"},
					{Value: allow[1], Layer: local},
					{Value: allow[2], Layer: local, Shadowed: true},
				},
				Warnings: 1,
			},
			{
				Key: "qwerty", Value: "1",
				Contribs: []settings.Contribution{{Layer: user, Value: "1", Status: settings.StatusWins}},
				Winner:   user,
			},
			{
				Key: "spinnerTipsEnabled", Documented: true,
				Doc: settings.Doc{Key: "spinnerTipsEnabled", Scope: settings.ScopeUser},
				Contribs: []settings.Contribution{{
					Layer: local, Value: true, Status: settings.StatusIgnored, Reason: "User only",
				}},
				Warnings: 1,
			},
			{
				Key: "theme", Documented: true,
				Doc: settings.Doc{Key: "theme", Anchor: "theme", Default: `"dark"`, Scope: settings.ScopeAny},
			},
			{
				Key: "taskOutputMaxChars", Documented: true,
				Doc: settings.Doc{Key: "taskOutputMaxChars", Removed: "v2.1.277"},
			},
		},
	}
}

func settingsModel(snap *settings.Snapshot) Model {
	m := Model{
		st:     &state.State{Settings: snap},
		keys:   defaultKeys(),
		tab:    tabSettings,
		width:  100,
		height: 30,
	}
	m.detail.Width, m.detail.Height = 64, 20
	m.refreshBindings()
	return m
}

func rowLabels(rows []rowItem) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.label)
	}
	return out
}

func TestSettingsRows_HeadersAndIndicators(t *testing.T) {
	m := settingsModel(settingsFixture())
	rows := m.settingsRows()

	if len(rows) != 2+4 { // two headers + the four set keys
		t.Fatalf("rows = %d, want 6: %q", len(rows), rowLabels(rows))
	}
	if !rows[0].isHeader || rows[0].label != "project: clasp (auto)" {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if !rows[1].isHeader || rows[1].label != "4 set · 2 ▲ · a: all" {
		t.Errorf("row 1 = %+v", rows[1])
	}
	wantInd := map[string]string{"model": "●", "permissions.allow": "▲", "qwerty": "?", "spinnerTipsEnabled": "▲"}
	for _, r := range rows[2:] {
		name := strings.Fields(r.label)[0]
		want, ok := wantInd[name]
		if !ok {
			t.Errorf("unexpected row %q", r.label)
			continue
		}
		if !strings.Contains(r.ind, want) {
			t.Errorf("%s indicator = %q, want %q", name, r.ind, want)
		}
	}
}

func TestSettingsRows_HeaderVariants(t *testing.T) {
	snap := settingsFixture()
	snap.ProjectAuto = false
	if got := settingsModel(snap).settingsRows()[0].label; got != "project: clasp" {
		t.Errorf("picked project header = %q", got)
	}
	snap.Project = ""
	if got := settingsModel(snap).settingsRows()[0].label; got != "project: none" {
		t.Errorf("no project header = %q", got)
	}
}

func TestSettingsRows_SetVersusAll(t *testing.T) {
	m := settingsModel(settingsFixture())
	if n := m.lenFor(tabSettings); n != 4 {
		t.Errorf("set-only len = %d, want 4", n)
	}
	m.settingsAll = true
	rows := m.settingsRows()
	if n := m.lenFor(tabSettings); n != 6 {
		t.Errorf("all len = %d, want 6", n)
	}
	if rows[1].label != "6 keys · a: set only" {
		t.Errorf("all-mode counts = %q", rows[1].label)
	}
	var theme rowItem
	for _, r := range rows {
		if strings.HasPrefix(r.label, "theme") {
			theme = r
		}
	}
	if !strings.Contains(theme.ind, "·") || !strings.HasSuffix(theme.label, "—") {
		t.Errorf("unset row = %+v", theme)
	}
}

func TestSettingsRows_RightAlignedTag(t *testing.T) {
	m := settingsModel(settingsFixture())
	labelW := m.listOuter() - 2 - 3
	tags := map[string]string{
		"model": "user", "permissions.allow": "local", "qwerty": "user", "spinnerTipsEnabled": "ignored",
	}
	for _, r := range m.settingsRows()[2:] {
		if got := utf8.RuneCountInString(r.label); got != labelW {
			t.Errorf("%q is %d wide, want %d", r.label, got, labelW)
		}
		name := strings.Fields(r.label)[0]
		if !strings.HasSuffix(r.label, " "+tags[name]) {
			t.Errorf("%q should end with tag %q", r.label, tags[name])
		}
	}
	// A long key is cut with an ellipsis and the tag stays flush right.
	got := settingsLabel("extraKnownMarketplacesAndMore", "user", labelW)
	if !strings.Contains(got, "…") || !strings.HasSuffix(got, " user") || utf8.RuneCountInString(got) != labelW {
		t.Errorf("long key label = %q", got)
	}
}

func TestSourceTag(t *testing.T) {
	l1 := &settings.Layer{Kind: settings.LayerUser}
	l2 := &settings.Layer{Kind: settings.LayerLocal}
	merged := settings.Item{Contribs: []settings.Contribution{
		{Layer: l2, Status: settings.StatusMerged}, {Layer: l1, Status: settings.StatusMerged},
	}}
	single := settings.Item{Contribs: []settings.Contribution{{Layer: l1, Status: settings.StatusMerged}}}
	if got := sourceTag(merged); got != "merged" {
		t.Errorf("multi-source tag = %q", got)
	}
	if got := sourceTag(single); got != "user" {
		t.Errorf("single-source tag = %q", got)
	}
	if got := sourceTag(settings.Item{}); got != "—" {
		t.Errorf("unset tag = %q", got)
	}
}

func TestToggleSettingsAll_ClampsCursor(t *testing.T) {
	m := settingsModel(settingsFixture())
	m.settingsAll = true
	m.cursors[tabSettings] = 5 // taskOutputMaxChars: unset, last in the all list
	m.toggleSettingsAll()
	if m.settingsAll {
		t.Fatal("toggle should switch to set-only")
	}
	if got, want := m.cursors[tabSettings], 3; got != want {
		t.Errorf("cursor = %d, want clamp to %d", got, want)
	}
}

func TestToggleSettingsAll_KeepsSelectedKey(t *testing.T) {
	m := settingsModel(settingsFixture())
	m.cursors[tabSettings] = 2 // qwerty in set-only
	m.toggleSettingsAll()
	it, ok := m.settingsItem(m.cursors[tabSettings])
	if !ok || it.Key != "qwerty" {
		t.Errorf("selected after toggle = %q (cursor %d)", it.Key, m.cursors[tabSettings])
	}
}

func TestClampCursors_AfterShrink(t *testing.T) {
	m := settingsModel(settingsFixture())
	m.st.Skills = []state.Skill{{Name: "only"}}
	m.cursors[tabSkills] = 7
	m.cursors[tabSettings] = 40
	m.cursors[tabMCP] = 3 // empty list stays at 0
	m.clampCursors()
	if m.cursors[tabSkills] != 0 || m.cursors[tabMCP] != 0 {
		t.Errorf("cursors = %v, want skills and MCP at 0", m.cursors)
	}
	if m.cursors[tabSettings] != 3 {
		t.Errorf("settings cursor = %d, want 3", m.cursors[tabSettings])
	}
}

func TestVisualIndex_HeaderFreeTab(t *testing.T) {
	rows := []rowItem{{label: "a"}, {label: "b"}, {label: "c"}}
	for c := range rows {
		if got := visualIndex(rows, c); got != c {
			t.Errorf("visualIndex(%d) = %d on header-free rows", c, got)
		}
	}
	withHeaders := []rowItem{{isHeader: true}, {isHeader: true}, {label: "a"}, {label: "b"}}
	if got := visualIndex(withHeaders, 1); got != 3 {
		t.Errorf("visualIndex with headers = %d, want 3", got)
	}
}

func TestRenderList_CursorOnHeaderFreeTab(t *testing.T) {
	m := settingsModel(settingsFixture())
	m.tab = tabSkills
	m.st.Skills = []state.Skill{{Name: "alpha"}, {Name: "bravo"}, {Name: "charlie"}}
	m.cursors[tabSkills] = 2
	var marked string
	for _, line := range strings.Split(m.renderList(), "\n") {
		if strings.Contains(line, "▌") {
			marked = line
		}
	}
	if !strings.Contains(marked, "charlie") {
		t.Errorf("cursor row = %q, want charlie", marked)
	}
}

func TestRenderList_SettingsCursorSkipsHeaders(t *testing.T) {
	m := settingsModel(settingsFixture())
	var marked string
	for _, line := range strings.Split(m.renderList(), "\n") {
		if strings.Contains(line, "▌") {
			marked = line
		}
	}
	if !strings.Contains(marked, "model") {
		t.Errorf("cursor 0 row = %q, want the first key", marked)
	}
	if !strings.Contains(m.settingsDetail(0), "model") {
		t.Error("detail for cursor 0 should be the first key")
	}
}

func TestSettingsDetail_STEAndMissingPath(t *testing.T) {
	m := settingsModel(settingsFixture())

	d := m.settingsDetail(0)
	for _, want := range []string{
		"Choose the model that Claude Code starts with.", "Value:", `"opus"`, "Set by:", "user",
		"Layers, highest first", "--settings", "not visible to clasp", "✓ wins",
		"Not seen by clasp: server-managed settings, MDM profile.",
		"--model, ANTHROPIC_MODEL", "Docs: settings-reference#model · catalog fetched 2026-10-12",
		".claude/settings.local.json", "not set", "no file",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("model detail missing %q:\n%s", want, d)
		}
	}
	if strings.Index(d, "managed") > strings.Index(d, "--settings") {
		t.Error("managed rows should come before --settings")
	}

	d = m.settingsDetail(1)
	for _, want := range []string{"Entries (3)", "▲ path missing", "duplicate", "Bash(go *)", "3 entries"} {
		if !strings.Contains(d, want) {
			t.Errorf("permissions.allow detail missing %q:\n%s", want, d)
		}
	}
}

func TestSettingsDetail_UndocumentedIgnoredUnsetRemoved(t *testing.T) {
	m := settingsModel(settingsFixture())

	d := m.settingsDetail(2)
	if !strings.Contains(strings.Join(strings.Fields(d), " "), "Not in official docs. It can be a typo, a removed key, or a key newer than the catalog (2026-10-12).") {
		t.Errorf("undocumented detail:\n%s", d)
	}
	if strings.Contains(d, "Docs: settings-reference") {
		t.Error("undocumented key should have no docs footer")
	}

	d = m.settingsDetail(3)
	if !strings.Contains(d, "ignored here (User only)") {
		t.Errorf("ignored detail:\n%s", d)
	}

	m.settingsAll = true
	d = m.settingsDetail(4) // theme
	if !strings.Contains(d, `unset · default: "dark"`) || !strings.Contains(d, "Set by:") {
		t.Errorf("unset detail:\n%s", d)
	}
	d = m.settingsDetail(5) // removed
	if !strings.Contains(d, "Removed in v2.1.277. Claude Code ignores it.") {
		t.Errorf("removed detail:\n%s", d)
	}
}

func TestSettingsDetail_GlobalKeyShowsOnlyGlobalLayer(t *testing.T) {
	snap := settingsFixture()
	g := &snap.Layers[4]
	snap.Items = []settings.Item{{
		Key: "autoUpdates", Documented: true,
		Doc:      settings.Doc{Key: "autoUpdates", Scope: settings.ScopeGlobal, STE: "Say if updates install by themselves."},
		Value:    false,
		Winner:   g,
		Contribs: []settings.Contribution{{Layer: g, Value: false, Status: settings.StatusWins}},
	}}
	d := settingsModel(snap).settingsDetail(0)
	if !strings.Contains(d, "global") || !strings.Contains(d, "~/.claude.json") && !strings.Contains(d, ".claude.json") {
		t.Errorf("global row missing:\n%s", d)
	}
	for _, bad := range []string{"--settings", "managed ", "project ", "user "} {
		if strings.Contains(d, bad) {
			t.Errorf("global key detail should not mention %q:\n%s", bad, d)
		}
	}
}

func TestSettingsDetail_MDMAndInvalidJSON(t *testing.T) {
	snap := settingsFixture()
	snap.MDMPresent = true
	snap.Layers[3].Err = errors.New("parse /home/u/.claude/settings.json: boom")
	d := settingsModel(snap).settingsDetail(0)
	if !strings.Contains(d, "MDM profile present; Claude Code may skip managed files.") {
		t.Errorf("MDM line missing:\n%s", d)
	}
	if !strings.Contains(d, "invalid JSON") {
		t.Errorf("invalid JSON status missing:\n%s", d)
	}
}

func TestSettingsDetail_EmptyAndNil(t *testing.T) {
	snap := settingsFixture()
	snap.Items = nil
	if d := settingsModel(snap).settingsDetail(0); !strings.Contains(d, "no keys set in any file · a lists all keys") {
		t.Errorf("empty state = %q", d)
	}
	m := settingsModel(nil)
	if d := m.settingsDetail(0); !strings.Contains(d, "not loaded") {
		t.Errorf("nil snapshot notice = %q", d)
	}
	if rows := m.settingsRows(); rows != nil {
		t.Errorf("nil snapshot rows = %v", rows)
	}
}

func TestSettingsDetail_NarrowWidthDoesNotPanic(t *testing.T) {
	m := settingsModel(settingsFixture())
	m.settingsAll = true
	m.detail.Width = 20
	for c := 0; c < 6; c++ {
		_ = m.settingsDetail(c)
	}
}

func pickerModel() Model {
	m := settingsModel(settingsFixture())
	m.st.Sessions = []state.Session{
		{PID: 1, CWD: "/work/old", StartedAt: 100},
		{PID: 2, CWD: testProject, StartedAt: 300},
		{PID: 3, CWD: "/work/old", StartedAt: 200},
		{PID: 4, CWD: "", StartedAt: 400},
	}
	return m
}

func TestProjectPicker_OrderAndDedupe(t *testing.T) {
	p := newProjectPicker(pickerModel())
	var got []string
	for _, o := range p.opts {
		got = append(got, o.project+"|"+o.tag)
	}
	want := []string{
		"|" + testProject, // follow latest (auto) shows the resolved dir
		testProject + "|session",
		"/work/old|session",
		"/work/other|known", // /work/clasp is already listed
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("options = %q\nwant      %q", got, want)
	}
	if p.opts[0].label != "Follow latest session (auto)" {
		t.Errorf("first label = %q", p.opts[0].label)
	}
}

func TestProjectPicker_Keys(t *testing.T) {
	press := func(m Model, s string) (Model, tea.Cmd) {
		var msg tea.KeyMsg
		switch s {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
		}
		next, cmd := m.Update(msg)
		return next.(Model), cmd
	}

	m := pickerModel()
	m, _ = press(m, "p")
	if m.picker == nil {
		t.Fatal("p should open the picker on the Settings tab")
	}
	m, _ = press(m, "j")
	m, _ = press(m, "j")
	m, _ = press(m, "j")
	m, _ = press(m, "j") // clamps at the last option
	if m.picker.cursor != 3 {
		t.Errorf("cursor = %d, want 3", m.picker.cursor)
	}
	m, _ = press(m, "k")
	m.cursors[tabSettings] = 2
	var cmd tea.Cmd
	m, cmd = press(m, "enter")
	if m.picker != nil || m.project != "/work/old" {
		t.Errorf("after enter: picker=%v project=%q", m.picker, m.project)
	}
	if m.cursors[tabSettings] != 0 {
		t.Errorf("settings cursor = %d, want reset to 0", m.cursors[tabSettings])
	}
	if cmd == nil {
		t.Fatal("enter should return a refresh cmd")
	}
	if _, ok := cmd().(RefreshMsg); !ok {
		t.Errorf("cmd produced %T, want RefreshMsg", cmd())
	}

	// Choosing the first option goes back to auto.
	m, _ = press(m, "p")
	if m.picker.cursor != 2 { // opens on the active project
		t.Errorf("picker should open on the active project, cursor = %d", m.picker.cursor)
	}
	m, _ = press(m, "g")
	m, _ = press(m, "k")
	m, _ = press(m, "k")
	m, _ = press(m, "enter")
	if m.project != "" {
		t.Errorf("project = %q, want auto", m.project)
	}

	for _, closer := range []string{"esc", "p", "q"} {
		m, _ = press(m, "p")
		m, _ = press(m, closer)
		if m.picker != nil {
			t.Errorf("%s should close the picker", closer)
		}
	}
}

func TestProjectPicker_NotOpenedOffSettingsTab(t *testing.T) {
	m := pickerModel()
	m.tab = tabSkills
	m.refreshBindings()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if next.(Model).picker != nil {
		t.Error("p should do nothing off the Settings tab")
	}
}

func TestProjectPicker_Renders(t *testing.T) {
	m := pickerModel()
	m.picker = newProjectPicker(m)
	m.ready = true
	v := m.renderPicker()
	for _, want := range []string{"Project", "Follow latest session (auto)", "▌", "session", "known"} {
		if !strings.Contains(v, want) {
			t.Errorf("picker view missing %q:\n%s", want, v)
		}
	}
}

func TestAKeyTogglesAllKeys(t *testing.T) {
	m := settingsModel(settingsFixture())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if !next.(Model).settingsAll {
		t.Error("a should show all keys on the Settings tab")
	}
	m.tab = tabSkills
	m.refreshBindings()
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if next.(Model).settingsAll {
		t.Error("a should do nothing off the Settings tab")
	}
}

func TestPublishProject(t *testing.T) {
	ch := make(chan string, 1)
	m := settingsModel(settingsFixture())
	m.projects = ch

	m.publishProject()
	if got := <-ch; got != testProject {
		t.Errorf("sent %q, want %q", got, testProject)
	}
	m.publishProject() // unchanged: nothing sent
	select {
	case got := <-ch:
		t.Errorf("unexpected resend %q", got)
	default:
	}

	// A full channel never blocks, and the value is retried next time.
	m.st.Settings.Project = "/work/other"
	ch <- "stale"
	m.publishProject()
	if m.lastSentProject != testProject {
		t.Errorf("lastSentProject = %q after a dropped send", m.lastSentProject)
	}
	<-ch
	m.publishProject()
	if got := <-ch; got != "/work/other" {
		t.Errorf("retry sent %q", got)
	}

	// Nil channel is fine.
	m.projects = nil
	m.publishProject()
}
