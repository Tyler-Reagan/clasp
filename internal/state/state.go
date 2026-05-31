package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Session struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Status    string `json:"status"`
	Version   string `json:"version"`
	StartedAt int64  `json:"startedAt"`
}

type Skill struct {
	Name        string
	Description string    // SKILL.md frontmatter description:
	SourcePath  string    // actual path on disk — resolved target if symlink, directory path otherwise
	LineCount   int       // total lines in SKILL.md
	ModTime     time.Time // SKILL.md mtime
	BodyPreview string    // first paragraph of SKILL.md body
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
	ID          string
	Installs    []PluginInstall
	Enabled     bool
	// from .claude-plugin/plugin.json
	DisplayName string
	Description string
	Author      string
	// contents — all sourced from the install path
	MCPServers  []MCPServer
	SkillNames  []string
	HookEvents  []string // e.g. ["SessionStart", "Stop"]
	Commands    []string // names from commands/*.md
	Agents      []string // names from agents/*.md
	HasClaudeMD bool
}

func (p Plugin) Version() string {
	if len(p.Installs) > 0 {
		return p.Installs[0].Version
	}
	return ""
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
		loadSessions, loadSkills, loadPlugins, loadStandaloneMCPServers, loadProjectMCPServers, loadMemory,
	} {
		if err := fn(dir, s); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return s, firstErr
}

func loadSessions(dir string, s *State) error {
	pattern := filepath.Join(dir, "sessions", "*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob %s: %w", pattern, err)
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
	skillsDir := filepath.Join(dir, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", skillsDir, err)
	}
	for _, e := range entries {
		name := e.Name()
		skillPath := filepath.Join(dir, "skills", name)
		// os.Stat follows symlinks; DirEntry.IsDir() does not.
		fi, err := os.Stat(skillPath)
		if err != nil || !fi.IsDir() {
			continue
		}
		s.Skills = append(s.Skills, loadSkill(name, skillPath))
	}
	return nil
}

func loadSkill(name, skillPath string) Skill {
	sk := Skill{Name: name}

	// Resolve the actual location on disk — target if symlink, directory itself otherwise.
	if resolved, err := filepath.EvalSymlinks(skillPath); err == nil {
		sk.SourcePath = resolved
	} else {
		sk.SourcePath = skillPath
	}

	skillMD := filepath.Join(sk.SourcePath, "SKILL.md")
	data, err := os.ReadFile(skillMD)
	if err != nil {
		return sk
	}

	fi, err := os.Stat(skillMD)
	if err == nil {
		sk.ModTime = fi.ModTime()
	}
	sk.LineCount = strings.Count(string(data), "\n") + 1

	sk.Name, sk.Description, sk.BodyPreview = parseSkillMD(string(data))
	if sk.Name == "" {
		sk.Name = name
	}
	return sk
}

// parseSkillMD parses a SKILL.md file into its frontmatter fields and a body preview.
// Handles both inline description values and YAML block scalars (> and |).
func parseSkillMD(content string) (name, description, bodyPreview string) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		bodyPreview = firstParagraph(content)
		return
	}
	// Strip the opening ---
	rest := strings.TrimPrefix(content[3:], "\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		bodyPreview = firstParagraph(content)
		return
	}
	frontmatter := rest[:end]
	body := strings.TrimSpace(rest[end+4:])
	bodyPreview = firstParagraph(body)

	lines := strings.Split(frontmatter, "\n")
	for i := 0; i < len(lines); {
		k, v, ok := strings.Cut(lines[i], ":")
		if !ok {
			i++
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if v == ">" || v == "|" {
			// YAML block scalar: collect indented lines that follow.
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
		switch k {
		case "name":
			name = v
		case "description":
			description = v
		}
	}
	return
}

// firstParagraph returns the first non-empty paragraph (up to the first blank line),
// stripping leading markdown heading markers. Appends … if truncated at the line cap.
func firstParagraph(body string) string {
	const cap = 5
	var lines []string
	hitCap := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimLeft(line, "#")
		line = strings.TrimSpace(line)
		if line == "" {
			if len(lines) > 0 {
				break
			}
			continue
		}
		lines = append(lines, line)
		if len(lines) == cap {
			hitCap = true
			break
		}
	}
	out := strings.Join(lines, " ")
	if hitCap {
		out += "…"
	}
	return out
}

