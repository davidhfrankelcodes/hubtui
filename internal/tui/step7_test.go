package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/browser"
	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

type fakeOpener struct {
	mu     sync.Mutex
	opened []string
	err    error
}

func (o *fakeOpener) Open(_ context.Context, url string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.opened = append(o.opened, url)
	return o.err
}

func platformTag(name string, plats ...hub.Platform) hub.Tag {
	return hub.Tag{
		Name:      name,
		Digest:    pinnedDigest,
		MediaType: "application/vnd.oci.image.index.v1+json",
		Pushed:    testNow(),
		Platforms: plats,
	}
}

func amd64() hub.Platform {
	return hub.Platform{OS: "linux", Arch: "amd64", Digest: "sha256:" + strings.Repeat("a", 64), Size: 100}
}

func arm64() hub.Platform {
	return hub.Platform{OS: "linux", Arch: "arm64", Variant: "v8", Digest: "sha256:" + strings.Repeat("b", 64), Size: 90}
}

func armv7() hub.Platform {
	return hub.Platform{OS: "linux", Arch: "arm", Variant: "v7", Digest: "sha256:" + strings.Repeat("c", 64), Size: 80}
}

func win() hub.Platform {
	return hub.Platform{OS: "windows", Arch: "amd64", Digest: "sha256:" + strings.Repeat("d", 64), Size: 500}
}

func TestNextArch(t *testing.T) {
	opts := []string{"linux/amd64", "linux/arm64/v8", "windows/amd64"}
	steps := []string{"linux/amd64", "linux/arm64/v8", "windows/amd64", "", "linux/amd64"}
	cur := ""
	for i, want := range steps {
		cur = nextArch(opts, cur)
		if cur != want {
			t.Fatalf("step %d: %q, want %q", i, cur, want)
		}
	}
	if got := nextArch(nil, ""); got != "" {
		t.Errorf("no options: %q", got)
	}
	if got := nextArch(opts, "linux/s390x"); got != "" {
		t.Errorf("unknown current: %q, want back to all", got)
	}
}

func TestTagsArchFilter(t *testing.T) {
	reg := &fakeRegistry{pageSize: 100, tags: []hub.Tag{
		platformTag("multi", amd64(), arm64(), armv7()),
		platformTag("x86-only", amd64()),
		platformTag("windows", win()),
		platformTag("arm-only", armv7(), arm64()),
	}}
	m := newTestModel(reg)
	m = settle(t, m, m.init())

	if got := strings.Join(m.archOptions(), ","); got != "linux/amd64,linux/arm64/v8,linux/arm/v7,windows/amd64" {
		t.Fatalf("options = %s", got)
	}

	steps := []struct {
		arch, names, firstSize string
	}{
		{"linux/amd64", "multi,x86-only", "100 B"},
		{"linux/arm64/v8", "multi,arm-only", "90 B"},
		{"linux/arm/v7", "multi,arm-only", "80 B"},
		{"windows/amd64", "windows", "500 B"},
		{"", "multi,x86-only,windows,arm-only", "100 B"},
	}
	for _, s := range steps {
		m, _ = press(t, m, "a")
		if m.arch != s.arch {
			t.Fatalf("arch = %q, want %q", m.arch, s.arch)
		}
		if got := strings.Join(viewNames(m), ","); got != s.names {
			t.Errorf("arch %q: rows %s, want %s", s.arch, got, s.names)
		}
		if got := m.table.Rows()[0][2]; got != s.firstSize {
			t.Errorf("arch %q: size column %q, want the filtered platform's %q", s.arch, got, s.firstSize)
		}
		wantLabel := s.arch != ""
		if got := strings.Contains(m.statusLine(), "arch: "+shortPlatform(s.arch)); got != wantLabel {
			t.Errorf("arch %q: status line %q", s.arch, m.statusLine())
		}
	}
}

