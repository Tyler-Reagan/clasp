package ui

// columnsForWidth returns the section-index distribution per column for
// plugin-detail rendering. Sections are ordered by index in the caller's
// slice (see detailContent's tabPlugins branch): 0=Skills, 1=MCP Servers,
// 2=Commands, 3=Agents, 4=Hooks.
//
// Breakpoints:
//
//	innerW <  80  → single column (vertical stack)
//	innerW <  120 → 2 columns: Skills | the rest
//	innerW >= 120 → 3 columns: Skills | MCP Servers | the rest
//
// Skills gets its own column at every multi-column width because it's
// typically the longest section by a wide margin (a plugin can declare
// 15+ skills). Isolating it prevents one tall column from blowing out
// the visual balance with neighbors.
func columnsForWidth(innerW int) [][]int {
	switch {
	case innerW < 80:
		return [][]int{{0, 1, 2, 3, 4}}
	case innerW < 120:
		return [][]int{{0}, {1, 2, 3, 4}}
	default:
		return [][]int{{0}, {1}, {2, 3, 4}}
	}
}