func loadPlugins(dir string, s *State) error {
	manifestPath := filepath.Join(dir, "plugins", "installed_plugins.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", manifestPath, err)
	}
	var raw struct {
		Plugins map[string][]PluginInstall `json:"plugins"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", manifestPath, err)
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
			ip := installs[0].InstallPath
			readPluginMeta(ip, &p)
			p.MCPServers = readPluginMCPServers(ip, id)
			p.SkillNames = readPluginSkillNames(ip)
			p.HookEvents = readPluginHookEvents(ip)
			p.Commands = readPluginMDNames(ip, "commands")
			p.Agents = readPluginMDNames(ip, "agents")
			p.HasClaudeMD = fileExists(filepath.Join(ip, "CLAUDE.md"))
			s.MCPServers = append(s.MCPServers, p.MCPServers...)
		}
		s.Plugins = append(s.Plugins, p)
	}
	return nil
}

func readPluginMeta(installPath string, p *Plugin) {
	data, err := os.ReadFile(filepath.Join(installPath, ".claude-plugin", "plugin.json"))
	if err != nil {
		return
	}
	var raw struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Author      struct {
			Name string `json:"name"`
		} `json:"author"`
	}
	if json.Unmarshal(data, &raw) == nil {
		p.DisplayName = raw.Name
		p.Description = raw.Description
		p.Author = raw.Author.Name
	}
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

func readPluginHookEvents(installPath string) []string {
	data, err := os.ReadFile(filepath.Join(installPath, "hooks", "hooks.json"))
	if err != nil {
		return nil
	}
	var raw struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	// hooks.json may use the hooks key or be a flat object
	if err := json.Unmarshal(data, &raw); err != nil || raw.Hooks == nil {
		var flat map[string]json.RawMessage
		if json.Unmarshal(data, &flat) != nil {
			return nil
		}
		raw.Hooks = flat
	}
	events := make([]string, 0, len(raw.Hooks))
	for event := range raw.Hooks {
		events = append(events, event)
	}
	sort.Strings(events)
	return events
}

// readPluginMDNames lists non-template .md files in a plugin subdirectory,
// returning their base names without extension. Files prefixed with _ are skipped
// (convention: _conventions.md and similar are internal, not user-facing).
func readPluginMDNames(installPath, subdir string) []string {
	entries, err := os.ReadDir(filepath.Join(installPath, subdir))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "_") || strings.HasSuffix(n, ".tmpl") || e.IsDir() {
			continue
		}
		if strings.HasSuffix(n, ".md") {
			names = append(names, strings.TrimSuffix(n, ".md"))
		}
	}
	sort.Strings(names)
	return names
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// loadStandaloneMCPServers appends MCP servers declared outside of any plugin —
// the top-level mcpServers object in settings files, account-level MCPs from
// ~/.claude.json, and servers pending auth in mcp-needs-auth-cache.json.
// Plugin-provided names take precedence: a standalone entry with the same name is skipped.
// Must run after loadPlugins so pluginProvided is populated.
func loadStandaloneMCPServers(dir string, s *State) error {
	pluginProvided := make(map[string]bool, len(s.MCPServers))
	for _, srv := range s.MCPServers {
		pluginProvided[srv.Name] = true
	}

	home, _ := os.UserHomeDir()
	paths := []string{
		filepath.Join(dir, "settings.json"),
		filepath.Join(dir, "settings.local.json"),
		filepath.Join(home, ".claude.json"),
	}

	seen := make(map[string]bool)
	var standalone []MCPServer
	for _, path := range paths {
		servers, err := readStandaloneMCPFile(path)
		if err != nil {
			return err
		}
		for _, srv := range servers {
			if seen[srv.Name] || pluginProvided[srv.Name] {
				continue
			}
			seen[srv.Name] = true
			standalone = append(standalone, srv)
		}
	}

	// Account-level MCPs connected via claude.ai (stored in ~/.claude.json).
	for _, srv := range readClaudeAiMCPServers(filepath.Join(home, ".claude.json")) {
		if !seen[srv.Name] && !pluginProvided[srv.Name] {
			seen[srv.Name] = true
			standalone = append(standalone, srv)
		}
	}

	// MCPs that are configured but awaiting OAuth (mcp-needs-auth-cache.json).
	for _, srv := range readMCPNeedsAuthServers(filepath.Join(dir, "mcp-needs-auth-cache.json")) {
		if !seen[srv.Name] && !pluginProvided[srv.Name] {
			seen[srv.Name] = true
			standalone = append(standalone, srv)
		}
	}

	sort.Slice(standalone, func(i, j int) bool { return standalone[i].Name < standalone[j].Name })
	s.MCPServers = append(s.MCPServers, standalone...)
	return nil
}

func readStandaloneMCPFile(path string) ([]MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw struct {
		MCPServers map[string]struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	servers := make([]MCPServer, 0, len(raw.MCPServers))
	for name, cfg := range raw.MCPServers {
		servers = append(servers, MCPServer{Name: name, Type: cfg.Type, URL: cfg.URL})
	}
	return servers, nil
}

// readClaudeAiMCPServers reads the claudeAiMcpEverConnected list from ~/.claude.json,
// which records MCPs connected through the user's claude.ai account.
func readClaudeAiMCPServers(path string) []MCPServer {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw struct {
		Connected []string `json:"claudeAiMcpEverConnected"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	servers := make([]MCPServer, 0, len(raw.Connected))
	for _, name := range raw.Connected {
		servers = append(servers, MCPServer{Name: name, Type: "claude.ai"})
	}
	return servers
}

