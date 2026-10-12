package inventory

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
// or enabled on one side only.
func (it Item) Mismatched() bool {
	return (it.OnlyCLI() || it.OnlyDSK()) && !it.Account() ||
		it.Differs() || it.Twice(SideCLI) || it.Twice(SideDSK) || it.EnabledSplit()
}

// Inventory is the comparison of the CLI and Desktop stores.
type Inventory struct {
	Skills, Plugins, MCP []Item
	HasDesktop           bool // a Desktop support dir exists
	Errs                 []error
}
