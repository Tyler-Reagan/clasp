package inventory

import (
	"time"
)

// Kind is the type of thing an Item describes.
type Kind int

const (
	KindSkill Kind = iota
	KindPlugin
	KindMCP
)

func (k Kind) String() string {
	switch k {
	case KindSkill:
		return "skill"
	case KindPlugin:
		return "plugin"
	case KindMCP:
		return "mcp"
	}
	return "unknown"
}

// Side is the store an install was read from.
type Side int

const (
	SideCLI Side = iota // files under ~/.claude (and ~/.claude.json)
	SideDSK             // Claude Desktop's support dir
)

func (s Side) String() string {
	if s == SideDSK {
		return "DSK"
	}
	return "CLI"
}

// Install origins.
const (
	OriginPersonal        = "personal"
	OriginMarketplace     = "marketplace"
	OriginSynced          = "synced"
	OriginAnthropicSkills = "anthropic-skills"
	OriginRPM             = "rpm"
	OriginDesktopConfig   = "desktop-config"
	OriginSettings        = "settings"
	OriginAccount         = "account"
	OriginProject         = "project"
	OriginData            = "data" // plugins/data/<plugin>-<marketplace>, a plugin's kept data
)

// Install is one copy of an Item found in one store.
type Install struct {
	Side     Side
	Origin   string // one of the Origin constants
	Plugin   string // plugin name providing this skill/MCP (a plugin's own name for plugin installs); "" for personal/standalone
	PluginID string // marketplace installs: the installed_plugins.json key, e.g. "vercel@claude-plugins-official"; else ""
	Account  string // short org/account label for synced/Desktop installs, else ""
	Path     string
	Version  string
	Enabled  bool
	Hash     string // hex sha256; "" when not hashable (e.g. connectors)
	Desc     string // skill frontmatter description or plugin.json description, if any
	Type     string // MCP only: transport type
	Target   string // MCP only: url, or command when there is no url
}

// Item is everything found under one bare name: a skill dir name, a plugin
// manifest name or an MCP server name.
type Item struct {
	Kind     Kind
	Name     string
	Installs []Install
	Copies   []Copy // every on-disk copy, active ones included; see Copy
}

// OnSide reports whether the item has any install on side s.
func (it Item) OnSide(s Side) bool {
	for _, in := range it.Installs {
		if in.Side == s {
			return true
		}
	}
	return false
}

func (it Item) OnlyCLI() bool { return it.OnSide(SideCLI) && !it.OnSide(SideDSK) }
func (it Item) OnlyDSK() bool { return it.OnSide(SideDSK) && !it.OnSide(SideCLI) }

// Account reports whether every install is a claude.ai account connector.
func (it Item) Account() bool {
	if len(it.Installs) == 0 {
		return false
	}
	for _, in := range it.Installs {
		if in.Origin != OriginAccount {
			return false
		}
	}
	return true
}

// Differs reports whether the installs have more than one distinct non-empty
// hash. Versions never decide: the same version can carry different content.
func (it Item) Differs() bool {
	first := ""
	for _, in := range it.Installs {
		if in.Hash == "" {
			continue
		}
		if first == "" {
			first = in.Hash
		} else if in.Hash != first {
			return true
		}
	}
	return false
}

// Twice reports whether side s holds two installs from the same plugin (or
// two personal copies). Two different plugins sharing a name is not twice,
// nor is one plugin under two accounts or one server in two projects. An
// install with no account (a marketplace install) matches any account.
func (it Item) Twice(s Side) bool {
	var ins []Install
	for _, in := range it.Installs {
		if in.Side == s {
			ins = append(ins, in)
		}
	}
	for i := range ins {
		for _, b := range ins[i+1:] {
			if sameSource(ins[i], b) {
				return true
			}
		}
	}
	return false
}

func sameSource(a, b Install) bool {
	if a.Origin == OriginProject || b.Origin == OriginProject {
		return a.Origin == b.Origin && a.Path == b.Path
	}
	if a.Account != "" && b.Account != "" && a.Account != b.Account {
		return false
	}
	if a.Plugin != "" || b.Plugin != "" {
		return a.Plugin == b.Plugin
	}
	return a.Origin == b.Origin
}

