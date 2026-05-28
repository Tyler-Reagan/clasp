package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Session struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Status    string `json:"status"`
	Version   string `json:"version"`
	StartedAt int64  `json:"startedAt"`
}

type SkillMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

type Skill struct {
	SkillMeta
}

type PluginInstall struct {
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"`
	Version     string `json:"version"`
	InstalledAt string `json:"installedAt"`
	LastUpdated string `json:"lastUpdated"`
}

type MCPServer struct {
	Name   string
	Type   string
	URL    string
	Plugin string // plugin ID that provides this server, empty if standalone
}

type Plugin struct {
	ID         string
	Installs   []PluginInstall
	Enabled    bool
	MCPServers []MCPServer
	SkillNames []string
}

func (p Plugin) Version() string {
	if len(p.Installs) > 0 {
		return p.Installs[0].Version
	}
	return "unknown"
}

func (p Plugin) InstalledAt() string {
	if len(p.Installs) > 0 {
		return p.Installs[0].InstalledAt
	}
	return ""
}

func (p Plugin) Scope() string {
	if len(p.Installs) > 0 {
		return p.Installs[0].Scope
	}
	return ""
}

type MemoryEntry struct {
	Project string
	File    string
	Name    string
	Type    string
	Desc    string
	Body    string
}

type State struct {
	Sessions   []Session
	Skills     []Skill
	Plugins    []Plugin
	MCPServers []MCPServer
	Memory     []MemoryEntry
}

func ClaudeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func Load() (*State, error) {
	dir := ClaudeDir()
	s := &State{}
	var firstErr error

	for _, fn := range []func(string, *State) error{
		loadSessions, loadSkills, loadPlugins, loadMemory,
	} {
		if err := fn(dir, s); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return s, firstErr
}

func loadSessions(dir string, s *State) error {
	files, err := filepath.Glob(filepath.Join(dir, "sessions", "*.json"))
	if err != nil {
		return err
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var sess Session
		if json.Unmarshal(data, &sess) == nil {
			s.Sessions = append(s.Sessions, sess)
		}
	}
	return nil
}

func loadSkills(dir string, s *State) error {
	entries, err := os.ReadDir(filepath.Join(dir, "skills"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		skillPath := filepath.Join(dir, "skills", name)
		// Use os.Stat to follow symlinks — IsDir() on a DirEntry returns false for symlinked dirs.
		fi, err := os.Stat(skillPath)
		if err != nil || !fi.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(skillPath, "meta.json"))
		if err != nil {
			s.Skills = append(s.Skills, Skill{SkillMeta{Name: name}})
			continue
		}
		var meta SkillMeta
		if json.Unmarshal(data, &meta) != nil {
			meta.Name = name
		}
		s.Skills = append(s.Skills, Skill{meta})
	}
	return nil
}

func loadPlugins(dir string, s *State) error {
	data, err := os.ReadFile(filepath.Join(dir, "plugins", "installed_plugins.json"))
	if err != nil {
		return err
	}
	var raw struct {
		Plugins map[string][]PluginInstall `json:"plugins"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	enabled := map[string]bool{}
	if settingsData, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		var settings struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if json.Unmarshal(settingsData, &settings) == nil {
			enabled = settings.EnabledPlugins
		}
	}

	// sort for stable ordering
	ids := make([]string, 0, len(raw.Plugins))
	for id := range raw.Plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		installs := raw.Plugins[id]
		p := Plugin{
			ID:       id,
			Installs: installs,
			Enabled:  enabled[id],
		}
		if len(installs) > 0 && installs[0].InstallPath != "" {
			p.MCPServers = readPluginMCPServers(installs[0].InstallPath, id)
			p.SkillNames = readPluginSkillNames(installs[0].InstallPath)
			s.MCPServers = append(s.MCPServers, p.MCPServers...)
		}
		s.Plugins = append(s.Plugins, p)
	}
	return nil
}

func readPluginMCPServers(installPath, pluginID string) []MCPServer {
	data, err := os.ReadFile(filepath.Join(installPath, ".mcp.json"))
	if err != nil {
		return nil
	}
	var raw struct {
		MCPServers map[string]struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	names := make([]string, 0, len(raw.MCPServers))
	for name := range raw.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	servers := make([]MCPServer, 0, len(names))
	for _, name := range names {
		cfg := raw.MCPServers[name]
		servers = append(servers, MCPServer{
			Name:   name,
			Type:   cfg.Type,
			URL:    cfg.URL,
			Plugin: pluginID,
		})
	}
	return servers
}

func readPluginSkillNames(installPath string) []string {
	entries, err := os.ReadDir(filepath.Join(installPath, "skills"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		skillPath := filepath.Join(installPath, "skills", e.Name())
		fi, err := os.Stat(skillPath)
		if err != nil || !fi.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func loadMemory(dir string, s *State) error {
	entries, err := os.ReadDir(filepath.Join(dir, "projects"))
	if err != nil {
		return err
	}
	for _, proj := range entries {
		if !proj.IsDir() {
			continue
		}
		memDir := filepath.Join(dir, "projects", proj.Name(), "memory")
		files, _ := filepath.Glob(filepath.Join(memDir, "*.md"))
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			entry := parseMemoryFile(proj.Name(), filepath.Base(f), string(data))
			s.Memory = append(s.Memory, entry)
		}
	}
	return nil
}

func parseMemoryFile(project, filename, content string) MemoryEntry {
	entry := MemoryEntry{Project: project, File: filename, Body: content}
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return entry
	}
	parts := strings.SplitN(content[3:], "---", 2)
	if len(parts) < 2 {
		return entry
	}
	entry.Body = strings.TrimSpace(parts[1])
	for _, line := range strings.Split(parts[0], "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "name":
			entry.Name = v
		case "description":
			entry.Desc = v
		case "type":
			fields := strings.Fields(v)
			if len(fields) > 0 {
				entry.Type = fields[0]
			}
		}
	}
	return entry
}
