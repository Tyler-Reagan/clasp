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

	inv.Skills, inv.Plugins, inv.MCP = group(c.entries, c.copies)
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
// an item, CLI installs come before Desktop ones. Copies attach to the item of
// the same kind and name, creating an item that has only copies.
func group(entries []entry, copies []copyEntry) (skills, plugins, mcp []Item) {
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

	for _, ce := range copies {
		k := key{ce.kind, ce.name}
		it := items[k]
		if it == nil {
			it = &Item{Kind: ce.kind, Name: ce.name}
			items[k] = it
		}
		it.Copies = append(it.Copies, ce.cp)
	}

	for _, it := range items {
		sort.SliceStable(it.Copies, func(i, j int) bool {
			a, b := it.Copies[i], it.Copies[j]
			if a.Status != b.Status {
				return a.Status < b.Status
			}
			if a.Side != b.Side {
				return a.Side < b.Side
			}
			return a.Path < b.Path
		})
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
