package ui

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestWrapWords(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  []string
	}{
		{"empty string returns single empty line", "", 10, []string{""}},
		{"width zero returns input as-is", "hello world", 0, []string{"hello world"}},
		{"single line fits", "hello world", 20, []string{"hello world"}},
		{"wraps at word boundary", "the quick brown fox", 10, []string{"the quick", "brown fox"}},
		{"single word longer than width still emitted", "supercalifragilistic", 5, []string{"supercalifragilistic"}},
		{"multiple wraps", "a b c d e f g h", 3, []string{"a b", "c d", "e f", "g h"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapWords(tt.input, tt.width)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestProjectLabel(t *testing.T) {
	// projectLabel resolves $HOME-encoded prefixes. We test the home-prefix branch by
	// constructing an encoded path whose prefix matches what UserHomeDir actually returns.
	// Non-home-prefixed inputs are returned with - → / substitution.
	tests := []struct {
		name    string
		encoded string
		want    string
	}{
		{"non-home-prefixed gets slash substitution", "-tmp-foo-bar", "tmp/foo/bar"},
		{"only-hyphens becomes slashes", "-a-b-c", "a/b/c"},
		{"empty encoded returns itself", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := projectLabel(tt.encoded)
			// We can't assert exact equality for the home-prefixed case because UserHomeDir
			// is environment-dependent, so we only test branches that don't depend on $HOME.
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestProjectLabel_StripsHomePrefix verifies the home-strip branch by mirroring
// projectLabel's own encoding of $HOME (trim leading /, replace / with -).
func TestProjectLabel_StripsHomePrefix(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("UserHomeDir unavailable")
	}
	encodedHome := strings.ReplaceAll(strings.TrimPrefix(home, "/"), "/", "-")
	encoded := "-" + encodedHome + "-personal-clasp"
	if got, want := projectLabel(encoded), "personal/clasp"; got != want {
		t.Errorf("projectLabel(%q) = %q, want %q", encoded, got, want)
	}
}
