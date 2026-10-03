package tui

import (
	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
)

// Colors come from the terminal's 16-color ANSI palette so the UI follows the
// user's theme. Anything that must remain visible under NO_COLOR (the
// selection) uses an attribute, not a color.
type styles struct {
	title  lipgloss.Style
	dim    lipgloss.Style
	err    lipgloss.Style
	notice lipgloss.Style
	table  table.Styles
}

func newStyles() styles {
	return styles{
		title:  lipgloss.NewStyle().Bold(true),
		dim:    lipgloss.NewStyle().Foreground(lipgloss.BrightBlack),
		err:    lipgloss.NewStyle().Foreground(lipgloss.Red),
		notice: lipgloss.NewStyle().Foreground(lipgloss.Green),
		table: table.Styles{
			Header:   lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(lipgloss.Blue),
			Cell:     lipgloss.NewStyle().Padding(0, 1),
			Selected: lipgloss.NewStyle().Reverse(true),
		},
	}
}