func TestTagsArchFilterLoadsMore(t *testing.T) {
	tags := makeTags("t", 250)
	tags[220].Platforms = []hub.Platform{armv7()}
	reg := &fakeRegistry{tags: tags, pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.init())

	// Filter to a platform that only a tag on page 3 has; the screen should
	// keep loading until it finds it.
	m.arch = "linux/arm/v7"
	m.rebuild()
	m = settle(t, m, m.maybeLoadMore())
	if got := strings.Join(viewNames(m), ","); got != "t220" {
		t.Errorf("rows = %s, want the arm/v7 tag from page 3", got)
	}
}

func TestTagsEnterOpensDetail(t *testing.T) {
	reg := &fakeRegistry{tags: makeTags("t", 3), pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.init())
	m, cmd := press(t, m, "j", "enter")
	msgs := runCmds(t, cmd)
	open, ok := msgs[0].(openDetailMsg)
	if !ok || open.tag.Name != "t1" || open.repo != m.repo {
		t.Errorf("enter = %#v, want openDetailMsg for t1", msgs[0])
	}
}

func TestOpenInBrowser(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus string
		wantErr    bool
	}{
		{name: "opened", wantStatus: "opened https://hub.docker.com/_/nginx/tags?name=t0"},
		{name: "no browser shows the URL", err: fmt.Errorf("remote session: %w", browser.ErrUnavailable), wantStatus: "https://hub.docker.com/_/nginx/tags?name=t0"},
		{name: "launcher failed", err: errors.New("xdg-open: exit status 3"), wantStatus: "could not open browser: xdg-open: exit status 3", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := &fakeOpener{err: tt.err}
			reg := &fakeRegistry{tags: makeTags("t", 2), pageSize: 100}
			m := newTagsScreen(context.Background(), Deps{Registry: reg, Clipboard: &fakeClipboard{}, Browser: op, Now: testNow}, 1, hub.Repo{Namespace: "library", Name: "nginx"})
			m = settle(t, m, m.init())
			_, cmd := press(t, m, "o")
			for _, msg := range runCmds(t, cmd) {
				m.update(msg)
			}
			if len(op.opened) != 1 || op.opened[0] != "https://hub.docker.com/_/nginx/tags?name=t0" {
				t.Errorf("opened %v", op.opened)
			}
			if m.bar.text != tt.wantStatus || m.bar.isErr != tt.wantErr {
				t.Errorf("status = %q (error %v), want %q (error %v)", m.bar.text, m.bar.isErr, tt.wantStatus, tt.wantErr)
			}
		})
	}
}

func TestSearchOpenInBrowser(t *testing.T) {
	op := &fakeOpener{}
	reg := &fakeRegistry{repos: repoResults("grafana/grafana")}
	s := newSearchScreen(context.Background(), Deps{Registry: reg, Clipboard: &fakeClipboard{}, Browser: op, Now: testNow}, 1)
	s.update(tea.WindowSizeMsg{Width: 80, Height: 24})
	s.init()
	typeS(t, s, "grafana")
	sendS(t, s, keyMsg("enter"))
	runCmds(t, s.update(keyMsg("o")))
	if len(op.opened) != 1 || op.opened[0] != "https://hub.docker.com/r/grafana/grafana" {
		t.Errorf("opened %v", op.opened)
	}
}

func newTestDetail(cb *fakeClipboard, repo hub.Repo, tag hub.Tag) *detailScreen {
	return newTestDetailAliases(cb, repo, tag, aliasInfo{})
}

func newTestDetailAliases(cb *fakeClipboard, repo hub.Repo, tag hub.Tag, aliases aliasInfo) *detailScreen {
	d := newDetailScreen(context.Background(), Deps{Registry: &fakeRegistry{}, Clipboard: cb, Browser: &fakeOpener{}, Now: testNow}, 7, repo, tag, aliases)
	d.update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return d
}

