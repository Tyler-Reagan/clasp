package inventory

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// copyEntry is one Copy waiting to be grouped onto an Item.
type copyEntry struct {
	kind Kind
	name string
	cp   Copy
}

func (c *collector) addCopy(kind Kind, name string, cp Copy) {
	c.copies = append(c.copies, copyEntry{kind, name, cp})
}

// desktopRunning probes the Desktop app at most once per Load.
func (c *collector) desktopRunning() (running, known bool) {
	if !c.desktop.done {
		c.desktop.done = true
		c.desktop.running, c.desktop.known = desktopRunning()
	}
	return c.desktop.running, c.desktop.known
}

// addPluginCopy records the plugin dir as a Copy of plugin name. cp carries
// Status, Side, Origin, Version and OrphanedAt; the rest is filled in here. A
// stale or leftover copy also becomes a copy of each skill and MCP server it
// contains (an active one already shows up as Installs). Those copies keep the
// plugin copy's Path, so the path is always the unit that would be moved.
func (c *collector) addPluginCopy(dir, name string, cp Copy) {
	cp.Path, cp.Plugin = dir, name
	cp.Size, cp.ModTime = measure(dir)
	cp.LivePIDs = LiveUsers(dir)
	c.classify(&cp)
	c.addCopy(KindPlugin, name, cp)
	if cp.Status == CopyActive {
		return
	}

	mds, err := findSkillFiles(filepath.Join(dir, "skills"), 0)
	c.err(err)
	sep := string(filepath.Separator)
	sort.SliceStable(mds, func(i, j int) bool { return strings.Count(mds[i], sep) < strings.Count(mds[j], sep) })
	seen := map[string]bool{}
	for _, md := range mds {
		skill := filepath.Base(filepath.Dir(md))
		if !seen[skill] {
			seen[skill] = true
			c.addCopy(KindSkill, skill, cp)
		}
	}
	servers, err := readPluginMCP(filepath.Join(dir, ".mcp.json"))
	c.err(err)
	for _, server := range sortedKeys(servers) {
		c.addCopy(KindMCP, server, cp)
	}
}

// classify sets Why and Trashable. Only a leftover that Claude Code never
// sweeps (synced, rpm) can be trashable, and never while a live session uses
// it or, for rpm, unless Claude Desktop is known not to be running.
func (c *collector) classify(cp *Copy) {
	switch cp.Status {
	case CopyStale:
		if d := cp.DeleteAfter(); !d.IsZero() {
			cp.Why = "Claude Code deletes it after " + d.Local().Format("2006-01-02")
		} else {
			cp.Why = "not yet marked by Claude Code"
		}
		if len(cp.LivePIDs) > 0 {
			cp.Why += "; " + InUseBy(cp.LivePIDs)
		}
	case CopyLeftover:
		switch {
		case cp.Origin == OriginData:
			cp.Why = "kept plugin data (claude plugin uninstall --keep-data)"
		case len(cp.LivePIDs) > 0:
			cp.Why = InUseBy(cp.LivePIDs)
		case cp.Origin == OriginRPM:
			running, known := c.desktopRunning()
			switch {
			case !known:
				cp.Why = "can't tell whether Claude Desktop is running"
			case running:
				cp.Why = "quit Claude Desktop first"
			default:
				cp.Trashable, cp.Why = true, "safe to move to the Trash"
			}
		case cp.Origin == OriginSynced:
			cp.Trashable, cp.Why = true, "safe to move to the Trash"
		}
	}
}

// InUseBy is the reason given for a copy live sessions are using.
func InUseBy(pids []int) string {
	parts := make([]string, len(pids))
	for i, p := range pids {
		parts[i] = strconv.Itoa(p)
	}
	if len(parts) == 1 {
		return "in use by PID " + parts[0]
	}
	return "in use by PIDs " + strings.Join(parts, ", ")
}

// measure sums the regular files under dir and reads dir's mtime. It never
// follows symlinks and does not hash anything.
func measure(dir string) (size int64, mod time.Time) {
	if fi, err := os.Lstat(dir); err == nil {
		mod = fi.ModTime()
	}
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				size += fi.Size()
			}
		}
		return nil
	})
	return size, mod
}

