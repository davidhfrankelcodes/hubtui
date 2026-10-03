package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// fakeRegistry serves canned pages of tags; page N is tagPages[N-1].
type fakeRegistry struct {
	search   *hub.SearchPage
	tagPages [][]hub.Tag
	errs     map[int]error // by tags page number
	calls    []hub.TagsOptions
	repos    []hub.Repo
}

func (f *fakeRegistry) Search(_ context.Context, _ string, _ hub.PageOptions) (*hub.SearchPage, error) {
	if f.search == nil {
		return nil, errors.New("search not configured")
	}
	return f.search, nil
}

func (f *fakeRegistry) Tags(_ context.Context, repo hub.Repo, opts hub.TagsOptions) (*hub.TagPage, error) {
	f.calls = append(f.calls, opts)
	f.repos = append(f.repos, repo)
	if err := f.errs[opts.Page]; err != nil {
		return nil, err
	}
	i := opts.Page - 1
	if i >= len(f.tagPages) {
		return &hub.TagPage{Page: opts.Page}, nil
	}
	return &hub.TagPage{Tags: f.tagPages[i], Page: opts.Page, HasNext: i < len(f.tagPages)-1}, nil
}

func runApp(t *testing.T, reg hub.Registry, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	a := &app{stdout: &out, stderr: &errOut, registry: reg, openTUI: func(context.Context, *hub.Repo) error {
		t.Error("TUI started unexpectedly")
		return nil
	}}
	code = a.run(context.Background(), args)
	return code, out.String(), errOut.String()
}

func TestRunTUI(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		tty        bool
		tuiErr     error
		wantCode   int
		wantRepo   *hub.Repo
		wantSearch bool
		wantStderr string
	}{
		{name: "image opens tags screen", args: []string{"nginx"}, tty: true, wantRepo: &hub.Repo{Namespace: "library", Name: "nginx"}},
		{name: "namespaced image", args: []string{"grafana/grafana"}, tty: true, wantRepo: &hub.Repo{Namespace: "grafana", Name: "grafana"}},
		{name: "not a terminal prints usage", args: []string{"nginx"}, tty: false, wantCode: 2, wantStderr: "Usage:"},
		{name: "no args on a terminal opens search", args: nil, tty: true, wantSearch: true},
		{name: "no args off a terminal prints usage", args: nil, tty: false, wantCode: 2, wantStderr: "Usage:"},
		{name: "invalid image", args: []string{"nginx:latest"}, tty: true, wantCode: 2, wantStderr: "without a tag or digest"},
		{name: "TUI failure", args: []string{"nginx"}, tty: true, tuiErr: errors.New("no tty"), wantCode: 1, wantStderr: "no tty", wantRepo: &hub.Repo{Namespace: "library", Name: "nginx"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errOut bytes.Buffer
			var got *hub.Repo
			opened := false
			a := &app{stdout: &bytes.Buffer{}, stderr: &errOut, registry: &fakeRegistry{}, stdoutIsTTY: tt.tty,
				openTUI: func(_ context.Context, r *hub.Repo) error {
					opened, got = true, r
					return tt.tuiErr
				}}
			if code := a.run(context.Background(), tt.args); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, errOut.String())
			}
			if opened != (tt.wantRepo != nil || tt.wantSearch) {
				t.Fatalf("TUI opened = %v, want %v", opened, !opened)
			}
			if (got == nil) != (tt.wantRepo == nil) || (got != nil && *got != *tt.wantRepo) {
				t.Errorf("TUI opened with %v, want %v", got, tt.wantRepo)
			}
			if !strings.Contains(errOut.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tt.wantStderr)
			}
		})
	}
}

func tag(name string, arches ...string) hub.Tag {
	t := hub.Tag{Name: name, Digest: "sha256:" + strings.Repeat("0", 64)}
	for _, a := range arches {
		t.Platforms = append(t.Platforms, hub.Platform{OS: "linux", Arch: a, Digest: "sha256:" + name + "-" + a})
	}
	return t
}

