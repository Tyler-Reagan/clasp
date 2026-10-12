package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fixture is a fake machine under t.TempDir: a home with ~/.claude, a
// managed dir, and a project.
type fixture struct {
	t       *testing.T
	root    string
	env     Env
	project string
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
			ManagedDir: filepath.Join(root, "managed"),
		},
		project: filepath.Join(root, "proj"),
	}
	for _, d := range []string{f.env.ClaudeDir, f.env.ManagedDir, filepath.Join(f.project, ".claude")} {
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

func (f *fixture) user(s string) { f.write(filepath.Join(f.env.ClaudeDir, "settings.json"), s) }
func (f *fixture) proj(s string) { f.write(filepath.Join(f.project, ".claude", "settings.json"), s) }
func (f *fixture) local(s string) {
	f.write(filepath.Join(f.project, ".claude", "settings.local.json"), s)
}
func (f *fixture) managed(s string) {
	f.write(filepath.Join(f.env.ManagedDir, "managed-settings.json"), s)
}
func (f *fixture) global(s string) { f.write(filepath.Join(f.env.Home, ".claude.json"), s) }

// testCatalog covers each scope and merge kind the resolver handles.
func testCatalog() *Catalog {
	d := func(key string, scope Scope, merge MergeKind) Doc {
		return Doc{Key: key, Scope: scope, Merge: merge}
	}
	removed := d("taskOutputMaxChars", ScopeAny, MergeReplace)
	removed.Removed = "v2.1.277"
	return NewCatalog("2026-10-12", []Doc{
		d("spinnerTipsEnabled", ScopeAny, MergeReplace),
		d("model", ScopeAny, MergeReplace),
		d("permissions", ScopeAny, MergeUndocumented),
		d("permissions.allow", ScopeAny, MergeList),
		d("permissions.ask", ScopeAny, MergeList),
		d("permissions.deny", ScopeAny, MergeList),
		d("permissions.additionalDirectories", ScopeAny, MergeList),
		d("enabledPlugins", ScopeAny, MergeMap),
		d("extraKnownMarketplaces", ScopeAny, MergeMap),
		d("hooks", ScopeAny, MergeMap),
		d("statusLine", ScopeAny, MergeUndocumented),
		d("fallbackModel", ScopeAny, MergeList),
		d("modelPicker", ScopeUserManaged, MergeUndocumented),
		d("availableModels", ScopeAny, MergeList),
		d("modelSettings", ScopeAny, MergeMap),
		d("maxEffortLevel", ScopeAny, MergeReplace),
		d("apiKeyHelper", ScopeUserManaged, MergeReplace),
		d("copyOnSelect", ScopeGlobal, MergeReplace),
		d("sandbox", ScopeAny, MergeUndocumented),
		d("sandbox.network", ScopeAny, MergeUndocumented),
		d("sandbox.network.allowedDomains", ScopeAny, MergeList),
		d("autoMode", ScopeUserManaged, MergeUndocumented),
		d("autoMode.classifyAllShell", ScopeUserManaged, MergeReplace),
		d("strictPluginOnlyCustomization", ScopeManaged, MergeReplace),
		d("strictPluginOnlyCustomization.agents", ScopeManaged, MergeReplace),
		removed,
	})
}

func TestFlatten(t *testing.T) {
	cat := testCatalog()
	tests := []struct {
		name string
		in   string
		want map[string]any
	}{
		{
			name: "schema skipped, undocumented top-level kept",
			in:   `{"$schema": "x", "fooBar": 1, "model": "opus"}`,
			want: map[string]any{"fooBar": num("1"), "model": "opus"},
		},
		{
			name: "three levels deep",
			in:   `{"sandbox": {"network": {"allowedDomains": ["a.com"]}}}`,
			want: map[string]any{"sandbox.network.allowedDomains": []any{"a.com"}},
		},
		{
			name: "unknown sub-key stays on parent",
			in:   `{"autoMode": {"soft_deny": ["x"], "classifyAllShell": true}}`,
			want: map[string]any{
				"autoMode":                  map[string]any{"soft_deny": []any{"x"}},
				"autoMode.classifyAllShell": true,
			},
		},
		{
			name: "unknown sub-key two levels down stays on nearest parent",
			in:   `{"sandbox": {"network": {"zz": 1}, "yy": 2}}`,
			want: map[string]any{
				"sandbox":         map[string]any{"yy": num("2")},
				"sandbox.network": map[string]any{"zz": num("1")},
			},
		},
		{
			name: "parent with only known children is not a row",
			in:   `{"permissions": {"allow": ["Bash(ls)"]}}`,
			want: map[string]any{"permissions.allow": []any{"Bash(ls)"}},
		},
		{
			name: "parent with a non-object value is a row",
			in:   `{"permissions": false}`,
			want: map[string]any{"permissions": false},
		},
		{
			name: "array-member parent is not walked",
			in:   `{"strictPluginOnlyCustomization": ["agents"]}`,
			want: map[string]any{"strictPluginOnlyCustomization": []any{"agents"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := decodeObject([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]any{}
			flatten(cat, "", obj, got)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestDecodeObject(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{`{}`, false},
		{"  \n", false},
		{`{"a": 1.50}`, false},
		{`[1]`, true},
		{`{"a":`, true},
		{`{} {}`, true},
	}
	for _, tt := range tests {
		_, err := decodeObject([]byte(tt.in))
		if (err != nil) != tt.wantErr {
			t.Errorf("decodeObject(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
		}
	}
}

func TestManagedFilesOrder(t *testing.T) {
	f := newFixture(t)
	d := filepath.Join(f.env.ManagedDir, "managed-settings.d")
	for _, name := range []string{"20-b.json", "10-a.json", ".hidden.json", "x.txt"} {
		f.write(filepath.Join(d, name), `{}`)
	}
	got := managedFiles(f.env.ManagedDir)
	want := []string{
		filepath.Join(f.env.ManagedDir, "managed-settings.json"),
		filepath.Join(d, "10-a.json"),
		filepath.Join(d, "20-b.json"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("managedFiles = %v, want %v", got, want)
	}
	if got := managedFiles(""); got != nil {
		t.Errorf("managedFiles(\"\") = %v, want nil", got)
	}
}

func TestReadGlobal(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(f.root, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	f.global(`{
		"oauthAccount": {"emailAddress": "someone@example.com"},
		"copyOnSelect": true,
		"numStartups": 88,
		"projects": {
			"` + f.project + `": {"hasTrustDialogAccepted": false, "history": []},
			"` + other + `": {},
			"/does/not/exist": {"hasTrustDialogAccepted": true}
		}
	}`)

	g := readGlobal(testCatalog(), f.env, f.project)
	if g.layer.Err != nil || !g.layer.Present {
		t.Fatalf("layer: present=%v err=%v", g.layer.Present, g.layer.Err)
	}
	if want := map[string]any{"copyOnSelect": true}; !reflect.DeepEqual(g.layer.Values, want) {
		t.Errorf("Values = %v, want only %v", g.layer.Values, want)
	}
	if want := []string{other, f.project}; !reflect.DeepEqual(g.known, want) {
		t.Errorf("known = %v, want %v", g.known, want)
	}
	if g.trust == nil || *g.trust {
		t.Errorf("trust = %v, want false", g.trust)
	}

	if g := readGlobal(testCatalog(), f.env, other); g.trust != nil {
		t.Errorf("project without the flag: trust = %v, want nil", *g.trust)
	}
}

func TestDefaultEnv(t *testing.T) {
	env := DefaultEnv()
	if env.Home == "" || env.ClaudeDir != filepath.Join(env.Home, ".claude") {
		t.Errorf("DefaultEnv = %+v", env)
	}
}
