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

// Copier puts text on the native clipboard.
type Copier interface {
	Copy(ctx context.Context, text string) error
}

// Deps are the outside services screens use.
type Deps struct {
	Registry  hub.Registry
	Clipboard Copier
	Now       func() time.Time
}

// flashDuration is how long a confirmation stays in the status bar.
const flashDuration = 4 * time.Second

// clearStatusMsg expires the flash with the same id; a newer message keeps
// its full time.
type clearStatusMsg struct{ id int }

// clipboardMsg reports the native clipboard result for a yank.
type clipboardMsg struct{ err error }

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
	view  []int
	table table.Model

	width, height int
	status        string
	statusIsErr   bool
	// statusExact marks a status (a yank confirmation) that must be shown in
	// full, at the expense of the counters.
	statusExact bool
	statusID    int

	initCmd tea.Cmd
}

// NewTagsModel returns the Tags screen for repo. Requests derive from ctx.
func NewTagsModel(ctx context.Context, deps Deps, repo hub.Repo) TagsModel {
	st := newStyles()
	in := textinput.New()
	in.Prompt = "/"
	in.Placeholder = "regex"

	t := table.New(table.WithFocused(true), table.WithStyles(st.table))

	m := TagsModel{
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
	case clearStatusMsg:
		if msg.id == m.statusID {
			m.setStatus("", false)
		}
		return m, nil
	case clipboardMsg:
		// OSC 52 was already sent, so a missing native tool is expected
		// (SSH, headless); only a tool that ran and failed is worth showing.
		if msg.err != nil && !errors.Is(msg.err, clip.ErrUnavailable) {
			m.setStatus("native clipboard failed (OSC 52 was sent): "+msg.err.Error(), true)
		}
		return m, nil
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
		m.setStatus("", false)
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

// setStatus replaces the status message; it also invalidates any pending
// flash expiry so the new message is not cleared early.
func (m *TagsModel) setStatus(s string, isErr bool) {
	m.status, m.statusIsErr, m.statusExact = s, isErr, false
	m.statusID++
}

// flash shows a confirmation that clears itself.
func (m *TagsModel) flash(s string) tea.Cmd {
	m.setStatus(s, false)
	m.statusExact = true
	id := m.statusID
	return tea.Tick(flashDuration, func(time.Time) tea.Msg { return clearStatusMsg{id: id} })
}

func (m *TagsModel) setError(err error) {
	var rl *hub.RateLimitError
	switch {
	case errors.Is(err, hub.ErrPageLimit):
		// Not a failure: Hub simply stops here for anonymous users.
		m.hasNext = false
		m.setStatus(fmt.Sprintf("anonymous limit: first %d tags", len(m.all)), false)
	case errors.Is(err, hub.ErrNotFound):
		m.setStatus(fmt.Sprintf("repository %s not found", m.repo), true)
	case errors.As(err, &rl):
		m.setStatus(rl.Error()+" (press r to retry)", true)
	case errors.Is(err, context.Canceled):
	default:
		m.setStatus(err.Error()+" (press r to retry)", true)
	}
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
		m.setStatus("refreshing…", false)
		return m, m.fetch(m.ctx, 1, true)
	case "y", "Y", "p":
		return m.yank(msg.String())
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, tea.Batch(cmd, m.maybeLoadMore())
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

func (m TagsModel) yank(key string) (tea.Model, tea.Cmd) {
	t, ok := m.Selected()
	if !ok {
		m.setStatus("no tag selected", true)
		return m, nil
	}
	text, err := yankText(key, m.repo, t)
	if err != nil {
		m.setStatus("not copied: "+err.Error(), true)
		return m, nil
	}

	copier, ctx := m.clip, m.ctx
	native := func() tea.Msg { return clipboardMsg{err: copier.Copy(ctx, text)} }
	return m, tea.Batch(tea.SetClipboard(text), native, m.flash("copied "+text))
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
	if m.statusExact {
		// A yank confirmation must show what was copied; give it the whole
		// line, and if even that is too narrow, elide the middle so both
		// the image and the end of the digest stay checkable.
		if lipgloss.Width(left)+1+lipgloss.Width(r) > m.width {
			r = ""
		}
		return m.styles.notice.Render(elideMiddle(left, m.width)) + padTo(r, m.width-min(lipgloss.Width(left), m.width))
	}
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