func tagNames(t *testing.T, stdout string) []string {
	t.Helper()
	var tags []struct{ Name string }
	if err := json.Unmarshal([]byte(stdout), &tags); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", err, stdout)
	}
	names := []string{}
	for _, tg := range tags {
		names = append(names, tg.Name)
	}
	return names
}

func TestRunArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "version", args: []string{"--version"}, wantCode: 0, wantStdout: "hubtui "},
		{name: "help", args: []string{"-h"}, wantCode: 0, wantStderr: "Usage:"},
		{name: "unknown flag", args: []string{"--nope"}, wantCode: 2, wantStderr: "flag provided but not defined"},
		{name: "no args off a terminal prints usage", args: nil, wantCode: 2, wantStderr: "Usage:"},
		{name: "two images", args: []string{"nginx", "redis"}, wantCode: 2, wantStderr: "Usage:"},
		{name: "search help", args: []string{"search", "-h"}, wantCode: 0, wantStderr: "hubtui search <query>"},
		{name: "search without query", args: []string{"search", "--json"}, wantCode: 2, wantStderr: "missing query"},
		{name: "search without --json", args: []string{"search", "nginx"}, wantCode: 2, wantStderr: "--json is required"},
		{name: "after -- flags are positional", args: []string{"search", "--", "--json"}, wantCode: 2, wantStderr: "--json is required"},
		{name: "tags without image", args: []string{"tags", "--json"}, wantCode: 2, wantStderr: "exactly one image"},
		{name: "tags with two images", args: []string{"tags", "nginx", "redis", "--json"}, wantCode: 2, wantStderr: "exactly one image"},
		{name: "tags without --json", args: []string{"tags", "nginx"}, wantCode: 2, wantStderr: "--json is required"},
		{name: "tags with a tag", args: []string{"tags", "nginx:latest", "--json"}, wantCode: 2, wantStderr: "without a tag or digest"},
		{name: "tags negative limit", args: []string{"tags", "nginx", "--json", "--limit", "-1"}, wantCode: 2, wantStderr: "must not be negative"},
		{name: "tags bad limit", args: []string{"tags", "nginx", "--json", "--limit", "x"}, wantCode: 2, wantStderr: "invalid value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runApp(t, &fakeRegistry{}, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr)
			}
			if !strings.HasPrefix(stdout, tt.wantStdout) {
				t.Errorf("stdout = %q, want prefix %q", stdout, tt.wantStdout)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tt.wantStderr)
			}
			if tt.wantCode != 0 && stdout != "" {
				t.Errorf("stdout must be empty on failure, got %q", stdout)
			}
		})
	}
}

func TestSearchJSON(t *testing.T) {
	reg := &fakeRegistry{search: &hub.SearchPage{Results: []hub.SearchResult{
		{Repo: hub.Repo{Namespace: "library", Name: "nginx"}, Description: "Official build of Nginx.", Stars: 5, Pulls: 13413760258, Official: true},
		{Repo: hub.Repo{Namespace: "nginx", Name: "nginx-ingress"}, Description: "Ingress <controller> & more", Stars: 1, Pulls: 2},
	}}}
	code, stdout, stderr := runApp(t, reg, "search", "nginx", "--json")
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, stderr)
	}
	want := `[
  {
    "name": "nginx",
    "namespace": "library",
    "repository": "nginx",
    "official": true,
    "stars": 5,
    "pulls": 13413760258,
    "description": "Official build of Nginx."
  },
  {
    "name": "nginx/nginx-ingress",
    "namespace": "nginx",
    "repository": "nginx-ingress",
    "official": false,
    "stars": 1,
    "pulls": 2,
    "description": "Ingress <controller> & more"
  }
]
`
	if stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, want)
	}
}

func TestSearchJSONEmpty(t *testing.T) {
	code, stdout, _ := runApp(t, &fakeRegistry{search: &hub.SearchPage{}}, "search", "zzz", "--json")
	if code != 0 || stdout != "[]\n" {
		t.Errorf("code = %d, stdout = %q; want 0 and an empty array", code, stdout)
	}
}

