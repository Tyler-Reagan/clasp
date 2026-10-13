package inventory

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func (f *fixture) cacheDir(mkt, plugin, ver string) string {
	return filepath.Join(f.env.ClaudeDir, "plugins", "cache", mkt, plugin, ver)
}

func (f *fixture) markOrphaned(dir string, at time.Time) {
	f.t.Helper()
	p := filepath.Join(dir, ".orphaned_at")
	f.write(p, fmt.Sprint(at.UnixMilli()))
	if err := os.Chtimes(p, at, at); err != nil {
		f.t.Fatal(err)
	}
}

func copyByPath(t *testing.T, it Item, path string) Copy {
	t.Helper()
	for _, c := range it.Copies {
		if c.Path == path {
			return c
		}
	}
	t.Fatalf("%s has no copy at %s: %+v", it.Name, path, it.Copies)
	return Copy{}
}

func TestCacheCopies(t *testing.T) {
	fakeProcs(t, nil)
	f := newFixture(t)
	const id = "fd@mkt"
	var dirs []string
	for i := 0; i < 8; i++ {
		d := f.plugin(f.cacheDir("mkt", "fd", fmt.Sprintf("v%d", i)), plug{name: "fd", version: fmt.Sprintf("v%d", i), skills: map[string]string{"s": skillMD("S", "same")}})
		dirs = append(dirs, d)
	}
	f.installMarketplace(map[string]string{id: dirs[7]}, id)

	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ { // v0..v3 marked, v4..v6 not
		f.markOrphaned(dirs[i], base.Add(time.Duration(i)*24*time.Hour))
	}

	inv := f.load()
	noErrs(t, inv)
	fd := mustFind(t, inv.Plugins, "fd")
	if len(fd.Copies) != 8 || fd.StaleCount() != 7 {
		t.Fatalf("copies = %d, stale = %d; want 8 and 7", len(fd.Copies), fd.StaleCount())
	}
	if fd.Copies[0].Status != CopyActive || fd.Copies[0].Path != dirs[7] {
		t.Errorf("active copy should sort first: %+v", fd.Copies[0])
	}
	bare := Item{Kind: fd.Kind, Name: fd.Name, Installs: fd.Installs}
	if fd.Differs() != bare.Differs() || fd.Mismatched() != bare.Mismatched() || len(fd.Installs) != 1 {
		t.Errorf("stale copies changed install-level answers: differs=%v mismatched=%v installs=%d", fd.Differs(), fd.Mismatched(), len(fd.Installs))
	}

	for i := 0; i < 7; i++ {
		c := copyByPath(t, fd, dirs[i])
		if c.Status != CopyStale || c.Trashable || c.Version != fmt.Sprintf("v%d", i) || c.Size == 0 || c.ModTime.IsZero() {
			t.Errorf("v%d = %+v", i, c)
		}
		if i < 4 {
			want := base.Add(time.Duration(i) * 24 * time.Hour).Add(orphanGrace)
			if !c.DeleteAfter().Equal(want) {
				t.Errorf("v%d DeleteAfter = %v, want %v", i, c.DeleteAfter(), want)
			}
			if wantWhy := "Claude Code deletes it after " + want.Local().Format("2006-01-02"); c.Why != wantWhy {
				t.Errorf("v%d Why = %q, want %q", i, c.Why, wantWhy)
			}
		} else if !c.DeleteAfter().IsZero() || c.Why != "not yet marked by Claude Code" {
			t.Errorf("v%d unmarked: DeleteAfter=%v Why=%q", i, c.DeleteAfter(), c.Why)
		}
	}
	if act := copyByPath(t, fd, dirs[7]); act.Status != CopyActive || act.Why != "" || act.Trashable {
		t.Errorf("active = %+v", act)
	}

	// The skill inside a stale copy is a stale copy of the skill item; the
	// active one is already an Install.
	s := mustFind(t, inv.Skills, "s")
	if s.StaleCount() != 7 || len(s.Installs) != 1 || len(s.Copies) != 7 {
		t.Errorf("skill: stale=%d installs=%d copies=%d", s.StaleCount(), len(s.Installs), len(s.Copies))
	}
}

func TestCacheStaleInUse(t *testing.T) {
	start := time.Date(2026, 10, 12, 20, 11, 22, 0, time.UTC)
	fakeProcs(t, map[int]time.Time{2887: start})
	f := newFixture(t)
	act := f.plugin(f.cacheDir("mkt", "p", "2"), plug{name: "p"})
	old := f.plugin(f.cacheDir("mkt", "p", "1"), plug{name: "p"})
	f.installMarketplace(map[string]string{"p@mkt": act}, "p@mkt")
	writeInUse(f, old, 2887, "Mon Oct 12 20:11:22 2026")

	c := copyByPath(t, mustFind(t, f.load().Plugins, "p"), old)
	if c.Status != CopyStale || c.Trashable || len(c.LivePIDs) != 1 || c.Why != "not yet marked by Claude Code; in use by PID 2887" {
		t.Errorf("copy = %+v", c)
	}
}

