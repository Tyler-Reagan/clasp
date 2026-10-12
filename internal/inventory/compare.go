package inventory

import (
	"fmt"
	"sort"
)

// source is one store the inventory reads.
type source interface {
	load(c *collector)
}

// Load reads both stores and compares them. It never panics: a problem with
// one file or source goes to Errs and everything else still loads.
func Load(env Env) *Inventory {
	c := &collector{}
	inv := &Inventory{HasDesktop: hasDesktop(env.DesktopDir)}

	sources := []source{cliSource{env}}
	if inv.HasDesktop {
		sources = append(sources, desktopSource{env.DesktopDir})
	}
	for _, s := range sources {
		runSource(s, c)
	}

	inv.Skills, inv.Plugins, inv.MCP = group(c.entries)
	inv.Errs = c.errs
	return inv
}

// runSource loads s, turning a panic into an error.
func runSource(s source, c *collector) {
	defer func() {
		if r := recover(); r != nil {
			c.err(fmt.Errorf("inventory: %T: panic: %v", s, r))
		}
	}()
	s.load(c)
}

// group folds installs into one Item per (kind, name), sorted by name. Within
// an item, CLI installs come before Desktop ones.
func group(entries []entry) (skills, plugins, mcp []Item) {
	type key struct {
		kind Kind
		name string
	}
	items := map[key]*Item{}
	for _, e := range entries {
		k := key{e.kind, e.name}
		it := items[k]
		if it == nil {
			it = &Item{Kind: e.kind, Name: e.name}
			items[k] = it
		}
		it.Installs = append(it.Installs, e.in)
	}

	for _, it := range items {
		sort.SliceStable(it.Installs, func(i, j int) bool { return it.Installs[i].Side < it.Installs[j].Side })
		switch it.Kind {
		case KindSkill:
			skills = append(skills, *it)
		case KindPlugin:
			plugins = append(plugins, *it)
		case KindMCP:
			mcp = append(mcp, *it)
		}
	}
	for _, list := range [][]Item{skills, plugins, mcp} {
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	}
	return skills, plugins, mcp
}
