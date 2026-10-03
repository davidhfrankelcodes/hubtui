package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/clip"
	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

const invalidRegex = "invalid regex"

type sortMode int

const (
	sortPushed sortMode = iota
	sortName
	sortSize
	numSortModes
)

func (s sortMode) String() string {
	return [...]string{"pushed", "name", "size"}[s]
}

// clipboardMsg reports the native clipboard result for a yank.
type clipboardMsg struct {
	screen int
	err    error
}

func (m clipboardMsg) target() int { return m.screen }

// tagsPageMsg carries one fetched page back to Update. id ties it to the
// request that produced it so late responses can be dropped.
type tagsPageMsg struct {
	screen int
	id     int
	page   int
	res    *hub.TagPage
	err    error
}

func (m tagsPageMsg) target() int { return m.screen }

// tagsScreen lists the tags of one repository.
type tagsScreen struct {
	sid    int
	ctx    context.Context
	reg    hub.Registry
	clip   Copier
	now    func() time.Time
	styles styles
	repo   hub.Repo

	// all holds every loaded tag in server order (newest first).
	all      []hub.Tag
	seen     map[string]bool
	total    int
	nextPage int
	hasNext  bool

	reqID   int
	cancel  context.CancelFunc
	loading bool
	// stalled stops lazy loading after an error, so a failing request is not
	// retried every time the cursor moves. `r` clears it.
	stalled bool

	sort      sortMode
	filtering bool
	input     textinput.Model
	filterRE  *regexp.Regexp
	filterErr string

	// view maps table rows to indices in all, after filtering and sorting.
	visible []int
	table   table.Model

	width, height int
	bar           statusBar
}

// newTagsScreen returns the Tags screen for repo. Requests derive from ctx.
func newTagsScreen(ctx context.Context, deps Deps, id int, repo hub.Repo) *tagsScreen {
	st := newStyles()
	in := textinput.New()
	in.Prompt = "/"
	in.Placeholder = "regex"

	t := table.New(table.WithFocused(true), table.WithStyles(st.table))

	m := &tagsScreen{
		sid:      id,
		ctx:      ctx,
		reg:      deps.Registry,
		clip:     deps.Clipboard,
		now:      deps.Now,
		styles:   st,
		repo:     repo,
		seen:     map[string]bool{},
		nextPage: 1,
		input:    in,
		table:    t,
		width:    80,
		height:   24,
	}
	m.layout()
	return m
}

func (m *tagsScreen) id() int       { return m.sid }
func (m *tagsScreen) title() string { return m.repo.String() }

func (m *tagsScreen) init() tea.Cmd {
	return m.fetch(m.ctx, 1, false)
}

func (m *tagsScreen) suspend() {
	m.cancelRequest()
}

func (m *tagsScreen) resume() tea.Cmd {
	return m.maybeLoadMore()
}

// cancelRequest stops the in-flight request and makes its response stale.
func (m *tagsScreen) cancelRequest() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.reqID++
	m.loading = false
}

// fetch cancels whatever is in flight and starts loading page under parent.
func (m *tagsScreen) fetch(parent context.Context, page int, fresh bool) tea.Cmd {
	m.cancelRequest()
	id := m.reqID
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.loading = true

	reg, repo, sid := m.reg, m.repo, m.sid
	opts := hub.TagsOptions{PageOptions: hub.PageOptions{Page: page, PageSize: hub.MaxPageSize, Fresh: fresh}}
	return func() tea.Msg {
		res, err := reg.Tags(ctx, repo, opts)
		return tagsPageMsg{screen: sid, id: id, page: page, res: res, err: err}
	}
}

func (m *tagsScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.rebuild()
		return m.maybeLoadMore()
	case tagsPageMsg:
		return m.handlePage(msg)
	case clearStatusMsg:
		m.bar.expire(msg.id)
		return nil
	case clipboardMsg:
		// OSC 52 was already sent, so a missing native tool is expected
		// (SSH, headless); only a tool that ran and failed is worth showing.
		if msg.err != nil && !errors.Is(msg.err, clip.ErrUnavailable) {
			m.bar.set("native clipboard failed (OSC 52 was sent): "+msg.err.Error(), true)
		}
		return nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m.quit()
		}
		if m.filtering {
			return m.updateFilter(msg)
		}
		return m.handleKey(msg)
	}
	return nil
}

