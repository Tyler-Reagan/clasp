package inventory

import (
	"os"
	"path/filepath"
)

// desktopSource reads the Desktop store under its support dir. It decodes only
// named files and keys: config.json (OAuth token caches) is never opened and
// the local_* session dirs, vm_bundles, Cache and claude-code* are never
// entered. Session dirs hold per-session copies, not configuration.
type desktopSource struct{ dir string }

func (s desktopSource) load(c *collector) {
	sessions := filepath.Join(s.dir, "local-agent-mode-sessions")
	s.loadSkillsPlugin(c, sessions)
	s.loadRPM(c, sessions)
	s.loadConfig(c)
}

// idPairs lists the <a>/<b> account id dirs under root. Desktop writes the two
// ids in one order for skills-plugin and the other for rpm, so nothing here
// depends on which is which. Dirs named in skip are not account dirs.
func idPairs(root string, skip string) (pairs [][2]string, err error) {
	as, err := subdirs(root, "local_")
	for _, a := range as {
		if a == skip {
			continue
		}
		bs, e := subdirs(filepath.Join(root, a), "local_")
		if e != nil && err == nil {
			err = e
		}
		for _, b := range bs {
			pairs = append(pairs, [2]string{a, b})
		}
	}
	return pairs, err
}

// loadSkillsPlugin reads skills-plugin/<a>/<b>/, Desktop's own plugin of
// Anthropic and user skills. Its manifest.json lists each skill with an
// enabled flag; a skill the manifest does not list counts as enabled.
func (s desktopSource) loadSkillsPlugin(c *collector, sessions string) {
	root := filepath.Join(sessions, "skills-plugin")
	pairs, err := idPairs(root, "")
	c.err(err)
	for _, p := range pairs {
		dir := filepath.Join(root, p[0], p[1])
		meta, err := readPluginMeta(dir)
		c.err(err)
		name := meta.Name
		if name == "" {
			name = OriginAnthropicSkills
		}

		var manifest struct {
			Skills []struct {
				Name    string `json:"name"`
				Enabled *bool  `json:"enabled"`
			} `json:"skills"`
		}
		_, err = readJSON(filepath.Join(dir, "manifest.json"), &manifest)
		c.err(err)
		enabled := map[string]bool{}
		for _, sk := range manifest.Skills {
			enabled[sk.Name] = sk.Enabled == nil || *sk.Enabled
		}

		c.addPlugin(pluginDir{
			side: SideDSK, origin: OriginAnthropicSkills, account: accountLabel(p[0], p[1]), dir: dir,
			name: name, version: meta.Version, desc: meta.Description, enabled: true,
			skillState: func(skill string) (bool, bool) { on, listed := enabled[skill]; return on, listed },
		})
	}
}

// loadRPM reads <a>/<b>/rpm/, the plugins installed from the user's account.
// Each plugin_* dir has an opaque name, so it is identified by the name in its
// .claude-plugin/plugin.json (or, failing that, its manifest.json entry).
func (s desktopSource) loadRPM(c *collector, sessions string) {
	pairs, err := idPairs(sessions, "skills-plugin")
	c.err(err)
	for _, p := range pairs {
		rpm := filepath.Join(sessions, p[0], p[1], "rpm")
		if !isDir(rpm) {
			continue
		}
		account := accountLabel(p[0], p[1])

		var manifest struct {
			Plugins []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Enabled *bool  `json:"enabled"`
			} `json:"plugins"`
		}
		_, err := readJSON(filepath.Join(rpm, "manifest.json"), &manifest)
		c.err(err)
		listed := map[string]int{}
		for i, pl := range manifest.Plugins {
			listed[pl.ID] = i
		}

		dirs, err := subdirs(rpm)
		c.err(err)
		for _, d := range dirs {
			dir := filepath.Join(rpm, d)
			meta, err := readPluginMeta(dir)
			c.err(err)
			name, enabled := meta.Name, true
			if i, ok := listed[d]; ok {
				if name == "" {
					name = manifest.Plugins[i].Name
				}
				enabled = manifest.Plugins[i].Enabled == nil || *manifest.Plugins[i].Enabled
			}
			if name == "" {
				continue
			}
			c.addPlugin(pluginDir{
				side: SideDSK, origin: OriginRPM, account: account, dir: dir,
				name: name, version: meta.Version, desc: meta.Description, enabled: enabled,
			})
		}
	}
}

// loadConfig reads the MCP servers in claude_desktop_config.json. Only the
// mcpServers key is decoded, and within it only type, url and command:
// server env blocks can hold secrets.
func (s desktopSource) loadConfig(c *collector) {
	path := filepath.Join(s.dir, "claude_desktop_config.json")
	var cfg mcpFile
	_, err := readJSON(path, &cfg)
	c.err(err)
	for _, name := range sortedKeys(cfg.MCPServers) {
		c.add(KindMCP, name, Install{
			Side: SideDSK, Origin: OriginDesktopConfig, Path: path, Enabled: true, Hash: mcpHash(cfg.MCPServers[name]), Type: cfg.MCPServers[name].Type, Target: mcpTarget(cfg.MCPServers[name]),
		})
	}
}

// hasDesktop reports whether a Desktop support dir exists.
func hasDesktop(dir string) bool {
	if dir == "" {
		return false
	}
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}
