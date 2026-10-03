package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

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
	screen int
	id     int
	page   int
	res    *hub.TagPage
	err    error
}

func (m tagsPageMsg) target() int { return m.screen }

// tagsScreen lists the tags of one repository.
type tagsScreen struct {
	base
	repo hub.Repo

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

	// arch is the platform filter, as Platform.String() ("linux/arm64/v8");
	// empty shows every tag.
	arch string
	// stable hides prereleases, dev builds and commit builds.
	stable bool

	// grouping shows one row per version group instead of one per tag.
	// openGroup is the group being browsed; empty shows the group list.
	grouping  groupLevel
	openGroup string
	// lastGroup puts the cursor back on the group just left.
	lastGroup string
	groups    []tagGroup
	// groupCols records which column set the table has, so rows are never
	// set against the other set's columns.
	groupCols bool
}

// newTagsScreen returns the Tags screen for repo. Requests derive from ctx.
func newTagsScreen(ctx context.Context, deps Deps, id int, repo hub.Repo) *tagsScreen {
	in := textinput.New()
	in.Prompt = "/"
	in.Placeholder = "regex"

	m := &tagsScreen{
		base:     newBase(ctx, deps, id),
		repo:     repo,
		seen:     map[string]bool{},
		nextPage: 1,
		input:    in,
	}
	m.table = table.New(table.WithFocused(true), table.WithStyles(m.styles.table))
	m.layout()
	return m
}

func (m *tagsScreen) title() string      { return m.repo.String() }
func (m *tagsScreen) capturesText() bool { return m.filtering }

func (m *tagsScreen) help() []helpEntry {
	return []helpEntry{
		{"j/k ↑/↓", "move"},
		{"g / G", "top / bottom"},
		{"enter", "tag detail (platforms) / open group"},
		{"/", "filter tags by regex"},
		{"a", "cycle architecture filter"},
		{"u", "hide unstable tags (rc, beta, nightly, …)"},
		{"v", "group by version: major, minor, off"},
		{"s", "cycle sort: pushed, name, size"},
		{"y", "yank image:tag"},
		{"Y", "yank image:tag@sha256:… (multi-arch)"},
		{"p", "yank docker pull image:tag"},
		{"o", "open on hub.docker.com"},
		{"r", "refresh, bypassing the cache"},
		{"esc", "clear filter / back"},
		{"q", "quit"},
	}
}

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

	reg, repo, sid := m.deps.Registry, m.repo, m.sid
	opts := hub.TagsOptions{PageOptions: hub.PageOptions{Page: page, PageSize: hub.MaxPageSize, Fresh: fresh}}
	return func() tea.Msg {
		res, err := reg.Tags(ctx, repo, opts)
		return tagsPageMsg{screen: sid, id: id, page: page, res: res, err: err}
	}
}

