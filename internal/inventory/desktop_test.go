package inventory

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeSkillsPlugin writes the anthropic-skills plugin under the fixture
// account with one enabled and one disabled skill.
func (f *fixture) writeSkillsPlugin() {
	dir := f.skillsPluginDir()
	f.plugin(dir, plug{
		name: "anthropic-skills", version: "1.0.0",
		skills: map[string]string{
			"pdf":      skillMD("PDF", "pdf"),
			"docx":     skillMD("Docx", "docx"),
			"unlisted": skillMD("Unlisted", "u"),
		},
	})
	f.write(filepath.Join(dir, "manifest.json"), `{"lastUpdated":1,"skills":[
		{"skillId":"s1","name":"pdf","creatorType":"anthropic","enabled":true},
		{"skillId":"s2","name":"docx","creatorType":"user","enabled":false}]}`)
}

func TestAnthropicSkillsManifest(t *testing.T) {
	f := newFixture(t)
	f.writeSkillsPlugin()
	f.cli("skills/docx/SKILL.md", skillMD("Docx", "docx")) // CLI copy, enabled

	inv := f.load()
	noErrs(t, inv)
	if !inv.HasDesktop {
		t.Fatal("HasDesktop = false")
	}

	p := mustFind(t, inv.Plugins, "anthropic-skills")
	if !p.OnlyDSK() || p.Installs[0].Origin != OriginAnthropicSkills || p.Installs[0].Account != acctLbl || p.Installs[0].Version != "1.0.0" {
		t.Errorf("plugin = %+v", p)
	}

	enabled := map[string]bool{}
	for _, name := range []string{"pdf", "docx", "unlisted"} {
		it := mustFind(t, inv.Skills, name)
		for _, in := range onSide(it, SideDSK) {
			if in.Origin != OriginAnthropicSkills || in.Plugin != "anthropic-skills" {
				t.Errorf("%s install = %+v", name, in)
			}
			enabled[name] = in.Enabled
		}
	}
	if want := map[string]bool{"pdf": true, "docx": false, "unlisted": true}; !reflect.DeepEqual(enabled, want) {
		t.Errorf("enabled = %v, want %v (unlisted defaults to enabled)", enabled, want)
	}

	docx := mustFind(t, inv.Skills, "docx")
	if !docx.EnabledSplit() || !docx.Mismatched() {
		t.Error("docx is enabled on CLI and disabled on Desktop: want EnabledSplit and Mismatched")
	}
	if docx.Differs() {
		t.Error("docx has identical content on both sides")
	}
}

func TestRPMPlugins(t *testing.T) {
	f := newFixture(t)
	// Dir names are opaque; plugin.json names them. plugin_c has no plugin.json
	// and is named by the manifest; plugin_d is listed as disabled.
	f.plugin(f.rpmDir("plugin_aaa"), plug{
		name: "design", version: "1.2.0", desc: "Design",
		skills: map[string]string{"critique": skillMD("c", "1")},
		mcp:    `{"mcpServers":{"figma":{"type":"http","url":"https://figma"},"slack":{"type":"http","url":"https://slack"}}}`,
	})
	f.write(filepath.Join(f.rpmDir("plugin_c"), "skills", "cs", "SKILL.md"), skillMD("cs", "1"))
	f.plugin(f.rpmDir("plugin_d"), plug{name: "dis", skills: map[string]string{"ds": skillMD("ds", "1")}})
	f.dsk("local-agent-mode-sessions/"+acctID+"/"+orgID+"/rpm/manifest.json", `{"plugins":[
		{"id":"plugin_aaa","name":"design"},
		{"id":"plugin_c","name":"fromManifest"},
		{"id":"plugin_d","name":"dis","enabled":false}]}`)

	inv := f.load()
	noErrs(t, inv)
	if got, want := sortedNames(inv.Plugins), []string{"design", "dis", "fromManifest"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plugins = %v, want %v", got, want)
	}
	d := mustFind(t, inv.Plugins, "design").Installs[0]
	if d.Side != SideDSK || d.Origin != OriginRPM || d.Account != acctLbl || !d.Enabled || d.Version != "1.2.0" || d.Hash == "" {
		t.Errorf("design = %+v", d)
	}
	if dis := mustFind(t, inv.Plugins, "dis").Installs[0]; dis.Enabled {
		t.Error("dis is disabled in the manifest")
	}
	if s := mustFind(t, inv.Skills, "ds").Installs[0]; s.Enabled {
		t.Error("skill of a disabled rpm plugin should be disabled")
	}
	if s := mustFind(t, inv.Skills, "critique").Installs[0]; s.Origin != OriginRPM || s.Plugin != "design" {
		t.Errorf("critique = %+v", s)
	}
	if got, want := sortedNames(inv.MCP), []string{"figma", "slack"}; !reflect.DeepEqual(got, want) {
		t.Errorf("mcp = %v, want %v", got, want)
	}
}

