package inventory

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPersonalSkills(t *testing.T) {
	f := newFixture(t)
	content := skillMD("Does a thing", "body")
	f.cli("skills/alpha/SKILL.md", content)
	// A symlinked skill dir resolves to its target.
	target := filepath.Join(f.root, "agents", "beta")
	f.write(filepath.Join(target, "SKILL.md"), skillMD("Beta", "b"))
	if err := os.Symlink(target, filepath.Join(f.env.ClaudeDir, "skills", "beta")); err != nil {
		t.Fatal(err)
	}
	f.cli("skills/no-md/README.md", "not a skill")
	f.cli("skills/stray.txt", "file, not a dir")

	inv := f.load()
	noErrs(t, inv)
	if got, want := sortedNames(inv.Skills), []string{"alpha", "beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("skills = %v, want %v", got, want)
	}
	a := mustFind(t, inv.Skills, "alpha").Installs[0]
	if a.Side != SideCLI || a.Origin != OriginPersonal || !a.Enabled || a.Plugin != "" {
		t.Errorf("alpha install = %+v", a)
	}
	if a.Hash != sha([]byte(content)) {
		t.Errorf("alpha hash = %q, want sha256 of SKILL.md", a.Hash)
	}
	if a.Desc != "Does a thing" {
		t.Errorf("alpha desc = %q", a.Desc)
	}
	b := mustFind(t, inv.Skills, "beta").Installs[0]
	if want, _ := filepath.EvalSymlinks(target); b.Path != want {
		t.Errorf("beta path = %q, want resolved %q", b.Path, want)
	}
}

func TestSkillDescription(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"inline", "---\nname: a\ndescription: Short one\n---\nbody", "Short one"},
		{"folded", "---\ndescription: >\n  first line\n  second line\nname: a\n---\n", "first line second line"},
		{"literal", "---\ndescription: |\n  one\n  two\n---\n", "one\ntwo"},
		{"no frontmatter", "# Title\nbody", ""},
		{"unterminated", "---\ndescription: x\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := skillDescription(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMarketplacePlugin(t *testing.T) {
	f := newFixture(t)
	dir := f.plugin(filepath.Join(f.root, "cache", "mp", "tools", "1.2.3"), plug{
		name: "tools", version: "9.9.9", desc: "A toolbox",
		skills: map[string]string{
			"engineering/tdd": skillMD("Test first", "red green"),
			"plain":           skillMD("Plain", "p"),
		},
		mcp: `{"mcpServers":{"docs":{"type":"http","url":"https://docs.example/mcp"}}}`,
	})
	f.installMarketplace(map[string]string{"tools@market": dir, "bare@market": filepath.Join(f.root, "missing")}, "tools@market")

	inv := f.load()
	// The bare@market install path is missing: that is an error, not a crash.
	if len(inv.Errs) == 0 {
		t.Error("want an error for the missing install path")
	}

	p := mustFind(t, inv.Plugins, "tools").Installs[0]
	if p.Origin != OriginMarketplace || p.PluginID != "tools@market" || p.Plugin != "tools" || !p.Enabled {
		t.Errorf("plugin install = %+v", p)
	}
	// installed_plugins.json's version wins over plugin.json's.
	if p.Version != "1.2.3" || p.Desc != "A toolbox" || p.Path != dir || p.Hash == "" {
		t.Errorf("plugin install = %+v", p)
	}
	// The plugin whose dir is missing falls back to the id before '@'.
	if bare := mustFind(t, inv.Plugins, "bare").Installs[0]; bare.Enabled || bare.Hash != "" {
		t.Errorf("bare install = %+v", bare)
	}

	tdd := mustFind(t, inv.Skills, "tdd").Installs[0]
	if tdd.Plugin != "tools" || tdd.PluginID != "tools@market" || !tdd.Enabled || tdd.Origin != OriginMarketplace {
		t.Errorf("tdd install = %+v", tdd)
	}
	if tdd.Path != filepath.Join(dir, "skills", "engineering", "tdd") || tdd.Desc != "Test first" {
		t.Errorf("tdd path/desc = %q / %q", tdd.Path, tdd.Desc)
	}
	mustFind(t, inv.Skills, "plain")

	srv := mustFind(t, inv.MCP, "docs").Installs[0]
	if srv.Plugin != "tools" || srv.Hash != sha([]byte("http\nhttps://docs.example/mcp")) || !srv.Enabled {
		t.Errorf("docs install = %+v", srv)
	}
}

