package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// searchDebounce waits for a pause in typing so each keystroke does not
// cost a request against the rate limit.
const searchDebounce = 300 * time.Millisecond

const searchPageSize = 50

type searchResultMsg struct {
	screen int
	id     int
	page   int
	res    *hub.SearchPage
	err    error
}

func (m searchResultMsg) target() int { return m.screen }

// debounceMsg fires after typing pauses; seq ties it to the latest keystroke.
type debounceMsg struct {
	screen int
	seq    int
}

func (m debounceMsg) target() int { return m.screen }

// searchScreen is a query box above a table of matching repositories.
type searchScreen struct {
	sid    int
	ctx    context.Context
	reg    hub.Registry
	styles styles

	input        textinput.Model
	inputFocused bool
	debounceSeq  int

	// query is what the current results are for, which can lag the input.
	query    string
	results  []hub.SearchResult
	seen     map[hub.Repo]bool
	total    int
	nextPage int
	hasNext  bool

	reqID   int
	cancel  context.CancelFunc
	loading bool
	stalled bool

	table         table.Model
	width, height int
	bar           statusBar
}

func newSearchScreen(ctx context.Context, deps Deps, id int) *searchScreen {
	st := newStyles()
	in := textinput.New()
	in.Prompt = "search: "
	in.Placeholder = "repository name, e.g. nginx or grafana"

	s := &searchScreen{
		sid:    id,
		ctx:    ctx,
		reg:    deps.Registry,
		styles: st,
		input:  in,
		seen:   map[hub.Repo]bool{},
		table:  table.New(table.WithStyles(st.table)),
		width:  80,
		height: 24,
	}
	s.layout()
	return s
}

func (s *searchScreen) id() int       { return s.sid }
func (s *searchScreen) title() string { return "search" }

func (s *searchScreen) init() tea.Cmd { return s.focusInput() }

func (s *searchScreen) suspend() {
	s.cancelRequest()
	s.debounceSeq++ // a pending search must not fire while covered
}

func (s *searchScreen) resume() tea.Cmd { return s.maybeLoadMore() }

func (s *searchScreen) cancelRequest() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.reqID++
	s.loading = false
}

func (s *searchScreen) focusInput() tea.Cmd {
	s.inputFocused = true
	s.table.Blur()
	return s.input.Focus()
}

func (s *searchScreen) focusTable() {
	s.inputFocused = false
	s.input.Blur()
	s.table.Focus()
}

func (s *searchScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = msg.Width, msg.Height
		s.layout()
		s.rebuild()
		return s.maybeLoadMore()
	case searchResultMsg:
		return s.handleResults(msg)
	case debounceMsg:
		if msg.seq != s.debounceSeq {
			return nil
		}
		return s.search(false)
	case clearStatusMsg:
		s.bar.expire(msg.id)
		return nil
	case tea.KeyPressMsg:
		if s.inputFocused {
			return s.updateInput(msg)
		}
		return s.handleKey(msg)
	}
	// Cursor blink and similar belong to the input.
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return cmd
}

func (s *searchScreen) updateInput(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		cmd := s.search(false)
		if s.query != "" {
			s.focusTable()
		}
		return cmd
	case "down", "tab":
		if len(s.results) > 0 {
			s.focusTable()
		}
		return nil
	case "esc":
		if s.input.Value() != "" {
			s.input.SetValue("")
			s.debounceSeq++
			return s.search(false)
		}
		return nil
	}

	before := s.input.Value()
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	if s.input.Value() == before {
		return cmd
	}
	// The old request answers a question nobody is asking any more.
	s.cancelRequest()
	s.debounceSeq++
	seq, sid := s.debounceSeq, s.sid
	return tea.Batch(cmd, tea.Tick(searchDebounce, func(time.Time) tea.Msg { return debounceMsg{screen: sid, seq: seq} }))
}

func (s *searchScreen) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "q":
		s.cancelRequest()
		return tea.Quit
	case "enter":
		if r, ok := s.selected(); ok {
			return openTags(r.Repo)
		}
		return nil
	case "/", "esc":
		return s.focusInput()
	case "r":
		s.stalled = false
		return s.search(true)
	}
	var cmd tea.Cmd
	s.table, cmd = s.table.Update(msg)
	return tea.Batch(cmd, s.maybeLoadMore())
}

// search starts page 1 for the input's query. A different query clears the
// old results at once, so enter can never open a result from the previous
// search.
func (s *searchScreen) search(fresh bool) tea.Cmd {
	q := strings.TrimSpace(s.input.Value())
	if q != s.query || q == "" {
		s.query = q
		s.results, s.seen, s.total, s.hasNext = nil, map[hub.Repo]bool{}, 0, false
		s.bar.set("", false)
		s.rebuild()
	}
	if q == "" {
		s.cancelRequest()
		return nil
	}
	if fresh {
		s.bar.set("refreshing…", false)
	}
	return s.fetch(1, fresh)
}