// realSubdirs lists dir's subdirectories without following symlinks, skipping
// hidden entries.
func realSubdirs(dir string) ([]string, error) {
	ents, err := readDir(dir)
	var names []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names, err
}

// installedPlugins reads installed_plugins.json: every install path of every
// id (not only the first), and the ids themselves. ok is false when the file
// cannot be parsed (loadMarketplace reports that); a missing file is ok.
func (s cliSource) installedPlugins() (paths map[string]bool, ids map[string]bool, ok bool) {
	var raw struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if _, err := readJSON(filepath.Join(s.env.ClaudeDir, "plugins", "installed_plugins.json"), &raw); err != nil {
		return nil, nil, false
	}
	paths, ids = map[string]bool{}, map[string]bool{}
	for id, installs := range raw.Plugins {
		ids[id] = true
		for _, in := range installs {
			if in.InstallPath != "" {
				paths[filepath.Clean(in.InstallPath)] = true
			}
		}
	}
	return paths, ids, true
}

// loadCache lists every plugins/cache/<marketplace>/<plugin>/<version>/ dir.
// One that installed_plugins.json points at is active; any other is stale and
// is left to Claude Code, whose sweep marks it with .orphaned_at and deletes
// it after orphanGrace.
func (s cliSource) loadCache(c *collector, installed map[string]bool) {
	root := filepath.Join(s.env.ClaudeDir, "plugins", "cache")
	mkts, err := realSubdirs(root)
	c.err(err)
	for _, mkt := range mkts {
		plugins, err := realSubdirs(filepath.Join(root, mkt))
		c.err(err)
		for _, pl := range plugins {
			versions, err := realSubdirs(filepath.Join(root, mkt, pl))
			c.err(err)
			for _, ver := range versions {
				dir := filepath.Join(root, mkt, pl, ver)
				meta, err := readPluginMeta(dir)
				c.err(err)
				name := meta.Name
				if name == "" {
					name = pl
				}
				cp := Copy{Status: CopyStale, Side: SideCLI, Origin: OriginMarketplace, Version: ver}
				if installed[filepath.Clean(dir)] {
					cp.Status = CopyActive
				} else if fi, err := os.Lstat(filepath.Join(dir, ".orphaned_at")); err == nil && fi.Mode().IsRegular() {
					cp.OrphanedAt = fi.ModTime()
				}
				c.addPluginCopy(dir, name, cp)
			}
		}
	}
}

// loadData lists plugins/data/<plugin>-<marketplace>/. A dir that no installed
// "plugin@marketplace" maps to is kept data from an uninstall, shown as a
// leftover that is never trashable. -inline dirs cannot be matched to an
// install and are ignored.
func (s cliSource) loadData(c *collector, ids map[string]bool) {
	want := map[string]bool{}
	for id := range ids {
		if name, mkt, found := strings.Cut(id, "@"); found {
			want[name+"-"+mkt] = true
		}
	}
	var mkts map[string]json.RawMessage
	_, err := readJSON(filepath.Join(s.env.ClaudeDir, "plugins", "known_marketplaces.json"), &mkts)
	c.err(err)

	root := filepath.Join(s.env.ClaudeDir, "plugins", "data")
	dirs, err := realSubdirs(root)
	c.err(err)
	for _, d := range dirs {
		if strings.HasSuffix(d, "-inline") || want[d] {
			continue
		}
		name := d
		for mkt := range mkts {
			// The longest marketplace suffix wins.
			if base := strings.TrimSuffix(d, "-"+mkt); base != d && base != "" && len(d)-len(base) > len(d)-len(name) {
				name = base
			}
		}
		path := filepath.Join(root, d)
		cp := Copy{Status: CopyLeftover, Side: SideCLI, Origin: OriginData, Plugin: name, Path: path}
		cp.Size, cp.ModTime = measure(path)
		cp.LivePIDs = LiveUsers(path)
		c.classify(&cp)
		c.addCopy(KindPlugin, name, cp)
	}
}
