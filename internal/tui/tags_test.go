package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

func testNow() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }

type tagsCall struct {
	ctx  context.Context
	opts hub.TagsOptions
}

// fakeRegistry serves tags in pages of whatever size the test configures,
// independent of the requested page size.
type fakeRegistry struct {
	mu       sync.Mutex
	tags     []hub.Tag
	pageSize int
	errs     map[int]error
	calls    []tagsCall
}

func (f *fakeRegistry) Search(context.Context, string, hub.PageOptions) (*hub.SearchPage, error) {
	return &hub.SearchPage{}, nil
}

func (f *fakeRegistry) Tags(ctx context.Context, _ hub.Repo, opts hub.TagsOptions) (*hub.TagPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, tagsCall{ctx: ctx, opts: opts})
	if err := f.errs[opts.Page]; err != nil {
		return nil, err
	}
	start := min((opts.Page-1)*f.pageSize, len(f.tags))
	end := min(start+f.pageSize, len(f.tags))
	return &hub.TagPage{
		Tags:    f.tags[start:end],
		Total:   len(f.tags),
		Page:    opts.Page,
		HasNext: end < len(f.tags),
	}, nil
}

func (f *fakeRegistry) numCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeRegistry) lastCall() tagsCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

// makeTags returns n tags named prefix0..prefixN, newest first.
func makeTags(prefix string, n int) []hub.Tag {
	tags := make([]hub.Tag, n)
	for i := range tags {
		tags[i] = hub.Tag{
			Name:      fmt.Sprintf("%s%d", prefix, i),
			Digest:    fmt.Sprintf("sha256:%064d", i),
			Pushed:    testNow().Add(-time.Duration(i) * time.Hour),
			Platforms: []hub.Platform{{OS: "linux", Arch: "amd64", Size: int64(1000 * (i + 1))}},
		}
	}
	return tags
}

func newTestModel(reg hub.Registry) TagsModel {
	m := NewTagsModel(context.Background(), reg, hub.Repo{Namespace: "library", Name: "nginx"}, testNow)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(TagsModel)
}

func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func update(t *testing.T, m TagsModel, msg tea.Msg) (TagsModel, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	tm, ok := next.(TagsModel)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return tm, cmd
}

// press sends each key in turn, typing multi-character strings one rune at a
// time unless they name a special key. It returns the last command.
func press(t *testing.T, m TagsModel, keys ...string) (TagsModel, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		m, cmd = update(t, m, keyMsg(k))
	}
	return m, cmd
}

func typeText(t *testing.T, m TagsModel, s string) TagsModel {
	t.Helper()
	for _, r := range s {
		m, _ = update(t, m, keyMsg(string(r)))
	}
	return m
}

// pageMsg runs cmd and returns the tagsPageMsg it produces, or nil if it
// started no fetch. Batches are searched; other commands (cursor blink) are
// ignored rather than run, since they sleep.
func pageMsg(t *testing.T, cmd tea.Cmd) *tagsPageMsg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	results := make(chan tea.Msg, 1)
	go func() { results <- cmd() }()
	select {
	case msg := <-results:
		switch msg := msg.(type) {
		case tagsPageMsg:
			return &msg
		case tea.BatchMsg:
			for _, c := range msg {
				if pm := pageMsg(t, c); pm != nil {
					return pm
				}
			}
		}
		return nil
	case <-time.After(100 * time.Millisecond):
		return nil // a timer, not a fetch
	}
}

// settle delivers fetch results until the model stops fetching.
func settle(t *testing.T, m TagsModel, cmd tea.Cmd) TagsModel {
	t.Helper()
	for range 50 {
		pm := pageMsg(t, cmd)
		if pm == nil {
			return m
		}
		m, cmd = update(t, m, *pm)
	}
	t.Fatal("model kept fetching")
	return m
}

func viewNames(m TagsModel) []string {
	var out []string
	for _, i := range m.view {
		out = append(out, m.all[i].Name)
	}
	return out
}

func selected(t *testing.T, m TagsModel) string {
	t.Helper()
	tag, ok := m.Selected()
	if !ok {
		t.Fatal("nothing selected")
	}
	return tag.Name
}