func (m *tagsScreen) quit() tea.Cmd {
	m.cancelRequest()
	return tea.Quit
}

func (m *tagsScreen) handlePage(msg tagsPageMsg) tea.Cmd {
	if msg.id != m.reqID {
		return nil
	}
	m.loading = false
	m.cancel()
	m.cancel = nil

	if msg.err != nil {
		m.stalled = true
		m.setError(msg.err)
		return nil
	}

	if msg.page == 1 {
		m.all, m.seen = nil, map[string]bool{}
		m.bar.set("", false)
	}
	for _, t := range msg.res.Tags {
		// A push while paging shifts later pages, so a tag can show up twice.
		if !m.seen[t.Name] {
			m.seen[t.Name] = true
			m.all = append(m.all, t)
		}
	}
	m.total = msg.res.Total
	m.hasNext = msg.res.HasNext
	m.nextPage = msg.page + 1
	m.rebuild()
	return m.maybeLoadMore()
}

func (m *tagsScreen) setError(err error) {
	var rl *hub.RateLimitError
	switch {
	case errors.Is(err, hub.ErrPageLimit):
		// Not a failure: Hub simply stops here for anonymous users.
		m.hasNext = false
		m.bar.set(fmt.Sprintf("anonymous limit: first %d tags", len(m.all)), false)
	case errors.Is(err, hub.ErrNotFound):
		m.bar.set(fmt.Sprintf("repository %s not found", m.repo), true)
	case errors.As(err, &rl):
		m.bar.set(rl.Error()+" (press r to retry)", true)
	case errors.Is(err, context.Canceled):
	default:
		m.bar.set(err.Error()+" (press r to retry)", true)
	}
}

func (m *tagsScreen) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "q":
		return m.quit()
	case "/":
		m.filtering = true
		m.layout()
		return m.input.Focus()
	case "esc":
		// Esc undoes the filter first, then leaves the screen.
		if m.filterRE != nil || m.input.Value() != "" {
			m.clearFilter()
			return nil
		}
		return back
	case "s":
		m.sort = (m.sort + 1) % numSortModes
		m.rebuild()
		return nil
	case "r":
		m.stalled = false
		m.bar.set("refreshing…", false)
		return m.fetch(m.ctx, 1, true)
	case "y", "Y", "p":
		return m.yank(msg.String())
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return tea.Batch(cmd, m.maybeLoadMore())
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

func (m *tagsScreen) yank(key string) tea.Cmd {
	t, ok := m.selected()
	if !ok {
		m.bar.set("no tag selected", true)
		return nil
	}
	text, err := yankText(key, m.repo, t)
	if err != nil {
		m.bar.set("not copied: "+err.Error(), true)
		return nil
	}

	copier, ctx, sid := m.clip, m.ctx, m.sid
	native := func() tea.Msg { return clipboardMsg{screen: sid, err: copier.Copy(ctx, text)} }
	return tea.Batch(tea.SetClipboard(text), native, m.bar.flash(m.sid, "copied "+text))
}

func (m *tagsScreen) updateFilter(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		m.filtering = false
		m.input.Blur()
		m.layout()
		return m.maybeLoadMore()
	case "esc":
		m.clearFilter()
		return m.maybeLoadMore()
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.applyFilter(m.input.Value())
	return tea.Batch(cmd, m.maybeLoadMore())
}

func (m *tagsScreen) applyFilter(expr string) {
	if expr == "" {
		m.filterRE, m.filterErr = nil, ""
		m.rebuild()
		return
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		// Keep the last valid filter while the user is mid-way through typing.
		m.filterErr = invalidRegex
		return
	}
	m.filterRE, m.filterErr = re, ""
	m.rebuild()
}

func (m *tagsScreen) clearFilter() {
	m.filtering = false
	m.input.Blur()
	m.input.SetValue("")
	m.filterRE, m.filterErr = nil, ""
	m.layout()
	m.rebuild()
}