func TestMarketplaceDisabledByDefault(t *testing.T) {
	f := newFixture(t)
	dir := f.plugin(filepath.Join(f.root, "cache", "off"), plug{
		name: "off", skills: map[string]string{"s": skillMD("s", "1")},
		mcp: `{"mcpServers":{"srv":{"type":"http","url":"https://x"}}}`,
	})
	f.installMarketplace(map[string]string{"off@m": dir}) // not in enabledPlugins
	inv := f.load()
	noErrs(t, inv)
	for _, items := range [][]Item{inv.Plugins, inv.Skills, inv.MCP} {
		if in := items[0].Installs[0]; in.Enabled {
			t.Errorf("%s %s: enabled, want disabled", items[0].Kind, items[0].Name)
		}
	}
}

func TestNestedSkillsOneInstallPerPlugin(t *testing.T) {
	f := newFixture(t)
	dir := f.plugin(filepath.Join(f.root, "cache", "v"), plug{
		name: "v",
		skills: map[string]string{
			"next":          skillMD("top", "a"),
			"next/upstream": skillMD("vendored", "b"),
			"upstream":      skillMD("real", "c"),
		},
	})
	f.installMarketplace(map[string]string{"v@m": dir}, "v@m")
	inv := f.load()
	noErrs(t, inv)
	up := mustFind(t, inv.Skills, "upstream")
	if len(up.Installs) != 1 || up.Twice(SideCLI) || up.Differs() {
		t.Errorf("upstream installs = %+v, want the one shallowest copy", up.Installs)
	}
	if up.Installs[0].Path != filepath.Join(dir, "skills", "upstream") {
		t.Errorf("upstream path = %q, want the shallowest", up.Installs[0].Path)
	}
}

func TestSyncedPlugins(t *testing.T) {
	f := newFixture(t)
	f.plugin(f.syncedDir("design~g2"), plug{
		name: "design", version: "0040", desc: "Design things",
		skills: map[string]string{"critique": skillMD("Critique", "c")},
		mcp:    `{"mcpServers":{"figma":{"type":"http","url":"https://figma"}}}`,
		extra:  map[string]string{".in_use": "pid"},
	})
	f.plugin(f.syncedDir("off"), plug{name: "off", skills: map[string]string{"o": skillMD("o", "o")}})
	// Files beside the plugin dirs are not plugins.
	f.cli("plugins/synced/"+orgID+"_"+acctID+"/manifest.json", `{"plugins":[]}`)
	f.cli("plugins/synced/"+orgID+"_"+acctID+"/design~g2.meta.json", `{}`)
	f.cli("settings.json", `{"enabledPlugins":{"off@synced":false}}`)

	inv := f.load()
	noErrs(t, inv)
	if got, want := sortedNames(inv.Plugins), []string{"design", "off"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plugins = %v, want %v", got, want)
	}
	d := mustFind(t, inv.Plugins, "design").Installs[0]
	if d.Origin != OriginSynced || d.Account != acctLbl || d.Version != "0040" || !d.Enabled {
		t.Errorf("design install = %+v", d)
	}
	if o := mustFind(t, inv.Plugins, "off").Installs[0]; o.Enabled {
		t.Error("off@synced is false in enabledPlugins: want disabled")
	}
	if s := mustFind(t, inv.Skills, "o").Installs[0]; s.Enabled {
		t.Error("skill of a disabled synced plugin should be disabled")
	}
	if c := mustFind(t, inv.Skills, "critique").Installs[0]; c.Origin != OriginSynced || c.Account != acctLbl || c.Plugin != "design" {
		t.Errorf("critique install = %+v", c)
	}
	if m := mustFind(t, inv.MCP, "figma").Installs[0]; m.Origin != OriginSynced || m.Plugin != "design" {
		t.Errorf("figma install = %+v", m)
	}
}

