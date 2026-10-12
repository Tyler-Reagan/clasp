package inventory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileHashCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	write := func(content string, mtime time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	write("---\ndescription: one\n---\naaaa", t0)

	before := hashReads.Load()
	sum1, desc, err := fileHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if hashReads.Load() != before+1 || desc != "one" {
		t.Fatalf("first call: reads +%d, desc %q", hashReads.Load()-before, desc)
	}

	// Same size and mtime: served from the cache even though the bytes changed.
	write("---\ndescription: two\n---\nbbbb", t0)
	sum2, desc2, _ := fileHash(path)
	if sum2 != sum1 || desc2 != "one" || hashReads.Load() != before+1 {
		t.Errorf("unchanged stat: want cached sum, got reads +%d", hashReads.Load()-before)
	}

	// A new mtime re-reads and re-hashes.
	write("---\ndescription: two\n---\nbbbb", t0.Add(time.Minute))
	sum3, desc3, _ := fileHash(path)
	if sum3 == sum1 || desc3 != "two" || hashReads.Load() != before+2 {
		t.Errorf("changed mtime: sum changed=%v desc=%q reads +%d", sum3 != sum1, desc3, hashReads.Load()-before)
	}

	// So does a new size.
	write("longer content than before", t0.Add(time.Minute))
	if sum4, _, _ := fileHash(path); sum4 == sum3 || hashReads.Load() != before+3 {
		t.Errorf("changed size: reads +%d", hashReads.Load()-before)
	}
}

func TestFileHashErrors(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := fileHash(filepath.Join(dir, "missing")); err == nil {
		t.Error("want an error for a missing file")
	}
	if _, _, err := fileHash(dir); err == nil {
		t.Error("want an error for a directory")
	}
}

func TestLoadReusesHashCache(t *testing.T) {
	f := newFixture(t)
	f.cli("skills/a/SKILL.md", skillMD("a", "1"))
	p := f.plugin(filepath.Join(f.root, "cache", "p"), plug{
		name: "p", skills: map[string]string{"s": skillMD("s", "1")}, extra: map[string]string{"README.md": "r"},
	})
	f.installMarketplace(map[string]string{"p@m": p}, "p@m")

	first := f.load()
	reads := hashReads.Load()
	second := f.load()
	if n := hashReads.Load() - reads; n != 0 {
		t.Errorf("second Load re-read %d files, want 0", n)
	}
	if !reflectEqualInstalls(first, second) {
		t.Error("second Load gave different installs")
	}

	// Touching one file re-reads only that file.
	skill := filepath.Join(p, "skills", "s", "SKILL.md")
	if err := os.WriteFile(skill, []byte(skillMD("s", "changed")), 0o644); err != nil {
		t.Fatal(err)
	}
	reads = hashReads.Load()
	third := f.load()
	if n := hashReads.Load() - reads; n != 1 {
		t.Errorf("third Load re-read %d files, want 1", n)
	}
	if mustFind(t, third.Skills, "s").Installs[0].Hash == mustFind(t, first.Skills, "s").Installs[0].Hash {
		t.Error("skill hash did not change")
	}
	if mustFind(t, third.Plugins, "p").Installs[0].Hash == mustFind(t, first.Plugins, "p").Installs[0].Hash {
		t.Error("plugin tree hash did not change with its skill")
	}
}

func reflectEqualInstalls(a, b *Inventory) bool {
	if len(a.Skills) != len(b.Skills) || len(a.Plugins) != len(b.Plugins) {
		return false
	}
	for i := range a.Skills {
		if a.Skills[i].Installs[0] != b.Skills[i].Installs[0] {
			return false
		}
	}
	for i := range a.Plugins {
		if a.Plugins[i].Installs[0] != b.Plugins[i].Installs[0] {
			return false
		}
	}
	return true
}

func TestTreeHash(t *testing.T) {
	build := func(extra map[string]string) string {
		root := t.TempDir()
		files := map[string]string{"a.txt": "a", "sub/b.txt": "b", "sub/deep/c.txt": "c"}
		for k, v := range extra {
			files[k] = v
		}
		for rel, content := range files {
			p := filepath.Join(root, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		sum, err := treeHash(root)
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}

	base := build(nil)
	if build(nil) != base {
		t.Error("same tree in another dir should hash the same")
	}

	ignored := map[string]string{
		".in_use":                "pid",
		".DS_Store":              "x",
		"design.meta.json":       "{}",
		"sub/x.meta.json":        "{}",
		".git/HEAD":              "ref",
		"node_modules/m/i.js":    "js",
		"sub/node_modules/z.txt": "z",
	}
	if got := build(ignored); got != base {
		t.Error("marker files, .git/ and node_modules/ should not change the hash")
	}

	for name, extra := range map[string]map[string]string{
		"new file":             {"d.txt": "d"},
		"changed file":         {"a.txt": "A"},
		"moved file":           {"sub/a2.txt": "a"},
		"manifest.json counts": {"manifest.json": "{}"},
	} {
		if build(extra) == base {
			t.Errorf("%s: hash unchanged", name)
		}
	}
}

func TestTreeHashMissingRoot(t *testing.T) {
	if _, err := treeHash(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("want an error for a missing root")
	}
}

func TestTreeHashSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "f.txt"), []byte("f"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := treeHash(root)
	if err != nil {
		t.Fatal(err)
	}
	// A symlinked dir is not followed and a dangling link counts as nothing.
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "gone"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	got, err := treeHash(root)
	if err != nil || got != base {
		t.Errorf("hash = %q (err %v), want unchanged %q", got, err, base)
	}
	// A symlinked file counts as its target's content.
	if err := os.Symlink(filepath.Join(outside, "f.txt"), filepath.Join(root, "linkfile")); err != nil {
		t.Fatal(err)
	}
	if got, _ := treeHash(root); got == base {
		t.Error("symlinked file should be hashed")
	}
}
