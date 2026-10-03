package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// The invalid-regex message is model state elsewhere; here it must actually
// reach the screen, and emptying the input must bring every tag back.
func TestTagsFilterView(t *testing.T) {
	reg := &fakeRegistry{tags: makeTags("t", 12), pageSize: 100}
	m := newTestModel(reg)
	m = settle(t, m, m.init())
	m, _ = press(t, m, "/")
	m = typeText(t, m, "t1(")
	v := stripANSI(m.view())
	if !strings.Contains(v, "/t1(") || !strings.Contains(v, invalidRegex) {
		t.Errorf("view does not show the input and the error:\n%s", v)
	}
	if n := strings.Count(v, "\n") + 1; n != 24 {
		t.Errorf("view with the filter open is %d lines at 80x24", n)
	}
	for range 3 {
		m, _ = press(t, m, "backspace")
	}
	if m.filterRE != nil || m.filterErr != "" || len(m.visible) != 12 {
		t.Errorf("empty filter: re %v, err %q, %d rows; want all 12", m.filterRE, m.filterErr, len(m.visible))
	}
}

// Navigating away cancels requests; the cancellation is not news.
func TestTagsCanceledRequestShowsNothing(t *testing.T) {
	reg := &fakeRegistry{tags: makeTags("t", 3), pageSize: 100, errs: map[int]error{1: fmt.Errorf("listing tags: %w", context.Canceled)}}
	m := newTestModel(reg)
	m = settle(t, m, m.init())
	if m.bar.text != "" || m.bar.isErr {
		t.Errorf("status = %q (error %v), want nothing", m.bar.text, m.bar.isErr)
	}
}

func TestErrorWording(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"rate limited", fmt.Errorf("listing: %w", &hub.RateLimitError{RetryAfter: 90e9}), "rate limited by Docker Hub until 12:01:30; press r after that"},
		{"auth", fmt.Errorf("logging in as alice: %w: unauthorized", hub.ErrAuth), "Docker Hub login failed; check DOCKERHUB_TOKEN"},
		{"other", fmt.Errorf("request failed: dial tcp: no route to host"), "request failed: dial tcp: no route to host (press r to retry)"},
		// Hub down during login is retryable, not a token problem.
		{"login server error", fmt.Errorf("logging in as alice: docker hub: 500 Internal Server Error: internal error"), "logging in as alice: docker hub: 500 Internal Server Error: internal error (press r to retry)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBase(context.Background(), Deps{Now: testNow}, 1)
			b.showError(tt.err)
			if b.bar.text != tt.want || !b.bar.isErr {
				t.Errorf("status = %q (error %v), want %q", b.bar.text, b.bar.isErr, tt.want)
			}
		})
	}
}

func TestDetailMovesBetweenPlatforms(t *testing.T) {
	d := newTestDetail(&fakeClipboard{}, hub.Repo{Namespace: "library", Name: "nginx"}, platformTag("1.27", amd64(), arm64(), win()))
	for _, step := range []struct {
		key  string
		want int
	}{{"j", 1}, {"j", 2}, {"j", 2}, {"k", 1}, {"k", 0}, {"k", 0}} {
		d.update(keyMsg(step.key))
		if got := d.table.Cursor(); got != step.want {
			t.Fatalf("after %s cursor = %d, want %d", step.key, got, step.want)
		}
	}
}

func TestDescribeMediaType(t *testing.T) {
	tests := map[string]string{
		"application/vnd.oci.image.index.v1+json":                   "OCI image index (multi-platform)",
		"application/vnd.docker.distribution.manifest.list.v2+json": "Docker manifest list (multi-platform)",
		"application/vnd.oci.image.manifest.v1+json":                "OCI image manifest (single platform)",
		"application/vnd.docker.distribution.manifest.v2+json":      "Docker image manifest (single platform)",
		"application/vnd.docker.distribution.manifest.v1+prettyjws": "Docker schema 1 (legacy, not pullable by current Docker)",
		"application/vnd.docker.distribution.manifest.v1+json":      "Docker schema 1 (legacy, not pullable by current Docker)",
		"":                                    "-",
		"application/vnd.example.future+json": "application/vnd.example.future+json",
	}
	for in, want := range tests {
		if got := describeMediaType(in); got != want {
			t.Errorf("describeMediaType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStatusBarMessageStates(t *testing.T) {
	st := newStyles()
	var b statusBar
	if got := stripANSI(b.render(st, 80, "3/3 loaded", quota{}, true)); !strings.HasPrefix(got, "loading…") {
		t.Errorf("loading with no message: %q", got)
	}
	// A long error gives way to the counters, never the other way round.
	b.set(strings.Repeat("connection refused ", 10), true)
	got := stripANSI(b.render(st, 80, "3/3 loaded", quota{}, true))
	if !strings.HasSuffix(got, " 3/3 loaded") || !strings.Contains(got, "…") || lipgloss.Width(got) != 80 {
		t.Errorf("long error: %q", got)
	}
}

func TestAliasesSortedNaturally(t *testing.T) {
	tags := makeTags("t", 6)
	for i, n := range []string{"latest", "1.10", "1", "1.9.2", "mainline", "1.9"} {
		tags[i].Name = n
		tags[i].Digest = tags[0].Digest
	}
	m := newTestModel(&fakeRegistry{tags: tags, pageSize: 100})
	m = settle(t, m, m.init())
	if got := strings.Join(m.aliasInfo(tags[0]).names, ","); got != "1,1.9,1.9.2,1.10,mainline" {
		t.Errorf("aliases = %s", got)
	}
}

// The width helpers feed every table cell and status line; none may return
// something wider than asked for, at any width.
func FuzzTruncateAndElide(f *testing.F) {
	f.Add("nginx:1.27@sha256:"+strings.Repeat("a", 64), 20)
	f.Add("hello", 0)
	f.Add("日本語のタグ", 5)
	f.Add("x", -3)
	f.Fuzz(func(t *testing.T, s string, w int) {
		if !utf8.ValidString(s) || w > 500 {
			return
		}
		if got := truncate(s, w); lipgloss.Width(got) > max(w, 0) {
			t.Fatalf("truncate(%q, %d) = %q, %d wide", s, w, got, lipgloss.Width(got))
		}
		if got := elideMiddle(s, w); w >= 0 && utf8.RuneCountInString(got) > w {
			t.Fatalf("elideMiddle(%q, %d) = %q, %d runes", s, w, got, utf8.RuneCountInString(got))
		}
	})
}

// Sorting relies on naturalLess being a strict order.
func FuzzNaturalLess(f *testing.F) {
	f.Add("1.9", "1.10")
	f.Add("17-alpine", "17.1")
	f.Add("a01", "a1")
	f.Add("", "0")
	f.Fuzz(func(t *testing.T, a, b string) {
		if naturalLess(a, a) {
			t.Fatalf("naturalLess(%q, %q) is reflexive", a, a)
		}
		if naturalLess(a, b) && naturalLess(b, a) {
			t.Fatalf("naturalLess(%q, %q) holds both ways", a, b)
		}
	})
}
