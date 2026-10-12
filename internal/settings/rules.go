package settings

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const noteUntrusted = "Project rules apply after you trust this folder."

// trustKeys are keys whose project and local entries wait for folder trust.
var trustKeys = map[string]bool{"permissions.allow": true, "extraKnownMarketplaces": true}

// checkRules marks rule and directory entries that name missing paths, and
// project entries that are inactive because the folder is not trusted.
func checkRules(s *Snapshot, env Env) {
	untrusted := s.Project != "" && s.Trust != nil && !*s.Trust
	for i := range s.Items {
		it := &s.Items[i]
		switch it.Key {
		case "permissions.allow", "permissions.ask", "permissions.deny":
			for j := range it.Parts {
				p := &it.Parts[j]
				rule, _ := p.Value.(string)
				if path, missing, ok := checkRule(rule, p.Layer.Kind, env, s.Project); ok && missing {
					p.Missing = path
					it.Warnings++
				}
			}
		case "permissions.additionalDirectories":
			for j := range it.Parts {
				p := &it.Parts[j]
				dir, _ := p.Value.(string)
				if path := resolveDir(dir, env, s.Project); path != "" && !exists(path) {
					p.Missing = path
					it.Warnings++
				}
			}
		}

		if !untrusted || !trustKeys[it.Key] {
			continue
		}
		inactive := false
		for j := range it.Parts {
			p := &it.Parts[j]
			if k := p.Layer.Kind; k == LayerProject || k == LayerLocal {
				p.Inactive, inactive = true, true
			}
		}
		if inactive {
			it.Warnings++
			it.Notes = append(it.Notes, noteUntrusted)
		}
	}
}

// checkRule finds the path a Read or Edit rule names and whether it exists.
// ok is false for rules that are not checked: other tools, "!" specs, and
// anchors clasp cannot place (relative paths, "/" in managed files).
func checkRule(rule string, kind LayerKind, env Env, project string) (path string, missing, ok bool) {
	open := strings.IndexByte(rule, '(')
	end := strings.LastIndexByte(rule, ')')
	if open < 0 || end < open {
		return "", false, false
	}
	if tool := strings.TrimSpace(rule[:open]); tool != "Read" && tool != "Edit" {
		return "", false, false
	}
	spec := rule[open+1 : end]
	if spec == "" || strings.HasPrefix(spec, "!") {
		return "", false, false
	}

	var abs string
	switch {
	case strings.HasPrefix(spec, "//"):
		abs = spec[1:]
	case spec == "~" || strings.HasPrefix(spec, "~/"):
		abs = env.Home + spec[1:]
	case strings.HasPrefix(spec, "/"):
		switch kind {
		case LayerProject, LayerLocal:
			if project == "" {
				return "", false, false
			}
			abs = project + spec
		case LayerUser:
			abs = env.ClaudeDir + spec
		default:
			return "", false, false
		}
	default:
		return "", false, false
	}

	path = filepath.Clean(literalPrefix(abs))
	return path, !exists(path), true
}

// literalPrefix is the part of a path pattern before any glob character. If
// the pattern has a glob, the result is cut back to the enclosing directory.
func literalPrefix(pattern string) string {
	i := strings.IndexAny(pattern, "*?[{")
	if i < 0 {
		return pattern
	}
	dir := pattern[:i]
	if j := strings.LastIndexByte(dir, '/'); j >= 0 {
		dir = dir[:j]
	}
	if dir == "" {
		return "/"
	}
	return dir
}

// resolveDir places an additionalDirectories entry: "~" is home, relative
// paths are under the project. It returns "" when it cannot be placed.
func resolveDir(dir string, env Env, project string) string {
	switch {
	case dir == "":
		return ""
	case dir == "~" || strings.HasPrefix(dir, "~/"):
		return filepath.Join(env.Home, dir[1:])
	case filepath.IsAbs(dir):
		return filepath.Clean(dir)
	case project != "":
		return filepath.Join(project, dir)
	}
	return ""
}

// exists treats a permission error as present: the path is there, clasp just
// cannot see into it.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || errors.Is(err, fs.ErrPermission)
}
