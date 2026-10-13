package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tylerreagan/clasp/internal/inventory"
)

const (
	tOrg  = "aaaaaaaa-1111-2222-3333-444444444444"
	tAcct = "bbbbbbbb-5555-6666-7777-888888888888"
)

// trashFixture is a fake machine under t.TempDir with fakes for every seam:
// the Trash is a temp dir, and Desktop and live sessions answer as set.
type trashFixture struct {
	t        *testing.T
	env      inventory.Env
	trash    string
	running  bool
	known    bool
	live     map[string][]int // path -> live PIDs
	failMove map[string]bool  // path -> rename fails
	moves    []string
}

func newTrashFixture(t *testing.T) *trashFixture {
	t.Helper()
	root := t.TempDir()
	f := &trashFixture{
		t: t,
		env: inventory.Env{
			Home:       filepath.Join(root, "home"),
			ClaudeDir:  filepath.Join(root, "home", ".claude"),
			DesktopDir: filepath.Join(root, "desktop"),
		},
		trash:    filepath.Join(root, "Trash"),
		known:    true,
		live:     map[string][]int{},
		failMove: map[string]bool{},
	}
	for _, d := range []string{f.env.ClaudeDir, f.env.DesktopDir, f.trash} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	saved := []any{trashEnv, loadInventory, trashMove, liveUsers, desktopRunning}
	t.Cleanup(func() {
		trashEnv = saved[0].(func() inventory.Env)
		loadInventory = saved[1].(func(inventory.Env) *inventory.Inventory)
		trashMove = saved[2].(func(string) error)
		liveUsers = saved[3].(func(string) []int)
		desktopRunning = saved[4].(func() (bool, bool))
	})
	trashEnv = func() inventory.Env { return f.env }
	loadInventory = f.load
	trashMove = func(p string) error {
		f.moves = append(f.moves, p)
		if f.failMove[p] {
			return errors.New("operation not permitted")
		}
		return os.Rename(p, uniqueTrashName(f.trash, filepath.Base(p)))
	}
	liveUsers = func(p string) []int { return f.live[p] }
	desktopRunning = func() (bool, bool) { return f.running, f.known }
	return f
}

// load is the real inventory.Load, except that rpm leftovers take their
// Desktop answer from the fixture: the inventory package probes the real
// Desktop app, which this package cannot fake. It mirrors inventory's own
// classify for a leftover nobody has in use.
func (f *trashFixture) load(env inventory.Env) *inventory.Inventory {
	inv := inventory.Load(env)
	for _, items := range [][]inventory.Item{inv.Skills, inv.Plugins, inv.MCP} {
		for i := range items {
			for j := range items[i].Copies {
				c := &items[i].Copies[j]
				if c.Status != inventory.CopyLeftover || c.Origin != inventory.OriginRPM || len(c.LivePIDs) > 0 {
					continue
				}
				switch {
				case !f.known:
					c.Trashable, c.Why = false, "can't tell whether Claude Desktop is running"
				case f.running:
					c.Trashable, c.Why = false, "quit Claude Desktop first"
				default:
					c.Trashable, c.Why = true, "safe to move to the Trash"
				}
			}
		}
	}
	return inv
}