// EnabledSplit reports whether the item is enabled on one side and every
// install on the other side is disabled.
func (it Item) EnabledSplit() bool {
	var present, enabled [2]bool
	for _, in := range it.Installs {
		present[in.Side] = true
		enabled[in.Side] = enabled[in.Side] || in.Enabled
	}
	return present[SideCLI] && present[SideDSK] && enabled[SideCLI] != enabled[SideDSK]
}

// Mismatched reports whether the item deserves attention: present on only one
// side (unless it is an account connector), differing content, loaded twice,
// enabled on one side only, left behind by an incomplete uninstall, or
// uninstalled with copies still on disk (e.g. a cache version Claude Code has
// not swept yet).
func (it Item) Mismatched() bool {
	return (it.OnlyCLI() || it.OnlyDSK()) && !it.Account() ||
		it.Differs() || it.Twice(SideCLI) || it.Twice(SideDSK) || it.EnabledSplit() ||
		len(it.Leftovers()) > 0 || len(it.Installs) == 0 && len(it.Copies) > 0
}

// Inventory is the comparison of the CLI and Desktop stores.
type Inventory struct {
	Skills, Plugins, MCP []Item
	HasDesktop           bool // a Desktop support dir exists
	Errs                 []error
}

// CopyStatus says how an on-disk copy relates to what the tool loads.
type CopyStatus int

const (
	CopyActive   CopyStatus = iota // the copy the install records point at
	CopyStale                      // an old cache version Claude Code will sweep itself
	CopyLeftover                   // left behind by an uninstall; nothing sweeps it
)

func (s CopyStatus) String() string {
	switch s {
	case CopyActive:
		return "active"
	case CopyStale:
		return "stale"
	case CopyLeftover:
		return "leftover"
	}
	return "unknown"
}

// orphanGrace is how long Claude Code keeps a plugin cache version after it
// marks it with .orphaned_at before deleting it. Claude Code 2.1.296's
// orphaned-version sweep deletes a marked dir once the marker's mtime is more
// than 14 days old, unless a live session uses it.
const orphanGrace = 14 * 24 * time.Hour

// Copy is one on-disk copy of an Item: the active one, an old cache version,
// or something an uninstall left behind. Skill and MCP copies found inside a
// stale or leftover plugin copy carry that plugin copy's Path, so Path is
// always the unit that would be moved.
type Copy struct {
	Status     CopyStatus
	Side       Side
	Origin     string // one of the Origin constants
	Plugin     string // plugin providing the copy, "" when none
	Path       string
	Version    string
	Size       int64     // bytes in regular files; symlinks are not followed
	ModTime    time.Time // the directory's mtime
	OrphanedAt time.Time // mtime of Claude Code's .orphaned_at marker; zero if not marked
	LivePIDs   []int     // live sessions that have this copy in use
	Trashable  bool      // a leftover clasp may move to the Trash
	Why        string    // the human reason for the status or for Trashable
}

// DeleteAfter is when Claude Code may delete a marked cache copy; zero if the
// copy is not marked.
func (c Copy) DeleteAfter() time.Time {
	if c.OrphanedAt.IsZero() {
		return time.Time{}
	}
	return c.OrphanedAt.Add(orphanGrace)
}

// StaleCount is the number of stale copies.
func (it Item) StaleCount() int {
	n := 0
	for _, c := range it.Copies {
		if c.Status == CopyStale {
			n++
		}
	}
	return n
}

// Leftovers returns the copies left behind by an uninstall.
func (it Item) Leftovers() []Copy {
	var out []Copy
	for _, c := range it.Copies {
		if c.Status == CopyLeftover {
			out = append(out, c)
		}
	}
	return out
}

// TrashableLeftovers returns the leftovers clasp may move to the Trash.
func (it Item) TrashableLeftovers() []Copy {
	var out []Copy
	for _, c := range it.Leftovers() {
		if c.Trashable {
			out = append(out, c)
		}
	}
	return out
}

// Find returns the item of the given kind and name, or nil.
func (inv *Inventory) Find(kind Kind, name string) *Item {
	var items []Item
	switch kind {
	case KindSkill:
		items = inv.Skills
	case KindPlugin:
		items = inv.Plugins
	case KindMCP:
		items = inv.MCP
	}
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}
