package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// cliSource reads the CLI store: files under ~/.claude plus ~/.claude.json.
type cliSource struct{ env Env }

// mcpFile is the part of settings.json, settings.local.json and
// ~/.claude.json that declares standalone MCP servers.
type mcpFile struct {
	MCPServers map[string]mcpConfig `json:"mcpServers"`
}

func (s cliSource) load(c *collector) {
	// settings.json feeds both enabledPlugins and standalone MCP, so read it once.
	var settings struct {
		mcpFile
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	settingsPath := filepath.Join(s.env.ClaudeDir, "settings.json")
	_, err := readJSON(settingsPath, &settings)
	c.err(err)

	var local mcpFile
	localPath := filepath.Join(s.env.ClaudeDir, "settings.local.json")
	_, err = readJSON(localPath, &local)
	c.err(err)

	// ~/.claude.json is large and holds oauthAccount; decoding into a struct
	// with just these two fields drops everything else.
	var global struct {
		mcpFile
		Connected []string `json:"claudeAiMcpEverConnected"`
	}
	globalPath := filepath.Join(s.env.Home, ".claude.json")
	_, err = readJSON(globalPath, &global)
	c.err(err)

	s.loadPersonal(c)
	s.loadMarketplace(c, settings.EnabledPlugins)
	s.loadSynced(c, settings.EnabledPlugins)
	if paths, ids, ok := s.installedPlugins(); ok {
		s.loadCache(c, paths)
		s.loadData(c, ids)
	}
	loadStandaloneMCP(c, []mcpSource{
		{settingsPath, settings.MCPServers},
		{localPath, local.MCPServers},
		{globalPath, global.MCPServers},
	})
	s.loadConnectors(c, globalPath, global.Connected)
	s.loadProjectMCP(c)
}

// loadProjectMCP adds servers scoped to a known project: .mcp.json and the
// project's .claude/settings{,.local}.json, plus the Claude Preview server
// Claude Code provides when .claude/launch.json exists. Projects come from
// ~/.claude/projects/<encoded path>; folders that no longer exist are skipped.
func (s cliSource) loadProjectMCP(c *collector) {
	encoded, err := subdirs(filepath.Join(s.env.ClaudeDir, "projects"))
	c.err(err)
	for _, enc := range encoded {
		proj := strings.ReplaceAll(enc, "-", "/")
		if !isDir(proj) {
			continue
		}
		if launch := filepath.Join(proj, ".claude", "launch.json"); fileExists(launch) {
			c.add(KindMCP, "Claude Preview ("+filepath.Base(proj)+")", Install{
				Side: SideCLI, Origin: OriginProject, Path: launch, Enabled: true, Type: "built-in",
			})
		}
		for _, path := range []string{
			filepath.Join(proj, ".mcp.json"),
			filepath.Join(proj, ".claude", "settings.json"),
			filepath.Join(proj, ".claude", "settings.local.json"),
		} {
			var f mcpFile
			if _, err := readJSON(path, &f); err != nil {
				c.err(err)
				continue
			}
			for _, name := range sortedKeys(f.MCPServers) {
				cfg := f.MCPServers[name]
				c.add(KindMCP, name, Install{
					Side: SideCLI, Origin: OriginProject, Path: path, Enabled: true,
					Hash: mcpHash(cfg), Type: cfg.Type, Target: mcpTarget(cfg),
				})
			}
		}
	}
}

// loadPersonal reads ~/.claude/skills/*/SKILL.md, following symlinked dirs.
func (s cliSource) loadPersonal(c *collector) {
	skillsDir := filepath.Join(s.env.ClaudeDir, "skills")
	names, err := subdirs(skillsDir)
	c.err(err)
	for _, name := range names {
		dir := filepath.Join(skillsDir, name)
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		md := filepath.Join(dir, "SKILL.md")
		if _, err := os.Stat(md); err != nil {
			continue
		}
		sum, desc, err := fileHash(md)
		c.err(err)
		c.add(KindSkill, name, Install{
			Side: SideCLI, Origin: OriginPersonal, Path: dir, Enabled: true, Hash: sum, Desc: desc,
		})
	}
}

// loadMarketplace reads the plugins listed in installed_plugins.json. Like the
// Plugins tab, it follows each id's first install.
func (s cliSource) loadMarketplace(c *collector, enabled map[string]bool) {
	var raw struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
			Version     string `json:"version"`
		} `json:"plugins"`
	}
	_, err := readJSON(filepath.Join(s.env.ClaudeDir, "plugins", "installed_plugins.json"), &raw)
	if err != nil {
		c.err(err)
		return
	}
	for _, id := range sortedKeys(raw.Plugins) {
		installs := raw.Plugins[id]
		if len(installs) == 0 || installs[0].InstallPath == "" {
			continue
		}
		first := installs[0]
		meta, err := readPluginMeta(first.InstallPath)
		c.err(err)
		name := meta.Name
		if name == "" {
			name, _, _ = strings.Cut(id, "@")
		}
		version := first.Version
		if version == "" {
			version = meta.Version
		}
		c.addPlugin(pluginDir{
			side: SideCLI, origin: OriginMarketplace, id: id, dir: first.InstallPath,
			name: name, version: version, desc: meta.Description, enabled: enabled[id],
		})
	}
}

