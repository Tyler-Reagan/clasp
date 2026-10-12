package main

import (
	"bufio"
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"
)

// entry is one settings key as written to catalog.json. Field order and tags
// must stay in step with settings.Doc.
type entry struct {
	Key           string   `json:"key"`
	Anchor        string   `json:"anchor"`
	Topic         string   `json:"topic"`
	Scope         string   `json:"scope"`
	ScopeNote     string   `json:"scopeNote"`
	Summary       string   `json:"summary"`
	Type          string   `json:"type"`
	TypeValues    []string `json:"typeValues,omitempty"`
	Default       string   `json:"default"`
	OverrideFlags []string `json:"overrideFlags,omitempty"`
	OverrideEnv   []string `json:"overrideEnv,omitempty"`
	Merge         string   `json:"merge"`
	Removed       string   `json:"removed,omitempty"`
}

var validScopes = map[string]bool{
	"Any file":                true,
	"User, local, or managed": true,
	"User or managed":         true,
	"User":                    true,
	"Managed":                 true,
	"Global config":           true,
}

var (
	reIndexKey   = regexp.MustCompile("^\\[`([^`]+)`\\]\\(#([^)]+)\\)$")
	reSection    = regexp.MustCompile("^### `([^`]+)`\\s*$")
	reLabel      = regexp.MustCompile(`^\* \*\*(Scope|Type|Default|Per-session overrides)\*\*:\s*(.*)$`)
	reSubBullet  = regexp.MustCompile(`^ {2,3}\* (.*)$`)
	reScopeLabel = regexp.MustCompile("^(?:\\[`([^`]+)`\\]\\([^)]*\\)|`([^`]+)`)")
	reRemoved    = regexp.MustCompile(`Removed in (v[\d.]+)`)
	reLink       = regexp.MustCompile(`\[([^\[\]]*)\]\([^()]*\)`)
	reCode       = regexp.MustCompile("`([^`]+)`")
	// Flags may be camelCase (--allowedTools); a trailing value is ignored.
	reFlag = regexp.MustCompile(`^(--[a-z][a-zA-Z0-9-]*)(?:[ =].*)?$`)
	reEnv  = regexp.MustCompile(`^([A-Z][A-Z0-9_]{2,})(?:=.*)?$`)
)

// section holds the raw bullet values of one ### `key` section.
type section struct {
	scope, typ, def, overrides string
	typeValues                 []string
	removed                    string
	seen                       map[string]bool
}

// parse reads the settings reference markdown. The index table is
// authoritative for key, anchor, summary, topic, and scope; sections add type,
// default, overrides, and removal. Keys come back in index order.
func parse(src []byte) ([]entry, error) {
	lines := splitLines(src)

	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "## Settings index" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("no \"## Settings index\" heading")
	}

	entries, end, err := parseIndex(lines, start)
	if err != nil {
		return nil, err
	}
	sections, order, err := parseSections(lines[end:])
	if err != nil {
		return nil, err
	}

	if err := compareKeys(entries, order); err != nil {
		return nil, err
	}

	for i := range entries {
		e := &entries[i]
		s := sections[e.Key]
		e.Removed = s.removed
		if s.typ == "" && s.removed == "" {
			return nil, fmt.Errorf("key %q has no Type bullet", e.Key)
		}
		if s.scope != "" {
			label, note := splitScope(s.scope)
			if label != "" && label != e.Scope {
				warnf("key %q: index scope %q, section scope %q", e.Key, e.Scope, label)
			}
			e.ScopeNote = note
		}
		e.Type = plain(s.typ)
		for _, v := range s.typeValues {
			e.TypeValues = append(e.TypeValues, plain(v))
		}
		e.Default = plain(s.def)
		e.OverrideFlags, e.OverrideEnv = overrideTokens(s.overrides)
		e.Merge = mergeKind(e.Type)
	}
	return entries, nil
}

