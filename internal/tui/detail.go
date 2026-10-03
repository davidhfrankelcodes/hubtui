package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// detailScreen shows one tag: its digest and one row per platform.
type detailScreen struct {
	base
	repo  hub.Repo
	tag   hub.Tag
	table table.Model
}

func newDetailScreen(ctx context.Context, deps Deps, id int, repo hub.Repo, t hub.Tag) *detailScreen {
	d := &detailScreen{base: newBase(ctx, deps, id), repo: repo, tag: t}
	d.table = table.New(table.WithFocused(true), table.WithStyles(d.styles.table))
	d.layout()
	return d
}

func (d *detailScreen) title() string      { return d.repo.String() + ":" + d.tag.Name }
func (d *detailScreen) init() tea.Cmd      { return nil }
func (d *detailScreen) suspend()           {}
func (d *detailScreen) resume() tea.Cmd    { return nil }
func (d *detailScreen) capturesText() bool { return false }

func (d *detailScreen) help() []helpEntry {
	return []helpEntry{
		{"j/k ↑/↓", "move"},
		{"y", "yank image:tag"},
		{"Y", "yank image:tag@sha256:… (multi-arch)"},
		{"p", "yank docker pull image:tag"},
		{"o", "open on hub.docker.com"},
		{"esc", "back to tags"},
		{"q", "quit"},
	}
}

func (d *detailScreen) update(msg tea.Msg) tea.Cmd {
	if d.handleShared(msg) {
		return nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		d.width, d.height = msg.Width, msg.Height
		d.layout()
		return nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q":
			return tea.Quit
		case "esc":
			return back
		case "y", "Y", "p":
			return d.yank(msg.String(), d.repo, d.tag)
		case "o":
			return d.openURL(hub.WebURL(d.repo, d.tag.Name))
		}
		var cmd tea.Cmd
		d.table, cmd = d.table.Update(msg)
		return cmd
	}
	return nil
}

// infoLines is the header block above the platform table.
func (d *detailScreen) infoLines() []string {
	label := func(s string) string { return d.styles.dim.Render(fmt.Sprintf("%-8s", s)) }
	digest := d.tag.Digest
	if digest == "" {
		digest = "not reported by Docker Hub"
	}
	pushed := "-"
	if !d.tag.Pushed.IsZero() {
		pushed = relativeTime(d.tag.Pushed, d.deps.Now()) + " (" + d.tag.Pushed.UTC().Format("2006-01-02 15:04 MST") + ")"
	}
	w := max(d.width-9, 10)
	return []string{
		label("digest") + " " + elideMiddle(digest, w),
		label("type") + " " + truncate(describeMediaType(d.tag.MediaType), w),
		label("pushed") + " " + truncate(pushed, w),
	}
}

func (d *detailScreen) layout() {
	// Title, three info lines, a blank line and the status bar.
	d.table.SetHeight(max(d.height-6, 2))
	d.table.SetWidth(d.width)

	const platformW, sizeW = 18, 9
	digestW := max(d.width-3*2-platformW-sizeW, 12)
	d.table.SetColumns([]table.Column{
		{Title: "PLATFORM", Width: platformW},
		{Title: "DIGEST", Width: digestW},
		{Title: "SIZE", Width: sizeW},
	})

	rows := make([]table.Row, len(d.tag.Platforms))
	for i, p := range d.tag.Platforms {
		// Elide in the middle so a truncated digest still shows both ends.
		rows[i] = table.Row{p.String(), elideMiddle(p.Digest, digestW), humanSize(p.Size)}
	}
	d.table.SetRows(rows)
	if len(rows) > 0 && d.table.Cursor() < 0 {
		d.table.SetCursor(0)
	}
}

func (d *detailScreen) view() string {
	title := d.styles.title.Render(d.title()) + d.styles.dim.Render(" · tag detail")
	parts := append([]string{title}, d.infoLines()...)
	parts = append(parts, "")
	if len(d.tag.Platforms) == 0 {
		parts = append(parts, d.styles.dim.Render("Docker Hub reports no platform details for this tag."))
		parts = append(parts, strings.Repeat("\n", max(d.height-7, 0)))
	} else {
		parts = append(parts, d.table.View())
	}
	right := fmt.Sprintf("%d platforms", len(d.tag.Platforms))
	parts = append(parts, d.bar.render(d.styles, d.width, right, false))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// describeMediaType names the manifest kind in words; whether a tag is a
// multi-platform index is what people actually want to know.
func describeMediaType(mt string) string {
	switch mt {
	case "application/vnd.oci.image.index.v1+json":
		return "OCI image index (multi-platform)"
	case "application/vnd.docker.distribution.manifest.list.v2+json":
		return "Docker manifest list (multi-platform)"
	case "application/vnd.oci.image.manifest.v1+json":
		return "OCI image manifest (single platform)"
	case "application/vnd.docker.distribution.manifest.v2+json":
		return "Docker image manifest (single platform)"
	case "application/vnd.docker.distribution.manifest.v1+prettyjws", "application/vnd.docker.distribution.manifest.v1+json":
		return "Docker schema 1 (legacy, not pullable by current Docker)"
	case "":
		return "-"
	}
	return mt
}