func (f *trashFixture) write(path, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// plugin writes a plugin dir with a plugin.json, a skill and a README.
func (f *trashFixture) plugin(dir, name, version string) string {
	f.write(filepath.Join(dir, ".claude-plugin", "plugin.json"), fmt.Sprintf(`{"name":%q,"version":%q}`, name, version))
	f.write(filepath.Join(dir, "skills", "sk-"+name, "SKILL.md"), "---\nname: sk\ndescription: d\n---\n"+version+"\n")
	f.write(filepath.Join(dir, "README"), "readme "+version)
	return dir
}

func (f *trashFixture) syncedAcct() string {
	return filepath.Join(f.env.ClaudeDir, "plugins", "synced", tOrg+"_"+tAcct)
}

func (f *trashFixture) syncedDir(d string) string { return filepath.Join(f.syncedAcct(), d) }

func (f *trashFixture) syncedManifest(js string) {
	f.write(filepath.Join(f.syncedAcct(), "manifest.json"), js)
}

func (f *trashFixture) rpmRoot() string {
	return filepath.Join(f.env.DesktopDir, "local-agent-mode-sessions", tAcct, tOrg, "rpm")
}

func (f *trashFixture) cacheDir(mkt, pl, ver string) string {
	return filepath.Join(f.env.ClaudeDir, "plugins", "cache", mkt, pl, ver)
}

func (f *trashFixture) installed(id, path string) {
	f.write(filepath.Join(f.env.ClaudeDir, "plugins", "installed_plugins.json"),
		fmt.Sprintf(`{"version":2,"plugins":{%q:[{"scope":"user","installPath":%q,"version":"1"}]}}`, id, path))
}

// snapshot reads every regular file under dir, keyed by relative path.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, p)
			out[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestTrashLeftovers(t *testing.T) {
	type result struct {
		moved   []string          // paths
		refused map[string]string // path -> substring of the reason
		kept    []string          // paths that must still exist
	}
	tests := []struct {
		name  string
		kind  inventory.Kind
		item  string
		setup func(f *trashFixture) result
	}{
		{
			name: "synced older generation is moved; current one untouched",
			kind: inventory.KindPlugin, item: "x",
			setup: func(f *trashFixture) result {
				old := f.plugin(f.syncedDir("x"), "x", "1")
				cur := f.plugin(f.syncedDir("x~g2"), "x", "2")
				f.syncedManifest(`{"plugins":[{"name":"x","generation":2}]}`)
				return result{moved: []string{old}, kept: []string{cur}}
			},
		},
		{
			name: "asking via a skill inside the leftover moves the plugin dir once",
			kind: inventory.KindSkill, item: "sk-x",
			setup: func(f *trashFixture) result {
				old := f.plugin(f.syncedDir("x"), "x", "1")
				cur := f.plugin(f.syncedDir("x~g2"), "x", "2")
				f.syncedManifest(`{"plugins":[{"name":"x","generation":2}]}`)
				return result{moved: []string{old}, kept: []string{cur}}
			},
		},
		{
			name: "only active copies: nothing moved",
			kind: inventory.KindPlugin, item: "x",
			setup: func(f *trashFixture) result {
				cur := f.plugin(f.syncedDir("x~g2"), "x", "2")
				f.syncedManifest(`{"plugins":[{"name":"x","generation":2}]}`)
				return result{kept: []string{cur}}
			},
		},
		{
			name: "manifest changed since the UI: fresh load sees no leftover",
			kind: inventory.KindPlugin, item: "x",
			setup: func(f *trashFixture) result {
				a := f.plugin(f.syncedDir("x"), "x", "1")
				b := f.plugin(f.syncedDir("x~g2"), "x", "2")
				f.syncedManifest(`{"plugins":[{"name":"x","generation":2}]}`)
				// The UI showed "x" as a leftover; then the manifest went away.
				if err := os.Remove(filepath.Join(f.syncedAcct(), "manifest.json")); err != nil {
					f.t.Fatal(err)
				}
				return result{kept: []string{a, b}}
			},
		},
		{
			name: "stale cache copy is never a candidate",
			kind: inventory.KindPlugin, item: "p",
			setup: func(f *trashFixture) result {
				act := f.plugin(f.cacheDir("mkt", "p", "2"), "p", "2")
				old := f.plugin(f.cacheDir("mkt", "p", "1"), "p", "1")
				f.installed("p@mkt", act)
				return result{kept: []string{act, old}}
			},
		},
		{
			name: "plugins/data leftover is refused",
			kind: inventory.KindPlugin, item: "gone",
			setup: func(f *trashFixture) result {
				f.installed("p@mkt", f.plugin(f.cacheDir("mkt", "p", "1"), "p", "1"))
				f.write(filepath.Join(f.env.ClaudeDir, "plugins", "known_marketplaces.json"), `{"mkt":{}}`)
				d := filepath.Join(f.env.ClaudeDir, "plugins", "data", "gone-mkt")
				f.write(filepath.Join(d, "state.db"), "keep me")
				return result{refused: map[string]string{d: "kept plugin data"}, kept: []string{d}}
			},
		},
		{
			name: "live session holds it",
			kind: inventory.KindPlugin, item: "x",
			setup: func(f *trashFixture) result {
				old := f.plugin(f.syncedDir("x"), "x", "1")
				f.plugin(f.syncedDir("x~g2"), "x", "2")
				f.syncedManifest(`{"plugins":[{"name":"x","generation":2}]}`)
				f.live[old] = []int{4242}
				return result{refused: map[string]string{old: "in use by PID 4242"}, kept: []string{old}}
			},
		},
		{
			name: "rpm leftover with Desktop not running is moved",
			kind: inventory.KindPlugin, item: "gone",
			setup: func(f *trashFixture) result {
				live := f.plugin(filepath.Join(f.rpmRoot(), "plugin_live"), "live", "1")
				gone := f.plugin(filepath.Join(f.rpmRoot(), "plugin_gone"), "gone", "1")
				f.write(filepath.Join(f.rpmRoot(), "manifest.json"), `{"plugins":[{"id":"plugin_live","name":"live"}]}`)
				return result{moved: []string{gone}, kept: []string{live}}
			},
		},
		{
			name: "rpm leftover with Desktop running is refused",
			kind: inventory.KindPlugin, item: "gone",
			setup: func(f *trashFixture) result {
				f.running = true
				f.plugin(filepath.Join(f.rpmRoot(), "plugin_live"), "live", "1")
				gone := f.plugin(filepath.Join(f.rpmRoot(), "plugin_gone"), "gone", "1")
				f.write(filepath.Join(f.rpmRoot(), "manifest.json"), `{"plugins":[{"id":"plugin_live","name":"live"}]}`)
				return result{refused: map[string]string{gone: "quit Claude Desktop first"}, kept: []string{gone}}
			},
		},
		{
			name: "rpm leftover with Desktop state unknown is refused",
			kind: inventory.KindPlugin, item: "gone",
			setup: func(f *trashFixture) result {
				f.known = false
				f.plugin(filepath.Join(f.rpmRoot(), "plugin_live"), "live", "1")
				gone := f.plugin(filepath.Join(f.rpmRoot(), "plugin_gone"), "gone", "1")
				f.write(filepath.Join(f.rpmRoot(), "manifest.json"), `{"plugins":[{"id":"plugin_live","name":"live"}]}`)
				return result{refused: map[string]string{gone: "can't tell"}, kept: []string{gone}}
			},
		},
		{
			name: "symlinked leftover dir is refused",
			kind: inventory.KindPlugin, item: "x",
			setup: func(f *trashFixture) result {
				target := f.plugin(filepath.Join(filepath.Dir(f.env.Home), "elsewhere", "x"), "x", "1")
				f.plugin(f.syncedDir("x~g2"), "x", "2")
				f.syncedManifest(`{"plugins":[{"name":"x","generation":2}]}`)
				link := f.syncedDir("x")
				if err := os.Symlink(target, link); err != nil {
					f.t.Fatal(err)
				}
				return result{refused: map[string]string{link: "symlink"}, kept: []string{link, target}}
			},
		},
		{
			name: "rename failure is refused and the next path is still tried",
			kind: inventory.KindPlugin, item: "x",
			setup: func(f *trashFixture) result {
				g1 := f.plugin(f.syncedDir("x"), "x", "1")
				g2 := f.plugin(f.syncedDir("x~g2"), "x", "2")
				g3 := f.plugin(f.syncedDir("x~g3"), "x", "3")
				f.syncedManifest(`{"plugins":[{"name":"x","generation":3}]}`)
				f.failMove[g1] = true
				return result{moved: []string{g2}, refused: map[string]string{g1: "operation not permitted"}, kept: []string{g1, g3}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTrashFixture(t)
			want := tt.setup(f)
			before := map[string]map[string]string{}
			for _, p := range want.moved {
				before[p] = snapshot(t, p)
			}

			moved, refused, err := TrashLeftovers(tt.kind, tt.item)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			sort.Strings(moved)
			wantMoved := append([]string(nil), want.moved...)
			sort.Strings(wantMoved)
			if fmt.Sprint(moved) != fmt.Sprint(wantMoved) {
				t.Errorf("moved = %v, want %v", moved, wantMoved)
			}
			if len(refused) != len(want.refused) {
				t.Errorf("refused = %v, want %v", refused, want.refused)
			}
			for p, sub := range want.refused {
				if !strings.Contains(refused[p], sub) {
					t.Errorf("refused[%s] = %q, want it to mention %q", p, refused[p], sub)
				}
			}
			for _, p := range want.moved {
				if exists(p) {
					t.Errorf("%s still in place", p)
				}
				in := filepath.Join(f.trash, filepath.Base(p))
				if got := snapshot(t, in); fmt.Sprint(got) != fmt.Sprint(before[p]) {
					t.Errorf("trashed content differs:\n got %v\nwant %v", got, before[p])
				}
			}
			for _, p := range want.kept {
				if !exists(p) {
					t.Errorf("%s was moved", p)
				}
			}
		})
	}
}

func TestTrashLeftoversMissingItem(t *testing.T) {
	newTrashFixture(t)
	moved, refused, err := TrashLeftovers(inventory.KindPlugin, "nope")
	if err == nil || moved != nil || refused != nil {
		t.Fatalf("moved=%v refused=%v err=%v", moved, refused, err)
	}
}

// TestTrashLeftoversDeadPIDMoves uses the real LiveUsers: an .in_use file for
// a PID that does not exist does not hold the copy.
func TestTrashLeftoversDeadPIDMoves(t *testing.T) {
	f := newTrashFixture(t)
	liveUsers = inventory.LiveUsers
	old := f.plugin(f.syncedDir("x"), "x", "1")
	f.plugin(f.syncedDir("x~g2"), "x", "2")
	f.syncedManifest(`{"plugins":[{"name":"x","generation":2}]}`)
	f.write(filepath.Join(old, ".in_use", "99999999"), `{"pid":99999999,"procStart":"Mon Oct 12 20:11:22 2026"}`)

	moved, refused, err := TrashLeftovers(inventory.KindPlugin, "x")
	if err != nil || len(moved) != 1 || moved[0] != old || len(refused) != 0 {
		t.Fatalf("moved=%v refused=%v err=%v", moved, refused, err)
	}
}

// TestTrashLeftoversForgedInventory feeds paths the real loader would never
// produce, to prove the path guards hold on their own even if the inventory
// is wrong.
func TestTrashLeftoversForgedInventory(t *testing.T) {
	f := newTrashFixture(t)
	acct := f.syncedAcct()
	real := f.plugin(f.syncedDir("real"), "real", "1")
	nested := f.plugin(filepath.Join(real, "inner"), "inner", "1")
	outside := f.plugin(filepath.Join(f.env.Home, "Documents", "x"), "x", "1")
	cache := f.plugin(f.cacheDir("mkt", "p", "1"), "p", "1")
	activeElsewhere := f.plugin(f.syncedDir("act"), "act", "1")
	rpmBad := f.plugin(filepath.Join(f.env.DesktopDir, "local-agent-mode-sessions", "skills-plugin", tOrg, "rpm", "plugin_x"), "s", "1")

	leftover := func(path, origin string) inventory.Copy {
		return inventory.Copy{Status: inventory.CopyLeftover, Origin: origin, Path: path, Trashable: true, Why: "safe to move to the Trash"}
	}
	tests := []struct {
		name string
		cp   inventory.Copy
		why  string
	}{
		{"escape with ..", leftover(filepath.Join(acct, "..", "..", "..", "..", "Documents", "x"), inventory.OriginSynced), "not a plugin dir"},
		{"outside every root", leftover(outside, inventory.OriginSynced), "not a plugin dir"},
		{"the whole account dir", leftover(acct, inventory.OriginSynced), "not a plugin dir"},
		{"too deep", leftover(nested, inventory.OriginSynced), "not a plugin dir"},
		{"cache copy forged as trashable", leftover(cache, inventory.OriginMarketplace), "only synced and Desktop rpm"},
		{"same path as an active install", leftover(activeElsewhere, inventory.OriginSynced), "active install"},
		{"rpm under skills-plugin", leftover(rpmBad, inventory.OriginRPM), "not an rpm plugin dir"},
		{"not trashable in the fresh load", inventory.Copy{Status: inventory.CopyLeftover, Origin: inventory.OriginSynced, Path: real, Why: "in use by PID 7"}, "in use by PID 7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadInventory = func(inventory.Env) *inventory.Inventory {
				return &inventory.Inventory{Plugins: []inventory.Item{
					{Kind: inventory.KindPlugin, Name: "victim", Copies: []inventory.Copy{tt.cp}},
					{Kind: inventory.KindPlugin, Name: "act", Installs: []inventory.Install{{Origin: inventory.OriginSynced, Path: activeElsewhere}}},
				}}
			}
			moved, refused, err := TrashLeftovers(inventory.KindPlugin, "victim")
			if err != nil || len(moved) != 0 {
				t.Fatalf("moved=%v err=%v", moved, err)
			}
			if got := refused[filepath.Clean(tt.cp.Path)]; !strings.Contains(got, tt.why) {
				t.Errorf("refused = %v, want a reason with %q", refused, tt.why)
			}
		})
	}
	if len(f.moves) != 0 {
		t.Errorf("mover was called for %v", f.moves)
	}
	for _, p := range []string{real, nested, outside, cache, activeElsewhere, rpmBad} {
		if !exists(p) {
			t.Errorf("%s was moved", p)
		}
	}
}

// TestTrashLeftoversSymlinkedAncestor: a symlinked account dir inside the
// synced root is refused even though the plugin dir under it is real.
func TestTrashLeftoversSymlinkedAncestor(t *testing.T) {
	f := newTrashFixture(t)
	elsewhere := filepath.Join(filepath.Dir(f.env.Home), "elsewhere-acct")
	f.plugin(filepath.Join(elsewhere, "x"), "x", "1")
	f.plugin(filepath.Join(elsewhere, "x~g2"), "x", "2")
	f.write(filepath.Join(elsewhere, "manifest.json"), `{"plugins":[{"name":"x","generation":2}]}`)
	if err := os.MkdirAll(filepath.Dir(f.syncedAcct()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, f.syncedAcct()); err != nil {
		t.Fatal(err)
	}

	moved, refused, err := TrashLeftovers(inventory.KindPlugin, "x")
	if err != nil || len(moved) != 0 || len(refused) != 1 {
		t.Fatalf("moved=%v refused=%v err=%v", moved, refused, err)
	}
	if got := refused[f.syncedDir("x")]; !strings.Contains(got, "account dir is not a real directory") {
		t.Errorf("refused = %v", refused)
	}
	if !exists(filepath.Join(elsewhere, "x")) {
		t.Error("the dir behind the link was moved")
	}
}

func TestWithin(t *testing.T) {
	tests := []struct {
		path, dir string
		want      bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/c", "/a/b", true},
		{"/a/bc", "/a/b", false},
		{"/a", "/a/b", false},
		{"/a/b/..c", "/a/b", true},
	}
	for _, tt := range tests {
		if got := within(tt.path, tt.dir); got != tt.want {
			t.Errorf("within(%q, %q) = %v, want %v", tt.path, tt.dir, got, tt.want)
		}
	}
}
