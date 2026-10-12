package settings

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestLiteralPrefix(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/a/b/**", "/a/b"},
		{"/a/b/*.go", "/a/b"},
		{"/a/b*", "/a"},
		{"/a/b/file.txt", "/a/b/file.txt"},
		{"/a/{x,y}/c", "/a"},
		{"/a/b?/c", "/a"},
		{"/a/[ab]/c", "/a"},
		{"/**", "/"},
		{"/Library/Application Support/x/**", "/Library/Application Support/x"},
	}
	for _, tt := range tests {
		if got := literalPrefix(tt.in); got != tt.want {
			t.Errorf("literalPrefix(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCheckRule(t *testing.T) {
	root := t.TempDir()
	env := Env{Home: filepath.Join(root, "home"), ClaudeDir: filepath.Join(root, "home", ".claude")}
	project := filepath.Join(root, "proj")
	for _, d := range []string{
		filepath.Join(env.ClaudeDir, "agents"),
		filepath.Join(env.Home, "notes"),
		filepath.Join(project, "src"),
		filepath.Join(root, "abs dir", "with space"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name     string
		rule     string
		kind     LayerKind
		wantPath string // relative to root; "" when not checked
		missing  bool
	}{
		{"absolute live", "Read(/" + root + "/abs dir/with space/**)", LayerLocal, "abs dir/with space", false},
		{"absolute dead", "Edit(/" + root + "/gone/**)", LayerUser, "gone", true},
		{"home live", "Read(~/notes/*.md)", LayerProject, "home/notes", false},
		{"home dead", "Read(~/nope/**)", LayerProject, "home/nope", true},
		{"project root in project file", "Edit(/src/**)", LayerProject, "proj/src", false},
		{"project root in local file, dead", "Edit(/lib/**)", LayerLocal, "proj/lib", true},
		{"slash in user file is ~/.claude", "Read(/agents/**)", LayerUser, "home/.claude/agents", false},
		{"slash in managed file", "Read(/agents/**)", LayerManaged, "", false},
		{"relative", "Read(src/**)", LayerProject, "", false},
		{"dot relative", "Read(./src/**)", LayerProject, "", false},
		{"bang skipped", "Read(!/gone/**)", LayerProject, "", false},
		{"bash ignored", "Bash(ls /gone)", LayerProject, "", false},
		{"no parens", "Read", LayerProject, "", false},
		{"empty spec", "Read()", LayerProject, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, missing, ok := checkRule(tt.rule, tt.kind, env, project)
			if tt.wantPath == "" {
				if ok {
					t.Errorf("checked %q → %s, want skipped", tt.rule, path)
				}
				return
			}
			want := filepath.Join(root, tt.wantPath)
			if !ok || path != want || missing != tt.missing {
				t.Errorf("checkRule(%q) = %q, missing %v, ok %v; want %q, missing %v", tt.rule, path, missing, ok, want, tt.missing)
			}
		})
	}
}

func TestAdditionalDirectories(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(filepath.Join(f.project, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.local(`{"permissions": {"additionalDirectories": ["docs", "../gone", "~/.claude", "~/nope"]}}`)
	it := findItem(t, resolveWith(f.env, f.project, testCatalog()), "permissions.additionalDirectories")
	var missing []string
	for _, p := range it.Parts {
		if p.Missing != "" {
			missing = append(missing, p.Missing)
		}
	}
	want := []string{filepath.Join(f.root, "gone"), filepath.Join(f.env.Home, "nope")}
	if !slices.Equal(missing, want) || it.Warnings != 2 {
		t.Errorf("missing = %v (warnings %d), want %v", missing, it.Warnings, want)
	}
}

func TestFolderTrust(t *testing.T) {
	tests := []struct {
		name     string
		projects string // body of the "projects" object in ~/.claude.json
		inactive bool
	}{
		{"untrusted", `"%s": {"hasTrustDialogAccepted": false}`, true},
		{"trusted", `"%s": {"hasTrustDialogAccepted": true}`, false},
		{"flag absent", `"%s": {}`, false},
		{"project absent", ``, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.global(`{"projects": {` + strings.ReplaceAll(tt.projects, "%s", f.project) + `}}`)
			f.user(`{"permissions": {"allow": ["Bash(u)"]}, "extraKnownMarketplaces": {"m": {"source": {}}}}`)
			f.local(`{"permissions": {"allow": ["Bash(l)"]}, "extraKnownMarketplaces": {"n": {"source": {}}}}`)
			s := resolveWith(f.env, f.project, testCatalog())

			for _, key := range []string{"permissions.allow", "extraKnownMarketplaces"} {
				it := findItem(t, s, key)
				for _, p := range it.Parts {
					want := tt.inactive && p.Layer.Kind == LayerLocal
					if p.Inactive != want {
						t.Errorf("%s part %v from %s: inactive %v, want %v", key, p.Value, p.Layer.Kind.Label(), p.Inactive, want)
					}
				}
				if got := slices.Contains(it.Notes, noteUntrusted); got != tt.inactive {
					t.Errorf("%s note present %v, want %v", key, got, tt.inactive)
				}
			}
		})
	}
}

// realRules are the 18 permissions.allow rules from clasp's own
// .claude/settings.local.json. Five name paths that no longer exist.
var realRules = []string{
	"Read(//Users/tylerreagan/.claude/plugins/cache/claude-plugins-official/vercel/0.43.0/hooks/**)",
	"Read(//Users/tylerreagan/.claude/plugins/cache/claude-plugins-official/vercel/0.43.0/**)",
	"Read(//Users/tylerreagan/Library/Application Support/@skills-bank/desktop/registry/skills/vendored/caveman/**)",
	"Read(//Users/tylerreagan/Library/Application Support/@skills-bank/desktop/registry/skills/personal/gh-cli/**)",
	"Bash(python3 *)",
	"Bash(go build *)",
	"Bash(git add *)",
	"Bash(git commit -m ' *)",
	"Bash(brew list *)",
	"Bash(brew info *)",
	"Read(//opt/homebrew/Caskroom/clasp/0.1.0/**)",
	"Read(//opt/homebrew/bin/**)",
	"Read(//opt/homebrew/Library/Taps/tyler-reagan/homebrew-tap/Casks/**)",
	"Read(//opt/homebrew/Library/Taps/**)",
	"Bash(brew link *)",
	"Read(//opt/homebrew/Caskroom/clasp/**)",
	"Bash(brew reinstall *)",
	"Read(//Users/tylerreagan/.claude/**)",
}

func TestDeadPathFixture(t *testing.T) {
	f := newFixture(t)
	// Move each absolute path under the temp root: "//Users/x" → "/" + root + "/Users/x".
	var rules []string
	for _, r := range realRules {
		r = strings.Replace(r, "(//Users/", "(/"+f.root+"/Users/", 1)
		r = strings.Replace(r, "(//opt/", "(/"+f.root+"/opt/", 1)
		rules = append(rules, r)
	}
	for _, d := range []string{
		"Users/tylerreagan/.claude/plugins/cache/claude-plugins-official",
		"Users/tylerreagan/Library/Application Support/@skills-bank/desktop/registry/skills",
		"opt/homebrew/bin",
		"opt/homebrew/Library/Taps/tyler-reagan/homebrew-tap/Casks",
		"opt/homebrew/Caskroom/clasp",
	} {
		if err := os.MkdirAll(filepath.Join(f.root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data := `{"permissions": {"allow": [` + quoteAll(rules) + `]}}`
	f.local(data)

	for _, cat := range []*Catalog{testCatalog(), Default()} {
		it := findItem(t, resolveWith(f.env, f.project, cat), "permissions.allow")
		if len(it.Parts) != 18 {
			t.Fatalf("%d parts, want 18", len(it.Parts))
		}
		var missing []string
		for _, p := range it.Parts {
			if p.Missing != "" {
				missing = append(missing, strings.TrimPrefix(p.Missing, f.root))
			}
		}
		sort.Strings(missing)
		want := []string{
			"/Users/tylerreagan/.claude/plugins/cache/claude-plugins-official/vercel/0.43.0",
			"/Users/tylerreagan/.claude/plugins/cache/claude-plugins-official/vercel/0.43.0/hooks",
			"/Users/tylerreagan/Library/Application Support/@skills-bank/desktop/registry/skills/personal/gh-cli",
			"/Users/tylerreagan/Library/Application Support/@skills-bank/desktop/registry/skills/vendored/caveman",
			"/opt/homebrew/Caskroom/clasp/0.1.0",
		}
		if !slices.Equal(missing, want) || it.Warnings != 5 {
			t.Errorf("missing (warnings %d):\n%s\nwant:\n%s", it.Warnings, strings.Join(missing, "\n"), strings.Join(want, "\n"))
		}
	}
}

func quoteAll(ss []string) string {
	var out []string
	for _, s := range ss {
		out = append(out, FormatValue(s))
	}
	return strings.Join(out, ",")
}