func TestSyncedNameFallsBackToDir(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.syncedDir("noname~g12"), "skills", "s", "SKILL.md"), skillMD("s", "1"))
	inv := f.load()
	noErrs(t, inv)
	mustFind(t, inv.Plugins, "noname")
}

func TestStandaloneMCPAndConnectors(t *testing.T) {
	f := newFixture(t)
	f.cli("settings.json", `{"mcpServers":{"shared":{"type":"http","url":"https://settings"},"cmd":{"command":"npx"}}}`)
	f.cli("settings.local.json", `{"mcpServers":{"local-only":{"type":"sse","url":"https://local"}}}`)
	f.write(filepath.Join(f.env.Home, ".claude.json"), `{
		"oauthAccount": {"emailAddress": "secret@example.com", "accessToken": "tok"},
		"mcpServers": {"shared": {"type":"http","url":"https://global"}, "global-only": {"type":"http","url":"https://g"}},
		"claudeAiMcpEverConnected": ["claude.ai Gmail", "claude.ai Notion"]
	}`)
	f.cli("mcp-needs-auth-cache.json", `{"claude.ai Notion":{"t":1},"claude.ai Figma":{"t":2},"plugin:design:figma":{"t":3}}`)

	inv := f.load()
	noErrs(t, inv)
	want := []string{"claude.ai Figma", "claude.ai Gmail", "claude.ai Notion", "cmd", "global-only", "local-only", "shared"}
	if got := sortedNames(inv.MCP); !reflect.DeepEqual(got, want) {
		t.Fatalf("mcp = %v, want %v", got, want)
	}

	shared := mustFind(t, inv.MCP, "shared")
	if len(shared.Installs) != 1 || shared.Installs[0].Hash != sha([]byte("http\nhttps://settings")) {
		t.Errorf("shared = %+v, want the settings.json declaration only", shared.Installs)
	}
	if shared.Installs[0].Origin != OriginSettings {
		t.Errorf("shared origin = %q", shared.Installs[0].Origin)
	}
	if cmd := mustFind(t, inv.MCP, "cmd").Installs[0]; cmd.Hash != sha([]byte("\nnpx")) {
		t.Errorf("cmd hash = %q, want hash of the command", cmd.Hash)
	}

	for _, name := range []string{"claude.ai Gmail", "claude.ai Notion", "claude.ai Figma"} {
		it := mustFind(t, inv.MCP, name)
		if !it.Account() || it.Mismatched() || it.Installs[0].Hash != "" || it.Installs[0].Origin != OriginAccount {
			t.Errorf("%s: account=%v mismatched=%v install=%+v", name, it.Account(), it.Mismatched(), it.Installs[0])
		}
		if len(it.Installs) != 1 {
			t.Errorf("%s: %d installs, want connectors deduped", name, len(it.Installs))
		}
	}
	if !mustFind(t, inv.MCP, "shared").Mismatched() {
		t.Error("a non-connector present on one side only is mismatched")
	}
}

func TestOAuthAccountNeverStored(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.env.Home, ".claude.json"), `{"oauthAccount":{"emailAddress":"secret@example.com"},"claudeAiMcpEverConnected":["claude.ai X"]}`)
	inv := f.load()
	for _, it := range inv.MCP {
		for _, in := range it.Installs {
			if strings.Contains(strings.Join([]string{in.Path, in.Desc, in.Account, in.Version}, " "), "secret") {
				t.Errorf("secret leaked into %+v", in)
			}
		}
	}
}