func (s *searchScreen) fetch(page int, fresh bool) tea.Cmd {
	s.cancelRequest()
	id := s.reqID
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancel = cancel
	s.loading = true

	reg, query, sid := s.reg, s.query, s.sid
	opts := hub.PageOptions{Page: page, PageSize: searchPageSize, Fresh: fresh}
	return func() tea.Msg {
		res, err := reg.Search(ctx, query, opts)
		return searchResultMsg{screen: sid, id: id, page: page, res: res, err: err}
	}
}

func (s *searchScreen) handleResults(msg searchResultMsg) tea.Cmd {
	if msg.id != s.reqID {
		return nil
	}
	s.loading = false
	s.cancel()
	s.cancel = nil

	if msg.err != nil {
		s.stalled = true
		var rl *hub.RateLimitError
		switch {
		case errors.Is(msg.err, hub.ErrPageLimit):
			s.hasNext = false
			s.bar.set(fmt.Sprintf("anonymous limit: first %d results", len(s.results)), false)
		case errors.As(msg.err, &rl):
			s.bar.set(rl.Error()+" (press r to retry)", true)
		default:
			s.bar.set(msg.err.Error()+" (press r to retry)", true)
		}
		return nil
	}

	if msg.page == 1 {
		s.results, s.seen = nil, map[hub.Repo]bool{}
		s.bar.set("", false)
	}
	for _, r := range msg.res.Results {
		if !s.seen[r.Repo] {
			s.seen[r.Repo] = true
			s.results = append(s.results, r)
		}
	}
	s.total, s.hasNext, s.nextPage = msg.res.Total, msg.res.HasNext, msg.page+1
	if len(s.results) == 0 {
		s.bar.set(fmt.Sprintf("no repositories match %q", s.query), false)
		if !s.inputFocused {
			return s.focusInput()
		}
	}
	s.rebuild()
	return s.maybeLoadMore()
}

func (s *searchScreen) maybeLoadMore() tea.Cmd {
	if !s.hasNext || s.loading || s.stalled {
		return nil
	}
	if len(s.results)-s.table.Cursor() > s.table.Height() {
		return nil
	}
	return s.fetch(s.nextPage, false)
}

func (s *searchScreen) selected() (hub.SearchResult, bool) {
	c := s.table.Cursor()
	if c < 0 || c >= len(s.results) {
		return hub.SearchResult{}, false
	}
	return s.results[c], true
}

func (s *searchScreen) rebuild() {
	cursor := s.table.Cursor()
	rows := make([]table.Row, len(s.results))
	for i, r := range s.results {
		official := ""
		if r.Official {
			official = "official"
		}
		rows[i] = table.Row{
			r.Repo.String(),
			compactNumber(int64(r.Stars)),
			compactNumber(r.Pulls),
			official,
			strings.Join(strings.Fields(r.Description), " "),
		}
	}
	s.table.SetRows(rows)
	// Appending a page keeps the cursor; a new result set starts at the top.
	if len(rows) > 0 {
		s.table.SetCursor(min(max(cursor, 0), len(rows)-1))
	}
}

func (s *searchScreen) layout() {
	// Title, query and status bar take a line each.
	s.table.SetHeight(max(s.height-3, 2))
	s.table.SetWidth(s.width)
	s.input.SetWidth(max(s.width-len(s.input.Prompt)-1, 1))

	const starsW, pullsW, officialW = 6, 6, 8
	rest := max(s.width-5*2-starsW-pullsW-officialW, 20)
	nameW := min(rest/2, 40)
	s.table.SetColumns([]table.Column{
		{Title: "NAME", Width: nameW},
		{Title: "STARS", Width: starsW},
		{Title: "PULLS", Width: pullsW},
		{Title: "", Width: officialW},
		{Title: "DESCRIPTION", Width: rest - nameW},
	})
}

func (s *searchScreen) view() string {
	title := s.styles.title.Render("hubtui") + s.styles.dim.Render(" · search Docker Hub")
	return lipgloss.JoinVertical(lipgloss.Left, title, s.input.View(), s.table.View(), s.statusLine())
}

func (s *searchScreen) statusLine() string {
	right := ""
	if s.query != "" && (len(s.results) > 0 || s.total > 0) {
		right = fmt.Sprintf("%d/%d results", len(s.results), s.total)
	}
	return s.bar.render(s.styles, s.width, right, s.loading)
}