func TestTagsJSONShape(t *testing.T) {
	pushed := time.Date(2026, 9, 29, 19, 52, 23, 0, time.UTC)
	const index = "sha256:756444d493424be61c13714ec55c97a733942d67772fca9d1724fb264f8bde08"
	reg := &fakeRegistry{tagPages: [][]hub.Tag{{
		{
			Name: "1.27", Digest: index, MediaType: "application/vnd.oci.image.index.v1+json", Pushed: pushed,
			Platforms: []hub.Platform{{OS: "linux", Arch: "arm64", Variant: "v8", Digest: "sha256:arm", Size: 42}},
		},
		{Name: "1.9.8", MediaType: "application/vnd.docker.distribution.manifest.v1+prettyjws"},
	}}}
	code, stdout, stderr := runApp(t, reg, "tags", "nginx", "--json")
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, stderr)
	}
	want := `[
  {
    "name": "1.27",
    "reference": "nginx:1.27",
    "pinned_reference": "nginx:1.27@sha256:756444d493424be61c13714ec55c97a733942d67772fca9d1724fb264f8bde08",
    "digest": "sha256:756444d493424be61c13714ec55c97a733942d67772fca9d1724fb264f8bde08",
    "media_type": "application/vnd.oci.image.index.v1+json",
    "pushed": "2026-09-29T19:52:23Z",
    "platforms": [
      {
        "os": "linux",
        "architecture": "arm64",
        "variant": "v8",
        "digest": "sha256:arm",
        "size": 42
      }
    ]
  },
  {
    "name": "1.9.8",
    "reference": "nginx:1.9.8",
    "pinned_reference": null,
    "digest": null,
    "media_type": "application/vnd.docker.distribution.manifest.v1+prettyjws",
    "pushed": null,
    "platforms": []
  }
]
`
	if stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, want)
	}
	if got := reg.repos[0]; got != (hub.Repo{Namespace: "library", Name: "nginx"}) {
		t.Errorf("requested repo %+v, want library/nginx", got)
	}
}

func TestTagsPaging(t *testing.T) {
	pages := [][]hub.Tag{
		{tag("a", "amd64"), tag("b", "amd64", "arm64"), tag("c", "amd64")},
		{tag("d", "amd64"), tag("e", "arm64"), tag("f", "amd64")},
		{tag("g", "arm64")},
	}
	tests := []struct {
		name          string
		args          []string
		errs          map[int]error
		wantCode      int
		wantNames     []string
		wantPages     int
		wantPageSize  int
		wantStderr    string
		wantNoStdout  bool
		wantStderrNot string
	}{
		{
			name:      "limit stops mid-page without fetching more",
			args:      []string{"--limit", "2"},
			wantNames: []string{"a", "b"}, wantPages: 1, wantPageSize: 2,
		},
		{
			name:      "limit spans pages",
			args:      []string{"--limit", "5"},
			wantNames: []string{"a", "b", "c", "d", "e"}, wantPages: 2, wantPageSize: 5,
		},
		{
			name:      "limit 0 fetches every page",
			args:      []string{"--limit", "0"},
			wantNames: []string{"a", "b", "c", "d", "e", "f", "g"}, wantPages: 3, wantPageSize: hub.MaxPageSize,
		},
		{
			name:      "arch filter across pages uses full pages",
			args:      []string{"--arch", "ARM64", "--limit", "2"},
			wantNames: []string{"b", "e"}, wantPages: 2, wantPageSize: hub.MaxPageSize,
		},
		{
			name:      "arch filter with no matches",
			args:      []string{"--arch", "s390x"},
			wantNames: []string{}, wantPages: 3, wantPageSize: hub.MaxPageSize,
		},
		{
			name:      "page limit keeps partial results and warns",
			args:      []string{"--limit", "0"},
			errs:      map[int]error{2: &hub.APIError{StatusCode: 403, Message: "pagination offset too large for anonymous requests"}},
			wantNames: []string{"a", "b", "c"}, wantPages: 2, wantPageSize: hub.MaxPageSize,
			wantStderr: "warning: stopped after 3 tags",
		},
		{
			name:         "not found fails",
			errs:         map[int]error{1: &hub.APIError{StatusCode: 404, Message: "object not found"}},
			wantCode:     1,
			wantPages:    1,
			wantPageSize: 100,
			wantStderr:   "object not found",
			wantNoStdout: true,
		},
		{
			name:         "rate limit mid-way fails rather than printing a partial list",
			args:         []string{"--limit", "0"},
			errs:         map[int]error{2: &hub.RateLimitError{RetryAfter: 30 * time.Second}},
			wantCode:     1,
			wantPages:    2,
			wantPageSize: hub.MaxPageSize,
			wantStderr:   "retry in 30s",
			wantNoStdout: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &fakeRegistry{tagPages: pages, errs: tt.errs}
			args := append([]string{"tags", "nginx", "--json"}, tt.args...)
			code, stdout, stderr := runApp(t, reg, args...)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tt.wantStderr)
			}
			if tt.wantNoStdout {
				if stdout != "" {
					t.Errorf("stdout = %q, want empty", stdout)
				}
			} else if got := tagNames(t, stdout); strings.Join(got, ",") != strings.Join(tt.wantNames, ",") {
				t.Errorf("tags = %v, want %v", got, tt.wantNames)
			}
			if len(reg.calls) != tt.wantPages {
				t.Errorf("fetched %d pages, want %d", len(reg.calls), tt.wantPages)
			}
			for i, c := range reg.calls {
				if c.Page != i+1 || c.PageSize != tt.wantPageSize || c.Order != hub.OrderNewest {
					t.Errorf("call %d = %+v, want page %d size %d newest first", i, c, i+1, tt.wantPageSize)
				}
			}
		})
	}
}

