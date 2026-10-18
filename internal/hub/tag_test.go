package hub

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestTag(t *testing.T) {
	postgres := Repo{"library", "postgres"}
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		tag      string
		wantPath string // empty: no request may be made
		wantErr  string
		wantIs   error
		check    func(t *testing.T, tag *Tag)
	}{
		{
			name:     "found",
			handler:  fixture(t, 200, "tag_postgres_17-alpine.json"),
			tag:      "17-alpine",
			wantPath: "/v2/namespaces/library/repositories/postgres/tags/17-alpine",
			check: func(t *testing.T, tag *Tag) {
				// The index digest, confirmed with `docker buildx imagetools inspect`.
				if tag.Name != "17-alpine" || tag.Digest != "sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24" {
					t.Errorf("tag = %s %s", tag.Name, tag.Digest)
				}
				if tag.MediaType != "application/vnd.oci.image.index.v1+json" || tag.Pushed.IsZero() {
					t.Errorf("media type %q, pushed %v", tag.MediaType, tag.Pushed)
				}
				// 16 images on the wire, half of them attestations.
				if len(tag.Platforms) != 8 || tag.Platforms[0].String() != "linux/amd64" {
					t.Errorf("platforms = %v", tag.Platforms)
				}
			},
		},
		{
			name:     "missing tag",
			handler:  fixture(t, 404, "tag_not_found.json"),
			tag:      "no-such-tag-xyz",
			wantPath: "/v2/namespaces/library/repositories/postgres/tags/no-such-tag-xyz",
			wantIs:   ErrNotFound,
			wantErr:  "tag 'no-such-tag-xyz' not found",
		},
		{
			// A tag name is a path segment; an invalid one must not reach
			// the network at all.
			name:    "invalid name",
			handler: fixture(t, 200, "tag_postgres_17-alpine.json"),
			tag:     "../../repositories",
			wantErr: "invalid tag",
		},
		{
			name:     "answer for a different tag",
			handler:  fixture(t, 200, "tag_postgres_17-alpine.json"),
			tag:      "18-alpine",
			wantPath: "/v2/namespaces/library/repositories/postgres/tags/18-alpine",
			wantErr:  `answered for tag "17-alpine"`,
		},
		{
			name:     "malformed body",
			handler:  raw(200, nil, "{"),
			tag:      "17",
			wantPath: "/v2/namespaces/library/repositories/postgres/tags/17",
			wantErr:  "getting postgres:17",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, reqs := newTestClient(t, tt.handler)
			tag, err := c.Tag(context.Background(), postgres, tt.tag)
			switch {
			case tt.wantPath == "" && len(*reqs) != 0:
				t.Errorf("made %d requests, want none", len(*reqs))
			case tt.wantPath != "" && (len(*reqs) != 1 || (*reqs)[0].URL.Path != tt.wantPath):
				t.Errorf("requests = %v, want one to %s", *reqs, tt.wantPath)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
					t.Errorf("err = %v, want errors.Is %v", err, tt.wantIs)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, tag)
		})
	}
}

func TestTagsNameFilter(t *testing.T) {
	c, reqs := newTestClient(t, fixture(t, 200, "tags_postgres_name_17.json"))
	p, err := c.Tags(context.Background(), Repo{"library", "postgres"}, TagsOptions{Name: "17."})
	if err != nil {
		t.Fatal(err)
	}
	if got := (*reqs)[0].URL.Query().Get("name"); got != "17." {
		t.Errorf("name = %q, want 17.", got)
	}
	if len(p.Tags) == 0 || p.HasNext {
		t.Fatalf("got %d tags, HasNext %v", len(p.Tags), p.HasNext)
	}
	for _, tag := range p.Tags {
		if !strings.HasPrefix(tag.Name, "17.") {
			t.Errorf("filtered page holds %s", tag.Name)
		}
	}

	// Without a filter the parameter is left out, not sent empty.
	if _, err := c.Tags(context.Background(), Repo{"library", "postgres"}, TagsOptions{}); err != nil {
		t.Fatal(err)
	}
	if q := (*reqs)[1].URL.Query(); q.Has("name") {
		t.Errorf("unfiltered request sent name=%q", q.Get("name"))
	}
}

// A filtered page is a different list; serving it for the unfiltered one
// would silently hide tags.
func TestCacheKeysTagsByName(t *testing.T) {
	ctx := context.Background()
	next := &countingRegistry{}
	c := NewCache(next, DefaultCacheTTL)
	nginx := Repo{"library", "nginx"}

	for _, name := range []string{"", "1.", "", "1."} {
		if _, err := c.Tags(ctx, nginx, TagsOptions{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if next.tags != 2 {
		t.Errorf("%d fetches, want one per filter", next.tags)
	}

	for range 2 {
		if _, err := c.Tag(ctx, nginx, "1.27"); err != nil {
			t.Fatal(err)
		}
	}
	if next.tag != 2 {
		t.Errorf("Tag was answered from the cache; %d fetches, want 2", next.tag)
	}
}
