package ui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary = lipgloss.Color("#00d9ff")
	colorMuted   = lipgloss.Color("#6c7086")
	colorGreen   = lipgloss.Color("#a6e3a1")
	colorYellow  = lipgloss.Color("#f9e2af")
	colorRed     = lipgloss.Color("#f38ba8")
	colorText    = lipgloss.Color("#cdd6f4")
	colorBorder  = lipgloss.Color("#313244")
	colorActive  = lipgloss.Color("#89b4fa")
	colorSel     = lipgloss.Color("#45475a")

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			Padding(0, 1)

	styleTabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorActive).
			Underline(true).
			Padding(0, 1)

	styleTabInactive = lipgloss.NewStyle().
				Foreground(colorMuted).
				Padding(0, 1)

	styleSelected = lipgloss.NewStyle().
			Background(colorSel).
			Foreground(colorText).
			Bold(true)

	styleNormal = lipgloss.NewStyle().Foreground(colorText)
	styleMuted  = lipgloss.NewStyle().Foreground(colorMuted)
	styleGreen  = lipgloss.NewStyle().Foreground(colorGreen)
	styleYellow = lipgloss.NewStyle().Foreground(colorYellow)
	styleRed    = lipgloss.NewStyle().Foreground(colorRed)

	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder)

	styleKey = lipgloss.NewStyle().
			Foreground(colorMuted).
			Bold(false)

	styleVal = lipgloss.NewStyle().Foreground(colorText)

	styleDetailTitle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorActive)
)
