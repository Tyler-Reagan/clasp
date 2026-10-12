package inventory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

const (
	orgID   = "aaaaaaaa-1111-2222-3333-444444444444"
	acctID  = "bbbbbbbb-5555-6666-7777-888888888888"
	acctLbl = "aaaaaaaa/bbbbbbbb"
)

// fixture is a fake machine under t.TempDir: a home with ~/.claude and a
// Desktop support dir.
type fixture struct {
	t    *testing.T
	root string
	env  Env
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	f := &fixture{
		t:    t,
		root: root,
		env: Env{
			Home:       home,
			ClaudeDir:  filepath.Join(home, ".claude"),
			DesktopDir: filepath.Join(root, "desktop"),
		},
	}
	for _, d := range []string{f.env.ClaudeDir, f.env.DesktopDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) write(path, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) cli(rel, content string) { f.write(filepath.Join(f.env.ClaudeDir, rel), content) }
func (f *fixture) dsk(rel, content string) { f.write(filepath.Join(f.env.DesktopDir, rel), content) }

// skillMD is a SKILL.md with a description; body varies the content hash.
func skillMD(desc, body string) string {
	return fmt.Sprintf("---\nname: x\ndescription: %s\n---\n%s\n", desc, body)
}

// plug describes a plugin directory.
type plug struct {
	name, version, desc string
	skills              map[string]string // path under skills/ -> SKILL.md content
	mcp                 string            // .mcp.json content, "" for none
	extra               map[string]string // other files, e.g. markers
}

// plugin writes p under dir. Pass name "" to omit plugin.json.
func (f *fixture) plugin(dir string, p plug) string {
	f.t.Helper()
	if p.name != "" {
		f.write(filepath.Join(dir, ".claude-plugin", "plugin.json"),
			fmt.Sprintf(`{"name":%q,"version":%q,"description":%q}`, p.name, p.version, p.desc))
	}
	for rel, content := range p.skills {
		f.write(filepath.Join(dir, "skills", rel, "SKILL.md"), content)
	}
	if p.mcp != "" {
		f.write(filepath.Join(dir, ".mcp.json"), p.mcp)
	}
	for rel, content := range p.extra {
		f.write(filepath.Join(dir, rel), content)
	}
	return dir
}

// installMarketplace registers plugin dirs in installed_plugins.json and
// enables the listed ids in settings.json.
func (f *fixture) installMarketplace(installs map[string]string, enabled ...string) {
	f.t.Helper()
	plugins := ""
	for _, id := range sortedKeys(installs) {
		if plugins != "" {
			plugins += ","
		}
		plugins += fmt.Sprintf(`%q:[{"scope":"user","installPath":%q,"version":"1.2.3"}]`, id, installs[id])
	}
	f.cli("plugins/installed_plugins.json", `{"version":2,"plugins":{`+plugins+`}}`)
	en := ""
	for i, id := range enabled {
		if i > 0 {
			en += ","
		}
		en += fmt.Sprintf(`%q:true`, id)
	}
	f.cli("settings.json", `{"enabledPlugins":{`+en+`}}`)
}

// syncedDir is the CLI synced dir for the fixture account.
func (f *fixture) syncedDir(dir string) string {
	return filepath.Join(f.env.ClaudeDir, "plugins", "synced", orgID+"_"+acctID, dir)
}

// rpmDir is a Desktop rpm plugin dir (rpm lives under <acct>/<org>).
func (f *fixture) rpmDir(id string) string {
	return filepath.Join(f.env.DesktopDir, "local-agent-mode-sessions", acctID, orgID, "rpm", id)
}

// skillsPluginDir is the Desktop skills-plugin dir (<org>/<acct>).
func (f *fixture) skillsPluginDir() string {
	return filepath.Join(f.env.DesktopDir, "local-agent-mode-sessions", "skills-plugin", orgID, acctID)
}

func (f *fixture) load() *Inventory {
	f.t.Helper()
	return Load(f.env)
}

func find(items []Item, name string) *Item {
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

func mustFind(t *testing.T, items []Item, name string) Item {
	t.Helper()
	it := find(items, name)
	if it == nil {
		names := make([]string, len(items))
		for i, x := range items {
			names[i] = x.Name
		}
		t.Fatalf("no item %q in %v", name, names)
	}
	return *it
}

func onSide(it Item, s Side) []Install {
	var out []Install
	for _, in := range it.Installs {
		if in.Side == s {
			out = append(out, in)
		}
	}
	return out
}

func noErrs(t *testing.T, inv *Inventory) {
	t.Helper()
	for _, err := range inv.Errs {
		t.Errorf("unexpected error: %v", err)
	}
}

func sortedNames(items []Item) []string {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	sort.Strings(names)
	return names
}
