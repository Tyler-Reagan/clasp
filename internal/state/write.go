package state

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// TogglePlugin flips the enabled state of a plugin in ~/.claude/settings.json.
// Convention: present+true = enabled, absent = disabled (mirrors what Claude Code writes).
// All other fields in settings.json are preserved verbatim.
func TogglePlugin(id string) error {
	path := filepath.Join(ClaudeDir(), "settings.json")

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// use raw message map so unknown fields survive the round-trip
	raw := map[string]json.RawMessage{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
	}

	enabled := map[string]bool{}
	if ep, ok := raw["enabledPlugins"]; ok {
		_ = json.Unmarshal(ep, &enabled)
	}

	if enabled[id] {
		delete(enabled, id) // disable: remove key entirely, matching Claude Code's convention
	} else {
		enabled[id] = true
	}

	epBytes, err := json.Marshal(enabled)
	if err != nil {
		return err
	}
	raw["enabledPlugins"] = json.RawMessage(epBytes)

	out, err := json.MarshalIndent(raw, "", "    ")
	if err != nil {
		return err
	}

	// atomic write: temp file + rename avoids partial writes
	tmp := path + ".clasp.tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