func TestDesktopConfigMCP(t *testing.T) {
	f := newFixture(t)
	f.dsk("claude_desktop_config.json", `{
		"mcpServers": {
			"remote": {"type":"http","url":"https://r.example"},
			"local": {"command":"npx","args":["-y","srv"],"env":{"TOKEN":"super-secret"}}
		},
		"preferences": {"anything": [1, 2]}
	}`)
	inv := f.load()
	noErrs(t, inv)
	if got, want := sortedNames(inv.MCP), []string{"local", "remote"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mcp = %v, want %v", got, want)
	}
	r := mustFind(t, inv.MCP, "remote")
	if in := r.Installs[0]; in.Side != SideDSK || in.Origin != OriginDesktopConfig || !in.Enabled ||
		in.Hash != sha([]byte("http\nhttps://r.example")) || in.Path != filepath.Join(f.env.DesktopDir, "claude_desktop_config.json") {
		t.Errorf("remote = %+v", in)
	}
	if in := mustFind(t, inv.MCP, "local").Installs[0]; in.Hash != sha([]byte("\nnpx\x00-y\x00srv")) ||
		in.Target != "npx (+2 args)" {
		t.Errorf("local = %+v, want hash of command and args, target without args", in)
	}
	if !r.OnlyDSK() || !r.Mismatched() {
		t.Error("a Desktop-only server is mismatched")
	}
}

func TestEmptyDesktopConfig(t *testing.T) {
	f := newFixture(t)
	f.dsk("claude_desktop_config.json", `{"mcpServers":{}}`)
	inv := f.load()
	noErrs(t, inv)
	if len(inv.MCP) != 0 {
		t.Errorf("mcp = %v, want none", sortedNames(inv.MCP))
	}
}

func TestTwoDesktopAccountsMerged(t *testing.T) {
	f := newFixture(t)
	const org2, acct2 = "cccccccc-1", "dddddddd-2"
	// Account 1: skills-plugin plus an rpm plugin.
	f.writeSkillsPlugin()
	f.plugin(f.rpmDir("plugin_1"), plug{name: "p1", skills: map[string]string{"s1": skillMD("s1", "1")}})
	// Account 2: its own skills-plugin (ids in the other order on disk) and rpm plugin.
	sessions := filepath.Join(f.env.DesktopDir, "local-agent-mode-sessions")
	f.plugin(filepath.Join(sessions, "skills-plugin", org2, acct2), plug{
		name: "anthropic-skills", skills: map[string]string{"extra": skillMD("extra", "x")},
	})
	f.plugin(filepath.Join(sessions, acct2, org2, "rpm", "plugin_2"), plug{
		name: "p2", skills: map[string]string{"s2": skillMD("s2", "2")},
	})

	inv := f.load()
	noErrs(t, inv)
	for _, name := range []string{"s1", "s2", "extra", "pdf"} {
		mustFind(t, inv.Skills, name)
	}
	if got, want := sortedNames(inv.Plugins), []string{"anthropic-skills", "p1", "p2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plugins = %v, want %v", got, want)
	}
	if acct := mustFind(t, inv.Skills, "s2").Installs[0].Account; acct != "cccccccc/dddddddd" {
		t.Errorf("s2 account = %q", acct)
	}
	if acct := mustFind(t, inv.Skills, "s1").Installs[0].Account; acct != acctLbl {
		t.Errorf("s1 account = %q", acct)
	}
	// anthropic-skills comes from both accounts: two installs, one per account.
	as := mustFind(t, inv.Plugins, "anthropic-skills")
	if len(as.Installs) != 2 || as.Installs[0].Account == as.Installs[1].Account {
		t.Errorf("anthropic-skills installs = %+v", as.Installs)
	}
}

func TestAccountLabelsAgreeAcrossStores(t *testing.T) {
	f := newFixture(t)
	f.plugin(f.syncedDir("design"), plug{name: "design", skills: map[string]string{"c": skillMD("c", "1")}})
	f.plugin(f.rpmDir("plugin_x"), plug{name: "design", skills: map[string]string{"c": skillMD("c", "1")}})
	f.writeSkillsPlugin()
	inv := f.load()
	noErrs(t, inv)
	for _, it := range append(inv.Plugins, inv.Skills...) {
		for _, in := range it.Installs {
			if in.Origin != OriginPersonal && in.Account != acctLbl {
				t.Errorf("%s %s (%s %s): account = %q, want %q", it.Kind, it.Name, in.Side, in.Origin, in.Account, acctLbl)
			}
		}
	}
}

