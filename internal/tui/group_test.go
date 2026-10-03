package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// Names are from live Docker Hub tags.
func TestGroupKey(t *testing.T) {
	tests := []struct {
		name         string
		major, minor string
	}{
		{"1.31.6-alpine3.24-perl", "1", "1.31"},
		{"1.31", "1", "1.31"},
		{"17.6-bookworm", "17", "17.6"},
		{"17", "17", "17"},
		{"1-alpine", "1", "1"},
		{"3.15.0rc2", "3", "3.15"},
		{"18beta3-trixie", "18", "18"},
		{"v3.5.1", "3", "3.5"},
		{"24.04", "24", "24.04"},
		{"22.10-minimal", "22", "22.10"},
		{"0.9.1", "0", "0.9"},
		{"07.1", "7", "7.1"},
		{"latest", otherGroup, otherGroup},
		{"alpine3.24", otherGroup, otherGroup},
		{"jammy-20260911", otherGroup, otherGroup},
		{"20260918", otherGroup, otherGroup},
		{"v", otherGroup, otherGroup},
		{"", otherGroup, otherGroup},
		{"1.", "1", "1"},
	}
	for _, tt := range tests {
		if got := groupKey(tt.name, groupMajor); got != tt.major {
			t.Errorf("groupKey(%q, major) = %q, want %q", tt.name, got, tt.major)
		}
		if got := groupKey(tt.name, groupMinor); got != tt.minor {
			t.Errorf("groupKey(%q, minor) = %q, want %q", tt.name, got, tt.minor)
		}
	}
}

func TestBuildGroups(t *testing.T) {
	base := testNow()
	all := []hub.Tag{
		{Name: "latest", Pushed: base},
		{Name: "1.9.1", Pushed: base.Add(-5 * time.Hour)},
		{Name: "1.10.0", Pushed: base.Add(-2 * time.Hour)},
		{Name: "1.10.1", Pushed: base.Add(-time.Hour)},
		{Name: "2.0", Pushed: base.Add(-3 * time.Hour)},
		{Name: "bookworm", Pushed: base.Add(-4 * time.Hour)},
		{Name: "1.10", Pushed: base.Add(-time.Hour)}, // ties keep the first seen
	}
	idx := []int{0, 1, 2, 3, 4, 5, 6}

	got := buildGroups(all, idx, groupMinor)
	var keys []string
	for _, g := range got {
		keys = append(keys, g.key)
	}
	if strings.Join(keys, ",") != "2.0,1.10,1.9,other" {
		t.Fatalf("keys = %v, want newest version first, other last", keys)
	}
	if g := got[1]; len(g.tags) != 3 || all[g.newest].Name != "1.10.1" || !g.pushed.Equal(base.Add(-time.Hour)) {
		t.Errorf("1.10 group = %d tags, newest %s at %v", len(g.tags), all[g.newest].Name, g.pushed)
	}
	if g := got[3]; len(g.tags) != 2 || all[g.newest].Name != "latest" {
		t.Errorf("other group = %d tags, newest %s", len(g.tags), all[g.newest].Name)
	}

	// Only the given indices count: filters apply before grouping.
	got = buildGroups(all, []int{1, 4}, groupMajor)
	if len(got) != 2 || got[0].key != "2" || got[1].key != "1" || len(got[1].tags) != 1 {
		t.Errorf("filtered groups = %+v", got)
	}
	if len(buildGroups(all, nil, groupMajor)) != 0 {
		t.Error("no tags should give no groups")
	}
}

// versionTags returns tags newest first with the given names, each with its
// own digest so a yank names exactly one of them.
func versionTags(names ...string) []hub.Tag {
	tags := makeTags("t", len(names))
	for i, n := range names {
		tags[i].Name = n
		tags[i].Digest = "sha256:" + strings.Repeat(string("0123456789abcdef"[i%16]), 64)
		tags[i].MediaType = "application/vnd.oci.image.index.v1+json"
	}
	return tags
}

func rowKeys(m *tagsScreen) string {
	var keys []string
	for _, r := range m.table.Rows() {
		keys = append(keys, r[0])
	}
	return strings.Join(keys, ",")
}