func TestCacheNoInstallIsItsOwnItem(t *testing.T) {
	fakeProcs(t, nil)
	f := newFixture(t)
	dir := f.plugin(f.cacheDir("mkt", "gone", "1"), plug{name: "gone"})
	f.cli("plugins/installed_plugins.json", `{"version":2,"plugins":{}}`)
	noName := f.cacheDir("mkt", "nameless", "1") // no plugin.json: falls back to the dir name
	f.write(filepath.Join(noName, "README"), "x")

	inv := f.load()
	noErrs(t, inv)
	it := mustFind(t, inv.Plugins, "gone")
	if len(it.Installs) != 0 || len(it.Copies) != 1 || it.Copies[0].Status != CopyStale || it.Copies[0].Path != dir {
		t.Errorf("item = %+v", it)
	}
	if it.Mismatched() {
		t.Error("a stale copy alone is not a mismatch")
	}
	if n := mustFind(t, inv.Plugins, "nameless"); n.StaleCount() != 1 {
		t.Errorf("nameless = %+v", n)
	}
}

func TestCacheActiveViaSecondInstall(t *testing.T) {
	fakeProcs(t, nil)
	f := newFixture(t)
	first := f.plugin(f.cacheDir("mkt", "p", "1"), plug{name: "p"})
	second := f.plugin(f.cacheDir("mkt", "p", "2"), plug{name: "p"})
	f.cli("plugins/installed_plugins.json", fmt.Sprintf(
		`{"version":2,"plugins":{"p@mkt":[{"scope":"user","installPath":%q},{"scope":"project","installPath":%q}]}}`, first, second+"/"))
	inv := f.load()
	p := mustFind(t, inv.Plugins, "p")
	if p.StaleCount() != 0 || len(p.Copies) != 2 {
		t.Errorf("copies = %+v", p.Copies)
	}
}

func TestCacheSkippedWhenInstalledPluginsBroken(t *testing.T) {
	fakeProcs(t, nil)
	f := newFixture(t)
	f.plugin(f.cacheDir("mkt", "p", "1"), plug{name: "p"})
	f.cli("plugins/installed_plugins.json", `{broken`)
	inv := f.load()
	if len(inv.Plugins) != 0 {
		t.Errorf("nothing may be called stale without installed_plugins.json: %+v", inv.Plugins)
	}
}

func TestSyncedLeftovers(t *testing.T) {
	fakeProcs(t, nil)
	tests := []struct {
		name         string
		manifest     string // "" = none
		wantInstalls []string
		wantLeftover []string // dir names
	}{
		{"older generation", `{"plugins":[{"name":"x","generation":2}]}`, []string{"x~g2"}, []string{"x"}},
		{"no manifest", "", []string{"x", "x~g2"}, nil},
		{"unreadable manifest", `{oops`, []string{"x", "x~g2"}, nil},
		{"bare generation is 1", `{"plugins":[{"name":"x"}]}`, []string{"x"}, []string{"x~g2"}},
		{"name not in manifest", `{"plugins":[{"name":"y"}]}`, nil, []string{"x", "x~g2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			var paths = map[string]string{}
			for _, d := range []string{"x", "x~g2"} {
				paths[d] = f.plugin(f.syncedDir(d), plug{name: "x", version: d, skills: map[string]string{"sk": skillMD("S", d)}})
			}
			if tt.manifest != "" {
				f.write(filepath.Join(filepath.Dir(f.syncedDir("x")), "manifest.json"), tt.manifest)
			}
			f.write(filepath.Join(filepath.Dir(f.syncedDir("x")), "x.meta.json"), `{}`)

			inv := f.load()
			x := inv.Find(KindPlugin, "x")
			if x == nil {
				t.Fatal("no item x")
			}
			var gotInstalls []string
			for _, in := range x.Installs {
				gotInstalls = append(gotInstalls, filepath.Base(in.Path))
			}
			if fmt.Sprint(gotInstalls) != fmt.Sprint(tt.wantInstalls) {
				t.Errorf("installs = %v, want %v", gotInstalls, tt.wantInstalls)
			}
			var gotLeft []string
			for _, c := range x.Leftovers() {
				gotLeft = append(gotLeft, filepath.Base(c.Path))
				if !c.Trashable || c.Origin != OriginSynced || c.Why != "safe to move to the Trash" {
					t.Errorf("leftover = %+v", c)
				}
			}
			if fmt.Sprint(gotLeft) != fmt.Sprint(tt.wantLeftover) {
				t.Errorf("leftovers = %v, want %v", gotLeft, tt.wantLeftover)
			}
			if len(x.Copies) != 2 {
				t.Errorf("want both dirs listed as copies, got %d", len(x.Copies))
			}
			if want := len(tt.wantLeftover) > 0; x.Mismatched() != want && !x.Differs() && !x.Twice(SideCLI) {
				t.Errorf("Mismatched = %v with leftovers=%v", x.Mismatched(), tt.wantLeftover)
			}
			// the skill inside a leftover copy is a leftover copy of the skill
			sk := mustFind(t, inv.Skills, "sk")
			if len(sk.Leftovers()) != len(tt.wantLeftover) || len(sk.Installs) != len(tt.wantInstalls) {
				t.Errorf("skill: leftovers=%d installs=%d", len(sk.Leftovers()), len(sk.Installs))
			}
		})
	}
}

