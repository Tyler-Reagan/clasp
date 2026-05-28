# clasp

A read-only terminal UI companion for [Claude Code](https://claude.ai/code) — think k9s, but for your AI coding session. Runs as a separate process (tmux pane, split terminal, etc.) and watches `~/.claude` state files in real time.

```
████ █    ████ ████ ████
█    █    █  █ █    █  █
█    █    ████  ███ ████
████ ████ █  █ ████ █                         Sessions  Skills  Plugins  MCP  Memory
────────────────────────────────────────────────────────────────────────────────────
╭──────────────────────────────╮╭──────────────────────────────────────────────────╮
│ ▌● 41667  ~/Projects         ││ Session                                          │
│  ● 69607  ~/Projects/clasp   ││                                                  │
│                              ││ PID:           41667                             │
│                              ││ Session ID:    4bdf515e-70e7-4149-a535-abb788…   │
│                              ││ CWD:           ~/Projects                        │
│                              ││ Status:        idle                              │
│                              ││ Version:       2.1.152                           │
╰──────────────────────────────╯╰──────────────────────────────────────────────────╯
 ↑↓/jk list · gg/G top/end · hl/tab switch · ^d/^u scroll · enter zoom · ? help · q quit
```

The wordmark and accents render in **copper** (`#b87333`); body text is **parchment** (`#d4c5a0`), borders are **steel** (`#5c5c5c`). Warm dark theme, no cyan or sky blue in sight.

## Why

Claude Code's slash commands are powerful but disruptive mid-conversation. clasp surfaces skills, plugins, and session state in a dedicated pane so you can browse and inspect without injecting anything into your active session. It never touches the Claude Code process — it only reads files.

## Features

- **Sessions** — live view of active Claude Code sessions (PID, CWD, status, version)
- **Skills** — browse all installed skills with description, source path, and SKILL.md preview
- **Plugins** — installed plugins with enabled/disabled status, install metadata, and bundled contents (skills, MCP servers, commands, agents, hooks)
- **MCP** — MCP servers contributed by plugins plus standalone servers from `settings.json` / `~/.claude.json`
- **Memory** — all memory entries across projects, grouped by project, with type, description, and full body
- **Zoom mode** — press `Enter` on any list row to expand the detail pane full-width for in-depth reading; `Esc` returns. Plugin detail re-flows into 2 or 3 columns at wide terminals so heavy plugins (vercel, etc.) stay readable without scrolling.
- **Branded visual identity** — pixel-art "clasp" wordmark inspired by the Claude Code brand; custom warm palette (copper accent, parchment text, steel chrome) instead of generic Catppuccin defaults
- **Context-sensitive help** — `?` opens an overlay listing the keybindings that fire in the current mode (browse vs zoom)
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

### Browse mode (default)

| Key | Action |
|---|---|
| `j` / `k` or `↑` / `↓` | Navigate list |
| `gg` / `G` | Jump to top / end of list (vim-style two-press for top) |
| `Tab` / `Shift+Tab` or `h` / `l` | Switch tabs (also reloads state) |
| `Ctrl+D` / `Ctrl+U` | Scroll detail pane half-page down/up |
| `Ctrl+F` / `Ctrl+B` | Scroll detail pane full-page down/up |
| `Enter` | Zoom into the highlighted item (full-width detail) |
| `space` | Toggle plugin on/off (Plugins tab only) |
| `r` | Force refresh |
| `?` | Help overlay |
| `q` / `Ctrl+C` | Quit |

### Zoom mode (after `Enter` on a list item)

| Key | Action |
|---|---|
| `j` / `k` or `↑` / `↓` | Scroll detail line-by-line |
| `gg` / `G` | Jump to top / bottom of detail |
| `Ctrl+D` / `Ctrl+U` / `Ctrl+F` / `Ctrl+B` | Half- / full-page scroll |
| `Tab` / `Shift+Tab` or `h` / `l` | Exit zoom AND switch tabs (lands in browse mode) |
| `Esc` | Return to browse mode on the current tab |
| `?` | Help overlay |
| `q` / `Ctrl+C` | Quit |

The active tab is wrapped in brackets (e.g. `[Plugins]`) while zoomed, so you keep the visual anchor that tabs are still reachable.

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
    │   ├── model.go             # Bubble Tea root model, focus dispatch, layout
    │   ├── keys.go              # keyMap (bubbles/key bindings) + contextKeys help wrapper
    │   ├── vim.go               # gg two-press chord state machine
    │   ├── columns.go           # plugin-detail multi-column breakpoints
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

**Milestone 3 — vim-style navigation and zoom** ✓
- [x] Vim-style `gg`/`G`, `Ctrl+D`/`Ctrl+U`/`Ctrl+F`/`Ctrl+B` scroll
- [x] Zoom mode (`Enter`) for full-width detail reading
- [x] Context-sensitive `?` help overlay
- [x] Adaptive multi-column plugin detail at wide terminals

**Milestone 4 — visual identity** ✓
- [x] Custom warm palette (copper accent, parchment text, steel chrome)
- [x] Pixel-art "clasp" wordmark inspired by the Claude Code brand
- [x] Branded loading / empty / error states

**Later** — polish opportunities tracked in [open issues](https://github.com/Tyler-Reagan/clasp/issues):
- Wordmark offset-shadow effect, scroll-percentage indicator, clipboard yank, small-terminal degradation, parse-warning surfacing, `CLAUDE.md` for the repo, terminal screenshot, glamour render caching
- Session history / command log viewer
- Token and cost metrics from session JSONL

## Built with

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — Elm-architecture TUI framework
- [Bubbles](https://github.com/charmbracelet/bubbles) — scrollable viewport component
- [Lipgloss](https://github.com/charmbracelet/lipgloss) — layout and styling
- [fsnotify](https://github.com/fsnotify/fsnotify) — cross-platform filesystem events
