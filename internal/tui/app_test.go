package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// sendA delivers msg to the app and then every fetch result it produces,
// until nothing is fetching.
func sendA(t *testing.T, a *App, msg tea.Msg) {
	t.Helper()
	_, cmd := a.Update(msg)
	for range 50 {
		var next []tea.Cmd
		for _, m := range runCmds(t, cmd) {
			switch m.(type) {
			case tagsPageMsg, searchResultMsg, openTagsMsg, openDetailMsg, backMsg:
				_, c := a.Update(m)
				next = append(next, c)
			}
		}
		if len(next) == 0 {
			return
		}
		cmd = tea.Batch(next...)
	}
	t.Fatal("app kept fetching")
}

func newTestApp(t *testing.T, reg *fakeRegistry, repo *hub.Repo) *App {
	t.Helper()
	a := NewApp(context.Background(), Deps{Registry: reg, Clipboard: &fakeClipboard{}, Now: testNow}, repo)
	sendA(t, a, tea.WindowSizeMsg{Width: 100, Height: 30})
	_, _ = a.Update(nil)
	cmd := a.Init()
	for _, m := range runCmds(t, cmd) {
		sendA(t, a, m)
	}
	return a
}

func topTags(t *testing.T, a *App) *tagsScreen {
	t.Helper()
	ts, ok := a.top().(*tagsScreen)
	if !ok {
		t.Fatalf("top screen is %T, want tags", a.top())
	}
	return ts
}

func TestAppSearchToTagsAndBack(t *testing.T) {
	reg := &fakeRegistry{repos: repoResults("nginx", "grafana/grafana"), tags: makeTags("t", 5), pageSize: 100}
	a := newTestApp(t, reg, nil)
	search, ok := a.top().(*searchScreen)
	if !ok {
		t.Fatalf("starts on %T, want search", a.top())
	}

	for _, r := range "grafana" {
		sendA(t, a, keyMsg(string(r)))
	}
	sendA(t, a, keyMsg("enter"))
	sendA(t, a, keyMsg("enter"))

	tags := topTags(t, a)
	if tags.repo != (hub.Repo{Namespace: "grafana", Name: "grafana"}) {
		t.Errorf("opened %v", tags.repo)
	}
	if len(tags.all) != 5 {
		t.Errorf("tags screen loaded %d tags", len(tags.all))
	}
	if tags.width != 100 || tags.height != 30 {
		t.Errorf("new screen is %dx%d, want the window size", tags.width, tags.height)
	}
	if v := a.View(); !v.AltScreen || v.WindowTitle != "hubtui · grafana/grafana" {
		t.Errorf("view: alt %v, title %q", v.AltScreen, v.WindowTitle)
	}

	sendA(t, a, keyMsg("esc"))
	if a.top() != search || len(a.stack) != 1 {
		t.Fatalf("esc did not return to search: stack %d", len(a.stack))
	}
	if search.query != "grafana" || len(search.results) != 1 || search.inputFocused {
		t.Errorf("search lost its state: query %q, %d results, input focused %v", search.query, len(search.results), search.inputFocused)
	}
}

func TestAppEscClearsFilterBeforeLeaving(t *testing.T) {
	reg := &fakeRegistry{repos: repoResults("nginx"), tags: makeTags("t", 5), pageSize: 100}
	a := newTestApp(t, reg, nil)
	sendA(t, a, openTagsMsg{repo: hub.Repo{Namespace: "library", Name: "nginx"}})

	sendA(t, a, keyMsg("/"))
	sendA(t, a, keyMsg("1"))
	sendA(t, a, keyMsg("enter"))
	sendA(t, a, keyMsg("esc"))
	if len(a.stack) != 2 || topTags(t, a).filterRE != nil {
		t.Fatalf("first esc should clear the filter and stay: stack %d", len(a.stack))
	}
	sendA(t, a, keyMsg("esc"))
	if len(a.stack) != 1 {
		t.Error("second esc did not go back")
	}
}

