package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// Copier puts text on the native clipboard.
type Copier interface {
	Copy(ctx context.Context, text string) error
}

// Opener opens a URL in the user's browser.
type Opener interface {
	Open(ctx context.Context, url string) error
}

// Deps are the outside services screens use.
type Deps struct {
	Registry  hub.Registry
	Clipboard Copier
	Browser   Opener
	Now       func() time.Time
}

// screen is one page of the UI. The App keeps them in a stack.
type screen interface {
	id() int
	init() tea.Cmd
	update(tea.Msg) tea.Cmd
	view() string
	title() string
	// suspend stops in-flight work when another screen covers this one;
	// resume restarts whatever is needed when it is uncovered.
	suspend()
	resume() tea.Cmd
	// capturesText reports whether keys are going into a text field, where
	// "?" and "q" are just characters.
	capturesText() bool
	help() []helpEntry
}

// addressed messages belong to one screen. Each screen has an ID that is
// unique for the session, so a late response for a closed screen can never
// land on a newer one.
type addressed interface {
	target() int
}

// openTagsMsg asks the App to push the Tags screen for repo.
type openTagsMsg struct{ repo hub.Repo }

// openDetailMsg asks the App to push the detail screen for one tag.
type openDetailMsg struct {
	repo hub.Repo
	tag  hub.Tag
}

// backMsg asks the App to pop the top screen.
type backMsg struct{}

func openTags(repo hub.Repo) tea.Cmd { return func() tea.Msg { return openTagsMsg{repo: repo} } }

func openDetail(repo hub.Repo, t hub.Tag) tea.Cmd {
	return func() tea.Msg { return openDetailMsg{repo: repo, tag: t} }
}

func back() tea.Msg { return backMsg{} }

// App is the root model: a stack of screens plus the window size.
type App struct {
	ctx      context.Context
	deps     Deps
	stack    []screen
	nextID   int
	size     tea.WindowSizeMsg
	showHelp bool
}

// NewApp returns an App showing the Tags screen for repo, or the Search
// screen when repo is nil.
func NewApp(ctx context.Context, deps Deps, repo *hub.Repo) *App {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	a := &App{ctx: ctx, deps: deps, size: tea.WindowSizeMsg{Width: 80, Height: 24}}
	if repo != nil {
		a.stack = []screen{newTagsScreen(ctx, deps, a.newID(), *repo)}
	} else {
		a.stack = []screen{newSearchScreen(ctx, deps, a.newID())}
	}
	return a
}

func (a *App) newID() int {
	a.nextID++
	return a.nextID
}

func (a *App) top() screen { return a.stack[len(a.stack)-1] }

// Init implements tea.Model.
func (a *App) Init() tea.Cmd { return a.top().init() }

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Every screen keeps its layout current so it is right when uncovered.
		a.size = msg
		var cmds []tea.Cmd
		for _, s := range a.stack {
			cmds = append(cmds, s.update(msg))
		}
		return a, tea.Batch(cmds...)
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			for _, s := range a.stack {
				s.suspend()
			}
			return a, tea.Quit
		}
		// While help is up, any key just closes it.
		if a.showHelp {
			a.showHelp = false
			return a, nil
		}
		if msg.String() == "?" && !a.top().capturesText() {
			a.showHelp = true
			return a, nil
		}
	case openTagsMsg:
		return a, a.push(newTagsScreen(a.ctx, a.deps, a.newID(), msg.repo))
	case openDetailMsg:
		return a, a.push(newDetailScreen(a.ctx, a.deps, a.newID(), msg.repo, msg.tag))
	case backMsg:
		// The bottom screen has nowhere to go back to; q quits.
		if len(a.stack) == 1 {
			return a, nil
		}
		a.top().suspend()
		a.stack = a.stack[:len(a.stack)-1]
		return a, a.top().resume()
	case addressed:
		for _, s := range a.stack {
			if s.id() == msg.target() {
				return a, s.update(msg)
			}
		}
		return a, nil
	}
	return a, a.top().update(msg)
}

func (a *App) push(s screen) tea.Cmd {
	a.top().suspend()
	a.stack = append(a.stack, s)
	return tea.Batch(s.update(a.size), s.init())
}

// View implements tea.Model.
func (a *App) View() tea.View {
	content := a.top().view()
	if a.showHelp {
		content = overlayHelp(content, a.top().title(), a.top().help(), a.size.Width, a.size.Height)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "hubtui · " + a.top().title()
	return v
}

// Run runs the TUI until the user quits, starting on the Tags screen for repo
// or on the Search screen when repo is nil.
func Run(ctx context.Context, deps Deps, repo *hub.Repo) error {
	if _, err := tea.NewProgram(NewApp(ctx, deps, repo), tea.WithContext(ctx)).Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	return nil
}