func TestTagsInitialLoad(t *testing.T) {
	reg := &fakeRegistry{tags: makeTags("t", 250), pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.Init())

	if reg.numCalls() != 1 {
		t.Fatalf("calls = %d, want 1: a full page more than fills the screen", reg.numCalls())
	}
	opts := reg.lastCall().opts
	if opts.Page != 1 || opts.PageSize != hub.MaxPageSize || opts.Fresh || opts.Order != hub.OrderNewest {
		t.Errorf("first request = %+v", opts)
	}
	if len(m.all) != 100 || m.total != 250 || !m.hasNext || m.loading {
		t.Errorf("loaded %d of %d, hasNext %v, loading %v", len(m.all), m.total, m.hasNext, m.loading)
	}
	if got := selected(t, m); got != "t0" {
		t.Errorf("selected %q, want the newest tag", got)
	}
	if !strings.Contains(m.statusLine(), "100/250 loaded") {
		t.Errorf("status line %q lacks the loaded count", m.statusLine())
	}
}

func TestTagsLazyPagination(t *testing.T) {
	reg := &fakeRegistry{tags: makeTags("t", 250), pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.Init())

	m, cmd := press(t, m, "j", "j")
	if pageMsg(t, cmd) != nil {
		t.Fatal("fetched the next page while far from the bottom")
	}

	m, cmd = press(t, m, "G")
	pm := pageMsg(t, cmd)
	if pm == nil {
		t.Fatal("reaching the bottom did not fetch the next page")
	}
	if pm.page != 2 {
		t.Errorf("fetched page %d, want 2", pm.page)
	}

	// While page 2 is in flight, more movement must not start another request.
	calls := reg.numCalls()
	m, cmd = press(t, m, "k", "j")
	if pageMsg(t, cmd) != nil || reg.numCalls() != calls {
		t.Error("started a second request while one was in flight")
	}

	m, cmd = update(t, m, *pm)
	if len(m.all) != 200 {
		t.Fatalf("loaded %d tags, want 200", len(m.all))
	}
	if got := selected(t, m); got != "t99" {
		t.Errorf("selection moved to %q after appending a page", got)
	}
	if pageMsg(t, cmd) != nil {
		t.Error("fetched page 3 although the cursor is a full page from the bottom")
	}

	m, cmd = press(t, m, "G")
	m = settle(t, m, cmd)
	if len(m.all) != 250 || m.hasNext {
		t.Errorf("loaded %d, hasNext %v; want all 250 and no more pages", len(m.all), m.hasNext)
	}
	calls = reg.numCalls()
	m, cmd = press(t, m, "G", "k", "j")
	if pageMsg(t, cmd) != nil || reg.numCalls() != calls {
		t.Error("fetched past the last page")
	}
	_ = m
}

func TestTagsShortPagesFillTheScreen(t *testing.T) {
	reg := &fakeRegistry{tags: makeTags("t", 25), pageSize: 5}
	m := newTestModel(reg)
	m = settle(t, m, m.Init())
	if len(m.all) != 25 {
		t.Errorf("loaded %d tags, want all 25: pages shorter than the screen should keep loading", len(m.all))
	}
}

func TestTagsDeduplicatesAcrossPages(t *testing.T) {
	tags := makeTags("t", 6)
	// A push mid-browse shifts t2 onto the next page as well.
	tags = slices.Insert(tags, 3, tags[2])
	reg := &fakeRegistry{tags: tags, pageSize: 3}
	m := newTestModel(reg)
	m = settle(t, m, m.Init())
	if got := strings.Join(viewNames(m), ","); got != "t0,t1,t2,t3,t4,t5" {
		t.Errorf("tags = %s", got)
	}
}

func TestTagsStaleResponsesDiscarded(t *testing.T) {
	reg := &fakeRegistry{tags: makeTags("t", 10), pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.Init())

	m, first := press(t, m, "r")
	firstMsg := pageMsg(t, first)
	firstCtx := reg.lastCall().ctx

	reg.tags = makeTags("new", 3)
	m, second := press(t, m, "r")
	secondMsg := pageMsg(t, second)

	if firstCtx.Err() == nil {
		t.Error("starting a new request did not cancel the old one")
	}

	m, _ = update(t, m, *firstMsg)
	if got := viewNames(m)[0]; got != "t0" {
		t.Errorf("stale response was applied: first row %q", got)
	}
	if !m.loading {
		t.Error("stale response cleared the loading state of the current request")
	}

	m, _ = update(t, m, *secondMsg)
	if got := strings.Join(viewNames(m), ","); got != "new0,new1,new2" {
		t.Errorf("after refresh rows = %s", got)
	}
}

func TestTagsRefreshBypassesCache(t *testing.T) {
	reg := &fakeRegistry{tags: makeTags("t", 10), pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.Init())
	if reg.lastCall().opts.Fresh {
		t.Error("initial load asked to bypass the cache")
	}

	m, cmd := press(t, m, "r")
	if m.status != "refreshing…" {
		t.Errorf("status = %q", m.status)
	}
	m = settle(t, m, cmd)
	if opts := reg.lastCall().opts; !opts.Fresh || opts.Page != 1 {
		t.Errorf("refresh request = %+v, want fresh page 1", opts)
	}
	if m.status != "" {
		t.Errorf("status after refresh = %q, want cleared", m.status)
	}
}

func TestTagsErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus string
		wantErr    bool
		wantStall  bool
	}{
		{
			name:       "not found",
			err:        &hub.APIError{StatusCode: 404, Message: "object not found"},
			wantStatus: "repository nginx not found",
			wantErr:    true, wantStall: true,
		},
		{
			name:       "rate limited",
			err:        &hub.RateLimitError{RetryAfter: 30 * time.Second},
			wantStatus: "retry in 30s",
			wantErr:    true, wantStall: true,
		},
		{
			name:       "network",
			err:        fmt.Errorf("listing tags: %w", context.DeadlineExceeded),
			wantStatus: "deadline exceeded",
			wantErr:    true, wantStall: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &fakeRegistry{tags: makeTags("t", 10), pageSize: 100, errs: map[int]error{1: tt.err}}
			m := newTestModel(reg)
			m = settle(t, m, m.Init())

			if !strings.Contains(m.status, tt.wantStatus) || m.statusIsErr != tt.wantErr {
				t.Errorf("status = %q (error %v), want %q (error %v)", m.status, m.statusIsErr, tt.wantStatus, tt.wantErr)
			}
			if m.loading {
				t.Error("still loading after an error")
			}

			calls := reg.numCalls()
			m, cmd := press(t, m, "j", "G")
			if pageMsg(t, cmd) != nil || reg.numCalls() != calls {
				t.Error("retried automatically after an error")
			}

			reg.errs = nil
			m, cmd = press(t, m, "r")
			m = settle(t, m, cmd)
			if len(m.all) != 10 || m.statusIsErr {
				t.Errorf("after r: %d tags, status %q", len(m.all), m.status)
			}
		})
	}
}

func TestTagsAnonymousPageLimit(t *testing.T) {
	reg := &fakeRegistry{
		tags:     makeTags("t", 30),
		pageSize: 10,
		errs:     map[int]error{3: &hub.APIError{StatusCode: 403, Message: "pagination offset too large for anonymous requests"}},
	}
	m := newTestModel(reg)
	m = settle(t, m, m.Init())

	if len(m.all) != 20 || m.hasNext {
		t.Errorf("loaded %d, hasNext %v; want 20 and no more", len(m.all), m.hasNext)
	}
	if m.statusIsErr || !strings.Contains(m.status, "anonymous limit: first 20 tags") {
		t.Errorf("status = %q (error %v)", m.status, m.statusIsErr)
	}
}

func TestTagsFilter(t *testing.T) {
	tags := []hub.Tag{
		{Name: "1.27-alpine", Pushed: testNow()},
		{Name: "1.27", Pushed: testNow().Add(-time.Hour)},
		{Name: "alpine-perl", Pushed: testNow().Add(-2 * time.Hour)},
		{Name: "1.26-alpine", Pushed: testNow().Add(-3 * time.Hour)},
	}
	reg := &fakeRegistry{tags: tags, pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.Init())

	m, _ = press(t, m, "/")
	if !m.filtering {
		t.Fatal("/ did not open the filter")
	}
	// Keys go to the input, not to the screen: "s" must not change the sort.
	m = typeText(t, m, "^1\\.2[67]-alpine$")
	if m.sort != sortPushed {
		t.Error("typing in the filter triggered a screen key")
	}
	if got := strings.Join(viewNames(m), ","); got != "1.27-alpine,1.26-alpine" {
		t.Errorf("filtered = %s", got)
	}

	// An unfinished regex keeps the last valid filter.
	m = typeText(t, m, "(")
	if m.filterErr == "" {
		t.Error("invalid regex not reported")
	}
	if got := len(m.view); got != 2 {
		t.Errorf("invalid regex changed the view to %d rows", got)
	}
	m, _ = press(t, m, "backspace")
	if m.filterErr != "" {
		t.Errorf("filterErr = %q after fixing the regex", m.filterErr)
	}

	m, _ = press(t, m, "enter")
	if m.filtering || len(m.view) != 2 {
		t.Errorf("enter: filtering %v, %d rows; want closed input, filter kept", m.filtering, len(m.view))
	}
	if !strings.Contains(m.statusLine(), "2 match") {
		t.Errorf("status line %q does not show the match count", m.statusLine())
	}

	m, _ = press(t, m, "esc")
	if m.filterRE != nil || len(m.view) != 4 || m.input.Value() != "" {
		t.Errorf("esc did not clear the filter: %d rows, value %q", len(m.view), m.input.Value())
	}

	m, _ = press(t, m, "/")
	m = typeText(t, m, "perl")
	m, _ = press(t, m, "esc")
	if m.filtering || len(m.view) != 4 {
		t.Errorf("esc while typing: filtering %v, %d rows", m.filtering, len(m.view))
	}
}

