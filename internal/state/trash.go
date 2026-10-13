package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tylerreagan/clasp/internal/inventory"
)

// Seams for TrashLeftovers. Tests replace them; the defaults read the real
// machine and move into the real ~/.Trash.
var (
	// trashEnv is where the inventory and the allowed roots live.
	trashEnv = inventory.DefaultEnv
	// loadInventory reads a fresh inventory. TrashLeftovers never trusts the
	// inventory the UI showed: anything may have changed since.
	loadInventory = inventory.Load
	// trashMove relocates a path into the Trash. It must never delete.
	trashMove = moveToTrash
	// liveUsers lists live sessions holding a plugin copy via .in_use.
	liveUsers = inventory.LiveUsers
	// desktopRunning reports whether the Claude Desktop app is running.
	desktopRunning = inventory.DesktopRunning
)

// TrashLeftovers moves the trashable leftover copies of one item to the
// Trash. It is the only place clasp moves plugin directories, so it reloads
// the inventory and then re-checks every guard for each path immediately
// before its rename; a path that fails any guard is refused, never moved.
//
// moved lists the paths now in the Trash. refused maps each leftover path
// that was not moved to the reason. err is set only when the item cannot be
// found; a failed rename is a refusal and the remaining paths are still tried.
//
// It never deletes anything and never touches plugins/cache (Claude Code
// sweeps stale versions itself) or plugins/data (kept on purpose with
// --keep-data).
func TrashLeftovers(kind inventory.Kind, name string) (moved []string, refused map[string]string, err error) {
	env := trashEnv()
	inv := loadInventory(env)
	if inv == nil {
		return nil, nil, fmt.Errorf("could not read the inventory")
	}
	it := inv.Find(kind, name)
	if it == nil {
		return nil, nil, fmt.Errorf("no %s named %q", kind, name)
	}

	refused = map[string]string{}
	guard := newTrashGuard(env, inv)

	// Several items (a plugin and the skills and servers inside it) share one
	// copy path, and the plugin dir is always the unit moved, so group copies
	// by cleaned path and judge each path once.
	var order []string
	byPath := map[string][]inventory.Copy{}
	for _, c := range it.Copies {
		if c.Status != inventory.CopyLeftover {
			continue // active and stale copies are never candidates
		}
		p := filepath.Clean(c.Path)
		if _, seen := byPath[p]; !seen {
			order = append(order, p)
		}
		byPath[p] = append(byPath[p], c)
	}

	for _, p := range order {
		if why := guard.check(p, byPath[p]); why != "" {
			refused[p] = why
			continue
		}
		if err := trashMove(p); err != nil {
			refused[p] = "move to the Trash failed: " + err.Error()
			continue
		}
		moved = append(moved, p)
	}
	return moved, refused, nil
}

// trashGuard holds what every path is checked against: the allowed roots and
// every path the fresh inventory says is in use.
type trashGuard struct {
	syncedRoot   string // <ClaudeDir>/plugins/synced
	sessionsRoot string // <DesktopDir>/local-agent-mode-sessions; "" without Desktop
	// protected are paths that must not be moved, nor anything containing
	// them: every Install of every item and every active copy.
	protected []string
	// containers are dirs that a moved path must not sit inside: plugin
	// installs and active copies, which are whole plugin dirs.
	containers []string
}

func newTrashGuard(env inventory.Env, inv *inventory.Inventory) *trashGuard {
	g := &trashGuard{syncedRoot: filepath.Join(filepath.Clean(env.ClaudeDir), "plugins", "synced")}
	if env.DesktopDir != "" {
		g.sessionsRoot = filepath.Join(filepath.Clean(env.DesktopDir), "local-agent-mode-sessions")
	}
	for _, items := range [][]inventory.Item{inv.Skills, inv.Plugins, inv.MCP} {
		for _, it := range items {
			for _, in := range it.Installs {
				if in.Path == "" {
					continue
				}
				p := filepath.Clean(in.Path)
				g.protected = append(g.protected, p)
				if it.Kind == inventory.KindPlugin {
					g.containers = append(g.containers, p)
				}
			}
			for _, c := range it.Copies {
				if c.Status == inventory.CopyActive && c.Path != "" {
					p := filepath.Clean(c.Path)
					g.protected = append(g.protected, p)
					g.containers = append(g.containers, p)
				}
			}
		}
	}
	return g
}

