# clasp

A near read-only terminal UI companion for [Claude Code](https://claude.ai/code) — Runs as a separate process (tmux pane, split terminal, etc.) and watches `~/.claude` state files in real time. It observes; the only writes are two deliberate, opt-in actions (toggle a plugin, delete a memory) that touch `~/.claude` files atomically and never signal the Claude Code process.

```
████ █    ████ ████ ████
█    █    █  █ █    █  █
█    █    ████  ███ ████
████ ████ █  █ ████ █               Sessions  Skills  Plugins  MCP  Memory  Settings
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

Claude Code's slash commands are powerful but disruptive mid-conversation. clasp surfaces skills, plugins, and session state in a dedicated pane so you can browse and inspect without injecting anything into your active session. It never signals or writes to the Claude Code process itself; its two write actions (toggling a plugin, trashing a memory) touch only `~/.claude` config/state files, atomically.

## Features

- **Sessions** — live view of active Claude Code sessions (PID, CWD, status, version)
- **Skills** — browse all installed skills with description, source path, and SKILL.md preview
- **Plugins** — installed plugins with enabled/disabled status, install metadata, and bundled contents (skills, MCP servers, commands, agents, hooks)
- **MCP** — all MCP servers: plugin-bundled servers, standalone entries from `settings.json` / `settings.local.json`, account-level integrations from `~/.claude.json` (`claudeAiMcpEverConnected`), and servers awaiting OAuth from `mcp-needs-auth-cache.json`
- **Memory** — all memory entries across projects, grouped by project, with type, description, and full body
- **Settings** — every Claude Code setting you have set, with a one-sentence description, its effective value, the file that supplied it, and every other file that also sets it (managed, project local, project, user, and the documented keys in `~/.claude.json`). List keys such as `permissions.allow` show each entry with its source, and `Read`/`Edit` rules that point at paths that no longer exist are flagged. Press `a` to browse all documented keys with their defaults, and `p` to pick which project's settings apply (default: the most recent active session)
- **Claude Code and Claude Desktop side by side** — the Skills, Plugins and MCP tabs show a **CLI** and a **DSK** column: `●` enabled, `○` disabled, `·` absent, `◇` claude.ai account connector. Rows flag `≠` when installs with the same name differ in content (SHA-256, so a copy with the same version string but newer files is still caught) and `×2` when one plugin is installed twice on the same side (for example a marketplace install plus its claude.ai-synced copy). Press `s` to filter: all, Claude Code only, Desktop only, mismatched. The detail pane lists every install with its origin, account, path, version and hash. Columns show files on disk; which copy a Desktop session loads is not documented, and clasp says so
- **Zoom mode** — press `Enter` on any list row to expand the detail pane full-width for in-depth reading; `Esc` returns. Plugin detail re-flows into 2 or 3 columns at wide terminals so heavy plugins (vercel, etc.) stay readable without scrolling.
- **Branded visual identity** — pixel-art "clasp" wordmark inspired by the Claude Code brand; custom warm palette (copper accent, parchment text, steel chrome) instead of generic Catppuccin defaults
- **Context-sensitive help** — `?` opens an overlay listing the keybindings that fire in the current mode (browse vs zoom)
- **Live updates** — `fsnotify` watches `~/.claude` and refreshes automatically on any change; tab-switch also triggers a fresh load
- **Plugin toggle** — press `space` in the Plugins tab to enable/disable a plugin (atomic temp-file + rename on `~/.claude/settings.json`)
- **Memory delete** — press `d` in the Memory tab to remove an entry: confirm with `y` and the `.md` file moves to the macOS Trash (recoverable in Finder), with its pointer line pruned from `MEMORY.md` in the same step. `MEMORY.md` itself is the index and is never deletable. The only destructive action clasp offers, and the only one gated behind a confirmation.
- **Otherwise zero interference** — those two `~/.claude` writes aside, clasp never writes to or signals the Claude Code process

## Requirements

- Go 1.24+
- [Claude Code](https://claude.ai/code) installed (provides `~/.claude` state)
- A terminal that supports 256 colors

## Installation

### Homebrew (macOS)

```bash
brew install tyler-reagan/tap/clasp
```

This installs a prebuilt `clasp` binary onto your `$PATH`. Upgrade with `brew upgrade tyler-reagan/tap/clasp`.

### go install (macOS / Linux)

```bash
go install github.com/tylerreagan/clasp@latest
```

### From source

```bash
git clone https://github.com/Tyler-Reagan/clasp
cd clasp
go build -o clasp .
mv clasp /usr/local/bin/clasp   # or anywhere on your $PATH
```

Linux users: Homebrew casks are macOS-only, so use `go install` or grab a prebuilt
tarball from the [releases page](https://github.com/Tyler-Reagan/clasp/releases).

Check your version any time with `clasp --version`.

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
| `d` | Delete memory entry → macOS Trash (Memory tab only; confirm with `y`) |
| `s` | Cycle source filter: all / Claude Code only / Desktop only / mismatched (Skills, Plugins, MCP; needs Claude Desktop) |
| `a` | Show all documented keys / only set keys (Settings tab only) |
| `p` | Pick the project whose settings apply (Settings tab only) |
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
| Skills | `~/.claude/skills/*/SKILL.md` (follows symlinks) + each plugin's `skills/**/SKILL.md` (marketplace and claude.ai-synced) |
| Plugins | `~/.claude/plugins/installed_plugins.json` + `~/.claude/plugins/synced/` + `settings.json` + each plugin's `.claude-plugin/plugin.json`, `.mcp.json`, `skills/`, `hooks/hooks.json`, `commands/`, `agents/` |
| MCP | each plugin's `.mcp.json` + `mcpServers` in `~/.claude/settings.json`, `~/.claude/settings.local.json`, `~/.claude.json` + `claudeAiMcpEverConnected` in `~/.claude.json` + `~/.claude/mcp-needs-auth-cache.json` |
| Memory | `~/.claude/projects/*/memory/*.md` |
| Settings | `~/.claude/settings.json`, `<project>/.claude/settings.json`, `<project>/.claude/settings.local.json`, managed `managed-settings.json` + `managed-settings.d/*.json`, and the documented Global config keys in `~/.claude.json`. Server-managed settings, MDM profiles and `--settings` flags are not visible to clasp and are labelled as such |

The **DSK** column reads Claude Desktop's own store (`~/Library/Application Support/Claude` on macOS, `~/.config/Claude` on Linux): the `anthropic-skills` plugin under `local-agent-mode-sessions/skills-plugin/` (with its `manifest.json` enabled flags), account plugins under `local-agent-mode-sessions/*/*/rpm/` (matched by `.claude-plugin/plugin.json` name), and `mcpServers` in `claude_desktop_config.json`. Every org/account folder is read. clasp never opens Desktop's `config.json` (it holds sign-in tokens) or per-session folders. Desktop's Code tab reads `~/.claude` like the CLI, so its files count under CLI.

A background goroutine uses `fsnotify` to watch these files and directories (including `~/.claude.json`, the managed settings directory, the selected project's `.claude/`, `plugins/synced`, `claude_desktop_config.json` and Desktop's skill and plugin folders) and sends one refresh message per burst of changes.

Setting descriptions come from Anthropic's [settings reference](https://code.claude.com/docs/en/settings-reference), rewritten in ASD-STE100 Simplified Technical English. The catalog is embedded at build time; clasp never goes online. To refresh it after the docs change:

```bash
cd internal/settings && go generate
```

The generator reports keys that lack a description in `ste.json`; `go test ./internal/settings` fails until every key has one.

## Project structure

```
clasp/
├── main.go                      # entry point; wires UI ↔ watcher project channel
└── internal/
    ├── state/
    │   ├── state.go             # reads and parses ~/.claude state files; LoadFor(project)
    │   └── write.go             # plugin enable/disable writer (atomic rename)
    ├── inventory/               # CLI vs Desktop skills, plugins, MCP: sources, hash cache, compare
    ├── settings/
    │   ├── catalog.go           # embedded docs catalog + STE descriptions
    │   ├── catalog.json         # generated from the settings reference
    │   ├── ste.json             # one ASD-STE100 sentence per key (hand-written)
    │   ├── layers.go            # reads each settings file into dotted keys
    │   ├── resolve.go           # precedence merge with per-file provenance
    │   ├── rules.go             # permission-rule path checks
    │   └── gen/                 # go generate: settings reference → catalog.json
    ├── ui/
    │   ├── model.go             # Bubble Tea root model, focus dispatch, layout
    │   ├── tab_inventory.go     # Skills/Plugins/MCP rows with CLI/DSK columns, source filter, install detail
    │   ├── tab_settings.go      # Settings tab rows, detail pane, project picker
    │   ├── keys.go              # keyMap (bubbles/key bindings) + contextKeys help wrapper
    │   ├── vim.go               # gg two-press chord state machine
    │   ├── columns.go           # plugin-detail multi-column breakpoints
    │   └── styles.go            # lipgloss palette
    └── watcher/watcher.go       # fsnotify → debounced RefreshMsg; follows the selected project
```

## Roadmap

**Milestone 1 — read-only viewport** ✓
- [x] Sessions tab — live PID, CWD, status, version
- [x] Skills tab — installed skills with description, source path, and preview
- [x] Plugins tab — installed plugins with enabled/disabled state and bundled contents
- [x] MCP tab — MCP servers from plugins, standalone config files, account-level claude.ai integrations, and needs-auth servers
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

**Milestone 5 — first destructive action** ✓
- [x] Delete a memory entry with `d` in the Memory tab: `y`-gated confirmation, file → macOS Trash, `MEMORY.md` index line pruned in the same step; the index file itself is never deletable

**Milestone 6 — config legibility** (in progress)
- [x] Settings tab — effective value, source file, layers, STE descriptions, dead permission paths
- [x] Claude Code and Claude Desktop side by side — CLI/DSK columns, source filter, content-hash drift and duplicate installs
- [ ] Stale caches and incomplete uninstalls
- [ ] Cruft: worktrees, transcripts, dead projects, memories
- [ ] Model and effort audit

**Later** — polish opportunities tracked in [open issues](https://github.com/Tyler-Reagan/clasp/issues):
- Wordmark offset-shadow effect, scroll-percentage indicator, clipboard yank, small-terminal degradation, parse-warning surfacing, `CLAUDE.md` for the repo, terminal screenshot, glamour render caching
- Session history / command log viewer
- Token and cost metrics from session JSONL

## Built with

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — Elm-architecture TUI framework
- [Bubbles](https://github.com/charmbracelet/bubbles) — scrollable viewport component
- [Lipgloss](https://github.com/charmbracelet/lipgloss) — layout and styling
- [fsnotify](https://github.com/fsnotify/fsnotify) — cross-platform filesystem events
