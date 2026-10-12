package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Env holds the locations clasp reads settings from. Tests point it at temp
// dirs; DefaultEnv fills it for the current machine.
type Env struct {
	Home       string   // ~/.claude.json lives here
	ClaudeDir  string   // ~/.claude, holds the user settings.json
	ManagedDir string   // managed-settings.json and managed-settings.d/; "" if none on this OS
	MDMPaths   []string // MDM plists, only stat'ed
}

// DefaultEnv returns the settings locations for the current user and OS.
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	env := Env{Home: home, ClaudeDir: filepath.Join(home, ".claude")}
	switch runtime.GOOS {
	case "darwin":
		env.ManagedDir = "/Library/Application Support/ClaudeCode"
		const plist = "com.anthropic.claudecode.plist"
		env.MDMPaths = []string{filepath.Join("/Library/Managed Preferences", plist)}
		if name := username(); name != "" {
			env.MDMPaths = append(env.MDMPaths, filepath.Join("/Library/Managed Preferences", name, plist))
		}
	case "linux":
		env.ManagedDir = "/etc/claude-code"
	}
	return env
}

func username() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

// LayerKind is the kind of file a layer was read from.
type LayerKind int

const (
	LayerUser LayerKind = iota
	LayerProject
	LayerLocal
	LayerManaged
	LayerGlobal // ~/.claude.json
)

// Label is the short lowercase name the UI shows for the kind.
func (k LayerKind) Label() string {
	switch k {
	case LayerUser:
		return "user"
	case LayerProject:
		return "project"
	case LayerLocal:
		return "local"
	case LayerManaged:
		return "managed"
	case LayerGlobal:
		return "global"
	}
	return "unknown"
}

// Layer is one settings file. A missing file is Present=false; a file that
// does not parse has Err set and contributes nothing.
type Layer struct {
	Kind    LayerKind
	Path    string
	Present bool
	Err     error
	Note    string         // e.g. "same file as user"
	Values  map[string]any // flattened dotted key → value
}

// arrayMembers are catalog parents whose "children" are strings inside an
// array, not JSON paths, so flatten never descends into them.
var arrayMembers = map[string]bool{"strictPluginOnlyCustomization": true}

// readLayers reads every settings layer, lowest precedence first: user,
// project, local, then each managed file in the order later-wins. The global
// layer is read separately by readGlobal.
func readLayers(cat *Catalog, env Env, project string) []Layer {
	layers := []Layer{readSettingsFile(cat, LayerUser, filepath.Join(env.ClaudeDir, "settings.json"))}

	if project != "" {
		dir := filepath.Join(project, ".claude")
		path := filepath.Join(dir, "settings.json")
		if sameDir(dir, env.ClaudeDir) {
			// Project is $HOME: its settings.json is the user file.
			layers = append(layers, Layer{Kind: LayerProject, Path: path, Note: "same file as user"})
		} else {
			layers = append(layers, readSettingsFile(cat, LayerProject, path))
		}
		layers = append(layers, readSettingsFile(cat, LayerLocal, filepath.Join(dir, "settings.local.json")))
	}

	for _, path := range managedFiles(env.ManagedDir) {
		layers = append(layers, readSettingsFile(cat, LayerManaged, path))
	}
	return layers
}

// managedFiles lists managed-settings.json (even if missing, so the UI can say
// so) and then managed-settings.d/*.json in alphabetical order. Hidden files
// and non-JSON files in the drop-in dir are skipped.
func managedFiles(dir string) []string {
	if dir == "" {
		return nil
	}
	files := []string{filepath.Join(dir, "managed-settings.json")}
	dropIn := filepath.Join(dir, "managed-settings.d")
	entries, err := os.ReadDir(dropIn)
	if err != nil {
		return files
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || filepath.Ext(name) != ".json" || e.IsDir() {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		files = append(files, filepath.Join(dropIn, name))
	}
	return files
}

// sameDir reports whether a and b name the same directory, following symlinks.
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

func readSettingsFile(cat *Catalog, kind LayerKind, path string) Layer {
	l := Layer{Kind: kind, Path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l
	}
	l.Present = true
	if err != nil {
		l.Err = err
		return l
	}
	obj, err := decodeObject(data)
	if err != nil {
		l.Err = fmt.Errorf("parse %s: %w", path, err)
		return l
	}
	l.Values = map[string]any{}
	flatten(cat, "", obj, l.Values)
	return l
}

// decodeObject parses a JSON object, keeping numbers as json.Number so they
// print as written. An empty file reads as an empty object.
func decodeObject(data []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the top-level object")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("top level is not an object")
	}
	return obj, nil
}

// flatten copies obj's members into out under dotted catalog keys. An object
// whose key has catalog children is walked; members that match no catalog
// key stay together on the nearest documented parent. Top-level members not
// in the catalog are kept as undocumented keys. It returns the members of obj
// that found no home below prefix.
func flatten(cat *Catalog, prefix string, obj map[string]any, out map[string]any) map[string]any {
	var rest map[string]any
	for name, v := range obj {
		if prefix == "" && name == "$schema" {
			continue
		}
		full := prefix + name
		if sub, ok := v.(map[string]any); ok && cat.HasChildren(full) && !arrayMembers[full] {
			left := flatten(cat, full+".", sub, out)
			if len(left) == 0 {
				continue
			}
			v = left
		}
		if _, documented := cat.Lookup(full); documented || prefix == "" {
			out[full] = v
			continue
		}
		if rest == nil {
			rest = map[string]any{}
		}
		rest[name] = v
	}
	return rest
}

// global is what clasp keeps from ~/.claude.json.
type global struct {
	layer Layer
	known []string // project paths that exist as directories, sorted
	trust *bool    // selected project's hasTrustDialogAccepted; nil if unknown
}

// readGlobal reads ~/.claude.json. The file holds account and OAuth data, so
// only the documented Global config keys and each project's trust flag are
// decoded; every other member is dropped unparsed.
func readGlobal(cat *Catalog, env Env, project string) global {
	g := global{layer: Layer{Kind: LayerGlobal, Path: filepath.Join(env.Home, ".claude.json")}}
	data, err := os.ReadFile(g.layer.Path)
	if errors.Is(err, os.ErrNotExist) {
		return g
	}
	g.layer.Present = true
	if err != nil {
		g.layer.Err = err
		return g
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		g.layer.Err = fmt.Errorf("parse %s: %w", g.layer.Path, err)
		return g
	}

	g.layer.Values = map[string]any{}
	for _, d := range cat.Docs() {
		msg, ok := raw[d.Key]
		if d.Scope != ScopeGlobal || !ok {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(msg))
		dec.UseNumber()
		var v any
		if dec.Decode(&v) == nil {
			g.layer.Values[d.Key] = v
		}
	}

	var projects map[string]struct {
		Trusted *bool `json:"hasTrustDialogAccepted"`
	}
	if msg, ok := raw["projects"]; ok && json.Unmarshal(msg, &projects) == nil {
		for path, p := range projects {
			if fi, err := os.Stat(path); err == nil && fi.IsDir() {
				g.known = append(g.known, path)
			}
			if project != "" && filepath.Clean(path) == filepath.Clean(project) {
				g.trust = p.Trusted
			}
		}
		sort.Strings(g.known)
	}
	return g
}
