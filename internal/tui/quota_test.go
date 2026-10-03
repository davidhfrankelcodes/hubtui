package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

func TestQuotaText(t *testing.T) {
	st := newStyles()
	reset := testNow().Add(42 * time.Second)
	tests := []struct {
		name       string
		r          hub.RateLimit
		want       string
		wantUrgent bool
	}{
		{"plenty", hub.RateLimit{Limit: 180, Remaining: 175, Reset: reset}, "api 175/180", false},
		{"exactly at the threshold", hub.RateLimit{Limit: 180, Remaining: 18, Reset: reset}, "api 18/180", false},
		{"low says when it refills", hub.RateLimit{Limit: 180, Remaining: 17, Reset: reset}, "api 17/180 (full in 42s)", true},
		{"empty", hub.RateLimit{Limit: 600, Remaining: 0, Reset: reset}, "api 0/600 (full in 42s)", true},
		{"low without a reset time", hub.RateLimit{Limit: 180, Remaining: 2}, "api 2/180", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := quotaText(st, tt.r, testNow())
			if got := stripANSI(q.text); got != tt.want || q.urgent != tt.wantUrgent {
				t.Errorf("quotaText = %q urgent %v, want %q urgent %v", got, q.urgent, tt.want, tt.wantUrgent)
			}
		})
	}
}

func TestStatusLineQuota(t *testing.T) {
	quotaFn := func(remaining int) func() (hub.RateLimit, bool) {
		return func() (hub.RateLimit, bool) {
			return hub.RateLimit{Limit: 180, Remaining: remaining, Reset: testNow().Add(time.Minute)}, true
		}
	}
	newModel := func(rl func() (hub.RateLimit, bool)) *tagsScreen {
		m := newTestModel(&fakeRegistry{tags: makeTags("t", 3), pageSize: 100})
		m = settle(t, m, m.init())
		m.deps.RateLimit = rl
		return m
	}

	t.Run("hidden without a source", func(t *testing.T) {
		if line := stripANSI(newModel(nil).statusLine()); strings.Contains(line, "api ") {
			t.Errorf("status = %q", line)
		}
	})
	t.Run("hidden before any response", func(t *testing.T) {
		m := newModel(func() (hub.RateLimit, bool) { return hub.RateLimit{}, false })
		if line := stripANSI(m.statusLine()); strings.Contains(line, "api ") {
			t.Errorf("status = %q", line)
		}
	})
	t.Run("after the other counters", func(t *testing.T) {
		line := stripANSI(newModel(quotaFn(175)).statusLine())
		if !strings.HasSuffix(line, "3/3 loaded · sort: pushed · api 175/180") {
			t.Errorf("status = %q", line)
		}
	})

	// Every filter on makes the counters long; at 80 columns something has
	// to give, and the line must never overflow.
	crowd := func(m *tagsScreen) *tagsScreen {
		m.applyFilter("^t[0-9]+$")
		m.arch = "linux/arm64/v8"
		m.stable = true
		m.rebuild()
		return m
	}
	t.Run("plenty yields to counters when crowded", func(t *testing.T) {
		line := stripANSI(crowd(newModel(quotaFn(175))).statusLine())
		if strings.Contains(line, "api ") || !strings.Contains(line, "sort: pushed") {
			t.Errorf("status = %q", line)
		}
	})
	t.Run("low wins over counters when crowded", func(t *testing.T) {
		line := stripANSI(crowd(newModel(quotaFn(3))).statusLine())
		if !strings.HasSuffix(line, "api 3/180 (full in 1m0s)") || strings.Contains(line, "sort:") {
			t.Errorf("status = %q", line)
		}
	})
	for _, rem := range []int{175, 3} {
		m := crowd(newModel(quotaFn(rem)))
		for _, w := range []int{80, 60, 40} {
			m.width = w
			if got := len([]rune(stripANSI(m.statusLine()))); got > w {
				t.Errorf("remaining %d at width %d: status is %d wide", rem, w, got)
			}
		}
	}
}
