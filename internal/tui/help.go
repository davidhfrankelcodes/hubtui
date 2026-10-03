package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// overlayHelp draws the key list in a box centered over content, leaving the
// screen visible around it.
func overlayHelp(content, screenTitle string, entries []helpEntry, width, height int) string {
	st := newStyles()
	keyW := 0
	for _, e := range entries {
		keyW = max(keyW, lipgloss.Width(e.keys))
	}
	lines := []string{st.title.Render("keys · " + screenTitle), ""}
	for _, e := range entries {
		lines = append(lines, st.helpKey.Render(fmt.Sprintf("%-*s", keyW, e.keys))+"  "+e.desc)
	}
	lines = append(lines, "", st.dim.Render("press any key to close"))

	box := st.helpBox.Render(strings.Join(lines, "\n"))
	x := max((width-lipgloss.Width(box))/2, 0)
	y := max((height-lipgloss.Height(box))/2, 0)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(content),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
}
