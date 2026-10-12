package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSkillMD(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantName    string
		wantDesc    string
		wantPreview string
	}{
		{
			name: "inline name and description",
			input: `---
name: my-skill
description: a short description
---
# Heading

The body paragraph.`,
			wantName: "my-skill",
			wantDesc: "a short description",
			// firstParagraph stops at the first blank line after content begins;
			// the heading is its own paragraph here, body is a second paragraph.
			wantPreview: "Heading",
		},
		{
			name: "folded block scalar description",
			input: `---
name: my-skill
description: >
  This description is
  folded across multiple
  lines.
---
Body.`,
			wantName:    "my-skill",
			wantDesc:    "This description is folded across multiple lines.",
			wantPreview: "Body.",
		},
		{
			name: "literal block scalar description",
			input: `---
name: my-skill
description: |
  Line one.
  Line two.
---
Body.`,
			wantName:    "my-skill",
			wantDesc:    "Line one.\nLine two.",
			wantPreview: "Body.",
		},
		{
			name: "no frontmatter falls back to body preview",
			input: `Just some content.

Second paragraph.`,
			wantName:    "",
			wantDesc:    "",
			wantPreview: "Just some content.",
		},
		{
			name: "unterminated frontmatter falls back to body preview",
			input: `---
name: x
this never closes`,
			wantName:    "",
			wantDesc:    "",
			wantPreview: "--- name: x this never closes",
		},
		{
			name: "frontmatter only, no body",
			input: `---
name: solo
description: alone
---`,
			wantName:    "solo",
			wantDesc:    "alone",
			wantPreview: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotDesc, gotPreview := parseSkillMD(tt.input)
			if gotName != tt.wantName {
				t.Errorf("name = %q, want %q", gotName, tt.wantName)
			}
			if gotDesc != tt.wantDesc {
				t.Errorf("description = %q, want %q", gotDesc, tt.wantDesc)
			}
			if gotPreview != tt.wantPreview {
				t.Errorf("preview = %q, want %q", gotPreview, tt.wantPreview)
			}
		})
	}
}

func TestFirstParagraph(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"single line", "Hello world.", "Hello world."},
		{"strips leading heading markers", "# Heading\n\nbody", "Heading"},
		{"stops at blank line", "Line one.\nLine two.\n\nNew paragraph.", "Line one. Line two."},
		{"caps at five lines with ellipsis", "a\nb\nc\nd\ne\nf\ng", "a b c d e…"},
		{"skips leading blank lines", "\n\nFirst real line.", "First real line."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstParagraph(tt.input); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseMemoryFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    MemoryEntry
	}{
		{
			name: "full frontmatter and body",
			content: `---
name: my-rule
description: a feedback rule
metadata:
  type: feedback
---
The rule body.`,
			want: MemoryEntry{
				Project: "proj",
				File:    "rule.md",
				Name:    "my-rule",
				Desc:    "a feedback rule",
				Type:    "feedback",
				Body:    "The rule body.",
			},
		},
		{
			name: "multi-word type takes first field only",
			content: `---
name: x
type: feedback extra-tag
---
Body.`,
			want: MemoryEntry{
				Project: "proj",
				File:    "rule.md",
				Name:    "x",
				Type:    "feedback",
				Body:    "Body.",
			},
		},
		{
			name:    "no frontmatter retains full content as body",
			content: "just a body, no frontmatter",
			want: MemoryEntry{
				Project: "proj",
				File:    "rule.md",
				Body:    "just a body, no frontmatter",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMemoryFile("proj", "rule.md", tt.content)
			if got != tt.want {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// Sanity check that parseSkillMD handles the real-world shape (frontmatter with
// extra fields that we don't extract — name+description must still be picked up).
func TestParseSkillMD_IgnoresUnknownFields(t *testing.T) {
	in := `---
name: real-skill
description: real description
author: someone
version: 1.0.0
---
Body.`
	gotName, gotDesc, _ := parseSkillMD(in)
	if gotName != "real-skill" {
		t.Errorf("name = %q", gotName)
	}
	if gotDesc != "real description" {
		t.Errorf("desc = %q", gotDesc)
	}
}

// Guard rail: parseSkillMD must not allocate forever on pathological input.
func TestParseSkillMD_LargeFrontmatterDoesNotHang(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\nname: x\ndescription: >\n")
	for i := 0; i < 1000; i++ {
		b.WriteString("  line\n")
	}
	b.WriteString("---\nbody")
	name, _, _ := parseSkillMD(b.String())
	if name != "x" {
		t.Errorf("name = %q", name)
	}
}

func TestLatestSessionCWD(t *testing.T) {
	older, newer, gone := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "deleted")

	tests := []struct {
		name     string
		sessions []Session
		want     string
	}{
		{"empty", nil, ""},
		{"picks newest", []Session{{CWD: older, StartedAt: 1}, {CWD: newer, StartedAt: 2}}, newer},
		{"order independent", []Session{{CWD: newer, StartedAt: 2}, {CWD: older, StartedAt: 1}}, newer},
		{"skips deleted dir", []Session{{CWD: older, StartedAt: 1}, {CWD: gone, StartedAt: 9}}, older},
		{"only deleted", []Session{{CWD: gone, StartedAt: 9}}, ""},
		{"skips empty cwd", []Session{{CWD: "", StartedAt: 9}, {CWD: older, StartedAt: 1}}, older},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := latestSessionCWD(tt.sessions); got != tt.want {
				t.Errorf("latestSessionCWD() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLatestSessionCWD_SkipsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := latestSessionCWD([]Session{{CWD: f, StartedAt: 1}}); got != "" {
		t.Errorf("latestSessionCWD() = %q, want empty for non-directory", got)
	}
}

func TestLoadFor_SetsSettings(t *testing.T) {
	proj := t.TempDir()
	s, _ := LoadFor(proj) // loader errors are expected on a machine without ~/.claude
	if s == nil || s.Settings == nil {
		t.Fatal("LoadFor() left Settings nil")
	}
	if s.Settings.ProjectAuto {
		t.Error("ProjectAuto = true for an explicit project")
	}
	if s.Settings.Project != proj {
		t.Errorf("Settings.Project = %q, want %q", s.Settings.Project, proj)
	}

	auto, _ := LoadFor("")
	if auto == nil || auto.Settings == nil || !auto.Settings.ProjectAuto {
		t.Error("LoadFor(\"\") should set Settings with ProjectAuto = true")
	}
}

func TestLoadFor_SetsInventory(t *testing.T) {
	s, _ := LoadFor(t.TempDir())
	if s == nil || s.Inventory == nil {
		t.Fatal("LoadFor() left Inventory nil")
	}
}
