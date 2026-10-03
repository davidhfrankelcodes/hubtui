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

// tagsPageMsg carries one fetched page back to Update. id ties it to the
// request that produced it so late responses can be dropped.
type tagsPageMsg struct {
	id   int
	page int
	res  *hub.TagPage
	err  error
}

// TagsModel is the Tags screen for one repository.
type TagsModel struct {
	ctx    context.Context
	reg    hub.Registry
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
	view  []int
	table table.Model

	width, height int
	status        string
	statusIsErr   bool

	initCmd tea.Cmd
}

// NewTagsModel returns the Tags screen for repo. Requests derive from ctx.
func NewTagsModel(ctx context.Context, reg hub.Registry, repo hub.Repo, now func() time.Time) TagsModel {
	st := newStyles()
	in := textinput.New()
	in.Prompt = "/"
	in.Placeholder = "regex"

	t := table.New(table.WithFocused(true), table.WithStyles(st.table))

	m := TagsModel{
		ctx:      ctx,
		reg:      reg,
		now:      now,
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
	// Init cannot change the model, so the first request's ID and cancel
	// func are recorded here and Init only hands back the command.
	m.initCmd = m.fetch(ctx, 1, false)
	return m
}

// Init implements tea.Model.
func (m TagsModel) Init() tea.Cmd {
	return m.initCmd
}

// fetch cancels whatever is in flight and starts loading page under parent.
func (m *TagsModel) fetch(parent context.Context, page int, fresh bool) tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	m.reqID++
	id := m.reqID
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.loading = true

	reg, repo := m.reg, m.repo
	opts := hub.TagsOptions{PageOptions: hub.PageOptions{Page: page, PageSize: hub.MaxPageSize, Fresh: fresh}}
	return func() tea.Msg {
		res, err := reg.Tags(ctx, repo, opts)
		return tagsPageMsg{id: id, page: page, res: res, err: err}
	}
}

// Update implements tea.Model.
func (m TagsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.rebuild()
		return m, m.maybeLoadMore()
	case tagsPageMsg:
		return m.handlePage(msg)
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m.quit()
		}
		if m.filtering {
			return m.updateFilter(msg)
		}
		return m.handleKey(msg)
	}
	return m, nil
}

func (m TagsModel) quit() (tea.Model, tea.Cmd) {
	if m.cancel != nil {
		m.cancel()
	}
	return m, tea.Quit
}