func TestSyncedLeftoverInUse(t *testing.T) {
	start := time.Date(2026, 10, 12, 20, 11, 22, 0, time.UTC)
	fakeProcs(t, map[int]time.Time{55: start})
	f := newFixture(t)
	old := f.plugin(f.syncedDir("x"), plug{name: "x"})
	f.plugin(f.syncedDir("x~g2"), plug{name: "x"})
	f.write(filepath.Join(filepath.Dir(old), "manifest.json"), `{"plugins":[{"name":"x","generation":2}]}`)
	writeInUse(f, old, 55, "Mon Oct 12 20:11:22 2026")
	c := mustFind(t, f.load().Plugins, "x").Leftovers()[0]
	if c.Trashable || c.Why != "in use by PID 55" {
		t.Errorf("copy = %+v", c)
	}
}

func TestRPMLeftovers(t *testing.T) {
	fakeProcs(t, nil)
	tests := []struct {
		name           string
		running, known bool
		wantTrash      bool
		wantWhy        string
	}{
		{"desktop not running", false, true, true, "safe to move to the Trash"},
		{"desktop running", true, true, false, "quit Claude Desktop first"},
		{"unknown", false, false, false, "can't tell whether Claude Desktop is running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeDesktop(t, tt.running, tt.known)
			f := newFixture(t)
			f.plugin(f.rpmDir("plugin_live"), plug{name: "live"})
			f.plugin(f.rpmDir("plugin_gone"), plug{name: "gone", skills: map[string]string{"sk": skillMD("S", "s")}, mcp: `{"mcpServers":{"srv":{"url":"https://x"}}}`})
			f.write(filepath.Join(filepath.Dir(f.rpmDir("x")), "manifest.json"), `{"plugins":[{"id":"plugin_live","name":"live"}]}`)

			inv := f.load()
			noErrs(t, inv)
			if live := mustFind(t, inv.Plugins, "live"); len(live.Installs) != 1 || len(live.Copies) != 1 || live.Copies[0].Status != CopyActive {
				t.Errorf("live = %+v", live)
			}
			gone := mustFind(t, inv.Plugins, "gone")
			if len(gone.Installs) != 0 || len(gone.Leftovers()) != 1 || !gone.Mismatched() {
				t.Fatalf("gone = %+v", gone)
			}
			c := gone.Leftovers()[0]
			if c.Trashable != tt.wantTrash || c.Why != tt.wantWhy || c.Side != SideDSK || c.Origin != OriginRPM {
				t.Errorf("copy = %+v", c)
			}
			if len(gone.TrashableLeftovers()) != btoi(tt.wantTrash) {
				t.Errorf("TrashableLeftovers = %v", gone.TrashableLeftovers())
			}
			if len(inv.Find(KindSkill, "sk").Copies) != 1 || len(inv.Find(KindMCP, "srv").Copies) != 1 || len(inv.Find(KindSkill, "sk").Installs) != 0 {
				t.Error("skill and MCP in the leftover should be copies, not installs")
			}
		})
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestRPMNoManifestKeepsAllActive(t *testing.T) {
	fakeProcs(t, nil)
	fakeDesktop(t, false, true)
	f := newFixture(t)
	f.plugin(f.rpmDir("plugin_a"), plug{name: "a"})
	inv := f.load()
	a := mustFind(t, inv.Plugins, "a")
	if len(a.Installs) != 1 || len(a.Leftovers()) != 0 {
		t.Errorf("a = %+v", a)
	}
}

func TestRPMLeftoverInUseByLivePID(t *testing.T) {
	fakeProcs(t, map[int]time.Time{9: {}})
	fakeDesktop(t, false, true)
	f := newFixture(t)
	d := f.plugin(f.rpmDir("plugin_gone"), plug{name: "gone"})
	f.write(filepath.Join(filepath.Dir(d), "manifest.json"), `{"plugins":[{"id":"plugin_other","name":"other"}]}`)
	writeInUse(f, d, 9, "")
	c := mustFind(t, f.load().Plugins, "gone").Leftovers()[0]
	if c.Trashable || c.Why != "in use by PID 9" {
		t.Errorf("copy = %+v", c)
	}
}

func TestDataLeftovers(t *testing.T) {
	fakeProcs(t, nil)
	f := newFixture(t)
	kept := f.plugin(f.cacheDir("claude-plugins-official", "keep", "1"), plug{name: "keep"})
	f.installMarketplace(map[string]string{"keep@claude-plugins-official": kept}, "keep@claude-plugins-official")
	f.cli("plugins/known_marketplaces.json", `{"claude-plugins-official":{},"plugins":{}}`)
	f.cli("plugins/data/keep-claude-plugins-official/state.json", "{}")          // installed: not listed
	f.cli("plugins/data/clangd-lsp-claude-plugins-official/state.json", "12345") // uninstalled: leftover
	f.cli("plugins/data/thing-inline/x", "x")                                    // ignored
	f.cli("plugins/data/mystery/x", "x")                                         // no known marketplace: whole name
	f.cli("plugins/data/foo-plugins-claude-plugins-official/x", "x")             // longest suffix wins
	inv := f.load()
	noErrs(t, inv)

	clangd := mustFind(t, inv.Plugins, "clangd-lsp")
	if len(clangd.Installs) != 0 || len(clangd.Leftovers()) != 1 || !clangd.Mismatched() {
		t.Fatalf("clangd = %+v", clangd)
	}
	c := clangd.Leftovers()[0]
	if c.Origin != OriginData || c.Trashable || c.Size != 5 || c.Why != "kept plugin data (claude plugin uninstall --keep-data)" {
		t.Errorf("copy = %+v", c)
	}
	if mustFind(t, inv.Plugins, "mystery").Leftovers()[0].Trashable {
		t.Error("data must never be trashable")
	}
	mustFind(t, inv.Plugins, "foo-plugins")
	for _, it := range inv.Plugins {
		if it.Name == "thing" || it.Name == "thing-inline" {
			t.Errorf("-inline dir listed: %+v", it)
		}
		if it.Name == "keep" && len(it.Copies) != 1 {
			t.Errorf("installed plugin's data dir listed: %+v", it.Copies)
		}
	}
}

func TestStaleCopyContents(t *testing.T) {
	fakeProcs(t, nil)
	f := newFixture(t)
	act := f.plugin(f.cacheDir("m", "p", "2"), plug{name: "p"})
	old := f.plugin(f.cacheDir("m", "p", "1"), plug{
		name: "p", skills: map[string]string{"deep/nested": skillMD("N", "n"), "top": skillMD("T", "t")},
		mcp: `{"srv":{"command":"x"}}`,
	})
	f.installMarketplace(map[string]string{"p@m": act}, "p@m")
	inv := f.load()
	noErrs(t, inv)
	for _, name := range []string{"nested", "top"} {
		sk := mustFind(t, inv.Skills, name)
		if len(sk.Installs) != 0 || sk.StaleCount() != 1 || sk.Copies[0].Plugin != "p" || sk.Copies[0].Path != old {
			t.Errorf("skill %s = %+v", name, sk)
		}
	}
	srv := mustFind(t, inv.MCP, "srv")
	if len(srv.Installs) != 0 || srv.StaleCount() != 1 || srv.Copies[0].Trashable {
		t.Errorf("mcp = %+v", srv)
	}
}

func TestCopiesDoNotFollowSymlinks(t *testing.T) {
	fakeProcs(t, nil)
	f := newFixture(t)
	outside := filepath.Join(f.root, "outside")
	f.write(filepath.Join(outside, "big"), "0123456789")
	dir := f.plugin(f.cacheDir("m", "p", "1"), plug{name: "p"})
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	f.cli("plugins/installed_plugins.json", `{"plugins":{}}`)
	size, _ := measure(dir)
	plain, _ := measure(f.plugin(f.cacheDir("m", "q", "1"), plug{name: "p"}))
	if size != plain {
		t.Errorf("size %d counts the symlink target (plain copy is %d)", size, plain)
	}
}

func TestFind(t *testing.T) {
	inv := &Inventory{Plugins: []Item{{Kind: KindPlugin, Name: "a"}}, Skills: []Item{{Kind: KindSkill, Name: "a"}}}
	if it := inv.Find(KindPlugin, "a"); it == nil || it.Kind != KindPlugin {
		t.Errorf("Find plugin = %v", it)
	}
	if inv.Find(KindMCP, "a") != nil || inv.Find(KindSkill, "zzz") != nil {
		t.Error("Find should return nil for a miss")
	}
}
