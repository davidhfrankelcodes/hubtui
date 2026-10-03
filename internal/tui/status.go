package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// flashDuration is how long a confirmation stays in the status bar.
const flashDuration = 4 * time.Second

// clearStatusMsg expires the flash with the same id; a newer message keeps
// its full time.
type clearStatusMsg struct {
	screen int
	id     int
}

func (m clearStatusMsg) target() int { return m.screen }

// statusBar is the bottom line of a screen: a message on the left, counters
// on the right.
type statusBar struct {
	text  string
	isErr bool
	// exact marks a message (a yank confirmation) that must be shown in full,
	// at the expense of the counters.
	exact bool
	id    int
}

// set replaces the message; it also invalidates any pending flash expiry so
// the new message is not cleared early.
func (b *statusBar) set(text string, isErr bool) {
	b.text, b.isErr, b.exact = text, isErr, false
	b.id++
}

// flash shows a confirmation that clears itself.
func (b *statusBar) flash(screen int, text string) tea.Cmd {
	b.set(text, false)
	b.exact = true
	id := b.id
	return tea.Tick(flashDuration, func(time.Time) tea.Msg { return clearStatusMsg{screen: screen, id: id} })
}

func (b *statusBar) expire(id int) {
	if id == b.id {
		b.set("", false)
	}
}

func (b statusBar) render(st styles, width int, right string, loading bool) string {
	r := ""
	if right != "" {
		r = st.dim.Render(right)
	}

	if b.exact {
		// A yank confirmation must show what was copied; give it the whole
		// line, and if even that is too narrow, elide the middle so both the
		// image and the end of the digest stay checkable.
		if lipgloss.Width(b.text)+1+lipgloss.Width(r) > width {
			r = ""
		}
		return st.notice.Render(elideMiddle(b.text, width)) + padTo(r, width-min(lipgloss.Width(b.text), width))
	}

	left, style := b.text, st.notice
	switch {
	case b.isErr:
		style = st.err
	case left == "" && loading:
		left, style = "loading…", st.dim
	}
	// Truncate the message, never the counters, when space runs out.
	avail := max(width-lipgloss.Width(r)-1, 0)
	l := style.Render(truncate(left, avail))
	gap := max(width-lipgloss.Width(l)-lipgloss.Width(r), 1)
	return l + strings.Repeat(" ", gap) + r
}

// padTo right-aligns s in w cells, or returns "" when it does not fit.
func padTo(s string, w int) string {
	if s == "" || lipgloss.Width(s)+1 > w {
		return ""
	}
	return strings.Repeat(" ", w-lipgloss.Width(s)) + s
}

// elideMiddle shortens s to w cells with "…" in the middle. If s contains a
// digest, the cut falls inside its hex so the image, tag and both ends of
// the hash stay readable.
func elideMiddle(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w < 3 {
		return string(r[:max(w, 0)])
	}
	head := (w - 1) / 2
	if i := strings.Index(s, "@sha256:"); i >= 0 {
		// Keep through "@sha256:" plus a few hex digits, if that leaves room
		// for a tail.
		if keep := len([]rune(s[:i])) + len("@sha256:") + 6; keep < w-7 {
			head = max(head, keep)
		}
	}
	tail := w - 1 - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

func truncate(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 1 {
		return ""
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > w-1 {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}
