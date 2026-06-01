package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	out, err := toggleEnabledPlugin(data, id)
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

// toggleEnabledPlugin is the pure JSON manipulation behind TogglePlugin.
// Given the current settings.json bytes (or nil/empty), flip the enabled state
// of id and return the new bytes. Unknown top-level fields are preserved verbatim
// thanks to the json.RawMessage round-trip.
func toggleEnabledPlugin(data []byte, id string) ([]byte, error) {
	raw := map[string]json.RawMessage{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
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
		return nil, err
	}
	raw["enabledPlugins"] = json.RawMessage(epBytes)

	return json.MarshalIndent(raw, "", "    ")
}

// DeleteMemory moves a single memory entry's .md file to the macOS Trash and
// prunes its line from the project's MEMORY.md index. The file is recoverable
// from the Trash in Finder.
//
// MEMORY.md itself is never a valid target — it is the index, not a memory.
// Index pruning is reported as an error if it fails, but the file is already
// trashed by then: a stale index is a recoverable inconsistency, lost data is not.
func DeleteMemory(project, file string) error {
	if file == "MEMORY.md" {
		return fmt.Errorf("MEMORY.md is the memory index, not an entry")
	}
	memDir := filepath.Join(ClaudeDir(), "projects", project, "memory")
	if err := moveToTrash(filepath.Join(memDir, file)); err != nil {
		return err
	}
	return pruneMemoryIndex(filepath.Join(memDir, "MEMORY.md"), file)
}

// moveToTrash relocates path into the macOS Trash (~/.Trash), where it remains
// recoverable via Finder. Collisions are resolved by appending " 2", " 3", …
// to the stem, mirroring Finder's own de-duplication. Files placed here this way
// appear in the Trash UI; "Put Back" won't know the original location, so recovery
// is by dragging out rather than Put Back.
func moveToTrash(path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	trash := filepath.Join(home, ".Trash")
	if fi, err := os.Stat(trash); err != nil || !fi.IsDir() {
		return fmt.Errorf("macOS Trash not found at %s", trash)
	}
	return os.Rename(path, uniqueTrashName(trash, filepath.Base(path)))
}

// uniqueTrashName returns a path inside trashDir that does not yet exist,
// starting from base and appending " 2", " 3", … to the stem on collision.
func uniqueTrashName(trashDir, base string) string {
	dest := filepath.Join(trashDir, base)
	if !fileExists(dest) {
		return dest
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; ; i++ {
		candidate := filepath.Join(trashDir, fmt.Sprintf("%s %d%s", stem, i, ext))
		if !fileExists(candidate) {
			return candidate
		}
	}
}

// pruneMemoryIndex rewrites MEMORY.md with any line referencing filename removed.
// A missing index is not an error (nothing to prune). The write is atomic
// (temp file + rename) to match TogglePlugin's settings.json handling.
func pruneMemoryIndex(indexPath, filename string) error {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	out, changed := removeIndexLine(string(data), filename)
	if !changed {
		return nil
	}
	tmp := indexPath + ".clasp.tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, indexPath)
}

// removeIndexLine drops every line of content that links to filename — i.e. that
// contains the markdown link target "](filename)". The MEMORY.md convention is
// one pointer line per memory (`- [Title](file.md) — hook`), so this removes the
// dangling pointer without disturbing the rest of the index. Pure, for testing.
func removeIndexLine(content, filename string) (string, bool) {
	marker := "](" + filename + ")"
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	changed := false
	for _, l := range lines {
		if strings.Contains(l, marker) {
			changed = true
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n"), changed
}
