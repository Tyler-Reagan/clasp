package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// entry is one install waiting to be grouped into an Item.
type entry struct {
	kind Kind
	name string
	in   Install
}

// collector gathers installs and per-source problems from the sources.
type collector struct {
	entries []entry
	errs    []error
}

func (c *collector) add(kind Kind, name string, in Install) {
	c.entries = append(c.entries, entry{kind, name, in})
}

func (c *collector) err(err error) {
	if err != nil {
		c.errs = append(c.errs, err)
	}
}

// readJSON decodes path into v. A missing file reports found=false and no error.
func readJSON(path string, v any) (found bool, err error) {
	data, err := readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return true, fmt.Errorf("parse %s: %w", path, err)
	}
	return true, nil
}

// mcpConfig is the part of an MCP server entry the inventory compares.
type mcpConfig struct {
	Type    string   `json:"type"`
	URL     string   `json:"url"`
	Command string   `json:"command"`
	Args    []string `json:"args"` // hashed, never shown: args can hold secrets
}

// mcpHash hashes what makes two servers the same: type plus url, or plus
// command and args for servers that have no url.
func mcpHash(cfg mcpConfig) string {
	target := mcpTarget(cfg)
	if target == "" {
		return ""
	}
	if cfg.URL == "" && len(cfg.Args) > 0 {
		// Most stdio servers share a launcher (npx, uvx), so args decide identity.
		target = cfg.Command + "\x00" + strings.Join(cfg.Args, "\x00")
	}
	return sha([]byte(cfg.Type + "\n" + target))
}

// mcpTarget is the server's url, or its command when it has no url.
func mcpTarget(cfg mcpConfig) string {
	if cfg.URL != "" {
		return cfg.URL
	}
	if cfg.Command != "" && len(cfg.Args) > 0 {
		return fmt.Sprintf("%s (+%d args)", cfg.Command, len(cfg.Args))
	}
	return cfg.Command
}

// pluginMeta is what the inventory reads from .claude-plugin/plugin.json.
type pluginMeta struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

func readPluginMeta(dir string) (pluginMeta, error) {
	var m pluginMeta
	_, err := readJSON(filepath.Join(dir, ".claude-plugin", "plugin.json"), &m)
	return m, err
}

// pluginDir is one plugin directory to turn into installs.
type pluginDir struct {
	side       Side
	origin     string
	id         string // installed_plugins.json key, marketplace installs only
	account    string
	dir        string
	name       string
	version    string
	desc       string
	enabled    bool
	skillState func(skill string) (enabled, listed bool) // per-skill override; nil means follow the plugin
}

// addPlugin adds the plugin itself, every skill under skills/ and every
// server in .mcp.json.
func (c *collector) addPlugin(p pluginDir) {
	sum, err := treeHash(p.dir)
	c.err(err)
	c.add(KindPlugin, p.name, Install{
		Side: p.side, Origin: p.origin, Plugin: p.name, PluginID: p.id, Account: p.account,
		Path: p.dir, Version: p.version, Enabled: p.enabled, Hash: sum, Desc: p.desc,
	})

	mds, err := findSkillFiles(filepath.Join(p.dir, "skills"), 0)
	c.err(err)
	// A plugin has one skill per name: vendored copies nested deeper (such as
	// skills/x/upstream/SKILL.md) must not read as the plugin loaded twice.
	sep := string(filepath.Separator)
	sort.SliceStable(mds, func(i, j int) bool { return strings.Count(mds[i], sep) < strings.Count(mds[j], sep) })
	seen := map[string]bool{}
	for _, md := range mds {
		name := filepath.Base(filepath.Dir(md))
		if seen[name] {
			continue
		}
		seen[name] = true
		enabled := p.enabled
		if p.skillState != nil {
			if on, listed := p.skillState(name); listed {
				enabled = on
			}
		}
		c.addSkill(p, md, enabled)
	}

	c.addPluginMCP(p)
}

func (c *collector) addSkill(p pluginDir, md string, enabled bool) {
	sum, desc, err := fileHash(md)
	c.err(err)
	dir := filepath.Dir(md)
	c.add(KindSkill, filepath.Base(dir), Install{
		Side: p.side, Origin: p.origin, Plugin: p.name, PluginID: p.id, Account: p.account,
		Path: dir, Version: p.version, Enabled: enabled, Hash: sum, Desc: desc,
	})
}

// addPluginMCP adds the servers a plugin declares in .mcp.json, either under
// an mcpServers key or as a flat name -> config object.
func (c *collector) addPluginMCP(p pluginDir) {
	path := filepath.Join(p.dir, ".mcp.json")
	var raw map[string]json.RawMessage
	found, err := readJSON(path, &raw)
	if err != nil {
		c.err(err)
		return
	}
	if !found {
		return
	}
	servers := map[string]mcpConfig{}
	if msg, ok := raw["mcpServers"]; ok {
		if err := json.Unmarshal(msg, &servers); err != nil {
			c.err(fmt.Errorf("parse %s: %w", path, err))
			return
		}
	} else {
		for name, msg := range raw {
			var cfg mcpConfig
			if json.Unmarshal(msg, &cfg) == nil && (cfg.URL != "" || cfg.Command != "") {
				servers[name] = cfg
			}
		}
	}
	for _, name := range sortedKeys(servers) {
		cfg := servers[name]
		c.add(KindMCP, name, Install{
			Side: p.side, Origin: p.origin, Plugin: p.name, PluginID: p.id, Account: p.account,
			Path: path, Version: p.version, Enabled: p.enabled, Hash: mcpHash(cfg), Type: cfg.Type, Target: mcpTarget(cfg),
		})
	}
}

// maxSkillDepth bounds the walk under a plugin's skills/ dir; it also ends
// symlink loops.
const maxSkillDepth = 8

// findSkillFiles returns every SKILL.md below dir, nested at any depth, in
// path order. Symlinked dirs are followed. A SKILL.md directly in the root is
// not a skill: skills are named after their parent dir.
func findSkillFiles(dir string, depth int) ([]string, error) {
	ents, err := readDir(dir)
	var out []string
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		p := filepath.Join(dir, name)
		fi, serr := os.Stat(p)
		switch {
		case serr != nil:
			continue
		case fi.IsDir():
			if depth < maxSkillDepth {
				sub, _ := findSkillFiles(p, depth+1)
				out = append(out, sub...)
			}
		case name == "SKILL.md" && depth > 0:
			out = append(out, p)
		}
	}
	return out, err
}

// skillDescription returns the description: value from SKILL.md frontmatter,
// including YAML block scalars (> and |).
func skillDescription(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return ""
	}
	rest := strings.TrimPrefix(content[3:], "\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return ""
	}
	lines := strings.Split(rest[:end], "\n")
	for i := 0; i < len(lines); {
		k, v, ok := strings.Cut(lines[i], ":")
		if !ok {
			i++
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if v == ">" || v == "|" {
			folded := v == ">"
			var sb strings.Builder
			i++
			for i < len(lines) {
				l := lines[i]
				if !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
					break
				}
				if sb.Len() > 0 {
					if folded {
						sb.WriteRune(' ')
					} else {
						sb.WriteRune('\n')
					}
				}
				sb.WriteString(strings.TrimSpace(l))
				i++
			}
			v = sb.String()
		} else {
			i++
		}
		if k == "description" {
			return v
		}
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
