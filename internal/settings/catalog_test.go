package settings

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestDefaultCatalog(t *testing.T) {
	c := Default()
	docs := c.Docs()

	if got := len(docs); got != 244 {
		t.Errorf("catalog has %d keys, want 244", got)
	}
	if _, err := time.Parse(time.DateOnly, c.Fetched()); err != nil {
		t.Errorf("fetched %q is not a date: %v", c.Fetched(), err)
	}

	seen := map[string]bool{}
	for _, d := range docs {
		if seen[d.Key] {
			t.Errorf("duplicate key %q", d.Key)
		}
		seen[d.Key] = true
		if !d.Scope.Valid() {
			t.Errorf("%s: invalid scope %q", d.Key, d.Scope)
		}
		switch d.Merge {
		case MergeReplace, MergeList, MergeMap, MergeUndocumented:
		default:
			t.Errorf("%s: invalid merge %q", d.Key, d.Merge)
		}
		if d.Anchor == "" || d.Summary == "" {
			t.Errorf("%s: missing anchor or summary", d.Key)
		}
		if d.Type == "" && d.Removed == "" {
			t.Errorf("%s: no type and not removed", d.Key)
		}
	}
}

// TestSTECoverage keeps catalog.json and ste.json in step: every documented
// key needs one sentence, and ste.json must not describe unknown keys.
func TestSTECoverage(t *testing.T) {
	var ste map[string]string
	if err := json.Unmarshal(steJSON, &ste); err != nil {
		t.Fatalf("ste.json: %v", err)
	}
	c := Default()

	var missing, extra []string
	for _, d := range c.Docs() {
		if strings.TrimSpace(d.STE) == "" {
			missing = append(missing, d.Key)
		}
	}
	for k := range ste {
		if _, ok := c.Lookup(k); !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("%d catalog keys without an STE line: %s", len(missing), strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		t.Errorf("%d ste.json keys not in catalog: %s", len(extra), strings.Join(extra, ", "))
	}
}

func TestSTELength(t *testing.T) {
	for _, d := range Default().Docs() {
		if n := len(strings.Fields(d.STE)); n > 25 {
			t.Errorf("%s: STE line has %d words, max 25", d.Key, n)
		}
	}
}

func TestCatalogLookup(t *testing.T) {
	c := NewCatalog("2026-01-02", []Doc{
		{Key: "model", Anchor: "model", Scope: ScopeAny, Merge: MergeReplace},
		{Key: "permissions", Anchor: "permissions", Scope: ScopeAny, Merge: MergeUndocumented},
		{Key: "permissions.allow", Anchor: "permissions-allow", Scope: ScopeAny, Merge: MergeList},
		{Key: "sandbox.network.allowedDomains", Anchor: "sandbox-network-alloweddomains", Scope: ScopeAny, Merge: MergeList},
	})

	tests := []struct {
		key          string
		wantFound    bool
		wantChildren bool
	}{
		{"model", true, false},
		{"permissions", true, true},
		{"permissions.allow", true, false},
		{"sandbox", false, true},
		{"sandbox.network", false, true},
		{"perm", false, false},
		{"missing", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			d, ok := c.Lookup(tt.key)
			if ok != tt.wantFound {
				t.Errorf("Lookup found = %v, want %v", ok, tt.wantFound)
			}
			if ok && d.Key != tt.key {
				t.Errorf("Lookup returned %q", d.Key)
			}
			if got := c.HasChildren(tt.key); got != tt.wantChildren {
				t.Errorf("HasChildren = %v, want %v", got, tt.wantChildren)
			}
		})
	}

	if got := c.Docs()[2].Key; got != "permissions.allow" {
		t.Errorf("Docs order: index 2 = %q", got)
	}
	if got := c.Fetched(); got != "2026-01-02" {
		t.Errorf("Fetched = %q", got)
	}
	if got, want := c.DocsURL("model"), "https://code.claude.com/docs/en/settings-reference#model"; got != want {
		t.Errorf("DocsURL = %q, want %q", got, want)
	}
}

func TestParseEmbeddedFoldsSTE(t *testing.T) {
	cat := []byte(`{"source":"https://example.com/ref.md","fetched":"2026-10-12","keys":[{"key":"a","anchor":"a","scope":"Any file","merge":"replace"}]}`)
	c, err := parseEmbedded(cat, []byte(`{"a":"Use this key.","zzz":"Unknown."}`))
	if err != nil {
		t.Fatal(err)
	}
	d, _ := c.Lookup("a")
	if d.STE != "Use this key." {
		t.Errorf("STE = %q", d.STE)
	}
	if got := c.DocsURL("a"); got != "https://example.com/ref#a" {
		t.Errorf("DocsURL = %q", got)
	}
	if _, err := parseEmbedded([]byte(`{`), []byte(`{}`)); err == nil {
		t.Error("want error for malformed catalog")
	}
}