// parseIndex reads the table after the index heading and returns the entries
// and the line just past the table.
func parseIndex(lines []string, start int) ([]entry, int, error) {
	i := start
	for i < len(lines) && !strings.HasPrefix(lines[i], "| Key") {
		if strings.HasPrefix(lines[i], "#") {
			return nil, 0, fmt.Errorf("settings index has no table")
		}
		i++
	}
	if i == len(lines) {
		return nil, 0, fmt.Errorf("settings index has no table")
	}
	i += 2 // header and separator rows

	var entries []entry
	seen := map[string]bool{}
	for ; i < len(lines) && strings.HasPrefix(lines[i], "|"); i++ {
		cells := splitRow(lines[i])
		if len(cells) != 4 {
			return nil, 0, fmt.Errorf("index line %d: want 4 cells, got %d", i+1, len(cells))
		}
		m := reIndexKey.FindStringSubmatch(cells[0])
		if m == nil {
			return nil, 0, fmt.Errorf("index line %d: bad key cell %q", i+1, cells[0])
		}
		key := m[1]
		if seen[key] {
			return nil, 0, fmt.Errorf("duplicate index key %q", key)
		}
		seen[key] = true
		scope := plain(cells[3])
		if !validScopes[scope] {
			return nil, 0, fmt.Errorf("key %q: unknown scope %q", key, scope)
		}
		entries = append(entries, entry{
			Key:     key,
			Anchor:  m[2],
			Summary: plain(cells[1]),
			Topic:   plain(cells[2]),
			Scope:   scope,
		})
	}
	if len(entries) == 0 {
		return nil, 0, fmt.Errorf("settings index table is empty")
	}
	return entries, i, nil
}

// parseSections walks ### `key` sections, skipping fenced code and MDX
// component lines. A section ends at the next heading of any level.
func parseSections(lines []string) (map[string]*section, []string, error) {
	sections := map[string]*section{}
	var order []string
	var cur *section
	var fence string     // open fence marker, "" when outside a fence
	var lastLabel string // label of the bullet that sub-bullets attach to
	inWarning := false
	var warning strings.Builder

	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, fence[:1]) == "" {
				fence = ""
			}
			continue
		}
		if f := fenceMarker(trimmed); f != "" {
			fence = f
			lastLabel = ""
			continue
		}

		if cur != nil && !inWarning && strings.HasPrefix(trimmed, "<Warning>") {
			inWarning = true
			l = strings.TrimPrefix(trimmed, "<Warning>")
		}
		if inWarning {
			body, closed := strings.CutSuffix(strings.TrimSpace(l), "</Warning>")
			warning.WriteString(body + " ")
			if closed {
				inWarning = false
				if m := reRemoved.FindStringSubmatch(warning.String()); m != nil && cur.removed == "" {
					cur.removed = strings.TrimRight(m[1], ".")
				}
				warning.Reset()
			}
			continue
		}

		if strings.HasPrefix(l, "#") {
			cur, lastLabel = nil, ""
			if m := reSection.FindStringSubmatch(l); m != nil {
				key := m[1]
				if sections[key] != nil {
					return nil, nil, fmt.Errorf("duplicate section %q", key)
				}
				cur = &section{seen: map[string]bool{}}
				sections[key] = cur
				order = append(order, key)
			}
			continue
		}
		if cur == nil {
			continue
		}

		if strings.HasPrefix(trimmed, "<") {
			lastLabel = ""
			continue
		}

		if m := reLabel.FindStringSubmatch(l); m != nil {
			label, val := m[1], m[2]
			lastLabel = ""
			if cur.seen[label] {
				continue
			}
			cur.seen[label] = true
			lastLabel = label
			switch label {
			case "Scope":
				cur.scope = val
			case "Type":
				cur.typ = val
			case "Default":
				cur.def = val
			case "Per-session overrides":
				cur.overrides = val
			}
			continue
		}
		if m := reSubBullet.FindStringSubmatch(l); m != nil {
			switch lastLabel {
			case "Type":
				cur.typeValues = append(cur.typeValues, m[1])
			case "Default":
				cur.def += " " + m[1]
			}
			continue
		}
		// Blank lines keep a sub-bullet list open; anything else that is not
		// deeper-indented list content closes it.
		if trimmed != "" && !strings.HasPrefix(l, "    ") {
			lastLabel = ""
		}
	}
	return sections, order, nil
}

