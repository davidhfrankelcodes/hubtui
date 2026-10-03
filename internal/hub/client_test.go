package hub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixture returns a handler that serves a recorded response from testdata/.
func fixture(t *testing.T, status int, name string) http.HandlerFunc {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}

func raw(status int, header http.Header, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range header {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// newTestClient starts a server running h and records each request it sees.
func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *[]*http.Request) {
	t.Helper()
	var reqs []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs = append(reqs, r)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(Options{BaseURL: srv.URL, HTTPClient: srv.Client(), UserAgent: "hubtui-test"})
	if err != nil {
		t.Fatal(err)
	}
	return c, &reqs
}

func TestSearch(t *testing.T) {
	tests := []struct {
		name        string
		handler     http.HandlerFunc
		query       string
		opts        PageOptions
		wantQuery   string
		wantErr     error
		wantResults int
		wantHasNext bool
		check       func(t *testing.T, p *SearchPage)
	}{
		{
			name:        "first page",
			handler:     fixture(t, 200, "search_nginx_p1.json"),
			query:       "nginx",
			opts:        PageOptions{PageSize: 3},
			wantQuery:   "page=1&page_size=3&query=nginx",
			wantResults: 3,
			wantHasNext: true,
			check: func(t *testing.T, p *SearchPage) {
				want := SearchResult{
					Repo:        Repo{"library", "nginx"},
					Description: "Official build of Nginx.",
					Stars:       21396,
					Pulls:       13413760258,
					Official:    true,
				}
				if p.Results[0] != want {
					t.Errorf("Results[0] = %+v, want %+v", p.Results[0], want)
				}
				if got := p.Results[1].Repo; got != (Repo{"nginx", "nginx-ingress"}) || p.Results[1].Official {
					t.Errorf("Results[1] = %+v", p.Results[1])
				}
				if p.Total != 293697 {
					t.Errorf("Total = %d", p.Total)
				}
			},
		},
		{
			name:        "second page",
			handler:     fixture(t, 200, "search_nginx_p2.json"),
			query:       "nginx",
			opts:        PageOptions{Page: 2, PageSize: 3},
			wantQuery:   "page=2&page_size=3&query=nginx",
			wantResults: 3,
			wantHasNext: true,
		},
		{
			name:        "no results",
			handler:     fixture(t, 200, "search_empty.json"),
			query:       "zzqqxxnotarepo123",
			wantQuery:   "page=1&page_size=50&query=zzqqxxnotarepo123",
			wantResults: 0,
			wantHasNext: false,
		},
		{
			name:      "page size capped",
			handler:   fixture(t, 200, "search_empty.json"),
			query:     "x",
			opts:      PageOptions{PageSize: 500},
			wantQuery: "page=1&page_size=100&query=x",
		},
		{
			name:    "anonymous pagination limit",
			handler: fixture(t, 403, "search_pagination_limit.json"),
			query:   "nginx",
			opts:    PageOptions{Page: 3, PageSize: 100},
			wantErr: ErrPageLimit,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, reqs := newTestClient(t, tt.handler)
			p, err := c.Search(context.Background(), tt.query, tt.opts)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			r := (*reqs)[0]
			if r.URL.Path != "/v2/search/repositories/" {
				t.Errorf("path = %q", r.URL.Path)
			}
			if tt.wantQuery != "" && r.URL.RawQuery != tt.wantQuery {
				t.Errorf("query = %q, want %q", r.URL.RawQuery, tt.wantQuery)
			}
			if len(p.Results) != tt.wantResults {
				t.Errorf("len(Results) = %d, want %d", len(p.Results), tt.wantResults)
			}
			if p.HasNext != tt.wantHasNext {
				t.Errorf("HasNext = %v, want %v", p.HasNext, tt.wantHasNext)
			}
			if tt.check != nil {
				tt.check(t, p)
			}
		})
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	c, reqs := newTestClient(t, raw(500, nil, ""))
	if _, err := c.Search(context.Background(), "   ", PageOptions{}); err == nil {
		t.Fatal("expected error for empty query")
	}
	if len(*reqs) != 0 {
		t.Errorf("made %d requests for an empty query", len(*reqs))
	}
}

func TestTags(t *testing.T) {
	tests := []struct {
		name        string
		handler     http.HandlerFunc
		repo        Repo
		opts        TagsOptions
		wantPath    string
		wantQuery   string
		wantTags    int
		wantHasNext bool
		check       func(t *testing.T, p *TagPage)
	}{
		{
			name:        "official multi-arch with attestations",
			handler:     fixture(t, 200, "tags_nginx_p1.json"),
			repo:        Repo{"library", "nginx"},
			opts:        TagsOptions{PageOptions: PageOptions{PageSize: 3}},
			wantPath:    "/v2/namespaces/library/repositories/nginx/tags",
			wantQuery:   "ordering=last_updated&page=1&page_size=3",
			wantTags:    3,
			wantHasNext: true,
			check: func(t *testing.T, p *TagPage) {
				tag := p.Tags[0]
				if tag.Name != "stable-alpine3.24-perl" {
					t.Errorf("Name = %q", tag.Name)
				}
				// The index digest, confirmed with `docker buildx imagetools inspect`.
				if tag.Digest != "sha256:756444d493424be61c13714ec55c97a733942d67772fca9d1724fb264f8bde08" {
					t.Errorf("Digest = %q", tag.Digest)
				}
				if tag.MediaType != "application/vnd.oci.image.index.v1+json" {
					t.Errorf("MediaType = %q", tag.MediaType)
				}
				wantPushed := time.Date(2026, 9, 29, 19, 52, 23, 733189000, time.UTC)
				if !tag.Pushed.Equal(wantPushed) {
					t.Errorf("Pushed = %v, want %v", tag.Pushed, wantPushed)
				}
				var plats []string
				for _, pl := range tag.Platforms {
					plats = append(plats, pl.String())
				}
				want := []string{"linux/amd64", "linux/ppc64le", "linux/386", "linux/s390x", "linux/arm/v7", "linux/arm64/v8", "linux/arm/v6", "linux/riscv64"}
				if len(plats) != len(want) {
					t.Fatalf("platforms = %v, want %v (attestations must be dropped)", plats, want)
				}
				for i := range want {
					if plats[i] != want[i] {
						t.Errorf("platform[%d] = %q, want %q", i, plats[i], want[i])
					}
				}
				amd64 := tag.Platforms[0]
				if amd64.Digest != "sha256:604b4e5233f9c948f4d26392354d76c582a4ce19833816374c1307b7fb59b53b" {
					t.Errorf("amd64 digest = %q, want the per-platform manifest digest", amd64.Digest)
				}
				if amd64.Size != 36364210 {
					t.Errorf("amd64 size = %d", amd64.Size)
				}
				if p.Total != 1339 {
					t.Errorf("Total = %d", p.Total)
				}
			},
		},
		{
			name:        "second page",
			handler:     fixture(t, 200, "tags_nginx_p2.json"),
			repo:        Repo{"library", "nginx"},
			opts:        TagsOptions{PageOptions: PageOptions{Page: 2, PageSize: 3}},
			wantPath:    "/v2/namespaces/library/repositories/nginx/tags",
			wantQuery:   "ordering=last_updated&page=2&page_size=3",
			wantTags:    3,
			wantHasNext: true,
		},
		{
			name:        "user namespace, docker manifest list",
			handler:     fixture(t, 200, "tags_grafana_p1.json"),
			repo:        Repo{"grafana", "grafana"},
			opts:        TagsOptions{PageOptions: PageOptions{PageSize: 3}},
			wantPath:    "/v2/namespaces/grafana/repositories/grafana/tags",
			wantQuery:   "ordering=last_updated&page=1&page_size=3",
			wantTags:    3,
			wantHasNext: true,
			check: func(t *testing.T, p *TagPage) {
				tag := p.Tags[0]
				if tag.Digest != "sha256:5c1a2935a24ddfc328cb387939ae669ad1405338d38596b7ef55bc36ec90f2e8" {
					t.Errorf("Digest = %q", tag.Digest)
				}
				if len(tag.Platforms) != 3 || tag.Platforms[1].String() != "linux/arm64" {
					t.Errorf("Platforms = %+v", tag.Platforms)
				}
			},
		},
		{
			name:     "name order",
			handler:  fixture(t, 200, "tags_nginx_p1.json"),
			repo:     Repo{"library", "nginx"},
			opts:     TagsOptions{Order: OrderName},
			wantPath: "/v2/namespaces/library/repositories/nginx/tags",
			// "-name" is ascending on this API.
			wantQuery:   "ordering=-name&page=1&page_size=50",
			wantTags:    3,
			wantHasNext: true,
		},
		{
			name:        "legacy tags with null digests",
			handler:     fixture(t, 200, "tags_nginx_legacy.json"),
			repo:        Repo{"library", "nginx"},
			wantPath:    "/v2/namespaces/library/repositories/nginx/tags",
			wantTags:    5,
			wantHasNext: true,
			check: func(t *testing.T, p *TagPage) {
				if p.Tags[0].Digest == "" {
					t.Error("1.7.7 should have a digest")
				}
				old := p.Tags[3]
				if old.Name != "1.9.8" || old.Digest != "" {
					t.Errorf("tag 3 = %q digest %q, want 1.9.8 with no digest", old.Name, old.Digest)
				}
				if len(old.Platforms) != 1 || old.Platforms[0].String() != "amd64" {
					t.Errorf("Platforms = %+v", old.Platforms)
				}
			},
		},
		{
			name:        "last page",
			handler:     fixture(t, 200, "tags_hello-seattle_last.json"),
			repo:        Repo{"library", "hello-seattle"},
			opts:        TagsOptions{PageOptions: PageOptions{Page: 3, PageSize: 3}},
			wantPath:    "/v2/namespaces/library/repositories/hello-seattle/tags",
			wantQuery:   "ordering=last_updated&page=3&page_size=3",
			wantTags:    2,
			wantHasNext: false,
			check: func(t *testing.T, p *TagPage) {
				if got := p.Tags[0].Platforms[0].String(); got != "windows/amd64" {
					t.Errorf("platform = %q", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, reqs := newTestClient(t, tt.handler)
			p, err := c.Tags(context.Background(), tt.repo, tt.opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			r := (*reqs)[0]
			if r.URL.Path != tt.wantPath {
				t.Errorf("path = %q, want %q", r.URL.Path, tt.wantPath)
			}
			if tt.wantQuery != "" && r.URL.RawQuery != tt.wantQuery {
				t.Errorf("query = %q, want %q", r.URL.RawQuery, tt.wantQuery)
			}
			if got := r.Header.Get("User-Agent"); got != "hubtui-test" {
				t.Errorf("User-Agent = %q", got)
			}
			if len(p.Tags) != tt.wantTags {
				t.Errorf("len(Tags) = %d, want %d", len(p.Tags), tt.wantTags)
			}
			if p.HasNext != tt.wantHasNext {
				t.Errorf("HasNext = %v, want %v", p.HasNext, tt.wantHasNext)
			}
			if tt.check != nil {
				tt.check(t, p)
			}
		})
	}
}

func TestTagsErrors(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name           string
		handler        http.HandlerFunc
		wantIs         error
		wantRetryAfter time.Duration
		wantRateLimit  bool
		wantMessage    string
	}{
		{
			name:        "repository not found",
			handler:     fixture(t, 404, "tags_not_found.json"),
			wantIs:      ErrNotFound,
			wantMessage: "object not found",
		},
		{
			name:        "anonymous pagination limit",
			handler:     fixture(t, 403, "tags_pagination_limit.json"),
			wantIs:      ErrPageLimit,
			wantMessage: "pagination offset too large",
		},
		{
			name:        "other 403 is not a page limit",
			handler:     raw(403, nil, `{"message":"forbidden"}`),
			wantMessage: "forbidden",
		},
		{
			name:          "429 with Retry-After seconds",
			handler:       raw(429, http.Header{"Retry-After": {"30"}}, ""),
			wantRateLimit: true, wantRetryAfter: 30 * time.Second,
		},
		{
			name:          "429 with Retry-After HTTP date",
			handler:       raw(429, http.Header{"Retry-After": {now.Add(90 * time.Second).Format(http.TimeFormat)}}, ""),
			wantRateLimit: true, wantRetryAfter: 90 * time.Second,
		},
		{
			name:          "429 falls back to X-RateLimit-Reset",
			handler:       raw(429, http.Header{"X-Ratelimit-Reset": {"1790942520"}}, ""), // now + 120s
			wantRateLimit: true, wantRetryAfter: 120 * time.Second,
		},
		{
			name:          "429 reset in the past backs off anyway",
			handler:       raw(429, http.Header{"Retry-After": {now.Add(-time.Minute).Format(http.TimeFormat)}}, ""),
			wantRateLimit: true, wantRetryAfter: defaultRateLimitWait,
		},
		{
			name:          "429 without hints backs off by default",
			handler:       raw(429, nil, "slow down"),
			wantRateLimit: true, wantRetryAfter: defaultRateLimitWait,
		},
		{
			name:        "HTML error page from the CDN",
			handler:     raw(502, http.Header{"Content-Type": {"text/html"}}, "<html><body>Bad gateway</body></html>"),
			wantMessage: "unexpected response",
		},
		{name: "malformed JSON", handler: raw(200, nil, `{"count": 3, "results": [`)},
		{name: "wrong JSON shape", handler: raw(200, nil, `{"count": "many", "results": {}}`)},
		{name: "HTML with 200", handler: raw(200, nil, "<html>maintenance</html>")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, tt.handler)
			c.now = func() time.Time { return now }

			p, err := c.Tags(context.Background(), Repo{"library", "nginx"}, TagsOptions{})
			if err == nil {
				t.Fatalf("expected error, got page %+v", p)
			}

			var rl *RateLimitError
			if got := errors.As(err, &rl); got != tt.wantRateLimit {
				t.Fatalf("RateLimitError = %v, want %v (err: %v)", got, tt.wantRateLimit, err)
			}
			if rl != nil && rl.RetryAfter != tt.wantRetryAfter {
				t.Errorf("RetryAfter = %v, want %v", rl.RetryAfter, tt.wantRetryAfter)
			}

			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("errors.Is(%v, %v) = false", err, tt.wantIs)
			}
			for _, sentinel := range []error{ErrNotFound, ErrPageLimit} {
				if !errors.Is(tt.wantIs, sentinel) && errors.Is(err, sentinel) {
					t.Errorf("error unexpectedly matches %v", sentinel)
				}
			}

			if tt.wantMessage != "" {
				var apiErr *APIError
				if !errors.As(err, &apiErr) {
					t.Fatalf("want *APIError, got %T: %v", err, err)
				}
				if !strings.Contains(apiErr.Message, tt.wantMessage) {
					t.Errorf("Message = %q, want containing %q", apiErr.Message, tt.wantMessage)
				}
			}
		})
	}
}

func TestContextCanceled(t *testing.T) {
	block := make(chan struct{})
	c, _ := newTestClient(t, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	})
	defer close(block)

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := c.Tags(ctx, Repo{"library", "nginx"}, TagsOptions{})
		errc <- err
	}()
	cancel()

	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not return after cancel")
	}
}

func TestNewClientRejectsRelativeURL(t *testing.T) {
	if _, err := NewClient(Options{BaseURL: "/v2"}); err == nil {
		t.Error("expected error for relative base URL")
	}
}
