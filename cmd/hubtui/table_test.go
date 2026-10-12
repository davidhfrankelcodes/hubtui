package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

func searchFixture() *fakeRegistry {
	return &fakeRegistry{search: &hub.SearchPage{Results: []hub.SearchResult{
		{Repo: hub.Repo{Namespace: "library", Name: "nginx"}, Description: "Official build of Nginx.", Stars: 21_400, Pulls: 13_413_760_258, Official: true},
		{
			Repo:        hub.Repo{Namespace: "nginx", Name: "nginx-ingress"},
			Description: "NGINX and NGINX Plus Ingress Controllers\nfor Kubernetes, with\ttabs and a description long enough to cut",
			Stars:       124, Pulls: 1_100_000_000,
		},
		{Repo: hub.Repo{Namespace: "nginx", Name: "unit"}, Stars: 3, Pulls: 999},
	}}}
}

// The table is the default; --table asks for it explicitly and prints the
// same thing.
func TestSearchTable(t *testing.T) {
	want := strings.Join([]string{
		"NAME                  STARS   PULLS   OFFICIAL   DESCRIPTION",
		"nginx                 21.4k   13.4B   [OK]       Official build of Nginx.",
		"nginx/nginx-ingress   124     1.1B               NGINX and NGINX Plus Ingress Controllers for Kubernetes, wi…",
		"nginx/unit            3       999                ",
		"",
	}, "\n")
	for _, args := range [][]string{{"search", "nginx"}, {"search", "nginx", "--table"}, {"search", "--table", "nginx"}} {
		code, stdout, stderr := runApp(t, searchFixture(), args...)
		if code != 0 {
			t.Fatalf("%v: exit code %d, stderr: %s", args, code, stderr)
		}
		if stdout != want {
			t.Errorf("%v: stdout =\n%s\nwant\n%s", args, stdout, want)
		}
	}
}

func TestSearchTableEmpty(t *testing.T) {
	code, stdout, _ := runApp(t, &fakeRegistry{search: &hub.SearchPage{}}, "search", "zzz")
	if code != 0 || stdout != "NAME   STARS   PULLS   OFFICIAL   DESCRIPTION\n" {
		t.Errorf("code = %d, stdout = %q; want 0 and only the header", code, stdout)
	}
}

func tagsFixture() *fakeRegistry {
	const index = "sha256:756444d493424be61c13714ec55c97a733942d67772fca9d1724fb264f8bde08"
	return &fakeRegistry{tagPages: [][]hub.Tag{{
		{
			Name: "1.27", Digest: index, Pushed: testNow().Add(-3 * time.Hour),
			Platforms: []hub.Platform{
				{OS: "linux", Arch: "arm64", Variant: "v8", Size: 42_000_000},
				{OS: "linux", Arch: "amd64", Size: 48_500_000},
			},
		},
		{Name: "1.27-rc1", Digest: index, Pushed: testNow().Add(-50 * time.Hour), Platforms: []hub.Platform{{OS: "linux", Arch: "amd64", Size: 999}}},
		// A legacy tag: no digest, no push date, no platforms.
		{Name: "1.9.8"},
	}}}
}

func TestTagsTable(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "default",
			args: []string{"tags", "nginx"},
			want: []string{
				"TAG        PLATFORMS        SIZE      PUSHED   DIGEST",
				"1.27       amd64,arm64/v8   48.5 MB   3h ago   756444d49342",
				"1.27-rc1   amd64            999 B     2d ago   756444d49342",
				"1.9.8      -                -         -        -",
			},
		},
		{
			// The size follows the filtered platform, as in the TUI.
			name: "arch and stable",
			args: []string{"tags", "nginx", "--table", "--arch", "arm64", "--stable"},
			want: []string{
				"TAG    PLATFORMS        SIZE      PUSHED   DIGEST",
				"1.27   amd64,arm64/v8   42.0 MB   3h ago   756444d49342",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runApp(t, tagsFixture(), tt.args...)
			if code != 0 {
				t.Fatalf("exit code %d, stderr: %s", code, stderr)
			}
			if want := strings.Join(tt.want, "\n") + "\n"; stdout != want {
				t.Errorf("stdout =\n%s\nwant\n%s", stdout, want)
			}
		})
	}
}

// Hitting Docker Hub's anonymous paging cap still prints what was collected,
// with the warning on stderr where it cannot corrupt the table.
func TestTagsTablePageLimit(t *testing.T) {
	reg := tagsFixture()
	reg.tagPages = append(reg.tagPages, nil)
	reg.errs = map[int]error{2: fmt.Errorf("listing tags: %w", hub.ErrPageLimit)}
	code, stdout, stderr := runApp(t, reg, "tags", "nginx", "--limit", "0")
	if code != 0 || strings.Count(stdout, "\n") != 4 || !strings.Contains(stderr, "warning: stopped after 3 tags") {
		t.Errorf("code = %d, stdout:\n%s\nstderr: %s", code, stdout, stderr)
	}
}
