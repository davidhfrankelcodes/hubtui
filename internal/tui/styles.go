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
	warn   lipgloss.Style
	notice lipgloss.Style
	table  table.Styles
	// The help box is opaque and bordered so it reads as a layer above the
	// screen, not part of it.
	helpBox lipgloss.Style
	helpKey lipgloss.Style
}

func newStyles() styles {
	return styles{
		title:  lipgloss.NewStyle().Bold(true),
		dim:    lipgloss.NewStyle().Foreground(lipgloss.BrightBlack),
		err:    lipgloss.NewStyle().Foreground(lipgloss.Red),
		warn:   lipgloss.NewStyle().Foreground(lipgloss.Yellow),
		notice: lipgloss.NewStyle().Foreground(lipgloss.Green),
		table: table.Styles{
			Header:   lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(lipgloss.Blue),
			Cell:     lipgloss.NewStyle().Padding(0, 1),
			Selected: lipgloss.NewStyle().Reverse(true),
		},
		helpBox: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Blue).Padding(0, 2),
		helpKey: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Yellow),
	}
}
