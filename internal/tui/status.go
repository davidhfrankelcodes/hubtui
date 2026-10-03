package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
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

// quota is the styled request-quota segment of the status bar.
type quota struct {
	text string
	// urgent marks a quota running low: worth more than the other counters
	// when the line is too narrow for both.
	urgent bool
}

// render lays out the bar. right is plain text shown dim; q goes after it.
func (b statusBar) render(st styles, width int, right string, q quota, loading bool) string {
	showQuota := q.text != ""
	if showQuota && right != "" && lipgloss.Width(right)+3+lipgloss.Width(q.text) > width {
		if q.urgent {
			right = ""
		} else {
			showQuota = false
		}
	}
	// Counters are clipped only when they alone are wider than the screen,
	// leaving the column that separates them from the message.
	r := ""
	if right != "" {
		r = st.dim.Render(truncate(right, width-1))
	}
	if showQuota {
		if r != "" {
			r += st.dim.Render(" · ")
		}
		r += q.text
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

// lowQuota is the share of the quota below which the indicator warns. Lazy
// paging and a narrow filter can spend a page per keypress.
const lowQuota = 0.1

// quotaText formats the request quota. It stays dim while there is plenty
// left; when it runs low it says when it refills.
func quotaText(st styles, r hub.RateLimit, now time.Time) quota {
	text := fmt.Sprintf("api %d/%d", r.Remaining, r.Limit)
	if float64(r.Remaining) >= lowQuota*float64(r.Limit) {
		return quota{text: st.dim.Render(text)}
	}
	if wait := r.Reset.Sub(now); wait > 0 {
		text += fmt.Sprintf(" (full in %s)", wait.Round(time.Second))
	}
	if r.Remaining == 0 {
		return quota{text: st.err.Render(text), urgent: true}
	}
	return quota{text: st.warn.Render(text), urgent: true}
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