// gSuffix matches the ~g2 style generation suffix on synced plugin dirs.
var gSuffix = regexp.MustCompile(`~g(\d+)$`)

// splitGeneration splits a synced dir name into its plugin name and
// generation; a bare name is generation 1.
func splitGeneration(dir string) (name string, gen int) {
	m := gSuffix.FindStringSubmatch(dir)
	if m == nil {
		return dir, 1
	}
	gen, _ = strconv.Atoi(m[1])
	return strings.TrimSuffix(dir, m[0]), gen
}

// syncedGenerations reads <acct>/manifest.json: the generation of each synced
// plugin by name (absent means 1). known is false when the manifest is
// missing or unreadable, and then no dir may be called a leftover: never
// guess. A manifest that parses with no plugins is known: every account
// plugin was removed, so every dir is a leftover.
func syncedGenerations(c *collector, acctDir string) (gens map[string]int, known bool) {
	var m struct {
		Plugins []struct {
			Name       string `json:"name"`
			Generation *int   `json:"generation"`
		} `json:"plugins"`
	}
	found, err := readJSON(filepath.Join(acctDir, "manifest.json"), &m)
	c.err(err)
	if !found || err != nil {
		return nil, false
	}
	gens = map[string]int{}
	for _, p := range m.Plugins {
		gens[p.Name] = 1
		if p.Generation != nil {
			gens[p.Name] = *p.Generation
		}
	}
	return gens, true
}

// loadSynced reads ~/.claude/plugins/synced/<org>_<account>/<plugin>[~gN]/,
// the account plugins Claude syncs down from claude.ai. Files beside the
// plugin dirs (manifest.json, *.meta.json) are not plugins. A dir from an
// older generation, or one the manifest no longer lists, is a leftover: it is
// recorded as a Copy and not loaded as an Install.
func (s cliSource) loadSynced(c *collector, enabled map[string]bool) {
	root := filepath.Join(s.env.ClaudeDir, "plugins", "synced")
	accounts, err := subdirs(root)
	c.err(err)
	for _, acct := range accounts {
		label := shortID(acct)
		if org, account, ok := strings.Cut(acct, "_"); ok {
			label = accountLabel(org, account)
		}
		gens, known := syncedGenerations(c, filepath.Join(root, acct))
		plugins, err := subdirs(filepath.Join(root, acct))
		c.err(err)
		for _, dir := range plugins {
			path := filepath.Join(root, acct, dir)
			meta, err := readPluginMeta(path)
			c.err(err)
			base, gen := splitGeneration(dir)
			name := meta.Name
			if name == "" {
				name = base
			}
			cp := Copy{Status: CopyActive, Side: SideCLI, Origin: OriginSynced, Version: meta.Version}
			if g, listed := gens[base]; known && (!listed || g != gen) {
				cp.Status = CopyLeftover
				c.addPluginCopy(path, name, cp)
				continue
			}
			c.addPluginCopy(path, name, cp)
			// Synced plugins load unless settings.json switches them off.
			on, set := enabled[name+"@synced"]
			c.addPlugin(pluginDir{
				side: SideCLI, origin: OriginSynced, account: label, dir: path,
				name: name, version: meta.Version, desc: meta.Description, enabled: !set || on,
			})
		}
	}
}

// mcpSource is the mcpServers object of one settings file.
type mcpSource struct {
	path    string
	servers map[string]mcpConfig
}

// loadStandaloneMCP adds servers declared outside any plugin. The first file
// to declare a name wins.
func loadStandaloneMCP(c *collector, files []mcpSource) {
	seen := map[string]bool{}
	for _, f := range files {
		for _, name := range sortedKeys(f.servers) {
			if seen[name] {
				continue
			}
			seen[name] = true
			c.add(KindMCP, name, Install{
				Side: SideCLI, Origin: OriginSettings, Path: f.path, Enabled: true, Hash: mcpHash(f.servers[name]), Type: f.servers[name].Type, Target: mcpTarget(f.servers[name]),
			})
		}
	}
}

// loadConnectors adds the claude.ai connectors: the claudeAiMcpEverConnected
// list from ~/.claude.json and the "claude.ai "-prefixed keys of
// mcp-needs-auth-cache.json. They live in the account, so they carry no hash.
func (s cliSource) loadConnectors(c *collector, globalPath string, connected []string) {
	seen := map[string]bool{}
	add := func(path, name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		c.add(KindMCP, name, Install{Side: SideCLI, Origin: OriginAccount, Path: path, Enabled: true})
	}
	for _, name := range connected {
		add(globalPath, name)
	}

	var needsAuth map[string]json.RawMessage
	authPath := filepath.Join(s.env.ClaudeDir, "mcp-needs-auth-cache.json")
	_, err := readJSON(authPath, &needsAuth)
	c.err(err)
	for _, name := range sortedKeys(needsAuth) {
		if strings.HasPrefix(name, "claude.ai ") {
			add(authPath, name)
		}
	}
}
