package inventory

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Env holds the locations the inventory reads. Tests point it at temp dirs;
// DefaultEnv fills it for the current machine.
type Env struct {
	Home       string // ~/.claude.json lives here
	ClaudeDir  string // ~/.claude
	DesktopDir string // Claude Desktop's support dir; "" or missing means no Desktop source
}

// DefaultEnv returns the inventory locations for the current user and OS.
// Windows has no Desktop path yet, so it reads as having no Desktop source.
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	env := Env{Home: home, ClaudeDir: filepath.Join(home, ".claude")}
	switch runtime.GOOS {
	case "darwin":
		env.DesktopDir = filepath.Join(home, "Library", "Application Support", "Claude")
	case "linux":
		env.DesktopDir = filepath.Join(home, ".config", "Claude")
	}
	return env
}

// fsHook, when set, sees every path the package reads or lists. Tests use it
// to prove that credential files and session dirs are never touched.
var fsHook func(path string)

func touch(path string) {
	if fsHook != nil {
		fsHook(path)
	}
}

// readFile is the only way the package reads file content.
func readFile(path string) ([]byte, error) {
	touch(path)
	return os.ReadFile(path)
}

// readDir lists a directory sorted by name. A missing directory is not an
// error: every location the inventory looks in is optional.
func readDir(path string) ([]fs.DirEntry, error) {
	touch(path)
	ents, err := os.ReadDir(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return ents, err
}

// isDir reports whether path is a directory, following symlinks.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// subdirs lists the names of dir's subdirectories (symlinks followed),
// skipping hidden entries and any with a prefix in skipPrefixes.
func subdirs(dir string, skipPrefixes ...string) ([]string, error) {
	ents, err := readDir(dir)
	var names []string
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !isDir(filepath.Join(dir, name)) {
			continue
		}
		skip := false
		for _, p := range skipPrefixes {
			skip = skip || strings.HasPrefix(name, p)
		}
		if !skip {
			names = append(names, name)
		}
	}
	return names, err
}

// accountLabel is the short label for an org/account id pair: the first 8
// characters of each id, sorted so that Desktop's <a>/<b> and <b>/<a> layouts
// and the CLI's <org>_<account> dir all give the same label.
func accountLabel(a, b string) string {
	ids := []string{a, b}
	sort.Strings(ids)
	return shortID(ids[0]) + "/" + shortID(ids[1])
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