// rebuild recomputes the visible rows, keeping the same tag selected.
func (m *tagsScreen) rebuild() {
	selected := m.selectedName()

	m.visible = m.visible[:0]
	for i, t := range m.all {
		if m.filterRE == nil || m.filterRE.MatchString(t.Name) {
			m.visible = append(m.visible, i)
		}
	}
	switch m.sort {
	case sortPushed:
		slices.SortStableFunc(m.visible, func(a, b int) int { return m.all[b].Pushed.Compare(m.all[a].Pushed) })
	case sortName:
		slices.SortStableFunc(m.visible, func(a, b int) int {
			x, y := m.all[a].Name, m.all[b].Name
			switch {
			case naturalLess(x, y):
				return -1
			case naturalLess(y, x):
				return 1
			}
			return 0
		})
	case sortSize:
		slices.SortStableFunc(m.visible, func(a, b int) int { return cmp.Compare(displaySize(m.all[b]), displaySize(m.all[a])) })
	}

	now := m.now()
	archW := m.table.Columns()[1].Width
	rows := make([]table.Row, len(m.visible))
	cursor := 0
	for r, i := range m.visible {
		t := m.all[i]
		rows[r] = table.Row{t.Name, archSummary(t.Platforms, archW), humanSize(displaySize(t)), relativeTime(t.Pushed, now), shortDigest(t.Digest)}
		if t.Name == selected {
			cursor = r
		}
	}
	m.table.SetRows(rows)
	// The table leaves its cursor at -1 after being emptied.
	if len(rows) > 0 {
		m.table.SetCursor(cursor)
	}
}

func (m *tagsScreen) selectedName() string {
	if t, ok := m.selected(); ok {
		return t.Name
	}
	return ""
}

// selected returns the tag under the cursor.
func (m *tagsScreen) selected() (hub.Tag, bool) {
	c := m.table.Cursor()
	if c < 0 || c >= len(m.visible) {
		return hub.Tag{}, false
	}
	return m.all[m.visible[c]], true
}

// maybeLoadMore fetches the next page once the cursor is within a screenful
// of the bottom. With a narrow filter that keeps happening until enough rows
// match or the tags run out, which is what someone filtering wants.
func (m *tagsScreen) maybeLoadMore() tea.Cmd {
	if !m.hasNext || m.loading || m.stalled {
		return nil
	}
	if len(m.visible)-m.table.Cursor() > m.table.Height() {
		return nil
	}
	return m.fetch(m.ctx, m.nextPage, false)
}

func (m *tagsScreen) layout() {
	// Title and status bar take a line each, plus the filter prompt when open.
	h := m.height - 2
	if m.filtering {
		h--
	}
	m.table.SetHeight(max(h, 2))
	m.table.SetWidth(m.width)
	// Leave room after the input for the invalid-regex message.
	m.input.SetWidth(max(m.width-len(m.input.Prompt)-len(invalidRegex)-3, 1))

	// Each cell has one column of padding on both sides. Narrow terminals
	// lose the digest first; it is the least useful column for picking a tag.
	const sizeW, pushedW = 9, 8
	digestW, cols := 12, 5
	if m.width < 72 {
		digestW, cols = 0, 4
	}
	rest := max(m.width-cols*2-sizeW-pushedW-digestW, 16)
	tagW := min(rest*6/10, 50)
	m.table.SetColumns([]table.Column{
		{Title: "TAG", Width: tagW},
		{Title: "ARCH", Width: rest - tagW},
		{Title: "SIZE", Width: sizeW},
		{Title: "PUSHED", Width: pushedW},
		{Title: "DIGEST", Width: digestW},
	})
}

func (m *tagsScreen) view() string {
	title := m.styles.title.Render(m.repo.String()) + m.styles.dim.Render(" · tags")

	parts := []string{title, m.table.View()}
	if m.filtering {
		line := m.input.View()
		if m.filterErr != "" {
			line += "  " + m.styles.err.Render(m.filterErr)
		}
		parts = append(parts, line)
	}
	parts = append(parts, m.statusLine())
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *tagsScreen) statusLine() string {
	var right []string
	if m.filterRE != nil {
		right = append(right, fmt.Sprintf("/%s/ %d match", m.filterRE, len(m.visible)))
	}
	right = append(right, fmt.Sprintf("%d/%d loaded", len(m.all), m.total), "sort: "+m.sort.String())
	return m.bar.render(m.styles, m.width, strings.Join(right, " · "), m.loading)
}
