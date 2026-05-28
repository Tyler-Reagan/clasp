package ui

import "github.com/charmbracelet/lipgloss"

// Palette
//
// clasp's visual identity is a warm, dark theme — copper accents on parchment
// text against steel chrome, with a deliberate nod to Claude's coral via the
// Claude Code wordmark and accent. Steel gray is the only cool note, used
// exclusively for borders and separators — everything legible to the eye
// lives in the warm half of the spectrum.
//
// The name "clasp" suggests metal hardware; the palette leans into that:
// hammered copper as accent, oxidized parchment as text, brushed steel as
// chrome, with the occasional terracotta/brass/olive for state colors.
var (
	// Core neutrals.
	colorBg             = lipgloss.Color("#1a1a1a") // terminal-bg-facing; most terminals own this
	colorParchment      = lipgloss.Color("#d4c5a0") // primary text / values
	colorMutedParchment = lipgloss.Color("#8a8170") // secondary text, hint descriptions
	colorSteel          = lipgloss.Color("#5c5c5c") // borders, separators

	// Accents.
	colorCopper       = lipgloss.Color("#b87333") // primary accent — active tab, selected row marker
	colorClaudeCoral  = lipgloss.Color("#cc785c") // secondary warm accent — wink at Claude's brand
	colorOlive        = lipgloss.Color("#a3b566") // success
	colorBrass        = lipgloss.Color("#d4a544") // warning
	colorTerracotta   = lipgloss.Color("#c75545") // danger

	// Style aliases — these are what the rest of the code references. Kept named
	// by semantic role so palette swaps only touch this file. The mapping from
	// role → palette color is the only place that needs to evolve as the design
	// matures.
	colorPrimary = colorCopper
	colorActive  = colorCopper
	colorText    = colorParchment
	colorMuted   = colorMutedParchment
	colorBorder  = colorSteel
	colorGreen   = colorOlive
	colorYellow  = colorBrass
	colorRed     = colorTerracotta

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorCopper).
			Padding(0, 1)

	styleTabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorCopper).
			Underline(true).
			Padding(0, 1)

	styleTabInactive = lipgloss.NewStyle().
				Foreground(colorMutedParchment).
				Padding(0, 1)

	// styleSelected styles the label of the cursor row — no background fill so
	// trailing whitespace in the list column is not highlighted.
	styleSelected = lipgloss.NewStyle().Bold(true).Foreground(colorParchment)

	styleNormal  = lipgloss.NewStyle().Foreground(colorParchment)
	styleMuted   = lipgloss.NewStyle().Foreground(colorMutedParchment)
	stylePrimary = lipgloss.NewStyle().Foreground(colorCopper)
	styleGreen   = lipgloss.NewStyle().Foreground(colorOlive)
	styleYellow  = lipgloss.NewStyle().Foreground(colorBrass)
	styleRed     = lipgloss.NewStyle().Foreground(colorTerracotta)
	styleCoral   = lipgloss.NewStyle().Foreground(colorClaudeCoral)

	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorSteel)

	styleKey = lipgloss.NewStyle().
			Foreground(colorMutedParchment).
			Bold(false)

	styleVal = lipgloss.NewStyle().Foreground(colorParchment)

	styleDetailTitle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorCopper)
)
