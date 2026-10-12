package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func num(s string) json.Number { return json.Number(s) }

func findItem(t *testing.T, s *Snapshot, key string) Item {
	t.Helper()
	for _, it := range s.Items {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("no item %q", key)
	return Item{}
}

// contribs renders an item's contributions as "label:status" highest first.
func contribs(it Item) []string {
	var out []string
	for _, c := range it.Contribs {
		out = append(out, c.Layer.Kind.Label()+":"+c.Status.String())
	}
	return out
}

func winner(it Item) string {
	if it.Winner == nil {
		return ""
	}
	return it.Winner.Kind.Label()
}

// TestDocsPrecedence follows the spinnerTipsEnabled example from the docs,
// adding one layer at a time.
func TestDocsPrecedence(t *testing.T) {
	tests := []struct {
		name         string
		user, proj   string
		local, mgd   string
		wantValue    any
		wantWinner   string
		wantContribs []string
	}{
		{
			name: "project over user",
			user: "false", proj: "true",
			wantValue: true, wantWinner: "project",
			wantContribs: []string{"project:wins", "user:overridden"},
		},
		{
			name: "local over project",
			user: "false", proj: "true", local: "false",
			wantValue: false, wantWinner: "local",
			wantContribs: []string{"local:wins", "project:overridden", "user:overridden"},
		},
		{
			name: "managed over all",
			user: "false", proj: "true", local: "false", mgd: "true",
			wantValue: true, wantWinner: "managed",
			wantContribs: []string{"managed:wins", "local:overridden", "project:overridden", "user:overridden"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			set := func(w func(string), v string) {
				if v != "" {
					w(`{"spinnerTipsEnabled": ` + v + `}`)
				}
			}
			set(f.user, tt.user)
			set(f.proj, tt.proj)
			set(f.local, tt.local)
			set(f.managed, tt.mgd)

			// Same result from the fixture catalog and the real one.
			for _, cat := range []*Catalog{testCatalog(), Default()} {
				it := findItem(t, resolveWith(f.env, f.project, cat), "spinnerTipsEnabled")
				if it.Value != tt.wantValue || winner(it) != tt.wantWinner {
					t.Errorf("value %v from %q, want %v from %q", it.Value, winner(it), tt.wantValue, tt.wantWinner)
				}
				if got := contribs(it); !reflect.DeepEqual(got, tt.wantContribs) {
					t.Errorf("contribs = %v, want %v", got, tt.wantContribs)
				}
			}
		})
	}
}

func TestListMerge(t *testing.T) {
	f := newFixture(t)
	f.user(`{"permissions": {"allow": ["Bash(ls)", "Bash(a)"]}}`)
	f.proj(`{"permissions": {"allow": ["Bash(b)"]}}`)
	f.local(`{"permissions": {"allow": ["Bash(c)", "Bash(ls)"]}}`)

	it := findItem(t, resolveWith(f.env, f.project, testCatalog()), "permissions.allow")
	type part struct {
		layer, value string
		shadowed     bool
	}
	var got []part
	for _, p := range it.Parts {
		got = append(got, part{p.Layer.Kind.Label(), p.Value.(string), p.Shadowed})
	}
	want := []part{
		{"local", "Bash(c)", false},
		{"local", "Bash(ls)", false},
		{"project", "Bash(b)", false},
		{"user", "Bash(ls)", true},
		{"user", "Bash(a)", false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parts = %v\nwant %v", got, want)
	}
	if v := FormatValue(it.Value); v != `["Bash(c)","Bash(ls)","Bash(b)","Bash(a)"]` {
		t.Errorf("value = %s", v)
	}
	if got := contribs(it); !reflect.DeepEqual(got, []string{"local:merged", "project:merged", "user:merged"}) {
		t.Errorf("contribs = %v", got)
	}
	if it.Winner != nil {
		t.Errorf("list merge has winner %s", winner(it))
	}
}

func TestManagedDropIns(t *testing.T) {
	f := newFixture(t)
	d := filepath.Join(f.env.ManagedDir, "managed-settings.d")
	f.managed(`{"model": "base", "permissions": {"deny": ["Bash(base)"]}}`)
	f.write(filepath.Join(d, "20-b.json"), `{"model": "b", "permissions": {"deny": ["Bash(b)"]}}`)
	f.write(filepath.Join(d, "10-a.json"), `{"model": "a", "permissions": {"deny": ["Bash(a)"]}}`)
	f.write(filepath.Join(d, ".hidden.json"), `{"model": "hidden"}`)
	f.write(filepath.Join(d, "x.txt"), `{"model": "txt"}`)

	s := resolveWith(f.env, "", testCatalog())

	var paths []string
	for _, l := range s.Layers {
		if l.Kind == LayerManaged {
			paths = append(paths, filepath.Base(l.Path))
		}
	}
	if want := []string{"20-b.json", "10-a.json", "managed-settings.json"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("managed layers = %v, want %v", paths, want)
	}

	model := findItem(t, s, "model")
	if model.Value != "b" || !strings.HasSuffix(model.Winner.Path, "20-b.json") {
		t.Errorf("model = %v from %s, want b from 20-b.json", model.Value, model.Winner.Path)
	}
	deny := findItem(t, s, "permissions.deny")
	if v := FormatValue(deny.Value); v != `["Bash(b)","Bash(a)","Bash(base)"]` {
		t.Errorf("deny = %s", v)
	}
}

func TestSpecialCases(t *testing.T) {
	f := newFixture(t)
	f.user(`{
		"fallbackModel": ["sonnet", "haiku"],
		"availableModels": ["opus"],
		"modelPicker": {"options": ["u"]},
		"modelSettings": {"opus": {"effort": "high"}},
		"maxEffortLevel": "high"
	}`)
	f.proj(`{"fallbackModel": ["opus"], "modelPicker": {"options": ["p"]}, "availableModels": ["sonnet"]}`)
	f.managed(`{"availableModels": ["haiku", "opus"]}`)
	s := resolveWith(f.env, f.project, testCatalog())

	t.Run("fallbackModel replaces whole value", func(t *testing.T) {
		it := findItem(t, s, "fallbackModel")
		if FormatValue(it.Value) != `["opus"]` || winner(it) != "project" || len(it.Parts) != 0 {
			t.Errorf("value %s from %s, parts %d", FormatValue(it.Value), winner(it), len(it.Parts))
		}
	})
	t.Run("modelPicker in project is ignored", func(t *testing.T) {
		it := findItem(t, s, "modelPicker")
		if got := contribs(it); !reflect.DeepEqual(got, []string{"project:ignored", "user:wins"}) {
			t.Errorf("contribs = %v", got)
		}
		if it.Contribs[0].Reason != "User or managed only" || it.Warnings != 1 {
			t.Errorf("reason %q, warnings %d", it.Contribs[0].Reason, it.Warnings)
		}
		if FormatValue(it.Value) != `{"options":["u"]}` {
			t.Errorf("value %s", FormatValue(it.Value))
		}
	})
	t.Run("availableModels managed list as-is", func(t *testing.T) {
		it := findItem(t, s, "availableModels")
		if got := contribs(it); !reflect.DeepEqual(got, []string{"managed:merged", "project:ignored", "user:ignored"}) {
			t.Errorf("contribs = %v", got)
		}
		if it.Contribs[1].Reason != noteManagedAsIs {
			t.Errorf("reason = %q", it.Contribs[1].Reason)
		}
		if FormatValue(it.Value) != `["haiku","opus"]` {
			t.Errorf("value %s", FormatValue(it.Value))
		}
	})
	t.Run("notes", func(t *testing.T) {
		if it := findItem(t, s, "modelSettings"); !slices.Contains(it.Notes, "effortLevel can change which file wins per model.") {
			t.Errorf("modelSettings notes = %v", it.Notes)
		}
		if it := findItem(t, s, "maxEffortLevel"); !slices.Contains(it.Notes, noteStricter) || it.Value != "high" {
			t.Errorf("maxEffortLevel notes = %v value %v", it.Notes, it.Value)
		}
		it := findItem(t, s, "strictPluginOnlyCustomization.agents")
		if !slices.Contains(it.Notes, noteArrayMember) || it.Set() {
			t.Errorf("array member: notes %v set %v", it.Notes, it.Set())
		}
	})
}

func TestScopeGating(t *testing.T) {
	f := newFixture(t)
	f.user(`{"apiKeyHelper": "user.sh", "copyOnSelect": false}`)
	f.proj(`{"apiKeyHelper": "proj.sh"}`)
	f.global(`{"copyOnSelect": true}`)
	s := resolveWith(f.env, f.project, testCatalog())

	it := findItem(t, s, "apiKeyHelper")
	if it.Value != "user.sh" || winner(it) != "user" {
		t.Errorf("apiKeyHelper = %v from %s", it.Value, winner(it))
	}
	if c := it.Contribs[0]; c.Status != StatusIgnored || c.Reason != "User or managed only" || it.Warnings != 1 {
		t.Errorf("project contribution: %v %q, warnings %d", c.Status, c.Reason, it.Warnings)
	}

	it = findItem(t, s, "copyOnSelect")
	if got := contribs(it); !reflect.DeepEqual(got, []string{"user:ignored", "global:wins"}) {
		t.Errorf("copyOnSelect contribs = %v", got)
	}
	if it.Value != true || it.Contribs[0].Reason != "only read from ~/.claude.json" {
		t.Errorf("copyOnSelect = %v, reason %q", it.Value, it.Contribs[0].Reason)
	}
}

func TestUndocumentedAndNested(t *testing.T) {
	f := newFixture(t)
	f.user(`{
		"$schema": "https://json.schemastore.org/claude-code-settings.json",
		"fooBar": {"x": 1},
		"sandbox": {"network": {"allowedDomains": ["a.com"]}},
		"autoMode": {"soft_deny": ["no"], "classifyAllShell": true}
	}`)
	s := resolveWith(f.env, "", testCatalog())

	foo := findItem(t, s, "fooBar")
	if foo.Documented || FormatValue(foo.Value) != `{"x":1}` {
		t.Errorf("fooBar documented=%v value=%s", foo.Documented, FormatValue(foo.Value))
	}
	for _, it := range s.Items {
		if it.Key == "$schema" {
			t.Error("$schema became an item")
		}
	}
	if it := findItem(t, s, "sandbox.network.allowedDomains"); FormatValue(it.Value) != `["a.com"]` {
		t.Errorf("allowedDomains = %s", FormatValue(it.Value))
	}
	if it := findItem(t, s, "sandbox"); it.Set() {
		t.Error("sandbox parent is set with no residual keys")
	}
	auto := findItem(t, s, "autoMode")
	if len(auto.Contribs) != 1 || FormatValue(auto.Contribs[0].Value) != `{"soft_deny":["no"]}` {
		t.Errorf("autoMode contribs = %+v", auto.Contribs)
	}
	if it := findItem(t, s, "autoMode.classifyAllShell"); it.Value != true {
		t.Errorf("classifyAllShell = %v", it.Value)
	}

	// Items are sorted case-insensitively.
	if !slices.IsSortedFunc(s.Items, func(a, b Item) int {
		return strings.Compare(strings.ToLower(a.Key), strings.ToLower(b.Key))
	}) {
		t.Error("items not sorted")
	}
}

func TestMapMerge(t *testing.T) {
	f := newFixture(t)
	f.user(`{
		"enabledPlugins": {"a": true, "b": true},
		"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "u"}]}]}
	}`)
	f.local(`{
		"enabledPlugins": {"b": false},
		"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "l"}]}], "PreToolUse": []}
	}`)
	s := resolveWith(f.env, f.project, testCatalog())

	plugins := findItem(t, s, "enabledPlugins")
	if FormatValue(plugins.Value) != `{"a":true,"b":false}` {
		t.Errorf("enabledPlugins = %s", FormatValue(plugins.Value))
	}
	type part struct {
		name, layer string
		value       any
		shadowed    bool
	}
	var got []part
	for _, p := range plugins.Parts {
		got = append(got, part{p.Name, p.Layer.Kind.Label(), p.Value, p.Shadowed})
	}
	want := []part{{"a", "user", true, false}, {"b", "local", false, false}, {"b", "user", true, true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parts = %v\nwant %v", got, want)
	}

	hooks := findItem(t, s, "hooks")
	wantHooks := `{"PreToolUse":[],"Stop":[{"hooks":[{"command":"l","type":"command"}]},{"hooks":[{"command":"u","type":"command"}]}]}`
	if v := FormatValue(hooks.Value); v != wantHooks {
		t.Errorf("hooks = %s\nwant %s", v, wantHooks)
	}
	for _, p := range hooks.Parts {
		if p.Shadowed {
			t.Errorf("hook part %s from %s shadowed; arrays should concatenate", p.Name, p.Layer.Kind.Label())
		}
	}
}

func TestUndocumentedMerge(t *testing.T) {
	f := newFixture(t)
	f.user(`{"statusLine": {"type": "command", "command": "u"}}`)
	f.proj(`{"statusLine": {"type": "command", "command": "p"}}`)
	it := findItem(t, resolveWith(f.env, f.project, testCatalog()), "statusLine")
	if got := contribs(it); !reflect.DeepEqual(got, []string{"project:listed", "user:listed"}) {
		t.Errorf("contribs = %v", got)
	}
	if it.Winner != nil || it.Value != nil || !slices.Contains(it.Notes, noteUndocumentedMerge) {
		t.Errorf("winner %v value %v notes %v", it.Winner, it.Value, it.Notes)
	}
}

func TestRemovedKey(t *testing.T) {
	f := newFixture(t)
	f.user(`{"taskOutputMaxChars": 1000}`)
	it := findItem(t, resolveWith(f.env, "", testCatalog()), "taskOutputMaxChars")
	if it.Warnings != 1 || !slices.Contains(it.Notes, "Removed in v2.1.277; Claude Code ignores it.") {
		t.Errorf("warnings %d notes %v", it.Warnings, it.Notes)
	}
	if FormatValue(it.Value) != "1000" {
		t.Errorf("value = %s", FormatValue(it.Value))
	}
}

func TestBrokenAndSharedLayers(t *testing.T) {
	t.Run("invalid JSON contributes nothing", func(t *testing.T) {
		f := newFixture(t)
		f.user(`{"model": "opus"}`)
		f.proj(`{"model": "sonnet",`)
		s := resolveWith(f.env, f.project, testCatalog())
		var proj *Layer
		for i := range s.Layers {
			if s.Layers[i].Kind == LayerProject {
				proj = &s.Layers[i]
			}
		}
		if proj == nil || proj.Err == nil || !proj.Present || proj.Values != nil {
			t.Fatalf("project layer = %+v", proj)
		}
		if it := findItem(t, s, "model"); it.Value != "opus" || len(it.Contribs) != 1 {
			t.Errorf("model = %v, contribs %v", it.Value, contribs(it))
		}
	})
	t.Run("project is home", func(t *testing.T) {
		f := newFixture(t)
		f.user(`{"model": "opus"}`)
		f.write(filepath.Join(f.env.ClaudeDir, "settings.local.json"), `{"spinnerTipsEnabled": false}`)
		s := resolveWith(f.env, f.env.Home, testCatalog())
		var labels []string
		for _, l := range s.Layers {
			labels = append(labels, l.Kind.Label())
			if l.Kind == LayerProject && (l.Present || l.Note != "same file as user") {
				t.Errorf("project layer = %+v", l)
			}
		}
		if want := []string{"managed", "local", "project", "user", "global"}; !reflect.DeepEqual(labels, want) {
			t.Errorf("layers = %v, want %v", labels, want)
		}
		if it := findItem(t, s, "model"); !reflect.DeepEqual(contribs(it), []string{"user:wins"}) {
			t.Errorf("model contribs = %v", contribs(it))
		}
		if it := findItem(t, s, "spinnerTipsEnabled"); winner(it) != "local" {
			t.Errorf("spinnerTipsEnabled from %q, want local", winner(it))
		}
	})
	t.Run("no project", func(t *testing.T) {
		f := newFixture(t)
		s := resolveWith(f.env, "", testCatalog())
		for _, l := range s.Layers {
			if l.Kind == LayerProject || l.Kind == LayerLocal {
				t.Errorf("unexpected %s layer", l.Kind.Label())
			}
		}
	})
}

func TestMDMPresent(t *testing.T) {
	f := newFixture(t)
	plist := filepath.Join(f.root, "mdm.plist")
	f.env.MDMPaths = []string{filepath.Join(f.root, "absent.plist"), plist}
	if resolveWith(f.env, "", testCatalog()).MDMPresent {
		t.Error("MDMPresent with no plist")
	}
	f.write(plist, "<plist/>")
	if !resolveWith(f.env, "", testCatalog()).MDMPresent {
		t.Error("MDMPresent false with a plist")
	}
}

func TestFormatValue(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{nil, "null"},
		{"a<b", `"a<b"`},
		{num("1.50"), "1.50"},
		{map[string]any{"b": 1, "a": []any{true}}, `{"a":[true],"b":1}`},
	}
	for _, tt := range tests {
		if got := FormatValue(tt.in); got != tt.want {
			t.Errorf("FormatValue(%#v) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

// TestSmoke resolves this machine's real settings. Run with CLASP_SMOKE=1.
func TestSmoke(t *testing.T) {
	if os.Getenv("CLASP_SMOKE") != "1" {
		t.Skip("set CLASP_SMOKE=1 to read this machine's settings")
	}
	project := os.Getenv("CLASP_SMOKE_PROJECT")
	if project == "" {
		project, _ = filepath.Abs("../..")
	}
	s := Resolve(DefaultEnv(), project)
	t.Logf("project %s, trust %v, mdm %v, %d known projects, fetched %s",
		s.Project, deref(s.Trust), s.MDMPresent, len(s.KnownProjects), s.Fetched)
	for _, l := range s.Layers {
		t.Logf("layer %-8s present=%-5v err=%v note=%q keys=%d %s", l.Kind.Label(), l.Present, l.Err, l.Note, len(l.Values), l.Path)
	}
	for _, it := range s.Items {
		if !it.Set() {
			continue
		}
		missing := 0
		for _, p := range it.Parts {
			if p.Missing != "" {
				missing++
				t.Logf("    missing %s", p.Missing)
			}
		}
		val := FormatValue(it.Value)
		if len(val) > 60 {
			val = val[:57] + "..."
		}
		t.Logf("%-28s doc=%-5v winner=%-8s contribs=%v parts=%d missing=%d warn=%d %s",
			it.Key, it.Documented, winner(it), contribs(it), len(it.Parts), missing, it.Warnings, val)
	}
}

func deref(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}
