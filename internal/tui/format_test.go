package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

func TestHumanSize(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "-"},
		{999, "999 B"},
		{1000, "1.0 kB"},
		{36364210, "36.4 MB"},
		{548929721, "548.9 MB"},
		{1_500_000_000, "1.5 GB"},
	}
	for _, tt := range tests {
		if got := humanSize(tt.n); got != tt.want {
			t.Errorf("humanSize(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestRelativeTime(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{49 * time.Hour, "2d ago"},
		{65 * 24 * time.Hour, "2mo ago"},
		{800 * 24 * time.Hour, "2y ago"},
	}
	for _, tt := range tests {
		if got := relativeTime(now.Add(-tt.ago), now); got != tt.want {
			t.Errorf("relativeTime(-%v) = %q, want %q", tt.ago, got, tt.want)
		}
	}
	if got := relativeTime(time.Time{}, now); got != "-" {
		t.Errorf("zero time = %q", got)
	}
}

func TestShortDigest(t *testing.T) {
	if got := shortDigest("sha256:756444d493424be61c13714ec55c97a733942d67772fca9d1724fb264f8bde08"); got != "756444d49342" {
		t.Errorf("shortDigest = %q", got)
	}
	if got := shortDigest(""); got != "-" {
		t.Errorf("empty digest = %q", got)
	}
}

func TestArchSummary(t *testing.T) {
	nginx := []hub.Platform{
		{OS: "linux", Arch: "amd64"},
		{OS: "linux", Arch: "ppc64le"},
		{OS: "linux", Arch: "386"},
		{OS: "linux", Arch: "s390x"},
		{OS: "linux", Arch: "arm", Variant: "v7"},
		{OS: "linux", Arch: "arm64", Variant: "v8"},
		{OS: "linux", Arch: "amd64"},
	}
	tests := []struct {
		name  string
		ps    []hub.Platform
		width int
		want  string
	}{
		{"common platforms first", nginx, 80, "amd64,arm64/v8,arm/v7,386,ppc64le,s390x"},
		{"overflow counts the rest", nginx, 20, "amd64,arm64/v8,+4"},
		{"exact fit needs no count", nginx, len("amd64,arm64/v8,arm/v7,386,ppc64le,s390x"), "amd64,arm64/v8,arm/v7,386,ppc64le,s390x"},
		{"too narrow for any name", nginx, 5, "amd64"},
		{"other OSes keep their prefix", []hub.Platform{{OS: "windows", Arch: "amd64"}, {Arch: "386"}}, 80, "386,windows/amd64"},
		{"none", nil, 80, "-"},
	}
	for _, tt := range tests {
		got := archSummary(tt.ps, tt.width)
		if got != tt.want {
			t.Errorf("%s: archSummary = %q, want %q", tt.name, got, tt.want)
		}
		if len(got) > tt.width && tt.width >= len("amd64,+5") {
			t.Errorf("%s: %q is wider than %d", tt.name, got, tt.width)
		}
	}
}

func TestDisplaySize(t *testing.T) {
	tag := hub.Tag{Platforms: []hub.Platform{
		{OS: "linux", Arch: "arm64", Size: 1},
		{OS: "windows", Arch: "amd64", Size: 2},
		{OS: "linux", Arch: "amd64", Size: 3},
	}}
	if got := displaySize(tag, ""); got != 3 {
		t.Errorf("displaySize = %d, want the linux/amd64 size", got)
	}
	if got := displaySize(hub.Tag{Platforms: []hub.Platform{{Arch: "arm64", Size: 7}}}, ""); got != 7 {
		t.Errorf("displaySize without amd64 = %d, want the first platform's", got)
	}
	if got := displaySize(tag, "linux/arm64"); got != 1 {
		t.Errorf("displaySize filtered to arm64 = %d, want that platform's size", got)
	}
}

func TestNaturalLess(t *testing.T) {
	names := []string{"1.10", "latest", "1.9.1", "1.9", "1.10-alpine", "1", "1.2", "01.3", "alpine3.20", "alpine3.9"}
	slices.SortFunc(names, func(a, b string) int {
		switch {
		case naturalLess(a, b):
			return -1
		case naturalLess(b, a):
			return 1
		}
		return 0
	})
	want := "1,1.2,01.3,1.9,1.9.1,1.10,1.10-alpine,alpine3.9,alpine3.20,latest"
	if got := strings.Join(names, ","); got != want {
		t.Errorf("sorted = %s\nwant     %s", got, want)
	}
}
