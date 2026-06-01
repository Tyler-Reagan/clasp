package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// readEnabled extracts the enabledPlugins map from settings bytes.
func readEnabled(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	ep := map[string]bool{}
	if v, ok := raw["enabledPlugins"]; ok {
		if err := json.Unmarshal(v, &ep); err != nil {
			t.Fatalf("unmarshal enabledPlugins: %v", err)
		}
	}
	return ep
}

func TestToggleEnabledPlugin_NilInput(t *testing.T) {
	out, err := toggleEnabledPlugin(nil, "foo")
	if err != nil {
		t.Fatalf("toggleEnabledPlugin(nil): %v", err)
	}
	ep := readEnabled(t, out)
	if !reflect.DeepEqual(ep, map[string]bool{"foo": true}) {
		t.Errorf("enabledPlugins = %v, want {foo:true}", ep)
	}
}

func TestToggleEnabledPlugin_EnableThenDisable(t *testing.T) {
	out, err := toggleEnabledPlugin(nil, "foo")
	if err != nil {
		t.Fatal(err)
	}
	if got := readEnabled(t, out); !got["foo"] {
		t.Fatalf("after enable, expected foo=true, got %v", got)
	}

	out2, err := toggleEnabledPlugin(out, "foo")
	if err != nil {
		t.Fatal(err)
	}
	got := readEnabled(t, out2)
	if _, present := got["foo"]; present {
		t.Errorf("after disable, expected foo absent from enabledPlugins, got %v", got)
	}
}

func TestToggleEnabledPlugin_PreservesUnknownFields(t *testing.T) {
	in := []byte(`{
  "enabledPlugins": {"foo": true},
  "effortLevel": "high",
  "statusLine": {"command": "echo hi", "nested": {"key": [1, 2, 3]}}
}`)
	out, err := toggleEnabledPlugin(in, "bar")
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	// Unknown fields must survive verbatim.
	for _, key := range []string{"effortLevel", "statusLine"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing preserved field %q", key)
		}
	}

	// effortLevel value should be intact.
	var effort string
	if err := json.Unmarshal(raw["effortLevel"], &effort); err != nil {
		t.Fatalf("unmarshal effortLevel: %v", err)
	}
	if effort != "high" {
		t.Errorf("effortLevel = %q, want %q", effort, "high")
	}

	// statusLine nested structure should be intact.
	var sl struct {
		Command string `json:"command"`
		Nested  struct {
			Key []int `json:"key"`
		} `json:"nested"`
	}
	if err := json.Unmarshal(raw["statusLine"], &sl); err != nil {
		t.Fatalf("unmarshal statusLine: %v", err)
	}
	if sl.Command != "echo hi" || !reflect.DeepEqual(sl.Nested.Key, []int{1, 2, 3}) {
		t.Errorf("statusLine corrupted: %+v", sl)
	}

	// Plugin state: foo was unchanged, bar got enabled.
	ep := readEnabled(t, out)
	if !ep["foo"] || !ep["bar"] {
		t.Errorf("enabledPlugins = %v, want both foo and bar true", ep)
	}
}

func TestToggleEnabledPlugin_DisableUsesAbsentConvention(t *testing.T) {
	in := []byte(`{"enabledPlugins": {"foo": true}}`)
	out, err := toggleEnabledPlugin(in, "foo")
	if err != nil {
		t.Fatal(err)
	}
	// Disable must remove the key entirely, not set it to false. Claude Code's convention
	// is present+true = enabled, absent = disabled.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	var ep map[string]bool
	if err := json.Unmarshal(raw["enabledPlugins"], &ep); err != nil {
		t.Fatal(err)
	}
	if _, present := ep["foo"]; present {
		t.Errorf("disabled plugin should be absent from map, got %v", ep)
	}
}

func TestToggleEnabledPlugin_InvalidJSON(t *testing.T) {
	_, err := toggleEnabledPlugin([]byte("{not json"), "foo")
	if err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}
}

func TestRemoveIndexLine(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		file        string
		want        string
		wantChanged bool
	}{
		{
			"removes the matching pointer line",
			"# Memory index\n\n- [A](a.md) — x\n- [B](b.md) — y\n",
			"a.md",
			"# Memory index\n\n- [B](b.md) — y\n",
			true,
		},
		{
			"no match leaves content and reports unchanged",
			"# Memory index\n\n- [A](a.md) — x\n",
			"z.md",
			"# Memory index\n\n- [A](a.md) — x\n",
			false,
		},
		{
			"filename substring does not over-match a longer name",
			"# Memory index\n\n- [A](a.md) — x\n- [AA](aa.md) — y\n",
			"a.md",
			"# Memory index\n\n- [AA](aa.md) — y\n",
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := removeIndexLine(tt.content, tt.file)
			if got != tt.want {
				t.Errorf("content = %q, want %q", got, tt.want)
			}
			if changed != tt.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
		})
	}
}

func TestUniqueTrashName(t *testing.T) {
	dir := t.TempDir()

	if got, want := uniqueTrashName(dir, "a.md"), filepath.Join(dir, "a.md"); got != want {
		t.Fatalf("no collision: got %q, want %q", got, want)
	}

	mustTouch(t, filepath.Join(dir, "a.md"))
	if got, want := uniqueTrashName(dir, "a.md"), filepath.Join(dir, "a 2.md"); got != want {
		t.Fatalf("first collision: got %q, want %q", got, want)
	}

	mustTouch(t, filepath.Join(dir, "a 2.md"))
	if got, want := uniqueTrashName(dir, "a.md"), filepath.Join(dir, "a 3.md"); got != want {
		t.Fatalf("second collision: got %q, want %q", got, want)
	}
}

func TestDeleteMemory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".Trash"), 0o755); err != nil {
		t.Fatal(err)
	}
	proj := "-Users-test-proj"
	memDir := filepath.Join(home, ".claude", "projects", proj, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const file = "foo-bar.md"
	mustWrite(t, filepath.Join(memDir, file), "---\nname: foo-bar\n---\nbody")
	mustWrite(t, filepath.Join(memDir, "MEMORY.md"),
		"# Memory index\n\n- [Foo](foo-bar.md) — hook\n- [Other](other.md) — keep\n")

	if err := DeleteMemory(proj, file); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}

	if _, err := os.Stat(filepath.Join(memDir, file)); !os.IsNotExist(err) {
		t.Errorf("memory file still in place (err=%v); should be trashed", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".Trash", file)); err != nil {
		t.Errorf("file not found in Trash: %v", err)
	}

	idx := mustRead(t, filepath.Join(memDir, "MEMORY.md"))
	if strings.Contains(idx, "foo-bar.md") {
		t.Errorf("index still references the deleted file:\n%s", idx)
	}
	if !strings.Contains(idx, "other.md") {
		t.Errorf("index lost an unrelated entry:\n%s", idx)
	}
}

func TestDeleteMemory_RefusesIndexFile(t *testing.T) {
	// Refusal happens before any disk access, so no HOME setup is needed.
	if err := DeleteMemory("any-project", "MEMORY.md"); err == nil {
		t.Fatal("expected error deleting MEMORY.md, got nil")
	}
}

func mustTouch(t *testing.T, path string) {
	t.Helper()
	mustWrite(t, path, "")
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
