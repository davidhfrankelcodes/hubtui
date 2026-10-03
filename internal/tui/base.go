package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/browser"
	"github.com/davidhfrankelcodes/hubtui/internal/clip"
	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// clipboardMsg reports the native clipboard result for a yank.
type clipboardMsg struct {
	screen int
	err    error
}

func (m clipboardMsg) target() int { return m.screen }

// openedMsg reports the result of opening a page in the browser.
type openedMsg struct {
	screen int
	url    string
	err    error
}

func (m openedMsg) target() int { return m.screen }

// helpEntry is one line of the help overlay.
type helpEntry struct{ keys, desc string }

// base holds what every screen needs and handles the messages they share.
type base struct {
	sid           int
	ctx           context.Context
	deps          Deps
	styles        styles
	bar           statusBar
	width, height int
}

func newBase(ctx context.Context, deps Deps, id int) base {
	return base{sid: id, ctx: ctx, deps: deps, styles: newStyles(), width: 80, height: 24}
}

func (b *base) id() int { return b.sid }

// handleShared deals with status expiry, clipboard and browser results.
func (b *base) handleShared(msg tea.Msg) (handled bool) {
	switch msg := msg.(type) {
	case clearStatusMsg:
		b.bar.expire(msg.id)
	case clipboardMsg:
		// OSC 52 was already sent, so a missing native tool is expected
		// (SSH, headless); only a tool that ran and failed is worth showing.
		if msg.err != nil && !errors.Is(msg.err, clip.ErrUnavailable) {
			b.bar.set("native clipboard failed (OSC 52 was sent): "+msg.err.Error(), true)
		}
	case openedMsg:
		switch {
		case errors.Is(msg.err, browser.ErrUnavailable):
			// Over SSH the URL is the useful part; the terminal can usually
			// open it from the status bar.
			b.bar.set(msg.url, false)
		case msg.err != nil:
			b.bar.set("could not open browser: "+msg.err.Error(), true)
		default:
			b.bar.set("opened "+msg.url, false)
		}
	default:
		return false
	}
	return true
}

// yankText builds exactly what each yank key copies.
func yankText(key string, repo hub.Repo, t hub.Tag) (string, error) {
	switch key {
	case "Y":
		return hub.PinnedReference(repo, t)
	case "p":
		ref, err := hub.Reference(repo, t.Name)
		if err != nil {
			return "", err
		}
		return "docker pull " + ref, nil
	}
	return hub.Reference(repo, t.Name)
}

// yank copies the reference for key via OSC 52 and the native clipboard,
// and shows exactly what was copied.
func (b *base) yank(key string, repo hub.Repo, t hub.Tag) tea.Cmd {
	text, err := yankText(key, repo, t)
	if err != nil {
		b.bar.set("not copied: "+err.Error(), true)
		return nil
	}
	copier, ctx, sid := b.deps.Clipboard, b.ctx, b.sid
	native := func() tea.Msg { return clipboardMsg{screen: sid, err: copier.Copy(ctx, text)} }
	return tea.Batch(tea.SetClipboard(text), native, b.bar.flash(b.sid, "copied "+text))
}

func (b *base) openURL(url string) tea.Cmd {
	opener, ctx, sid := b.deps.Browser, b.ctx, b.sid
	return func() tea.Msg { return openedMsg{screen: sid, url: url, err: opener.Open(ctx, url)} }
}
