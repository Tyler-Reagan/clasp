package ui

import "github.com/charmbracelet/bubbles/key"

// keyMap centralizes every binding clasp responds to. Bindings flow through
// key.Matches in the Update dispatcher and through help.Model in the hint bar
// and ? overlay, so adding or relabeling a key only happens here.
//
// Context-sensitive enabling (e.g. Toggle is only meaningful on the Plugins
// tab) is handled via key.Binding.SetEnabled on tab/focus transitions.
type keyMap struct {
	// List cursor.
	Up   key.Binding
	Down key.Binding
	Top  key.Binding
	End  key.Binding

	// Tab navigation.
	NextTab key.Binding
	PrevTab key.Binding

	// Detail scroll. Wired in task #11.
	ScrollDown     key.Binding
	ScrollUp       key.Binding
	ScrollPageDown key.Binding
	ScrollPageUp   key.Binding

	// Zoom. Wired in task #13.
	Zoom   key.Binding
	UnZoom key.Binding

	// Actions.
	Toggle  key.Binding
	Refresh key.Binding
	Help    key.Binding
	Quit    key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:             key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:           key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		// Top is handled inline by the vim-g chord state machine (vim.go) rather
		// than via key.Matches — gg is a two-press combo, not a single binding.
		// The binding is kept here only so help.Model renders "gg top" in the bar.
		Top: key.NewBinding(key.WithHelp("gg", "top")),
		End: key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "end")),
		NextTab:        key.NewBinding(key.WithKeys("tab", "l"), key.WithHelp("tab/l", "next tab")),
		PrevTab:        key.NewBinding(key.WithKeys("shift+tab", "h"), key.WithHelp("⇧tab/h", "prev tab")),
		ScrollDown:     key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("^d", "scroll ½ down")),
		ScrollUp:       key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("^u", "scroll ½ up")),
		ScrollPageDown: key.NewBinding(key.WithKeys("ctrl+f"), key.WithHelp("^f", "page down")),
		ScrollPageUp:   key.NewBinding(key.WithKeys("ctrl+b"), key.WithHelp("^b", "page up")),
		Zoom:           key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "zoom")),
		UnZoom:         key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		Toggle:         key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle")),
		Refresh:        key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Help:           key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:           key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// contextKeys is a help.KeyMap wrapper that returns focus-aware help
// content. In browse vs zoom mode the same physical key has different
// semantics (j scrolls list vs. j scrolls detail), so help text must
// vary by context. Pass to help.Model.View as contextKeys{keys, focus}.
type contextKeys struct {
	keys  keyMap
	focus focus
}

// ShortHelp is the always-visible hint bar. Disabled bindings are skipped
// by help.Model. Order = display order, left to right.
func (c contextKeys) ShortHelp() []key.Binding {
	k := c.keys
	if c.focus == focusZoomedDetail {
		// Re-label the navigation pair for the zoom context. We mutate copies
		// (Binding is a value type) so the underlying keyMap is untouched.
		up, down := k.Up, k.Down
		up.SetHelp("↑/k", "scroll up")
		down.SetHelp("↓/j", "scroll down")
		return []key.Binding{up, down, k.Top, k.End, k.NextTab, k.UnZoom, k.Help, k.Quit}
	}
	return []key.Binding{k.Up, k.Down, k.Top, k.End, k.NextTab, k.Zoom, k.Refresh, k.Help, k.Quit}
}

// FullHelp is the ? overlay layout — column groups.
func (c contextKeys) FullHelp() [][]key.Binding {
	k := c.keys
	if c.focus == focusZoomedDetail {
		up, down := k.Up, k.Down
		up.SetHelp("↑/k", "scroll up")
		down.SetHelp("↓/j", "scroll down")
		return [][]key.Binding{
			{up, down, k.Top, k.End},
			{k.ScrollDown, k.ScrollUp, k.ScrollPageDown, k.ScrollPageUp},
			{k.NextTab, k.PrevTab, k.UnZoom},
			{k.Refresh, k.Help, k.Quit},
		}
	}
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.End},
		{k.NextTab, k.PrevTab, k.Zoom},
		{k.ScrollDown, k.ScrollUp, k.ScrollPageDown, k.ScrollPageUp},
		{k.Toggle, k.Refresh, k.Help, k.Quit},
	}
}