func (m *tagsScreen) update(msg tea.Msg) tea.Cmd {
	if m.handleShared(msg) {
		return nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.rebuild()
		return m.maybeLoadMore()
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
	switch {
	case errors.Is(err, hub.ErrPageLimit):
		// Not a failure: Hub simply stops here.
		m.hasNext = false
		m.bar.set(m.pageLimitText(len(m.all), "tags"), false)
	case errors.Is(err, hub.ErrNotFound):
		m.bar.set(fmt.Sprintf("repository %s not found", m.repo), true)
	default:
		m.showError(err)
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
		// Esc undoes the filter first, then closes an open group, then
		// leaves the screen.
		if m.filterRE != nil || m.input.Value() != "" {
			m.clearFilter()
			return nil
		}
		if m.openGroup != "" {
			m.openGroup = ""
			m.rebuild()
			return m.maybeLoadMore()
		}
		return back
	case "s":
		m.sort = (m.sort + 1) % numSortModes
		m.rebuild()
		return nil
	case "a":
		m.arch = nextArch(m.archOptions(), m.arch)
		m.rebuild()
		return m.maybeLoadMore()
	case "u":
		m.stable = !m.stable
		m.rebuild()
		return m.maybeLoadMore()
	case "v":
		// A group key means something different at another level ("1" holds
		// the floating 1-alpine tags at the minor level), so the selection
		// does not carry over; the selected tag's group does.
		tag := m.selectedName()
		m.grouping = (m.grouping + 1) % numGroupLevels
		m.openGroup, m.lastGroup, m.groups = "", "", nil
		if tag != "" {
			m.lastGroup = groupKey(tag, m.grouping)
		}
		m.rebuild()
		return m.maybeLoadMore()
	case "enter":
		if g, ok := m.selectedGroup(); ok {
			m.openGroup, m.lastGroup = g.key, g.key
			m.table.SetCursor(0)
			m.rebuild()
			return m.maybeLoadMore()
		}
		if t, ok := m.selected(); ok {
			return openDetail(m.repo, t, m.aliasInfo(t))
		}
		return nil
	case "o":
		tag := ""
		if t, ok := m.selected(); ok {
			tag = t.Name
		}
		return m.openURL(hub.WebURL(m.repo, tag))
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

func (m *tagsScreen) yank(key string) tea.Cmd {
	t, ok := m.selected()
	switch {
	case !ok && m.showingGroups():
		m.bar.set("open a group to yank one of its tags", true)
		return nil
	case !ok:
		m.bar.set("no tag selected", true)
		return nil
	}
	return m.base.yank(key, m.repo, t)
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

// rebuild recomputes the visible rows, keeping the same tag or group
// selected.
func (m *tagsScreen) rebuild() {
	selected, selectedGroup := m.selectedName(), m.lastGroup
	if g, ok := m.selectedGroup(); ok {
		selectedGroup = g.key
	}

	m.visible = m.visible[:0]
	for i, t := range m.all {
		if (m.filterRE == nil || m.filterRE.MatchString(t.Name)) && (m.arch == "" || t.HasPlatform(m.arch)) && (!m.stable || !hub.IsPrerelease(t.Name)) &&
			(m.openGroup == "" || groupKey(t.Name, m.grouping) == m.openGroup) {
			m.visible = append(m.visible, i)
		}
	}

	if m.showingGroups() {
		m.groups = buildGroups(m.all, m.visible, m.grouping)
		m.setColumns(true)
		m.setGroupRows(selectedGroup)
		return
	}
	m.groups = nil
	m.setColumns(false)

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
		slices.SortStableFunc(m.visible, func(a, b int) int { return cmp.Compare(displaySize(m.all[b], m.arch), displaySize(m.all[a], m.arch)) })
	}

	now := m.deps.Now()
	archW := m.table.Columns()[1].Width
	rows := make([]table.Row, len(m.visible))
	cursor := 0
	for r, i := range m.visible {
		t := m.all[i]
		rows[r] = table.Row{t.Name, archSummary(t.Platforms, archW), humanSize(displaySize(t, m.arch)), relativeTime(t.Pushed, now), shortDigest(t.Digest)}
		if t.Name == selected {
			cursor = r
		}
	}
	m.setRows(rows, cursor)
}

// setGroupRows shows one row per group, newest version first; sorting by
// push date or size would scatter versions, which defeats grouping.
func (m *tagsScreen) setGroupRows(selected string) {
	now := m.deps.Now()
	rows := make([]table.Row, len(m.groups))
	cursor := 0
	for r, g := range m.groups {
		rows[r] = table.Row{g.key, strconv.Itoa(len(g.tags)), m.all[g.newest].Name, relativeTime(g.pushed, now)}
		if g.key == selected {
			cursor = r
		}
	}
	m.setRows(rows, cursor)
}

func (m *tagsScreen) setRows(rows []table.Row, cursor int) {
	m.table.SetRows(rows)
	// The table leaves its cursor at -1 after being emptied.
	if len(rows) > 0 {
		m.table.SetCursor(cursor)
	}
}

// showingGroups reports whether the table lists groups rather than tags.
func (m *tagsScreen) showingGroups() bool {
	return m.grouping != groupOff && m.openGroup == ""
}

// selectedGroup returns the group under the cursor in the group list.
func (m *tagsScreen) selectedGroup() (tagGroup, bool) {
	c := m.table.Cursor()
	if !m.groupCols || c < 0 || c >= len(m.groups) {
		return tagGroup{}, false
	}
	return m.groups[c], true
}

// rowCount is the number of rows in the table, groups or tags.
func (m *tagsScreen) rowCount() int {
	if m.groupCols {
		return len(m.groups)
	}
	return len(m.visible)
}

func (m *tagsScreen) selectedName() string {
	if t, ok := m.selected(); ok {
		return t.Name
	}
	return ""
}

// selected returns the tag under the cursor; none while groups are listed.
func (m *tagsScreen) selected() (hub.Tag, bool) {
	c := m.table.Cursor()
	if m.groupCols || c < 0 || c >= len(m.visible) {
		return hub.Tag{}, false
	}
	return m.all[m.visible[c]], true
}

// aliasInfo finds t's aliases among the loaded tags. Aliases are usually
// pushed together, so they sit next to each other in newest-first order and
// are rarely split across pages, but the detail screen still says when the
// list may be incomplete.
func (m *tagsScreen) aliasInfo(t hub.Tag) aliasInfo {
	names := hub.Aliases(m.all, t)
	slices.SortFunc(names, func(a, b string) int {
		switch {
		case naturalLess(a, b):
			return -1
		case naturalLess(b, a):
			return 1
		}
		return 0
	})
	// The anonymous paging cap ends loading with hasNext false, so compare
	// against the total too.
	return aliasInfo{names: names, loaded: len(m.all), partial: m.hasNext || len(m.all) < m.total}
}

// maybeLoadMore fetches the next page once the cursor is within a screenful
// of the bottom. With a narrow filter that keeps happening until enough rows
// match or the tags run out, which is what someone filtering wants.
func (m *tagsScreen) maybeLoadMore() tea.Cmd {
	if !m.hasNext || m.loading || m.stalled {
		return nil
	}
	if m.rowCount()-m.table.Cursor() > m.table.Height() {
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

	m.setColumns(m.groupCols)
}

// setColumns gives the table the group or the tag columns, sized to the
// width. Rows are cleared first when the set changes: the table indexes row
// cells by column, so old rows against new columns would not line up.
func (m *tagsScreen) setColumns(groups bool) {
	if groups != m.groupCols {
		m.table.SetRows(nil)
		m.groupCols = groups
	}
	if groups {
		const groupW, countW, pushedW = 12, 6, 8
		newestW := max(m.width-4*2-groupW-countW-pushedW, 10)
		m.table.SetColumns([]table.Column{
			{Title: "VERSION", Width: groupW},
			{Title: "TAGS", Width: countW},
			{Title: "NEWEST TAG", Width: newestW},
			{Title: "PUSHED", Width: pushedW},
		})
		return
	}

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
	sub := " · tags"
	switch {
	case m.openGroup != "":
		sub += " · " + m.openGroup
	case m.grouping != groupOff:
		sub += " by " + m.grouping.String() + " version"
	}
	title := m.styles.title.Render(m.repo.String()) + m.styles.dim.Render(sub)

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

// archOptions lists the platforms seen in the loaded tags, common ones first.
func (m *tagsScreen) archOptions() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range m.all {
		for _, p := range t.Platforms {
			if s := p.String(); !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	slices.Sort(out)
	sortPlatforms(out)
	return out
}

// nextArch cycles all → each option → all. A filter that is no longer among
// the options (it cannot happen today, but pages only add) restarts the cycle.
func nextArch(options []string, current string) string {
	if current == "" {
		if len(options) == 0 {
			return ""
		}
		return options[0]
	}
	i := slices.Index(options, current)
	if i < 0 || i == len(options)-1 {
		return ""
	}
	return options[i+1]
}

func (m *tagsScreen) statusLine() string {
	var right []string
	if m.filterRE != nil {
		right = append(right, fmt.Sprintf("/%s/ %d match", m.filterRE, len(m.visible)))
	}
	if m.arch != "" {
		right = append(right, "arch: "+shortPlatform(m.arch))
	}
	if m.stable {
		right = append(right, "stable")
	}
	right = append(right, fmt.Sprintf("%d/%d loaded", len(m.all), m.total))
	if m.showingGroups() {
		// Counts grow as pages load; the loaded figure says how far along.
		right = append(right, fmt.Sprintf("%d groups", len(m.groups)))
	} else {
		right = append(right, "sort: "+m.sort.String())
	}
	return m.base.statusLine(strings.Join(right, " · "), m.loading)
}
