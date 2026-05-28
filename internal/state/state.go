package state

import (
	"encoding/json"
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
