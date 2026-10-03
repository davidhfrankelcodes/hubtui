package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the Docker Hub API root.
	DefaultBaseURL = "https://hub.docker.com"
	// DefaultPageSize is used when a request does not set one.
	DefaultPageSize = 50
	// MaxPageSize is the largest page Hub returns; larger requests are
	// silently capped by the server, so we cap them here to keep Page math honest.
	MaxPageSize = 100

	// A 100-tag page for a heavily multi-arch image is well under 1 MiB.
	maxBodyBytes = 16 << 20
)

// Registry is the read-only API the rest of the program depends on, so other
// registries can be added later without touching callers.
type Registry interface {
	Search(ctx context.Context, query string, opts PageOptions) (*SearchPage, error)
	Tags(ctx context.Context, repo Repo, opts TagsOptions) (*TagPage, error)
}

// PageOptions selects a page. Page is 1-based; zero values mean page 1 at
// DefaultPageSize.
type PageOptions struct {
	Page     int
	PageSize int
}

func (o PageOptions) normalize() (page, size int) {
	page, size = o.Page, o.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = DefaultPageSize
	}
	return page, min(size, MaxPageSize)
}

// TagOrder is the server-side sort order for tags.
type TagOrder int

const (
	// OrderNewest sorts by last push, newest first.
	OrderNewest TagOrder = iota
	// OrderName sorts by tag name, ascending.
	OrderName
)

// The API's ordering values are the reverse of what their names suggest:
// "last_updated" is newest first and "-name" is ascending. Verified live.
func (o TagOrder) param() string {
	if o == OrderName {
		return "-name"
	}
	return "last_updated"
}

// TagsOptions selects a page of tags and its order.
type TagsOptions struct {
	PageOptions
	Order TagOrder
}

// Options configures a Client.
type Options struct {
	BaseURL    string // defaults to DefaultBaseURL
	HTTPClient *http.Client
	UserAgent  string
}

// Client talks to the Docker Hub API.
type Client struct {
	baseURL   *url.URL
	http      *http.Client
	userAgent string
	now       func() time.Time
}

var _ Registry = (*Client)(nil)

// NewClient returns a Client for opts.
func NewClient(opts Options) (*Client, error) {
	base := opts.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("parsing base URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("base URL %q must be absolute", base)
	}

	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: u, http: hc, userAgent: opts.UserAgent, now: time.Now}, nil
}

// Search finds repositories matching query.
func (c *Client) Search(ctx context.Context, query string, opts PageOptions) (*SearchPage, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search query is empty")
	}
	page, size := opts.normalize()

	u := c.baseURL.JoinPath("v2", "search", "repositories/")
	u.RawQuery = url.Values{
		"query":     {query},
		"page":      {strconv.Itoa(page)},
		"page_size": {strconv.Itoa(size)},
	}.Encode()

	var raw searchResponse
	if err := c.get(ctx, u, &raw); err != nil {
		return nil, fmt.Errorf("searching %q: %w", query, err)
	}

	out := &SearchPage{Total: raw.Count, Page: page, HasNext: raw.Next != ""}
	for _, r := range raw.Results {
		out.Results = append(out.Results, SearchResult{
			Repo:        repoFromAPI(r.RepoName),
			Description: r.ShortDescription,
			Stars:       r.StarCount,
			Pulls:       r.PullCount,
			Official:    r.IsOfficial,
		})
	}
	return out, nil
}

// Tags lists one page of tags for repo.
func (c *Client) Tags(ctx context.Context, repo Repo, opts TagsOptions) (*TagPage, error) {
	page, size := opts.normalize()

	u := c.baseURL.JoinPath("v2", "namespaces", repo.Namespace, "repositories", repo.Name, "tags")
	u.RawQuery = url.Values{
		"page":      {strconv.Itoa(page)},
		"page_size": {strconv.Itoa(size)},
		"ordering":  {opts.Order.param()},
	}.Encode()

	var raw tagsResponse
	if err := c.get(ctx, u, &raw); err != nil {
		return nil, fmt.Errorf("listing tags for %s: %w", repo, err)
	}

	out := &TagPage{Total: raw.Count, Page: page, HasNext: raw.Next != ""}
	for _, r := range raw.Results {
		t := Tag{
			Name:      r.Name,
			Digest:    r.Digest,
			MediaType: r.MediaType,
			Pushed:    r.TagLastPushed,
		}
		if t.Pushed.IsZero() {
			t.Pushed = r.LastUpdated
		}
		for _, img := range r.Images {
			// Build attestations (provenance, SBOM) ride along in the index
			// as unknown/unknown; they are not runnable platforms.
			if img.OS == "unknown" && img.Architecture == "unknown" {
				continue
			}
			t.Platforms = append(t.Platforms, Platform{
				OS:      img.OS,
				Arch:    img.Architecture,
				Variant: img.Variant,
				Digest:  img.Digest,
				Size:    img.Size,
			})
		}
		out.Tags = append(out.Tags, t)
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, u *url.URL, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if len(body) > maxBodyBytes {
		return fmt.Errorf("response exceeds %d bytes", maxBodyBytes)
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return &RateLimitError{RetryAfter: retryAfter(resp.Header, c.now())}
	case resp.StatusCode != http.StatusOK:
		return &APIError{StatusCode: resp.StatusCode, Message: errorMessage(body)}
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// errorMessage pulls Hub's {"message": ...} out of an error body. Errors from
// the CDN in front of Hub are HTML, which is useless in a status bar.
func errorMessage(body []byte) string {
	var e struct {
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if json.Unmarshal(body, &e) == nil {
		if e.Message != "" {
			return e.Message
		}
		if e.Detail != "" {
			return e.Detail
		}
	}
	return "unexpected response"
}