func TestDetailRows(t *testing.T) {
	d := newTestDetail(&fakeClipboard{}, hub.Repo{Namespace: "library", Name: "nginx"}, platformTag("1.27", amd64(), arm64(), win()))
	rows := d.table.Rows()
	if len(rows) != 3 {
		t.Fatalf("%d rows, want one per platform", len(rows))
	}
	if rows[0][0] != "linux/amd64" || rows[1][0] != "linux/arm64/v8" || rows[2][0] != "windows/amd64" {
		t.Errorf("platforms = %v", rows)
	}
	if rows[1][2] != "90 B" {
		t.Errorf("arm64 size = %q", rows[1][2])
	}

	// At 80 columns the digest column cannot fit 71 characters; both ends stay.
	dg := rows[1][1]
	if !strings.HasPrefix(dg, "sha256:bbb") || !strings.HasSuffix(dg, "bbb") || !strings.Contains(dg, "…") {
		t.Errorf("narrow digest = %q", dg)
	}
	d.update(tea.WindowSizeMsg{Width: 120, Height: 24})
	if got := d.table.Rows()[1][1]; got != arm64().Digest {
		t.Errorf("wide digest = %q, want it in full", got)
	}

	info := stripANSI(strings.Join(d.infoLines(), "\n"))
	for _, want := range []string{pinnedDigest, "OCI image index (multi-platform)", "just now (2026-10-02 12:00 UTC)"} {
		if !strings.Contains(info, want) {
			t.Errorf("info lines %q lack %q", info, want)
		}
	}
	if v := d.view(); !strings.Contains(stripANSI(v), "3 platforms") {
		t.Error("status line does not count platforms")
	}
}

func TestDetailWithoutPlatforms(t *testing.T) {
	legacy := hub.Tag{Name: "1.9.8", MediaType: "application/vnd.docker.distribution.manifest.v1+prettyjws"}
	d := newTestDetail(&fakeClipboard{}, hub.Repo{Namespace: "library", Name: "nginx"}, legacy)
	v := stripANSI(d.view())
	for _, want := range []string{"not reported by Docker Hub", "legacy", "no platform details"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q", want)
		}
	}
}

// TestDetailYank is exact-match: the detail screen must copy the same
// references as the Tags screen, with the index digest for Y.
func TestDetailYank(t *testing.T) {
	tests := []struct{ key, want string }{
		{"y", "grafana/grafana:12.0"},
		{"Y", "grafana/grafana:12.0@" + pinnedDigest},
		{"p", "docker pull grafana/grafana:12.0"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			cb := &fakeClipboard{}
			d := newTestDetail(cb, hub.Repo{Namespace: "grafana", Name: "grafana"}, platformTag("12.0", amd64(), arm64()))
			d.table.SetCursor(1) // a platform row is selected; the yank is still the tag
			msgs := runCmds(t, d.update(keyMsg(tt.key)))
			if payload, _ := osc52Payload(msgs); payload != tt.want {
				t.Errorf("OSC 52 = %q, want %q", payload, tt.want)
			}
			if len(cb.copied) != 1 || cb.copied[0] != tt.want {
				t.Errorf("native = %v, want %q", cb.copied, tt.want)
			}
			if d.bar.text != "copied "+tt.want {
				t.Errorf("status = %q", d.bar.text)
			}
		})
	}
}

func TestDetailKeys(t *testing.T) {
	d := newTestDetail(&fakeClipboard{}, hub.Repo{Namespace: "library", Name: "nginx"}, platformTag("1.27", amd64()))
	if _, ok := runCmds(t, d.update(keyMsg("esc")))[0].(backMsg); !ok {
		t.Error("esc did not go back")
	}
	if _, ok := runCmds(t, d.update(keyMsg("q")))[0].(tea.QuitMsg); !ok {
		t.Error("q did not quit")
	}
	op := d.deps.Browser.(*fakeOpener)
	runCmds(t, d.update(keyMsg("o")))
	if len(op.opened) != 1 || op.opened[0] != "https://hub.docker.com/_/nginx/tags?name=1.27" {
		t.Errorf("opened %v", op.opened)
	}
}

