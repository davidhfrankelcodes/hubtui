package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

func TestTagsEnterPassesAliases(t *testing.T) {
	tags := makeTags("t", 4)
	// Natural order puts 1.9 before 1.10; server order is newest first.
	tags[0].Name, tags[2].Name, tags[3].Name = "latest", "1.10", "1.9"
	tags[2].Digest, tags[3].Digest = tags[0].Digest, tags[0].Digest

	tests := []struct {
		name        string
		pageSize    int
		filler      int
		capped      bool // Hub stopped paging before the total
		wantPartial bool
	}{
		{"all loaded", 100, 10, false, false},
		{"more pages", 4, 100, false, true},
		{"stopped by the paging cap", 100, 10, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filler := makeTags("x", tt.filler)
			for i := range filler {
				filler[i].Digest = fmt.Sprintf("sha256:%064x", 1000+i)
			}
			all := append(slices.Clone(tags), filler...)
			reg := &fakeRegistry{tags: all, pageSize: tt.pageSize}
			m := newTestModel(reg)
			m = settle(t, m, m.init())
			if tt.capped {
				m.total = len(all) + 900
			}
			_, cmd := press(t, m, "enter")
			open, ok := runCmds(t, cmd)[0].(openDetailMsg)
			if !ok || open.tag.Name != "latest" {
				t.Fatalf("enter did not open latest")
			}
			if got := strings.Join(open.aliases.names, ","); got != "1.9,1.10" {
				t.Errorf("aliases = %s, want 1.9,1.10", got)
			}
			if open.aliases.partial != tt.wantPartial || open.aliases.loaded != len(m.all) {
				t.Errorf("partial %v loaded %d, want %v %d", open.aliases.partial, open.aliases.loaded, tt.wantPartial, len(m.all))
			}
		})
	}
}

func TestDetailAliasLine(t *testing.T) {
	many := make([]string, 30)
	for i := range many {
		many[i] = fmt.Sprintf("1.31.%d-alpine", i)
	}
	tests := []struct {
		name    string
		digest  string
		aliases aliasInfo
		want    string
	}{
		{"none, all loaded", pinnedDigest, aliasInfo{loaded: 10}, "none"},
		{"none, partial", pinnedDigest, aliasInfo{loaded: 100, partial: true}, "none (in 100 loaded tags)"},
		{"some", pinnedDigest, aliasInfo{names: []string{"1.27", "mainline"}, loaded: 10}, "1.27, mainline"},
		{"some, partial", pinnedDigest, aliasInfo{names: []string{"1.27"}, loaded: 100, partial: true}, "1.27 (in 100 loaded tags)"},
		{"no digest", "", aliasInfo{loaded: 10}, "unknown: no digest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tag := platformTag("latest", amd64())
			tag.Digest = tt.digest
			d := newTestDetailAliases(&fakeClipboard{}, hub.Repo{Namespace: "library", Name: "nginx"}, tag, tt.aliases)
			if got := d.aliasLine(70); got != tt.want {
				t.Errorf("aliasLine = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("overflow counts the rest", func(t *testing.T) {
		d := newTestDetailAliases(&fakeClipboard{}, hub.Repo{Namespace: "library", Name: "nginx"}, platformTag("latest", amd64()), aliasInfo{names: many, loaded: 30})
		got := d.aliasLine(70)
		if len([]rune(got)) > 70 || !strings.HasPrefix(got, "1.31.0-alpine, ") || !strings.HasSuffix(got, " more") {
			t.Errorf("aliasLine = %q", got)
		}
		shown := strings.Count(strings.TrimSuffix(got, got[strings.LastIndex(got, " +"):]), ",") + 1
		if want := fmt.Sprintf(" +%d more", 30-shown); !strings.HasSuffix(got, want) {
			t.Errorf("aliasLine = %q, want suffix %q", got, want)
		}
	})
}

// TestDetailFitsAt80x24 guards the extra header line against pushing the
// status bar off a standard terminal.
func TestDetailFitsAt80x24(t *testing.T) {
	d := newTestDetailAliases(&fakeClipboard{}, hub.Repo{Namespace: "library", Name: "nginx"}, platformTag("latest", amd64(), arm64()), aliasInfo{names: []string{"1.27"}, loaded: 1})
	if n := strings.Count(d.view(), "\n") + 1; n > 24 {
		t.Errorf("view is %d lines at 80x24", n)
	}
	legacy := hub.Tag{Name: "1.9.8"}
	d = newTestDetail(&fakeClipboard{}, hub.Repo{Namespace: "library", Name: "nginx"}, legacy)
	if n := strings.Count(d.view(), "\n") + 1; n != 24 {
		t.Errorf("platformless view is %d lines at 80x24, want 24", n)
	}
}