func TestTagsFilterLoadsMoreToFindMatches(t *testing.T) {
	tags := makeTags("t", 300)
	tags[250].Name = "needle"
	reg := &fakeRegistry{tags: tags, pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.Init())

	m, _ = press(t, m, "/")
	var cmd tea.Cmd
	for _, r := range "needle" {
		m, cmd = update(t, m, keyMsg(string(r)))
		m = settle(t, m, cmd)
	}
	if got := strings.Join(viewNames(m), ","); got != "needle" {
		t.Errorf("filtered = %q, want the match from page 3", got)
	}
}

func TestTagsSort(t *testing.T) {
	tags := []hub.Tag{
		{Name: "1.10", Pushed: testNow(), Platforms: []hub.Platform{{OS: "linux", Arch: "amd64", Size: 200}}},
		{Name: "1.9", Pushed: testNow().Add(-time.Hour), Platforms: []hub.Platform{{OS: "linux", Arch: "amd64", Size: 300}}},
		{Name: "alpine", Pushed: testNow().Add(-2 * time.Hour), Platforms: []hub.Platform{{OS: "linux", Arch: "amd64", Size: 100}}},
		{Name: "1.9.1", Pushed: testNow().Add(-3 * time.Hour), Platforms: []hub.Platform{{OS: "linux", Arch: "arm64", Size: 400}}},
	}
	reg := &fakeRegistry{tags: tags, pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.Init())

	m, _ = press(t, m, "j") // select 1.9 so we can check it stays selected
	steps := []struct {
		mode sortMode
		want string
	}{
		{sortName, "1.9,1.9.1,1.10,alpine"},
		{sortSize, "1.9.1,1.9,1.10,alpine"},
		{sortPushed, "1.10,1.9,alpine,1.9.1"},
	}
	for _, s := range steps {
		m, _ = press(t, m, "s")
		if m.sort != s.mode {
			t.Fatalf("sort = %v, want %v", m.sort, s.mode)
		}
		if got := strings.Join(viewNames(m), ","); got != s.want {
			t.Errorf("sort %v: %s, want %s", s.mode, got, s.want)
		}
		if got := selected(t, m); got != "1.9" {
			t.Errorf("sort %v moved the selection to %q", s.mode, got)
		}
	}
}

func TestTagsQuitCancelsInFlight(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		t.Run(k, func(t *testing.T) {
			reg := &fakeRegistry{tags: makeTags("t", 10), pageSize: 100}
			m := newTestModel(reg)
			cmd := m.Init()
			_ = pageMsg(t, cmd) // the request has been made but not delivered
			ctx := reg.lastCall().ctx

			_, cmd = press(t, m, k)
			if cmd == nil {
				t.Fatal("no command returned")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Error("did not quit")
			}
			if ctx.Err() == nil {
				t.Error("in-flight request not canceled")
			}
		})
	}
}

func TestTagsQuitWhileFilteringOnlyWithCtrlC(t *testing.T) {
	reg := &fakeRegistry{tags: makeTags("t", 3), pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.Init())
	m, _ = press(t, m, "/")
	m, _ = press(t, m, "q")
	if m.input.Value() != "q" {
		t.Errorf("q while filtering should type, got value %q", m.input.Value())
	}
}

func TestTagsResize(t *testing.T) {
	reg := &fakeRegistry{tags: makeTags("t", 10), pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.Init())

	m, _ = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.table.Height() != 37 {
		t.Errorf("table viewport height = %d, want 37 (40 minus title, status and header)", m.table.Height())
	}
	if got := spannedWidth(m); got != 120 {
		t.Errorf("columns span %d cells, want the full 120", got)
	}

	m, _ = update(t, m, tea.WindowSizeMsg{Width: 60, Height: 15})
	if got := m.table.Columns()[4].Width; got != 0 {
		t.Errorf("digest column width at 60 cells = %d, want hidden", got)
	}
	if got := spannedWidth(m); got != 60 {
		t.Errorf("columns span %d cells, want the full 60", got)
	}
}

func spannedWidth(m TagsModel) int {
	total := 0
	for _, c := range m.table.Columns() {
		if c.Width > 0 {
			total += c.Width + 2
		}
	}
	return total
}