func TestTagsGroupingCycle(t *testing.T) {
	reg := &fakeRegistry{tags: versionTags("latest", "1.31.6", "1.31", "1.30.2", "2.0.1", "alpine", "1.30"), pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.init())

	m, _ = press(t, m, "v")
	if m.grouping != groupMajor || rowKeys(m) != "2,1,other" {
		t.Fatalf("major: grouping %v, rows %s", m.grouping, rowKeys(m))
	}
	if r := m.table.Rows()[1]; r[1] != "4" || r[2] != "1.31.6" {
		t.Errorf("group 1 row = %v, want 4 tags, newest 1.31.6", r)
	}
	v := stripANSI(m.view())
	if !strings.Contains(v, "nginx · tags by major version") || !strings.Contains(v, "3 groups") || strings.Contains(v, "sort:") {
		t.Errorf("view:\n%s", v)
	}
	if n := strings.Count(v, "\n") + 1; n != 24 {
		t.Errorf("group view is %d lines at 80x24", n)
	}

	m, _ = press(t, m, "v")
	if m.grouping != groupMinor || rowKeys(m) != "2.0,1.31,1.30,other" {
		t.Fatalf("minor: grouping %v, rows %s", m.grouping, rowKeys(m))
	}
	m, _ = press(t, m, "v")
	if m.grouping != groupOff || len(m.table.Rows()) != 7 || m.table.Rows()[0][0] != "latest" {
		t.Errorf("off: grouping %v, rows %s", m.grouping, rowKeys(m))
	}
}

func TestTagsOpenGroupAndBack(t *testing.T) {
	tags := versionTags("latest", "1.31.6", "1.31", "1.30.2", "2.0.1", "1.30")
	reg := &fakeRegistry{tags: tags, pageSize: 100}
	cb := &fakeClipboard{}
	m := newTestModelFor(reg, cb, hub.Repo{Namespace: "library", Name: "nginx"})
	m = settle(t, m, m.init())
	m, _ = press(t, m, "v", "v", "j", "j") // minor; cursor on 1.30

	m, _ = press(t, m, "enter")
	if m.openGroup != "1.30" || rowKeys(m) != "1.30.2,1.30" {
		t.Fatalf("open: group %q, rows %s", m.openGroup, rowKeys(m))
	}
	if !strings.Contains(stripANSI(m.view()), "nginx · tags · 1.30") {
		t.Error("title does not name the open group")
	}

	// Inside a group the yank keys work on tags as usual, exactly.
	m, _ = press(t, m, "j")
	msgs := runCmds(t, m.update(keyMsg("Y")))
	want := "nginx:1.30@" + tags[5].Digest
	if payload, _ := osc52Payload(msgs); payload != want {
		t.Errorf("Y in group = %q, want %q", payload, want)
	}

	// enter on a tag inside a group opens its detail, not another group.
	_, cmd := press(t, m, "enter")
	if open, ok := runCmds(t, cmd)[0].(openDetailMsg); !ok || open.tag.Name != "1.30" {
		t.Errorf("enter in group did not open the tag detail")
	}

	// A filter inside the group is undone before the group closes.
	m, _ = press(t, m, "/")
	m = typeText(t, m, "2$")
	m, _ = press(t, m, "enter")
	if rowKeys(m) != "1.30.2" {
		t.Fatalf("filter in group: rows %s", rowKeys(m))
	}
	m, _ = press(t, m, "esc")
	if m.openGroup != "1.30" || rowKeys(m) != "1.30.2,1.30" {
		t.Fatalf("first esc: group %q, rows %s; want the filter cleared, group kept", m.openGroup, rowKeys(m))
	}

	m, cmd = press(t, m, "esc")
	if m.openGroup != "" || cmd != nil {
		t.Fatalf("second esc: group %q, cmd %v; want back at the group list", m.openGroup, cmd)
	}
	if g, ok := m.selectedGroup(); !ok || g.key != "1.30" {
		t.Errorf("cursor on %+v after closing, want the group just left", g)
	}
	if _, ok := runCmds(t, m.update(keyMsg("esc")))[0].(backMsg); !ok {
		t.Error("esc from the group list did not go back")
	}
}

