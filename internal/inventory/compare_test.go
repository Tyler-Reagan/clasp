package inventory

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSameVersionDifferentContentDiffersAndTwice(t *testing.T) {
	f := newFixture(t)
	mp := f.plugin(filepath.Join(f.root, "cache", "mattpocock-skills", "1.2.3"), plug{
		name: "mattpocock-skills", version: "1.2.3",
		skills: map[string]string{"misc/tdd": skillMD("TDD", "old body"), "same": skillMD("same", "same")},
	})
	f.installMarketplace(map[string]string{"mattpocock-skills@official": mp}, "mattpocock-skills@official")
	f.plugin(f.syncedDir("mattpocock-skills"), plug{
		name: "mattpocock-skills", version: "1.2.3",
		skills: map[string]string{"misc/tdd": skillMD("TDD", "NEW body"), "same": skillMD("same", "same")},
	})

	inv := f.load()
	noErrs(t, inv)

	tdd := mustFind(t, inv.Skills, "tdd")
	if len(tdd.Installs) != 2 {
		t.Fatalf("tdd installs = %+v", tdd.Installs)
	}
	if !tdd.Differs() || !tdd.Twice(SideCLI) || tdd.Twice(SideDSK) || !tdd.Mismatched() {
		t.Errorf("tdd: differs=%v twiceCLI=%v twiceDSK=%v mismatched=%v", tdd.Differs(), tdd.Twice(SideCLI), tdd.Twice(SideDSK), tdd.Mismatched())
	}
	if a, b := tdd.Installs[0], tdd.Installs[1]; a.Version != b.Version || a.Hash == b.Hash {
		t.Errorf("want same version %q and different hashes: %+v", a.Version, tdd.Installs)
	}

	// Identical content is twice but does not differ.
	same := mustFind(t, inv.Skills, "same")
	if same.Differs() || !same.Twice(SideCLI) || !same.Mismatched() {
		t.Errorf("same: differs=%v twice=%v mismatched=%v", same.Differs(), same.Twice(SideCLI), same.Mismatched())
	}

	p := mustFind(t, inv.Plugins, "mattpocock-skills")
	if !p.Differs() || !p.Twice(SideCLI) || !p.Mismatched() {
		t.Errorf("plugin: differs=%v twice=%v mismatched=%v", p.Differs(), p.Twice(SideCLI), p.Mismatched())
	}
}

func TestTwoPluginsSharingSkillNameIsNotTwice(t *testing.T) {
	f := newFixture(t)
	a := f.plugin(filepath.Join(f.root, "cache", "a"), plug{name: "a", skills: map[string]string{"review": skillMD("a's", "from a")}})
	b := f.plugin(filepath.Join(f.root, "cache", "b"), plug{name: "b", skills: map[string]string{"review": skillMD("b's", "from b")}})
	f.installMarketplace(map[string]string{"a@m": a, "b@m": b}, "a@m", "b@m")

	inv := f.load()
	noErrs(t, inv)
	it := mustFind(t, inv.Skills, "review")
	if len(it.Installs) != 2 {
		t.Fatalf("installs = %+v", it.Installs)
	}
	if it.Twice(SideCLI) {
		t.Error("two different plugins sharing a skill name is not twice")
	}
	if !it.Differs() {
		t.Error("their content differs")
	}
}

func TestPersonalAndPluginCopyIsNotTwice(t *testing.T) {
	f := newFixture(t)
	f.cli("skills/review/SKILL.md", skillMD("mine", "mine"))
	a := f.plugin(filepath.Join(f.root, "cache", "a"), plug{name: "a", skills: map[string]string{"review": skillMD("mine", "mine")}})
	f.installMarketplace(map[string]string{"a@m": a}, "a@m")
	inv := f.load()
	noErrs(t, inv)
	it := mustFind(t, inv.Skills, "review")
	if it.Twice(SideCLI) || it.Differs() {
		t.Errorf("twice=%v differs=%v, want neither", it.Twice(SideCLI), it.Differs())
	}
}

func TestMalformedFilesReportErrsAndLoadTheRest(t *testing.T) {
	tests := []struct {
		name   string
		break_ func(f *fixture)
	}{
		{"installed_plugins.json", func(f *fixture) { f.cli("plugins/installed_plugins.json", `{"plugins": [`) }},
		{"settings.json", func(f *fixture) { f.cli("settings.json", `not json`) }},
		{"claude.json", func(f *fixture) { f.write(filepath.Join(f.env.Home, ".claude.json"), `{`) }},
		{"anthropic-skills manifest", func(f *fixture) { f.write(filepath.Join(f.skillsPluginDir(), "manifest.json"), `{"skills": "nope"}`) }},
		{"rpm manifest", func(f *fixture) {
			f.dsk("local-agent-mode-sessions/"+acctID+"/"+orgID+"/rpm/manifest.json", `[[[`)
		}},
		{"plugin.json", func(f *fixture) {
			f.write(filepath.Join(f.rpmDir("plugin_ok"), ".claude-plugin", "plugin.json"), `{"name":`)
		}},
		{"desktop config", func(f *fixture) { f.dsk("claude_desktop_config.json", `{"mcpServers": 5}`) }},
		{".mcp.json", func(f *fixture) { f.write(filepath.Join(f.rpmDir("plugin_ok"), ".mcp.json"), `{"mcpServers":{"a":`) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.cli("skills/personal/SKILL.md", skillMD("p", "p"))
			f.writeSkillsPlugin()
			f.plugin(f.rpmDir("plugin_ok"), plug{name: "ok", skills: map[string]string{"fine": skillMD("f", "f")}})
			tt.break_(f)

			inv := f.load()
			if len(inv.Errs) == 0 {
				t.Fatal("want an error for the malformed file")
			}
			mustFind(t, inv.Skills, "personal")
			mustFind(t, inv.Skills, "pdf")
			if tt.name != "plugin.json" {
				mustFind(t, inv.Skills, "fine")
			}
		})
	}
}

