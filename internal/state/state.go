package state

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	Version     string `json:"version"`
	InstalledAt string `json:"installedAt"`
}

type Plugin struct {
	ID       string
	Installs []PluginInstall
	Enabled  bool
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
	Sessions []Session
	Skills   []Skill
	Plugins  []Plugin
	Memory   []MemoryEntry
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
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		data, err := os.ReadFile(filepath.Join(dir, "skills", name, "meta.json"))
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

	for id, installs := range raw.Plugins {
		s.Plugins = append(s.Plugins, Plugin{
			ID:       id,
			Installs: installs,
			Enabled:  enabled[id],
		})
	}
	return nil
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
			entry.Type = strings.Fields(v)[0]
		}
	}
	return entry
}