func TestTagsGroupListYankAndOpen(t *testing.T) {
	cb := &fakeClipboard{}
	op := &fakeOpener{}
	reg := &fakeRegistry{tags: versionTags("1.31", "1.30"), pageSize: 100}
	m := newTagsScreen(context.Background(), Deps{Registry: reg, Clipboard: cb, Browser: op, Now: testNow}, 1, hub.Repo{Namespace: "library", Name: "nginx"})
	m.update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = settle(t, m, m.init())
	m, _ = press(t, m, "v")
	for _, k := range []string{"y", "Y", "p"} {
		msgs := runCmds(t, m.update(keyMsg(k)))
		if _, ok := osc52Payload(msgs); ok || len(cb.copied) != 0 {
			t.Errorf("%s on a group copied something", k)
		}
		if m.bar.text != "open a group to yank one of its tags" || !m.bar.isErr {
			t.Errorf("%s status = %q", k, m.bar.text)
		}
	}
	runCmds(t, m.update(keyMsg("o")))
	if len(op.opened) != 1 || op.opened[0] != "https://hub.docker.com/_/nginx" {
		t.Errorf("o on a group opened %v, want the repository page", op.opened)
	}
}

// Filters narrow the tags before they are grouped.
func TestTagsGroupingAfterFilters(t *testing.T) {
	reg := &fakeRegistry{tags: versionTags("3.15.0rc2", "3.15-rc", "3.14.2", "3.14", "3.13.9", "latest"), pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.init())
	m, _ = press(t, m, "u", "v", "v")
	if rowKeys(m) != "3.14,3.13,other" {
		t.Errorf("stable groups = %s", rowKeys(m))
	}
	m.applyFilter(`^3\.1[34]`)
	if rowKeys(m) != "3.14,3.13" || m.table.Rows()[0][1] != "2" {
		t.Errorf("filtered groups = %s, rows %v", rowKeys(m), m.table.Rows())
	}
}

// Groups are few, so the list keeps loading pages until it can fill the
// screen or the tags run out, as filters do.
func TestTagsGroupingLoadsMore(t *testing.T) {
	names := make([]string, 250)
	for i := range names {
		names[i] = fmt.Sprintf("1.%d.%d", 30-i/50, i%50)
	}
	reg := &fakeRegistry{tags: versionTags(names...), pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.init())
	if len(m.all) != 100 {
		t.Fatalf("initial load %d tags, want one page", len(m.all))
	}
	for range 2 {
		var cmd tea.Cmd
		m, cmd = press(t, m, "v")
		m = settle(t, m, cmd)
	}
	if len(m.all) != 250 || rowKeys(m) != "1.30,1.29,1.28,1.27,1.26" {
		t.Errorf("loaded %d tags, groups %s", len(m.all), rowKeys(m))
	}
}

// Switching between the four group columns and the five tag columns, and
// resizing in between, must never leave rows that do not fit the columns.
func TestTagsGroupingColumnSwitches(t *testing.T) {
	reg := &fakeRegistry{tags: versionTags("1.31", "1.30", "latest"), pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.init())
	for _, k := range []string{"v", "enter", "esc", "v", "v", "v", "enter", "v"} {
		m, _ = press(t, m, k)
		for _, w := range []int{80, 50, 120} {
			m.update(tea.WindowSizeMsg{Width: w, Height: 24})
			cols := len(m.table.Columns())
			for _, r := range m.table.Rows() {
				if len(r) != cols {
					t.Fatalf("after %s at width %d: row %v for %d columns", k, w, r, cols)
				}
			}
			_ = m.view()
		}
	}
}

func TestTagsGroupingCursor(t *testing.T) {
	// "1" is a group at both levels: the major line, and at the minor level
	// the floating 1-alpine tags, which sort below every 1.x.
	reg := &fakeRegistry{tags: versionTags("1.31.6", "1.31", "1-alpine", "1.30.2", "1.29.1", "2.0"), pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.init())

	m, _ = press(t, m, "j", "j", "j") // 1.30.2
	m, _ = press(t, m, "v")
	if g, ok := m.selectedGroup(); !ok || g.key != "1" {
		t.Errorf("off -> major: cursor on %q, want the selected tag's group", g.key)
	}
	m, _ = press(t, m, "v")
	if g, ok := m.selectedGroup(); !ok || g.key != "2.0" || m.table.Cursor() != 0 {
		t.Errorf("major -> minor: cursor %d on %q, want the top", m.table.Cursor(), g.key)
	}
	m, _ = press(t, m, "j", "j")
	m, _ = press(t, m, "v")
	if m.table.Cursor() != 0 {
		t.Errorf("minor -> off: cursor %d, want the top", m.table.Cursor())
	}
}
