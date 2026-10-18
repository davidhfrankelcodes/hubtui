package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// Distinct digests per tag, so an exact-match test names exactly one.
func digestFor(i int) string { return fmt.Sprintf("sha256:%064x", i+1) }

func resolveFixture() *fakeRegistry {
	both := []hub.Platform{{OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64", Variant: "v8"}}
	var tags []hub.Tag
	for i, n := range []string{"latest", "18.1-alpine", "18-alpine", "17.11-alpine", "17.10-alpine", "17-alpine", "17.4-alpine", "19rc1-alpine"} {
		tags = append(tags, hub.Tag{Name: n, Digest: digestFor(i), MediaType: "application/vnd.oci.image.index.v1+json", Platforms: both})
	}
	// Newest 17.x Alpine has no arm64 image.
	tags[3].Platforms = both[:1]
	tags = append(tags, hub.Tag{Name: "9.6", MediaType: "application/vnd.docker.distribution.manifest.v1+prettyjws"})
	return &fakeRegistry{tagPages: [][]hub.Tag{tags}}
}

// The printed reference is what lands in a Dockerfile, so these are exact.
func TestResolve(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		want        string
		wantLookups []string // nil: no single-tag lookup expected
		wantStderr  string
	}{
		{name: "tag", args: []string{"postgres:17-alpine"}, want: "postgres:17-alpine@" + digestFor(5), wantLookups: []string{"17-alpine"}},
		{name: "no tag means latest", args: []string{"postgres"}, want: "postgres:latest@" + digestFor(0), wantLookups: []string{"latest"}},
		{name: "full hub reference", args: []string{"docker.io/library/postgres:17-alpine"}, want: "postgres:17-alpine@" + digestFor(5), wantLookups: []string{"17-alpine"}},
		{name: "latest in line", args: []string{"postgres:17.4-alpine", "--latest"}, want: "postgres:17.11-alpine@" + digestFor(3)},
		{name: "latest skips a missing platform", args: []string{"--latest", "--arch", "arm64", "postgres:17.4-alpine"}, want: "postgres:17.10-alpine@" + digestFor(4)},
		{name: "latest across majors", args: []string{"postgres:17-alpine", "--latest", "--major"}, want: "postgres:18-alpine@" + digestFor(2)},
		{
			name: "moved digest is noted, new pin printed",
			args: []string{"postgres:17-alpine@" + digestFor(99)},
			want: "postgres:17-alpine@" + digestFor(5), wantLookups: []string{"17-alpine"},
			wantStderr: "has moved from " + digestFor(99),
		},
		{name: "current digest is silent", args: []string{"postgres:17-alpine@" + digestFor(5)}, want: "postgres:17-alpine@" + digestFor(5), wantLookups: []string{"17-alpine"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := resolveFixture()
			code, stdout, stderr := runApp(t, reg, append([]string{"resolve"}, tt.args...)...)
			if code != 0 {
				t.Fatalf("exit %d, stderr: %s", code, stderr)
			}
			if stdout != tt.want+"\n" {
				t.Errorf("stdout = %q, want %q", stdout, tt.want+"\n")
			}
			if tt.wantStderr == "" && stderr != "" || !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr = %q, want %q", stderr, tt.wantStderr)
			}
			if tt.wantLookups != nil && !slices.Equal(reg.tagLookups, tt.wantLookups) {
				t.Errorf("lookups = %v, want %v", reg.tagLookups, tt.wantLookups)
			}
		})
	}
}

func TestResolveJSON(t *testing.T) {
	code, stdout, stderr := runApp(t, resolveFixture(), "resolve", "postgres:17.4-alpine", "--latest", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, stdout)
	}
	if got["name"] != "17.11-alpine" || got["reference"] != "postgres:17.11-alpine" || got["pinned_reference"] != "postgres:17.11-alpine@"+digestFor(3) {
		t.Errorf("json = %s", stdout)
	}
}

func TestResolveErrors(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{name: "no image", args: nil, wantCode: 2, wantStderr: "exactly one image"},
		{name: "two images", args: []string{"postgres", "redis"}, wantCode: 2, wantStderr: "exactly one image"},
		{name: "major without latest", args: []string{"postgres:17", "--major"}, wantCode: 2, wantStderr: "--major only applies with --latest"},
		{name: "digest without tag", args: []string{"postgres@" + digestFor(1)}, wantCode: 2, wantStderr: "give a tag along with the digest"},
		{name: "other registry", args: []string{"ghcr.io/o/app:1"}, wantCode: 2, wantStderr: "only Docker Hub"},
		{name: "bad tag", args: []string{"postgres:-1"}, wantCode: 2, wantStderr: "invalid tag"},
		{name: "missing tag", args: []string{"postgres:nope"}, wantCode: 1, wantStderr: "not found"},
		{name: "platform missing", args: []string{"postgres:17.11-alpine", "--arch", "arm64"}, wantCode: 1, wantStderr: "has no arm64 image"},
		{name: "latest without version", args: []string{"postgres:latest", "--latest"}, wantCode: 1, wantStderr: "--latest needs a versioned tag"},
		{name: "nothing in line has the platform", args: []string{"postgres:17.4-alpine", "--latest", "--arch", "s390x"}, wantCode: 1, wantStderr: "no s390x tag"},
		// A reference that cannot be pinned is an error, never an unpinned line.
		{name: "legacy tag", args: []string{"postgres:9.6"}, wantCode: 1, wantStderr: "legacy schema-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runApp(t, resolveFixture(), append([]string{"resolve"}, tt.args...)...)
			if code != tt.wantCode || stdout != "" || !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("code %d (want %d), stdout %q, stderr %q (want %q)", code, tt.wantCode, stdout, stderr, tt.wantStderr)
			}
		})
	}
}

// Past the anonymous paging cap the answer is the best seen so far, with a
// warning that a newer tag may exist.
func TestResolveLatestPartial(t *testing.T) {
	reg := resolveFixture()
	reg.tagPages = append(reg.tagPages, nil)
	reg.errs = map[int]error{2: fmt.Errorf("listing tags: %w", hub.ErrPageLimit)}
	code, stdout, stderr := runApp(t, reg, "resolve", "postgres:17.4-alpine", "--latest")
	if code != 0 || stdout != "postgres:17.11-alpine@"+digestFor(3)+"\n" || !strings.Contains(stderr, "a newer one may exist") {
		t.Errorf("code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}