func TestAppDetailNavigation(t *testing.T) {
	reg := &fakeRegistry{pageSize: 100, tags: []hub.Tag{platformTag("1.27", amd64(), arm64())}}
	a := newTestApp(t, reg, &hub.Repo{Namespace: "library", Name: "nginx"})
	sendA(t, a, keyMsg("enter"))
	d, ok := a.top().(*detailScreen)
	if !ok {
		t.Fatalf("top is %T, want detail", a.top())
	}
	if d.tag.Name != "1.27" || d.width != 100 {
		t.Errorf("detail for %q at width %d", d.tag.Name, d.width)
	}
	if v := a.View(); v.WindowTitle != "hubtui · nginx:1.27" {
		t.Errorf("title %q", v.WindowTitle)
	}
	sendA(t, a, keyMsg("esc"))
	if _, ok := a.top().(*tagsScreen); !ok || len(a.stack) != 1 {
		t.Errorf("esc from detail: top %T, stack %d", a.top(), len(a.stack))
	}
}

func TestAppHelpOverlay(t *testing.T) {
	reg := &fakeRegistry{pageSize: 100, tags: makeTags("t", 3)}
	a := newTestApp(t, reg, &hub.Repo{Namespace: "library", Name: "nginx"})

	sendA(t, a, keyMsg("?"))
	if !a.showHelp {
		t.Fatal("? did not open help")
	}
	view := stripANSI(a.View().Content)
	for _, want := range []string{"keys · nginx", "cycle architecture filter", "press any key to close"} {
		if !strings.Contains(view, want) {
			t.Errorf("help view lacks %q", want)
		}
	}
	if !strings.Contains(view, "TAG") {
		t.Error("help replaced the screen instead of overlaying it")
	}
	for i, line := range strings.Split(view, "\n") {
		if w := len([]rune(line)); w > 100 {
			t.Errorf("line %d is %d wide in a 100-column window", i, w)
		}
	}

	// The key that closes help must not also act: q would otherwise quit.
	_, cmd := a.Update(keyMsg("q"))
	if a.showHelp || cmd != nil {
		t.Errorf("closing help: showHelp %v, cmd %v", a.showHelp, cmd)
	}
}

func TestAppHelpWhileTyping(t *testing.T) {
	a := newTestApp(t, &fakeRegistry{}, nil)
	sendA(t, a, keyMsg("?"))
	if a.showHelp {
		t.Error("? in the search box opened help instead of typing")
	}
	if got := a.top().(*searchScreen).input.Value(); got != "?" {
		t.Errorf("input = %q", got)
	}
}

func TestAppHelpFitsAt80x24(t *testing.T) {
	reg := &fakeRegistry{pageSize: 100, tags: makeTags("t", 3)}
	a := newTestApp(t, reg, &hub.Repo{Namespace: "library", Name: "nginx"})
	sendA(t, a, tea.WindowSizeMsg{Width: 80, Height: 24})
	sendA(t, a, keyMsg("?"))
	lines := strings.Split(stripANSI(a.View().Content), "\n")
	if len(lines) > 24 {
		t.Errorf("%d lines at 24 rows", len(lines))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "press any key to close") {
		t.Error("help box is cut off at 80x24")
	}
}

func TestSignedInWording(t *testing.T) {
	reg := &fakeRegistry{
		repos:      repoResults("nginx"),
		tags:       makeTags("t", 30),
		pageSize:   10,
		errs:       map[int]error{2: &hub.APIError{StatusCode: 403, Message: "pagination offset too large"}},
		searchErrs: nil,
	}
	deps := Deps{Registry: reg, Clipboard: &fakeClipboard{}, Browser: &fakeOpener{}, Now: testNow, User: "alice"}

	s := newSearchScreen(context.Background(), deps, 1)
	if !strings.Contains(stripANSI(s.view()), "signed in as alice") {
		t.Error("search title does not show the account")
	}

	m := newTagsScreen(context.Background(), deps, 2, hub.Repo{Namespace: "library", Name: "nginx"})
	m.update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = settle(t, m, m.init())
	if m.bar.text != "Docker Hub stopped after 10 tags" {
		t.Errorf("signed-in page limit status = %q; the anonymous wording would be wrong", m.bar.text)
	}
}