func (m TagsModel) handlePage(msg tagsPageMsg) (tea.Model, tea.Cmd) {
	if msg.id != m.reqID {
		return m, nil
	}
	m.loading = false
	m.cancel()
	m.cancel = nil

	if msg.err != nil {
		m.stalled = true
		m.setError(msg.err)
		return m, nil
	}

	if msg.page == 1 {
		m.all, m.seen = nil, map[string]bool{}
		m.status, m.statusIsErr = "", false
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
	return m, m.maybeLoadMore()
}

func (m *TagsModel) setError(err error) {
	var rl *hub.RateLimitError
	switch {
	case errors.Is(err, hub.ErrPageLimit):
		// Not a failure: Hub simply stops here for anonymous users.
		m.hasNext = false
		m.status = fmt.Sprintf("anonymous limit: first %d tags", len(m.all))
		m.statusIsErr = false
		return
	case errors.Is(err, hub.ErrNotFound):
		m.status = fmt.Sprintf("repository %s not found", m.repo)
	case errors.As(err, &rl):
		m.status = rl.Error() + " (press r to retry)"
	case errors.Is(err, context.Canceled):
		return
	default:
		m.status = err.Error() + " (press r to retry)"
	}
	m.statusIsErr = true
}

func (m TagsModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m.quit()
	case "/":
		m.filtering = true
		m.layout()
		return m, m.input.Focus()
	case "esc":
		if m.filterRE != nil || m.input.Value() != "" {
			m.clearFilter()
		}
		return m, nil
	case "s":
		m.sort = (m.sort + 1) % numSortModes
		m.rebuild()
		return m, nil
	case "r":
		m.stalled = false
		m.status, m.statusIsErr = "refreshing…", false
		return m, m.fetch(m.ctx, 1, true)
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, tea.Batch(cmd, m.maybeLoadMore())
}

func (m TagsModel) updateFilter(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.filtering = false
		m.input.Blur()
		m.layout()
		return m, m.maybeLoadMore()
	case "esc":
		m.clearFilter()
		return m, m.maybeLoadMore()
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.applyFilter(m.input.Value())
	return m, tea.Batch(cmd, m.maybeLoadMore())
}

func (m *TagsModel) applyFilter(expr string) {
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

func (m *TagsModel) clearFilter() {
	m.filtering = false
	m.input.Blur()
	m.input.SetValue("")
	m.filterRE, m.filterErr = nil, ""
	m.layout()
	m.rebuild()
}

// rebuild recomputes the visible rows, keeping the same tag selected.
func (m *TagsModel) rebuild() {
	selected := m.selectedName()

	m.view = m.view[:0]
	for i, t := range m.all {
		if m.filterRE == nil || m.filterRE.MatchString(t.Name) {
			m.view = append(m.view, i)
		}
	}
	switch m.sort {
	case sortPushed:
		slices.SortStableFunc(m.view, func(a, b int) int { return m.all[b].Pushed.Compare(m.all[a].Pushed) })
	case sortName:
		slices.SortStableFunc(m.view, func(a, b int) int {
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
		slices.SortStableFunc(m.view, func(a, b int) int { return cmp.Compare(displaySize(m.all[b]), displaySize(m.all[a])) })
	}

	now := m.now()
	archW := m.table.Columns()[1].Width
	rows := make([]table.Row, len(m.view))
	cursor := 0
	for r, i := range m.view {
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

func (m TagsModel) selectedName() string {
	if t, ok := m.Selected(); ok {
		return t.Name
	}
	return ""
}

// Selected returns the tag under the cursor.
func (m TagsModel) Selected() (hub.Tag, bool) {
	c := m.table.Cursor()
	if c < 0 || c >= len(m.view) {
		return hub.Tag{}, false
	}
	return m.all[m.view[c]], true
}

// maybeLoadMore fetches the next page once the cursor is within a screenful
// of the bottom. With a narrow filter that keeps happening until enough rows
// match or the tags run out, which is what someone filtering wants.
func (m *TagsModel) maybeLoadMore() tea.Cmd {
	if !m.hasNext || m.loading || m.stalled {
		return nil
	}
	if len(m.view)-m.table.Cursor() > m.table.Height() {
		return nil
	}
	return m.fetch(m.ctx, m.nextPage, false)
}

func (m *TagsModel) layout() {
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

// View implements tea.Model.
func (m TagsModel) View() tea.View {
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

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, parts...))
	v.AltScreen = true
	v.WindowTitle = "hubtui · " + m.repo.String()
	return v
}

func (m TagsModel) statusLine() string {
	var right []string
	if m.filterRE != nil {
		right = append(right, fmt.Sprintf("/%s/ %d match", m.filterRE, len(m.view)))
	}
	right = append(right, fmt.Sprintf("%d/%d loaded", len(m.all), m.total), "sort: "+m.sort.String())
	r := m.styles.dim.Render(strings.Join(right, " · "))

	left := m.status
	style := m.styles.notice
	switch {
	case m.statusIsErr:
		style = m.styles.err
	case left == "" && m.loading:
		left, style = "loading…", m.styles.dim
	}
	// Truncate the message, never the counters, when space runs out.
	avail := max(m.width-lipgloss.Width(r)-1, 0)
	l := style.Render(truncate(left, avail))
	gap := max(m.width-lipgloss.Width(l)-lipgloss.Width(r), 1)
	return l + strings.Repeat(" ", gap) + r
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