// check returns "" when path may be moved now, or the reason it may not.
// copies are the fresh inventory's leftover copies at path. The order puts
// the cheap, inventory-based answers first and the live probes (sessions,
// Desktop) last, as close to the rename as possible.
func (g *trashGuard) check(path string, copies []inventory.Copy) string {
	// (a) The fresh inventory must itself call every copy at this path
	// trashable. Its classification already folds in the origin, live
	// sessions and Desktop; a path the UI showed but the fresh load disagrees
	// on is refused with the fresh reason.
	origin := ""
	for _, c := range copies {
		if !c.Trashable {
			if c.Why != "" {
				return c.Why
			}
			return "not trashable"
		}
		if origin == "" {
			origin = c.Origin
		} else if c.Origin != origin {
			return "copies at this path disagree on where it came from"
		}
	}

	// (b) Only the two kinds of leftover nothing else sweeps. Marketplace
	// cache copies belong to Claude Code's own sweep, and plugins/data holds
	// data the user kept on purpose.
	if origin != inventory.OriginSynced && origin != inventory.OriginRPM {
		return "only synced and Desktop rpm leftovers can be moved"
	}

	// (c) A real directory, not a symlink: renaming a symlink would move
	// the link while the dir it points at stays, and a link swapped in after
	// the inventory was read could point anywhere.
	fi, err := os.Lstat(path)
	if err != nil {
		return "can't read it: " + err.Error()
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "it is a symlink"
	}
	if !fi.IsDir() {
		return "it is not a directory"
	}

	// (d) Exactly one plugin dir at the expected depth under an allowed root,
	// with no symlinked dir between the root and it. This keeps a bad path
	// (an escape with "..", a whole account dir, a dir somewhere else) from
	// ever reaching the rename.
	if why := g.underRoot(path, origin); why != "" {
		return why
	}

	// (e) Not anything the fresh inventory says is loaded: not the path of any
	// install or active copy, not a dir that contains one, and not inside an
	// active plugin dir.
	for _, p := range g.protected {
		if within(p, path) {
			return "it holds an active install (" + p + ")"
		}
	}
	for _, p := range g.containers {
		if within(path, p) {
			return "it is inside an active plugin (" + p + ")"
		}
	}

	// (f) No live session has it in use, probed again right now: a session
	// may have started since the inventory was read.
	if pids := liveUsers(path); len(pids) > 0 {
		return inventory.InUseBy(pids)
	}

	// (g) Desktop rpm dirs only while Claude Desktop is known not to be
	// running, probed again right now: a running app may still read them.
	if origin == inventory.OriginRPM {
		running, known := desktopRunning()
		switch {
		case !known:
			return "can't tell whether Claude Desktop is running"
		case running:
			return "quit Claude Desktop first"
		}
	}
	return ""
}

// underRoot checks guard (d) for path, already known to be a real dir.
func (g *trashGuard) underRoot(path, origin string) string {
	var root string
	var depth int
	switch origin {
	case inventory.OriginSynced:
		root, depth = g.syncedRoot, 2 // <acct>/<dir>
	case inventory.OriginRPM:
		if g.sessionsRoot == "" {
			return "no Claude Desktop dir"
		}
		root, depth = g.sessionsRoot, 4 // <a>/<b>/rpm/plugin_<id>
	default:
		return "no allowed root for origin " + origin
	}

	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil {
		return "it is outside " + root
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != depth {
		return "it is not a plugin dir directly under " + root
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "it is not a plugin dir directly under " + root
		}
	}
	switch origin {
	case inventory.OriginSynced:
		// The account dir must be a real dir, not a link elsewhere.
		fi, err := os.Lstat(filepath.Join(root, parts[0]))
		if err != nil || !fi.Mode().IsDir() {
			return "its account dir is not a real directory"
		}
	case inventory.OriginRPM:
		if parts[0] == "skills-plugin" || strings.HasPrefix(parts[0], "local_") || strings.HasPrefix(parts[1], "local_") ||
			parts[2] != "rpm" || !strings.HasPrefix(parts[3], "plugin_") || parts[3] == "plugin_" {
			return "it is not an rpm plugin dir"
		}
	}

	// No symlinked ancestors between the root and the dir: the resolved parent
	// must be the resolved root plus the same components. Links above the
	// root (a symlinked home, macOS /var) are allowed; links inside it are not.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "can't resolve " + root + ": " + err.Error()
	}
	parent := filepath.Dir(filepath.Clean(path))
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "can't resolve " + parent + ": " + err.Error()
	}
	if realParent != filepath.Join(realRoot, filepath.Join(parts[:depth-1]...)) {
		return "a directory above it is a symlink"
	}
	return ""
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
