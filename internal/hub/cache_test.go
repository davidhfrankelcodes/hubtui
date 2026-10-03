package hub

import (
	"context"
	"errors"
	"testing"
	"time"
)

type countingRegistry struct {
	searches, tags int
	err            error
}

func (r *countingRegistry) Search(context.Context, string, PageOptions) (*SearchPage, error) {
	r.searches++
	return &SearchPage{Total: r.searches}, r.err
}

func (r *countingRegistry) Tags(_ context.Context, _ Repo, opts TagsOptions) (*TagPage, error) {
	r.tags++
	if r.err != nil {
		return nil, r.err
	}
	return &TagPage{Page: opts.Page, Total: r.tags}, nil
}

func TestCacheTags(t *testing.T) {
	ctx := context.Background()
	nginx := Repo{"library", "nginx"}
	redis := Repo{"library", "redis"}
	page := func(n int) TagsOptions { return TagsOptions{PageOptions: PageOptions{Page: n}} }
	fresh := TagsOptions{PageOptions: PageOptions{Page: 1, Fresh: true}}

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	next := &countingRegistry{}
	c := NewCache(next, time.Minute)
	c.now = func() time.Time { return now }

	steps := []struct {
		name      string
		repo      Repo
		opts      TagsOptions
		advance   time.Duration
		wantCalls int
	}{
		{"first fetch hits the network", nginx, page(1), 0, 1},
		{"repeat is cached", nginx, page(1), 0, 1},
		{"zero page means page 1", nginx, page(0), 0, 1},
		{"other page is a miss", nginx, page(2), 0, 2},
		{"other repo is a miss", redis, page(1), 0, 3},
		{"other order is a miss", nginx, TagsOptions{Order: OrderName}, 0, 4},
		{"fresh bypasses", nginx, fresh, 0, 5},
		{"fresh result is cached", nginx, page(1), 0, 5},
		{"fresh dropped later pages of the same repo", nginx, page(2), 0, 6},
		{"fresh left other repos alone", redis, page(1), 0, 6},
		{"still valid just before expiry", redis, page(1), 59 * time.Second, 6},
		{"expired entries refetch", redis, page(1), time.Second, 7},
	}
	for _, s := range steps {
		now = now.Add(s.advance)
		if _, err := c.Tags(ctx, s.repo, s.opts); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if next.tags != s.wantCalls {
			t.Fatalf("%s: upstream calls = %d, want %d", s.name, next.tags, s.wantCalls)
		}
	}
}

func TestCacheDoesNotStoreErrors(t *testing.T) {
	next := &countingRegistry{err: errors.New("boom")}
	c := NewCache(next, time.Minute)
	for range 2 {
		if _, err := c.Tags(context.Background(), Repo{"library", "nginx"}, TagsOptions{}); err == nil {
			t.Fatal("expected error")
		}
	}
	if next.tags != 2 {
		t.Errorf("upstream calls = %d, want 2", next.tags)
	}
}

func TestCacheSearch(t *testing.T) {
	next := &countingRegistry{}
	c := NewCache(next, time.Minute)
	ctx := context.Background()
	_, _ = c.Search(ctx, "nginx", PageOptions{})
	_, _ = c.Search(ctx, "nginx", PageOptions{Page: 1, PageSize: DefaultPageSize})
	_, _ = c.Search(ctx, "redis", PageOptions{})
	_, _ = c.Search(ctx, "nginx", PageOptions{Fresh: true})
	if next.searches != 3 {
		t.Errorf("upstream calls = %d, want 3", next.searches)
	}
}