// readMCPNeedsAuthServers reads mcp-needs-auth-cache.json and returns any
// account-level servers (those prefixed "claude.ai ") that require OAuth.
func readMCPNeedsAuthServers(path string) []MCPServer {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	var servers []MCPServer
	for name := range raw {
		if strings.HasPrefix(name, "claude.ai ") {
			servers = append(servers, MCPServer{Name: name, Type: "claude.ai"})
		}
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	return servers
}

// loadProjectMCPServers scans each known project directory for MCP servers that
// are scoped to that project rather than declared globally:
//   - .claude/launch.json present → Claude Code auto-provisions a "Claude Preview" MCP
//   - .mcp.json → mcpServers block
//   - .claude/settings.json and .claude/settings.local.json → mcpServers block
//
// Project paths are derived by converting the encoded directory name back to a
// filesystem path (hyphens → slashes). Paths that don't resolve to a real
// directory are silently skipped.
func loadProjectMCPServers(dir string, s *State) error {
	existing := make(map[string]bool, len(s.MCPServers))
	for _, srv := range s.MCPServers {
		existing[srv.Name] = true
	}

	projectsDir := filepath.Join(dir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil // projects dir missing is not an error
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Decode encoded project path: "-Users-foo-bar" → "/Users/foo/bar"
		projectPath := strings.ReplaceAll(entry.Name(), "-", "/")
		if !fileExists(projectPath) {
			continue
		}
		projectName := filepath.Base(projectPath)

		// .claude/launch.json → Claude Code auto-provisions Claude Preview MCP
		if fileExists(filepath.Join(projectPath, ".claude", "launch.json")) {
			name := "Claude Preview (" + projectName + ")"
			if !existing[name] {
				existing[name] = true
				s.MCPServers = append(s.MCPServers, MCPServer{
					Name:   name,
					Type:   "built-in",
					Plugin: projectName,
				})
			}
		}

		// .mcp.json and .claude/settings{,.local}.json
		for _, cfgPath := range []string{
			filepath.Join(projectPath, ".mcp.json"),
			filepath.Join(projectPath, ".claude", "settings.json"),
			filepath.Join(projectPath, ".claude", "settings.local.json"),
		} {
			servers, err := readStandaloneMCPFile(cfgPath)
			if err != nil {
				continue
			}
			for _, srv := range servers {
				if existing[srv.Name] {
					continue
				}
				existing[srv.Name] = true
				srv.Plugin = projectName
				s.MCPServers = append(s.MCPServers, srv)
			}
		}
	}
	return nil
}

func loadMemory(dir string, s *State) error {
	projectsDir := filepath.Join(dir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", projectsDir, err)
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
