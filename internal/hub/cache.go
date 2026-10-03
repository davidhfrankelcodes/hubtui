package hub

import (
	"context"
	"sync"
	"time"
)

// DefaultCacheTTL keeps the UI snappy when moving between screens without
// hiding a fresh push for long.
const DefaultCacheTTL = 5 * time.Minute

// Cache is a Registry that remembers successful responses for a while.
// Setting Fresh on a page-1 request skips the cache and drops every cached
// page of that list, so later pages are refetched too and cannot mix old and
// new results.
//
// Cached pages are shared between callers and must not be modified.
type Cache struct {
	next Registry
	ttl  time.Duration
	now  func() time.Time

	mu     sync.Mutex
	search map[searchKey]cacheEntry[*SearchPage]
	tags   map[tagsKey]cacheEntry[*TagPage]
}

type searchKey struct {
	query          string
	page, pageSize int
}

type tagsKey struct {
	repo           Repo
	order          TagOrder
	page, pageSize int
}

type cacheEntry[T any] struct {
	value   T
	expires time.Time
}

var _ Registry = (*Cache)(nil)

// NewCache wraps next with an in-memory cache.
func NewCache(next Registry, ttl time.Duration) *Cache {
	return &Cache{
		next:   next,
		ttl:    ttl,
		now:    time.Now,
		search: map[searchKey]cacheEntry[*SearchPage]{},
		tags:   map[tagsKey]cacheEntry[*TagPage]{},
	}
}

// Search implements Registry.
func (c *Cache) Search(ctx context.Context, query string, opts PageOptions) (*SearchPage, error) {
	page, size := opts.normalize()
	key := searchKey{query: query, page: page, pageSize: size}

	c.mu.Lock()
	if opts.Fresh {
		for k := range c.search {
			if k.query == query {
				delete(c.search, k)
			}
		}
	} else if e, ok := c.search[key]; ok && c.now().Before(e.expires) {
		c.mu.Unlock()
		return e.value, nil
	}
	c.mu.Unlock()

	res, err := c.next.Search(ctx, query, opts)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.search[key] = cacheEntry[*SearchPage]{value: res, expires: c.now().Add(c.ttl)}
	c.mu.Unlock()
	return res, nil
}

// Tags implements Registry.
func (c *Cache) Tags(ctx context.Context, repo Repo, opts TagsOptions) (*TagPage, error) {
	page, size := opts.normalize()
	key := tagsKey{repo: repo, order: opts.Order, page: page, pageSize: size}

	c.mu.Lock()
	if opts.Fresh {
		for k := range c.tags {
			if k.repo == repo {
				delete(c.tags, k)
			}
		}
	} else if e, ok := c.tags[key]; ok && c.now().Before(e.expires) {
		c.mu.Unlock()
		return e.value, nil
	}
	c.mu.Unlock()

	res, err := c.next.Tags(ctx, repo, opts)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.tags[key] = cacheEntry[*TagPage]{value: res, expires: c.now().Add(c.ttl)}
	c.mu.Unlock()
	return res, nil
}