func fenceMarker(trimmed string) string {
	for _, c := range []string{"`", "~"} {
		n := len(trimmed) - len(strings.TrimLeft(trimmed, c))
		if n >= 3 {
			return strings.Repeat(c, n)
		}
	}
	return ""
}

// compareKeys fails when index and section key sets differ.
func compareKeys(entries []entry, order []string) error {
	inIndex := map[string]bool{}
	for _, e := range entries {
		inIndex[e.Key] = true
	}
	inSections := map[string]bool{}
	for _, k := range order {
		inSections[k] = true
	}
	var onlyIndex, onlySections []string
	for _, e := range entries {
		if !inSections[e.Key] {
			onlyIndex = append(onlyIndex, e.Key)
		}
	}
	for _, k := range order {
		if !inIndex[k] {
			onlySections = append(onlySections, k)
		}
	}
	if len(onlyIndex) > 0 || len(onlySections) > 0 {
		return fmt.Errorf("index/section mismatch: index only %v, sections only %v", onlyIndex, onlySections)
	}
	return nil
}

// splitScope returns the scope label and the plain text that follows it.
func splitScope(raw string) (label, note string) {
	m := reScopeLabel.FindStringSubmatchIndex(raw)
	if m == nil {
		return "", plain(raw)
	}
	if m[2] >= 0 {
		label = raw[m[2]:m[3]]
	} else {
		label = raw[m[4]:m[5]]
	}
	note = strings.TrimLeft(raw[m[1]:], " .,;:")
	return label, plain(note)
}

// overrideTokens collects backticked flags and env vars, deduped in order.
func overrideTokens(raw string) (flags, env []string) {
	seen := map[string]bool{}
	for _, m := range reCode.FindAllStringSubmatch(raw, -1) {
		tok := m[1]
		if f := reFlag.FindStringSubmatch(tok); f != nil {
			if !seen[f[1]] {
				seen[f[1]] = true
				flags = append(flags, f[1])
			}
		} else if e := reEnv.FindStringSubmatch(tok); e != nil {
			if !seen[e[1]] {
				seen[e[1]] = true
				env = append(env, e[1])
			}
		}
	}
	return flags, env
}

func mergeKind(typ string) string {
	t := strings.ToLower(typ)
	switch {
	case strings.HasPrefix(t, "array"):
		return "list"
	case strings.HasPrefix(t, "object mapping"), strings.HasPrefix(t, "object keyed by"):
		return "map"
	case strings.HasPrefix(t, "object with"):
		return "undocumented"
	default:
		return "replace"
	}
}

var unescaper = strings.NewReplacer(`\<`, "<", `\>`, ">", `\|`, "|", `\_`, "_", `\*`, "*")

// plain strips markdown links, emphasis, and code markers and collapses
// whitespace.
func plain(s string) string {
	for {
		next := reLink.ReplaceAllString(s, "$1")
		if next == s {
			break
		}
		s = next
	}
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "`", "")
	s = unescaper.Replace(s)
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// splitRow splits a markdown table row on unescaped pipes.
func splitRow(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	if strings.HasSuffix(row, "|") && !strings.HasSuffix(row, `\|`) {
		row = row[:len(row)-1]
	}
	var cells []string
	var b strings.Builder
	for i := 0; i < len(row); i++ {
		if row[i] == '\\' && i+1 < len(row) && row[i+1] == '|' {
			b.WriteString(`\|`)
			i++
			continue
		}
		if row[i] == '|' {
			cells = append(cells, strings.TrimSpace(b.String()))
			b.Reset()
			continue
		}
		b.WriteByte(row[i])
	}
	return append(cells, strings.TrimSpace(b.String()))
}

func splitLines(src []byte) []string {
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, strings.TrimRight(sc.Text(), "\r"))
	}
	return lines
}
