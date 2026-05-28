# clasp

A read-only terminal UI companion for [Claude Code](https://claude.ai/code) — think k9s, but for your AI coding session. Runs as a separate process (tmux pane, split terminal, etc.) and watches `~/.claude` state files in real time.

```
clasp  ────────────────────────────────────── Sessions  Skills  Plugins  MCP  Memory
────────────────────────────────────────────────────────────────────────────────────
╭──────────────────────────────╮╭──────────────────────────────────────────────────╮
│ ● 41667  ~/Projects          ││ Session                                           │
│ ● 69607  ~/Projects/clasp    ││                                                   │
│                              ││ PID:           41667                              │
│                              ││ Session ID:    4bdf515e-70e7-4149-a535-abb788…   │
│                              ││ CWD:           ~/Projects                         │
│                              ││ Status:        idle                               │
│                              ││ Version:       2.1.152                            │
╰──────────────────────────────╯╰──────────────────────────────────────────────────╯
 ↑↓/jk list · g/G top/end · hl/tab switch · PgUp/Dn scroll · r refresh · q quit
```

## Why

Claude Code's slash commands are powerful but disruptive mid-conversation. clasp surfaces skills, plugins, and session state in a dedicated pane so you can browse and inspect without injecting anything into your active session. It never touches the Claude Code process — it only reads files.

## Features

- **Sessions** — live view of active Claude Code sessions (PID, CWD, status, version)
- **Skills** — browse all installed skills with description, source path, and SKILL.md preview
- **Plugins** — installed plugins with enabled/disabled status, install metadata, and bundled contents (skills, MCP servers, commands, agents, hooks)
- **MCP** — MCP servers contributed by plugins plus standalone servers from `settings.json` / `~/.claude.json`
- **Memory** — all memory entries across projects, grouped by project, with type, description, and full body
- **Live updates** — `fsnotify` watches `~/.claude` and refreshes automatically on any change; tab-switch also triggers a fresh load
- **Plugin toggle** — press `space` in the Plugins tab to enable/disable a plugin (the only write path; uses atomic temp-file + rename on `~/.claude/settings.json`)
- **Otherwise zero interference** — never writes to or signals the Claude Code process

## Requirements

- Go 1.24+
- [Claude Code](https://claude.ai/code) installed (provides `~/.claude` state)
- A terminal that supports 256 colors

## Installation

```bash
git clone https://github.com/Tyler-Reagan/clasp
cd clasp
go build -o clasp .
```

Move the binary somewhere on your `$PATH`:

```bash
mv clasp /usr/local/bin/clasp
```

Or install directly with `go install`:

```bash
go install github.com/tylerreagan/clasp@latest
```

## Usage

Start clasp in a separate terminal pane alongside your Claude Code session:

```bash
clasp
```

| Key | Action |
|---|---|
| `j` / `k` or `↑` / `↓` | Navigate list |
| `g` / `G` | Jump to top / end of list |
| `Tab` / `Shift+Tab` or `h` / `l` | Switch tabs (also reloads state) |
| `PgUp` / `PgDn` | Scroll detail pane |
| `space` | Toggle plugin on/off (Plugins tab only) |
| `r` | Force refresh |
| `q` / `Ctrl+C` | Quit |

## How it works

clasp reads state directly from the files Claude Code writes to `~/.claude`:

| Tab | Source |
|---|---|
| Sessions | `~/.claude/sessions/<pid>.json` |
| Skills | `~/.claude/skills/*/SKILL.md` (follows symlinks) |
| Plugins | `~/.claude/plugins/installed_plugins.json` + `settings.json` + each plugin's `.claude-plugin/plugin.json`, `.mcp.json`, `skills/`, `hooks/hooks.json`, `commands/`, `agents/` |
| MCP | each plugin's `.mcp.json` + top-level `mcpServers` in `~/.claude/settings.json` and `~/.claude.json` |
| Memory | `~/.claude/projects/*/memory/*.md` |

A background goroutine uses `fsnotify` to watch these directories and sends a refresh message to the UI on any change.

## Project structure

```
clasp/
├── main.go                      # entry point; starts watcher goroutine
└── internal/
    ├── state/
    │   ├── state.go             # reads and parses ~/.claude state files
    │   └── write.go             # plugin enable/disable writer (atomic rename)
    ├── ui/
    │   ├── model.go             # Bubble Tea root model, layout, key handling
    │   └── styles.go            # lipgloss palette
    └── watcher/watcher.go       # fsnotify → RefreshMsg on file changes
```

## Roadmap

**Milestone 1 — read-only viewport** ✓
- [x] Sessions tab — live PID, CWD, status, version
- [x] Skills tab — installed skills with description, source path, and preview
- [x] Plugins tab — installed plugins with enabled/disabled state and bundled contents
- [x] MCP tab — MCP servers from plugins and from `~/.claude/settings.json` / `~/.claude.json`
- [x] Memory tab — all project memory entries with full body, grouped by project
- [x] fsnotify live refresh on any `~/.claude` change; tab-switch also reloads

**Milestone 2 — first write action** ✓
- [x] Toggle a plugin on/off with `space` in the Plugins tab (modifies `~/.claude/settings.json`)

**Later**
- [ ] Session history / command log viewer
- [ ] Token and cost metrics from session JSONL
- [ ] Dismissable error popup (k9s-style) for surfacing aggregated loader errors

## Built with

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — Elm-architecture TUI framework
- [Bubbles](https://github.com/charmbracelet/bubbles) — scrollable viewport component
- [Lipgloss](https://github.com/charmbracelet/lipgloss) — layout and styling
- [fsnotify](https://github.com/fsnotify/fsnotify) — cross-platform filesystem events