// TestTagsEndToEnd runs the real client against a recorded response, so the
// wire format and the JSON output are checked together.
func TestTagsEndToEnd(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tags_nginx_p1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	client, err := hub.NewClient(hub.Options{BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runApp(t, client, "tags", "docker.io/library/nginx", "--json", "--limit", "1", "--arch", "linux/arm/v7")
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, stderr)
	}
	if gotPath != "/v2/namespaces/library/repositories/nginx/tags" {
		t.Errorf("path = %q", gotPath)
	}
	var tags []tagJSON
	if err := json.Unmarshal([]byte(stdout), &tags); err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 {
		t.Fatalf("got %d tags, want 1", len(tags))
	}
	if tags[0].Digest == nil || *tags[0].Digest != "sha256:756444d493424be61c13714ec55c97a733942d67772fca9d1724fb264f8bde08" {
		t.Errorf("digest = %v, want the index digest", tags[0].Digest)
	}
	want := "nginx:stable-alpine3.24-perl@sha256:756444d493424be61c13714ec55c97a733942d67772fca9d1724fb264f8bde08"
	if tags[0].PinnedReference == nil || *tags[0].PinnedReference != want {
		t.Errorf("pinned_reference = %v, want %s", tags[0].PinnedReference, want)
	}
	if len(tags[0].Platforms) != 8 {
		t.Errorf("got %d platforms, want 8 (attestations excluded)", len(tags[0].Platforms))
	}
}

// The usage text is the only help a piped invocation gets; keep every form
// in one block, ahead of the prose and the flag list.
func TestUsageListsEveryForm(t *testing.T) {
	_, _, stderr := runApp(t, &fakeRegistry{}, "-h")
	block, _, _ := strings.Cut(stderr, "\n\n")
	for _, form := range []string{"hubtui search <query> --json", "hubtui tags <image> --json", "hubtui <image>", "hubtui --version"} {
		if !strings.Contains(block, form) {
			t.Errorf("usage block lacks %q:\n%s", form, block)
		}
	}
}

func TestResolveVersion(t *testing.T) {
	if got := resolveVersion("v1.2.3"); got != "v1.2.3" {
		t.Errorf("resolveVersion(injected) = %q, want %q", got, "v1.2.3")
	}
	if got := resolveVersion(""); got == "" {
		t.Error("resolveVersion(\"\") returned empty string")
	}
}
