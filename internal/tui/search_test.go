package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

func repoResults(names ...string) []hub.SearchResult {
	out := make([]hub.SearchResult, len(names))
	for i, n := range names {
		r, err := hub.ParseRepo(n)
		if err != nil {
			panic(err)
		}
		out[i] = hub.SearchResult{Repo: r, Official: r.Official(), Stars: 1000 * (i + 1), Pulls: 1_500_000, Description: "desc\nwith newline"}
	}
	return out
}

func newTestSearch(reg *fakeRegistry) *searchScreen {
	s := newSearchScreen(context.Background(), Deps{Registry: reg, Clipboard: &fakeClipboard{}, Now: testNow}, 1)
	s.update(tea.WindowSizeMsg{Width: 80, Height: 24})
	s.init()
	return s
}

// sendS delivers msg and then any fetch results, until the screen is idle.
func sendS(t *testing.T, s *searchScreen, msg tea.Msg) {
	t.Helper()
	cmd := s.update(msg)
	for range 50 {
		var next tea.Cmd
		found := false
		for _, m := range runCmds(t, cmd) {
			if r, ok := m.(searchResultMsg); ok {
				next, found = s.update(r), true
			}
		}
		if !found {
			return
		}
		cmd = next
	}
	t.Fatal("screen kept fetching")
}

func typeS(t *testing.T, s *searchScreen, text string) {
	t.Helper()
	for _, r := range text {
		s.update(keyMsg(string(r)))
	}
}

// fireDebounce delivers the debounce tick for the latest keystroke, as the
// timer would.
func fireDebounce(t *testing.T, s *searchScreen) {
	t.Helper()
	sendS(t, s, debounceMsg{screen: s.sid, seq: s.debounceSeq})
}

func resultNames(s *searchScreen) string {
	var out []string
	for _, r := range s.results {
		out = append(out, r.Repo.String())
	}
	return strings.Join(out, ",")
}

func TestSearchDebouncesTyping(t *testing.T) {
	reg := &fakeRegistry{repos: repoResults("nginx", "nginx/nginx-ingress", "redis")}
	s := newTestSearch(reg)

	typeS(t, s, "ngi")
	if reg.numSearches() != 0 {
		t.Fatal("searched before typing paused")
	}
	stale := debounceMsg{screen: s.sid, seq: s.debounceSeq - 1}
	sendS(t, s, stale)
	if reg.numSearches() != 0 {
		t.Error("a superseded debounce tick started a search")
	}

	fireDebounce(t, s)
	if reg.numSearches() != 1 || reg.lastSearch().query != "ngi" {
		t.Fatalf("searches = %d, last %+v", reg.numSearches(), reg.lastSearch())
	}
	if opts := reg.lastSearch().opts; opts.Page != 1 || opts.PageSize != searchPageSize || opts.Fresh {
		t.Errorf("request = %+v", opts)
	}
	if got := resultNames(s); got != "nginx,nginx/nginx-ingress" {
		t.Errorf("results = %s", got)
	}
	if !s.inputFocused {
		t.Error("live results stole focus from the input")
	}
}

func TestSearchTypingCancelsInFlight(t *testing.T) {
	reg := &fakeRegistry{repos: repoResults("nginx", "redis")}
	s := newTestSearch(reg)

	typeS(t, s, "ngi")
	cmd := s.update(debounceMsg{screen: s.sid, seq: s.debounceSeq})
	old := runCmds(t, cmd)[0].(searchResultMsg) // fetched, not yet delivered
	oldCtx := reg.lastSearch().ctx

	typeS(t, s, "x")
	if oldCtx.Err() == nil {
		t.Error("typing did not cancel the in-flight search")
	}
	s.update(old)
	if len(s.results) != 0 {
		t.Errorf("stale results applied: %s", resultNames(s))
	}
}

func TestSearchEnterAndOpen(t *testing.T) {
	reg := &fakeRegistry{repos: repoResults("nginx", "grafana/grafana")}
	s := newTestSearch(reg)

	typeS(t, s, "grafana")
	sendS(t, s, keyMsg("enter"))
	if reg.numSearches() != 1 {
		t.Fatalf("enter made %d searches, want 1 without waiting for the debounce", reg.numSearches())
	}
	if s.inputFocused {
		t.Fatal("enter did not move focus to the results")
	}

	cmd := s.update(keyMsg("enter"))
	msgs := runCmds(t, cmd)
	if len(msgs) != 1 {
		t.Fatalf("got %v, want one message", msgs)
	}
	open, ok := msgs[0].(openTagsMsg)
	if !ok || open.repo != (hub.Repo{Namespace: "grafana", Name: "grafana"}) {
		t.Errorf("enter on a result = %#v, want openTagsMsg for grafana/grafana", msgs[0])
	}
}

func TestSearchNewQueryClearsOldResultsAtOnce(t *testing.T) {
	reg := &fakeRegistry{repos: repoResults("nginx", "redis")}
	s := newTestSearch(reg)
	typeS(t, s, "nginx")
	sendS(t, s, keyMsg("enter"))

	s.update(keyMsg("/"))
	for range "nginx" {
		s.update(keyMsg("backspace"))
	}
	typeS(t, s, "redis")
	s.update(keyMsg("enter")) // the request has not answered yet

	if len(s.results) != 0 {
		t.Fatalf("old results still shown: %s", resultNames(s))
	}
	if cmd := s.update(keyMsg("enter")); cmd != nil {
		if _, isOpen := runCmds(t, cmd)[0].(openTagsMsg); isOpen {
			t.Error("enter opened a result from the previous search")
		}
	}
}

