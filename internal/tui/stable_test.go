package tui

import (
	"strings"
	"testing"
)

func TestTagsStableToggle(t *testing.T) {
	tags := makeTags("t", 6)
	for i, name := range []string{"3.15.0rc2", "latest", "3.15.0b4-slim", "3.14.2", "nightly", "3.14.2-alpine"} {
		tags[i].Name = name
	}
	reg := &fakeRegistry{tags: tags, pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.init())
	m, _ = press(t, m, "j") // select latest, which stays visible

	m, _ = press(t, m, "u")
	if !m.stable {
		t.Fatal("u did not turn the stable filter on")
	}
	if got := strings.Join(viewNames(m), ","); got != "latest,3.14.2,3.14.2-alpine" {
		t.Errorf("stable rows = %s", got)
	}
	if name := m.selectedName(); name != "latest" {
		t.Errorf("selection moved to %q", name)
	}
	if !strings.Contains(stripANSI(m.statusLine()), "stable") {
		t.Error("status line does not show the stable filter")
	}

	// It stacks with the regex filter.
	m.applyFilter("^3")
	if got := strings.Join(viewNames(m), ","); got != "3.14.2,3.14.2-alpine" {
		t.Errorf("stable + /^3/ rows = %s", got)
	}

	m.clearFilter()
	m, _ = press(t, m, "u")
	if m.stable || len(m.visible) != 6 {
		t.Errorf("u again: stable %v, %d rows, want all 6", m.stable, len(m.visible))
	}
}

// TestTagsStableKeepsLoading checks that hiding most tags pulls in more pages,
// as the regex and arch filters do.
func TestTagsStableKeepsLoading(t *testing.T) {
	tags := makeTags("rc", 300) // "rc0", "rc1", … are all prereleases
	tags[250].Name = "1.0"
	reg := &fakeRegistry{tags: tags, pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.init())
	m, cmd := press(t, m, "u")
	m = settle(t, m, cmd)
	if got := strings.Join(viewNames(m), ","); got != "1.0" {
		t.Errorf("rows = %s, want the stable tag from page 3", got)
	}
}