func TestItemsSortedByName(t *testing.T) {
	f := newFixture(t)
	for _, n := range []string{"zeta", "alpha", "mid"} {
		f.cli("skills/"+n+"/SKILL.md", skillMD(n, n))
	}
	f.plugin(f.rpmDir("plugin_1"), plug{name: "beta", skills: map[string]string{"alpha": skillMD("a", "different")}})
	inv := f.load()
	if got, want := sortedNames(inv.Skills), []string{"alpha", "mid", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := 1; i < len(inv.Skills); i++ {
		if inv.Skills[i-1].Name >= inv.Skills[i].Name {
			t.Errorf("not sorted: %q before %q", inv.Skills[i-1].Name, inv.Skills[i].Name)
		}
	}
	// Within an item, CLI installs precede Desktop ones.
	if in := mustFind(t, inv.Skills, "alpha").Installs; in[0].Side != SideCLI || in[1].Side != SideDSK {
		t.Errorf("alpha installs = %+v", in)
	}
}

func TestLoadEmptyMachine(t *testing.T) {
	root := t.TempDir()
	inv := Load(Env{Home: root, ClaudeDir: filepath.Join(root, ".claude")})
	noErrs(t, inv)
	if inv.HasDesktop || len(inv.Skills)+len(inv.Plugins)+len(inv.MCP) != 0 {
		t.Errorf("inv = %+v", inv)
	}
}

func TestLoadUnreadableSourceDoesNotPanic(t *testing.T) {
	f := newFixture(t)
	// A plugin.json that is a directory, and a skills dir that is a file.
	if err := os.MkdirAll(filepath.Join(f.env.ClaudeDir, "plugins", "synced", orgID+"_"+acctID, "p", ".claude-plugin", "plugin.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.cli("skills", "a file where the skills dir should be")
	inv := f.load()
	if len(inv.Errs) == 0 {
		t.Error("want errors")
	}
}

func TestItemPredicates(t *testing.T) {
	in := func(side Side, origin, plugin, hash string, enabled bool) Install {
		return Install{Side: side, Origin: origin, Plugin: plugin, Hash: hash, Enabled: enabled}
	}
	tests := []struct {
		name                                                                      string
		installs                                                                  []Install
		onlyCLI, onlyDSK, account, differs, twiceCLI, twiceDSK, split, mismatched bool
	}{
		{name: "none"},
		{
			name:     "both sides identical",
			installs: []Install{in(SideCLI, "personal", "", "h", true), in(SideDSK, "rpm", "p", "h", true)},
		},
		{
			name:       "cli only",
			installs:   []Install{in(SideCLI, "personal", "", "h", true)},
			onlyCLI:    true,
			mismatched: true,
		},
		{
			name:       "dsk only",
			installs:   []Install{in(SideDSK, "rpm", "p", "h", true)},
			onlyDSK:    true,
			mismatched: true,
		},
		{
			name:     "connector",
			installs: []Install{in(SideCLI, "account", "", "", true)},
			onlyCLI:  true,
			account:  true,
		},
		{
			name:       "connector on cli plus a real dsk server of the same name",
			installs:   []Install{in(SideCLI, "account", "", "", true), in(SideDSK, "desktop-config", "", "h", true)},
			mismatched: false,
		},
		{
			name:       "differs",
			installs:   []Install{in(SideCLI, "personal", "", "a", true), in(SideDSK, "rpm", "p", "b", true)},
			differs:    true,
			mismatched: true,
		},
		{
			name:     "empty hashes do not differ",
			installs: []Install{in(SideCLI, "account", "", "", true), in(SideDSK, "rpm", "p", "h", true)},
		},
		{
			name:       "same plugin twice on cli",
			installs:   []Install{in(SideCLI, "marketplace", "p", "h", true), in(SideCLI, "synced", "p", "h", true), in(SideDSK, "rpm", "p", "h", true)},
			twiceCLI:   true,
			mismatched: true,
		},
		{
			name:     "different plugins on cli",
			installs: []Install{in(SideCLI, "marketplace", "p", "h", true), in(SideCLI, "marketplace", "q", "h", true), in(SideDSK, "rpm", "p", "h", true)},
		},
		{
			name:       "two personal copies",
			installs:   []Install{in(SideCLI, "personal", "", "h", true), in(SideCLI, "personal", "", "h", true), in(SideDSK, "rpm", "p", "h", true)},
			twiceCLI:   true,
			mismatched: true,
		},
		{
			name:       "same plugin twice on dsk",
			installs:   []Install{in(SideDSK, "rpm", "p", "h", true), in(SideDSK, "rpm", "p", "h", true), in(SideCLI, "personal", "", "h", true)},
			twiceDSK:   true,
			mismatched: true,
		},
		{
			name:       "enabled on cli, disabled on dsk",
			installs:   []Install{in(SideCLI, "personal", "", "h", true), in(SideDSK, "rpm", "p", "h", false)},
			split:      true,
			mismatched: true,
		},
		{
			name:       "disabled on cli, enabled on dsk",
			installs:   []Install{in(SideCLI, "marketplace", "p", "h", false), in(SideDSK, "rpm", "p", "h", true)},
			split:      true,
			mismatched: true,
		},
		{
			name:     "one enabled install on the disabled side is not a split",
			installs: []Install{in(SideCLI, "marketplace", "p", "h", false), in(SideCLI, "synced", "p", "h", true), in(SideDSK, "rpm", "p", "h", true)},
			twiceCLI: true, mismatched: true,
		},
		{
			name:     "disabled on both sides",
			installs: []Install{in(SideCLI, "marketplace", "p", "h", false), in(SideDSK, "rpm", "p", "h", false)},
		},
		{
			name:       "disabled on the only side is not a split",
			installs:   []Install{in(SideCLI, "marketplace", "p", "h", false)},
			onlyCLI:    true,
			mismatched: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			it := Item{Installs: tt.installs}
			got := map[string][2]bool{
				"OnlyCLI":      {it.OnlyCLI(), tt.onlyCLI},
				"OnlyDSK":      {it.OnlyDSK(), tt.onlyDSK},
				"Account":      {it.Account(), tt.account},
				"Differs":      {it.Differs(), tt.differs},
				"Twice(CLI)":   {it.Twice(SideCLI), tt.twiceCLI},
				"Twice(DSK)":   {it.Twice(SideDSK), tt.twiceDSK},
				"EnabledSplit": {it.EnabledSplit(), tt.split},
				"Mismatched":   {it.Mismatched(), tt.mismatched},
			}
			for name, v := range got {
				if v[0] != v[1] {
					t.Errorf("%s = %v, want %v", name, v[0], v[1])
				}
			}
		})
	}
}

func TestSamePluginUnderTwoDesktopAccountsIsNotTwice(t *testing.T) {
	f := newFixture(t)
	for _, acct := range []string{acctID, "cccccccc-9999-0000-1111-222222222222"} {
		f.plugin(filepath.Join(f.env.DesktopDir, "local-agent-mode-sessions", acct, orgID, "rpm", "plugin_x"), plug{
			name: "design", version: "1.2.0", skills: map[string]string{"ux-copy": skillMD("UX", "body")},
		})
	}
	inv := f.load()
	noErrs(t, inv)
	for _, it := range []Item{mustFind(t, inv.Plugins, "design"), mustFind(t, inv.Skills, "ux-copy")} {
		if len(it.Installs) != 2 || it.Twice(SideDSK) {
			t.Errorf("%s: installs=%d twice=%v, want 2 installs and not twice", it.Name, len(it.Installs), it.Twice(SideDSK))
		}
	}
}

func TestStdioServersDifferByArgs(t *testing.T) {
	f := newFixture(t)
	f.cli("settings.json", `{"mcpServers":{"gh":{"command":"npx","args":["-y","@a/server"]}}}`)
	f.dsk("claude_desktop_config.json", `{"mcpServers":{"gh":{"command":"npx","args":["-y","@b/server"]}}}`)
	inv := f.load()
	noErrs(t, inv)
	if gh := mustFind(t, inv.MCP, "gh"); !gh.Differs() {
		t.Errorf("gh with different args should differ: %+v", gh.Installs)
	}
}

func TestProjectMCPServers(t *testing.T) {
	f := newFixture(t)
	var projs []string
	for _, name := range []string{"alpha", "beta"} {
		proj := filepath.Join(f.root, "work", name)
		projs = append(projs, proj)
		f.write(filepath.Join(proj, ".mcp.json"), `{"mcpServers":{"db":{"type":"stdio","command":"db-mcp"}}}`)
		if err := os.MkdirAll(filepath.Join(f.env.ClaudeDir, "projects", strings.ReplaceAll(proj, "/", "-")), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.write(filepath.Join(projs[0], ".claude", "launch.json"), `{}`)
	inv := f.load()
	noErrs(t, inv)
	db := mustFind(t, inv.MCP, "db")
	if len(db.Installs) != 2 || db.Twice(SideCLI) || db.Installs[0].Origin != OriginProject {
		t.Errorf("db = %+v, want two project installs, not twice", db.Installs)
	}
	if p := mustFind(t, inv.MCP, "Claude Preview (alpha)"); p.Installs[0].Type != "built-in" {
		t.Errorf("preview = %+v", p.Installs)
	}
}