func TestSearchLazyPagination(t *testing.T) {
	var names []string
	for i := range 120 {
		names = append(names, fmt.Sprintf("user/app%d", i))
	}
	reg := &fakeRegistry{repos: repoResults(names...)}
	s := newTestSearch(reg)
	typeS(t, s, "app")
	sendS(t, s, keyMsg("enter"))
	if len(s.results) != 50 || !s.hasNext {
		t.Fatalf("loaded %d, hasNext %v", len(s.results), s.hasNext)
	}

	sendS(t, s, keyMsg("G"))
	sendS(t, s, keyMsg("G"))
	if len(s.results) != 120 || s.hasNext {
		t.Errorf("loaded %d, hasNext %v; want all 120", len(s.results), s.hasNext)
	}
	if !strings.Contains(s.statusLine(), "120/120 results") {
		t.Errorf("status line %q", s.statusLine())
	}
}

func TestSearchNoResults(t *testing.T) {
	reg := &fakeRegistry{repos: repoResults("nginx")}
	s := newTestSearch(reg)
	typeS(t, s, "zzz")
	sendS(t, s, keyMsg("enter"))
	if !strings.Contains(s.bar.text, `no repositories match "zzz"`) {
		t.Errorf("status = %q", s.bar.text)
	}
	if !s.inputFocused {
		t.Error("with nothing to select, focus should return to the input")
	}
}

func TestSearchErrorsAndRefresh(t *testing.T) {
	reg := &fakeRegistry{
		repos:      repoResults("nginx"),
		searchErrs: map[int]error{1: &hub.RateLimitError{}},
	}
	s := newTestSearch(reg)
	typeS(t, s, "nginx")
	sendS(t, s, keyMsg("enter"))
	if !s.bar.isErr || !strings.Contains(s.bar.text, "rate limit") {
		t.Fatalf("status = %q", s.bar.text)
	}

	reg.searchErrs = nil
	sendS(t, s, keyMsg("r"))
	if !reg.lastSearch().opts.Fresh {
		t.Error("r did not bypass the cache")
	}
	if got := resultNames(s); got != "nginx" || s.bar.isErr {
		t.Errorf("after r: results %s, status %q", got, s.bar.text)
	}
}

func TestSearchPageLimit(t *testing.T) {
	var names []string
	for i := range 120 {
		names = append(names, fmt.Sprintf("user/app%d", i))
	}
	reg := &fakeRegistry{
		repos:      repoResults(names...),
		searchErrs: map[int]error{2: &hub.APIError{StatusCode: 403, Message: "pagination too large for anonymous requests"}},
	}
	s := newTestSearch(reg)
	typeS(t, s, "app")
	sendS(t, s, keyMsg("enter"))
	sendS(t, s, keyMsg("G"))
	if s.hasNext || s.bar.isErr || s.bar.text != "anonymous limit: first 50 results" {
		t.Errorf("hasNext %v, status %q (error %v)", s.hasNext, s.bar.text, s.bar.isErr)
	}
}

func TestSearchKeys(t *testing.T) {
	reg := &fakeRegistry{repos: repoResults("nginx")}
	s := newTestSearch(reg)

	// In the input, q is just a letter.
	if cmd := s.update(keyMsg("q")); cmd != nil {
		for _, m := range runCmds(t, cmd) {
			if _, quit := m.(tea.QuitMsg); quit {
				t.Fatal("q in the input quit")
			}
		}
	}
	s.update(keyMsg("esc"))
	if s.input.Value() != "" {
		t.Errorf("esc left %q in the input", s.input.Value())
	}

	typeS(t, s, "nginx")
	sendS(t, s, keyMsg("enter"))
	s.update(keyMsg("esc"))
	if !s.inputFocused {
		t.Error("esc in the results did not return to the input")
	}
	s.update(keyMsg("down"))
	if s.inputFocused {
		t.Error("down in the input did not move to the results")
	}
	cmd := s.update(keyMsg("q"))
	if _, quit := runCmds(t, cmd)[0].(tea.QuitMsg); !quit {
		t.Error("q in the results did not quit")
	}
}

func TestSearchRows(t *testing.T) {
	reg := &fakeRegistry{repos: []hub.SearchResult{
		{Repo: hub.Repo{Namespace: "library", Name: "nginx"}, Official: true, Stars: 21396, Pulls: 13413760258, Description: "Official build\nof Nginx."},
		{Repo: hub.Repo{Namespace: "nginx", Name: "nginx-ingress"}, Stars: 122, Pulls: 1087658762},
	}}
	s := newTestSearch(reg)
	typeS(t, s, "nginx")
	sendS(t, s, keyMsg("enter"))
	rows := s.table.Rows()
	want := [][]string{
		{"nginx", "21.4k", "13.4B", "official", "Official build of Nginx."},
		{"nginx/nginx-ingress", "122", "1.1B", "", ""},
	}
	for i, w := range want {
		if strings.Join(rows[i], "|") != strings.Join(w, "|") {
			t.Errorf("row %d = %q, want %q", i, rows[i], w)
		}
	}
}
