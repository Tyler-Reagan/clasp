package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseMini(t *testing.T) {
	src, err := os.ReadFile("testdata/mini.md")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	want := []entry{
		{
			Key:           "alpha",
			Anchor:        "alpha",
			Topic:         "Model and responses",
			Scope:         "Any file",
			Summary:       "Pick the alpha mode for --alpha runs",
			Type:          "Boolean",
			TypeValues:    []string{"true: alpha is on", "false: alpha is off, see the docs"},
			Default:       "false",
			OverrideFlags: []string{"--alpha-mode"},
			OverrideEnv:   []string{"ALPHA_MODE"},
			Merge:         "replace",
		},
		{
			Key:        "beta",
			Anchor:     "beta",
			Topic:      "Model and responses",
			Scope:      "User or managed",
			ScopeNote:  "A repository can't set it for you.",
			Summary:    "Choose a beta level | or none",
			Type:       "string, one of:",
			TypeValues: []string{`"low": less`, `"high": more`},
			Default:    `"low"`,
			Merge:      "replace",
		},
		{
			Key:     "oldKey",
			Anchor:  "oldkey",
			Topic:   "Memory and context",
			Scope:   "Any file",
			Summary: "Removed in v9.9.9",
			Merge:   "replace",
			Removed: "v9.9.9",
		},
		{
			Key:     "sandbox.network.allowedDomains",
			Anchor:  "sandbox-network-alloweddomains",
			Topic:   "Sandbox settings",
			Scope:   "Managed",
			Summary: "Pre-allow domains",
			Type:    "array of strings, each a domain",
			Default: "unset",
			Merge:   "list",
		},
		{
			Key:     "hooks",
			Anchor:  "hooks",
			Topic:   "Hooks and automation",
			Scope:   "Global config",
			Summary: "Run commands on events",
			Type:    "object keyed by hook event; each value is an array",
			Default: "unset & empty",
			Merge:   "map",
		},
	}

	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(entries), len(want))
	}
	for i, w := range want {
		t.Run(w.Key, func(t *testing.T) {
			if !reflect.DeepEqual(entries[i], w) {
				t.Errorf("got  %+v\nwant %+v", entries[i], w)
			}
		})
	}
}

const errIndex = `## Settings index

| Key | Description | Topic | Scope |
| :- | :- | :- | :- |
`

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name: "section without index row",
			input: errIndex + "| [`a`](#a) | A | T | Any file |\n\n" +
				"### `a`\n\n* **Type**: Boolean\n\n### `b`\n\n* **Type**: Boolean\n",
			wantErr: "mismatch",
		},
		{
			name: "index row without section",
			input: errIndex + "| [`a`](#a) | A | T | Any file |\n| [`b`](#b) | B | T | Any file |\n\n" +
				"### `a`\n\n* **Type**: Boolean\n",
			wantErr: "mismatch",
		},
		{
			name:    "duplicate index key",
			input:   errIndex + "| [`a`](#a) | A | T | Any file |\n| [`a`](#a) | A | T | Any file |\n",
			wantErr: "duplicate",
		},
		{
			name: "duplicate section",
			input: errIndex + "| [`a`](#a) | A | T | Any file |\n\n" +
				"### `a`\n\n* **Type**: Boolean\n\n### `a`\n\n* **Type**: Boolean\n",
			wantErr: "duplicate",
		},
		{
			name:    "unknown scope",
			input:   errIndex + "| [`a`](#a) | A | T | Project |\n\n### `a`\n\n* **Type**: Boolean\n",
			wantErr: "unknown scope",
		},
		{
			name:    "missing type",
			input:   errIndex + "| [`a`](#a) | A | T | Any file |\n\n### `a`\n\n* **Default**: unset\n",
			wantErr: "no Type",
		},
		{
			name:    "no index",
			input:   "# Settings\n",
			wantErr: "Settings index",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse([]byte(tt.input))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestPlain(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"[`a`](#a) and [b [c](x)](y)", "a and b c"},
		{"**bold**  `code`\n  wrap", "bold code wrap"},
		{`\<tag\> a\|b snake\_case \*`, "<tag> a|b snake_case *"},
		{"Tom &amp; Jerry&#39;s", "Tom & Jerry's"},
	}
	for _, tt := range tests {
		if got := plain(tt.in); got != tt.want {
			t.Errorf("plain(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMergeKind(t *testing.T) {
	tests := []struct {
		typ, want string
	}{
		{"array of strings", "list"},
		{"Array of objects", "list"},
		{"object mapping names to values", "map"},
		{"object keyed by event", "map"},
		{"object with `enabled`", "undocumented"},
		{"Boolean", "replace"},
		{"string, one of:", "replace"},
	}
	for _, tt := range tests {
		if got := mergeKind(tt.typ); got != tt.want {
			t.Errorf("mergeKind(%q) = %q, want %q", tt.typ, got, tt.want)
		}
	}
}