func TestSyncedAndDesktopCopiesIdentical(t *testing.T) {
	f := newFixture(t)
	p := plug{
		name: "design", version: "1.2.0",
		skills: map[string]string{"critique": skillMD("c", "same"), "nested/deep": skillMD("d", "same")},
		mcp:    `{"mcpServers":{"figma":{"type":"http","url":"https://figma"}}}`,
	}
	synced := p
	synced.extra = map[string]string{".in_use": "123", ".DS_Store": "junk", "design.meta.json": "{}", ".git/HEAD": "x", "node_modules/m/index.js": "y"}
	f.plugin(f.syncedDir("design~g2"), synced)
	f.plugin(f.rpmDir("plugin_x"), p)

	inv := f.load()
	noErrs(t, inv)
	for _, set := range []struct {
		kind  string
		items []Item
		name  string
	}{{"plugin", inv.Plugins, "design"}, {"skill", inv.Skills, "critique"}, {"skill", inv.Skills, "deep"}, {"mcp", inv.MCP, "figma"}} {
		it := mustFind(t, set.items, set.name)
		if !it.OnSide(SideCLI) || !it.OnSide(SideDSK) {
			t.Errorf("%s %s should be on both sides", set.kind, set.name)
		}
		if it.Differs() || it.Twice(SideCLI) || it.Twice(SideDSK) || it.Mismatched() {
			t.Errorf("%s %s: differs=%v twice=%v/%v mismatched=%v, want none",
				set.kind, set.name, it.Differs(), it.Twice(SideCLI), it.Twice(SideDSK), it.Mismatched())
		}
	}
}

func TestNoDesktop(t *testing.T) {
	for name, dir := range map[string]string{"empty": "", "missing": "/nonexistent/desktop/dir"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.env.DesktopDir = dir
			f.cli("skills/a/SKILL.md", skillMD("a", "1"))
			inv := f.load()
			noErrs(t, inv)
			if inv.HasDesktop {
				t.Error("HasDesktop = true")
			}
			if it := mustFind(t, inv.Skills, "a"); it.OnSide(SideDSK) {
				t.Error("skill a on Desktop")
			}
		})
	}
}

// TestDesktopForbiddenPathsNeverTouched plants bait where the inventory must
// not look: Desktop's config.json (OAuth caches), per-session local_ dirs,
// vm_bundles, Cache and claude-code. Every path the package reads or lists
// goes through fsHook, so the test sees if any of them is touched.
func TestDesktopForbiddenPathsNeverTouched(t *testing.T) {
	f := newFixture(t)
	f.writeSkillsPlugin()
	f.dsk("config.json", `{ this is not json: oauth tokens`)
	for _, rel := range []string{"vm_bundles/x/SKILL.md", "Cache/y/SKILL.md", "claude-code/z/SKILL.md", "claude-code-vm/w/SKILL.md"} {
		f.dsk(rel, skillMD("bait", "bait"))
	}
	sessions := "local-agent-mode-sessions/"
	for _, rel := range []string{
		"skills-plugin/" + orgID + "/local_bait/.claude-plugin/plugin.json",
		sessions + "local_bait/" + acctID + "/rpm/plugin_z/.claude-plugin/plugin.json",
		sessions + acctID + "/local_session/skills/evil/SKILL.md",
		sessions + acctID + "/" + orgID + "/local_session/skills/evil/SKILL.md",
		sessions + acctID + "/" + orgID + "/local_session/.claude/skills/evil/SKILL.md",
	} {
		f.dsk(rel, `{"name":"evil"}`)
	}
	f.plugin(f.rpmDir("plugin_ok"), plug{name: "ok", skills: map[string]string{"fine": skillMD("fine", "1")}})

	var touched []string
	fsHook = func(path string) { touched = append(touched, path) }
	t.Cleanup(func() { fsHook = nil })

	inv := f.load()
	noErrs(t, inv)
	fsHook = nil

	if len(touched) == 0 {
		t.Fatal("hook saw no reads")
	}
	for _, p := range touched {
		rel, err := filepath.Rel(f.env.DesktopDir, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue // CLI side
		}
		top := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		if rel == "config.json" || top == "vm_bundles" || top == "Cache" || strings.HasPrefix(top, "claude-code") {
			t.Errorf("touched forbidden path %s", p)
		}
		for _, seg := range strings.Split(rel, string(filepath.Separator)) {
			if strings.HasPrefix(seg, "local_") {
				t.Errorf("descended into session dir: %s", p)
			}
		}
	}
	for _, name := range []string{"bait", "evil"} {
		if find(inv.Skills, name) != nil {
			t.Errorf("skill %q from a forbidden location was loaded", name)
		}
	}
	mustFind(t, inv.Skills, "fine")
	if find(inv.Plugins, "evil") != nil {
		t.Error("plugin evil loaded from a session dir")
	}
}

func TestDefaultEnv(t *testing.T) {
	env := DefaultEnv()
	if env.Home == "" || env.ClaudeDir != filepath.Join(env.Home, ".claude") {
		t.Errorf("env = %+v", env)
	}
	if env.DesktopDir != "" && !strings.HasPrefix(env.DesktopDir, env.Home) {
		t.Errorf("DesktopDir = %q, want it under home", env.DesktopDir)
	}
}