func TestAppDirectTagsHasNoBack(t *testing.T) {
	reg := &fakeRegistry{tags: makeTags("t", 5), pageSize: 100}
	a := newTestApp(t, reg, &hub.Repo{Namespace: "library", Name: "nginx"})
	if len(topTags(t, a).all) != 5 {
		t.Fatal("tags did not load")
	}
	sendA(t, a, keyMsg("esc"))
	if len(a.stack) != 1 {
		t.Errorf("stack = %d after esc on the only screen", len(a.stack))
	}
}

func TestAppNavigatingAwayCancelsRequests(t *testing.T) {
	reg := &fakeRegistry{repos: repoResults("nginx"), tags: makeTags("t", 300), pageSize: 100}
	a := newTestApp(t, reg, nil)
	sendA(t, a, openTagsMsg{repo: hub.Repo{Namespace: "library", Name: "nginx"}})
	tags := topTags(t, a)

	// Start a page-2 load and leave before it is delivered.
	_, cmd := a.Update(keyMsg("G"))
	late := runCmds(t, cmd)
	ctx := reg.lastCall().ctx
	sendA(t, a, keyMsg("esc"))
	if ctx.Err() == nil {
		t.Error("leaving the screen did not cancel its request")
	}

	// The late page must not reach the closed screen, nor a new screen for
	// the same repository whose request IDs restart.
	sendA(t, a, openTagsMsg{repo: hub.Repo{Namespace: "library", Name: "nginx"}})
	fresh := topTags(t, a)
	before := len(fresh.all)
	for _, m := range late {
		if pm, ok := m.(tagsPageMsg); ok {
			if pm.screen == fresh.sid {
				t.Fatal("new screen reused the closed screen's ID")
			}
			pm.id = fresh.reqID // even with a colliding request ID
			sendA(t, a, pm)
		}
	}
	if len(fresh.all) != before || len(tags.all) != 100 {
		t.Errorf("late page delivered: new screen %d→%d tags, old screen %d", before, len(fresh.all), len(tags.all))
	}
}

func TestAppCoveredSearchResumesLoading(t *testing.T) {
	var names []string
	for i := range 120 {
		names = append(names, "user/app"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	reg := &fakeRegistry{repos: repoResults(names...), tags: makeTags("t", 3), pageSize: 100}
	a := newTestApp(t, reg, nil)
	search := a.top().(*searchScreen)
	for _, r := range "app" {
		sendA(t, a, keyMsg(string(r)))
	}
	sendA(t, a, keyMsg("enter"))

	// Reach the bottom so page 2 starts, then open a repo before it lands.
	_, cmd := a.Update(keyMsg("G"))
	pending := runCmds(t, cmd)
	searchCtx := reg.lastSearch().ctx
	sendA(t, a, openTagsMsg{repo: hub.Repo{Namespace: "library", Name: "nginx"}})
	if searchCtx.Err() == nil {
		t.Error("covering the search screen did not cancel its request")
	}
	for _, m := range pending {
		sendA(t, a, m)
	}
	if len(search.results) != 50 {
		t.Errorf("covered screen applied a canceled page: %d results", len(search.results))
	}

	sendA(t, a, keyMsg("esc"))
	if len(search.results) != 100 {
		t.Errorf("after returning, %d results; the interrupted page should load", len(search.results))
	}
}

func TestAppResizeReachesCoveredScreens(t *testing.T) {
	reg := &fakeRegistry{repos: repoResults("nginx"), tags: makeTags("t", 3), pageSize: 100}
	a := newTestApp(t, reg, nil)
	sendA(t, a, openTagsMsg{repo: hub.Repo{Namespace: "library", Name: "nginx"}})
	sendA(t, a, tea.WindowSizeMsg{Width: 90, Height: 20})
	search := a.stack[0].(*searchScreen)
	if search.width != 90 || search.height != 20 {
		t.Errorf("covered search is %dx%d", search.width, search.height)
	}
}

func TestAppCtrlCQuitsFromAnyScreen(t *testing.T) {
	reg := &fakeRegistry{repos: repoResults("nginx"), tags: makeTags("t", 300), pageSize: 100}
	a := newTestApp(t, reg, nil)
	sendA(t, a, keyMsg("/")) // typing in the search box
	_, cmd := a.Update(keyMsg("ctrl+c"))
	if _, ok := runCmds(t, cmd)[0].(tea.QuitMsg); !ok {
		t.Error("ctrl+c did not quit")
	}
}
