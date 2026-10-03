package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
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
	// Fresh asks caching layers to bypass and refresh their copy.
	Fresh bool
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

// defaultRateLimitWait applies when a 429 carries no hint, so callers still
// back off instead of retrying at once.
const defaultRateLimitWait = 30 * time.Second

// Options configures a Client.
type Options struct {
	BaseURL    string // defaults to DefaultBaseURL
	HTTPClient *http.Client
	UserAgent  string
	// Username and Token (a personal access token) enable authenticated
	// requests. Both or neither must be set.
	Username string
	Token    Secret
}

// Client talks to the Docker Hub API. It is safe for concurrent use.
type Client struct {
	baseURL   *url.URL
	http      *http.Client
	userAgent string
	username  string
	token     Secret
	now       func() time.Time

	mu sync.Mutex
	// bearer is the access token from logging in; empty until the first
	// authenticated request or after it is rejected.
	bearer Secret
	// blockedUntil is set by a 429; requests before then fail without
	// touching the network, so nothing can retry in a tight loop.
	blockedUntil time.Time
	// rateMu guards the quota separately: mu is held through a login, and
	// the login response reports the quota too.
	rateMu sync.Mutex
	// rate is the quota from the last response that reported one.
	rate      RateLimit
	rateKnown bool
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

	if (opts.Username == "") != (opts.Token == "") {
		return nil, errors.New("docker hub credentials need both a username and a token")
	}

	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		baseURL:   u,
		http:      hc,
		userAgent: opts.UserAgent,
		username:  opts.Username,
		token:     opts.Token,
		now:       time.Now,
	}, nil
}

// Format implements fmt.Formatter. fmt cannot call Secret's Format on
// unexported fields, so the client redacts itself as a whole.
func (c *Client) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprintf(f, "hub.Client{base: %s, user: %q, authenticated: %t}", c.baseURL, c.username, c.Authenticated())
}

// Authenticated reports whether the client sends credentials.
func (c *Client) Authenticated() bool { return c.token != "" }

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
	if err := c.checkBlocked(); err != nil {
		return err
	}

	bearer, err := c.authorization(ctx)
	if err != nil {
		return err
	}
	status, header, body, err := c.do(ctx, http.MethodGet, u, nil, bearer)
	if err != nil {
		return err
	}
	// An access token can expire mid-session; log in again once and retry.
	if status == http.StatusUnauthorized && c.Authenticated() {
		c.dropBearer(bearer)
		if bearer, err = c.authorization(ctx); err != nil {
			return err
		}
		if status, header, body, err = c.do(ctx, http.MethodGet, u, nil, bearer); err != nil {
			return err
		}
	}

	if err := c.checkStatus(status, header, body); err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

func (c *Client) checkStatus(status int, header http.Header, body []byte) error {
	switch {
	case status == http.StatusTooManyRequests:
		wait := retryAfter(header, c.now())
		if wait <= 0 {
			wait = defaultRateLimitWait
		}
		c.mu.Lock()
		c.blockedUntil = c.now().Add(wait)
		c.mu.Unlock()
		return &RateLimitError{RetryAfter: wait}
	case status != http.StatusOK:
		return &APIError{StatusCode: status, Message: errorMessage(body)}
	}
	return nil
}

func (c *Client) checkBlocked() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if wait := c.blockedUntil.Sub(c.now()); wait > 0 {
		return &RateLimitError{RetryAfter: wait}
	}
	return nil
}

// authorization returns the bearer token to send, logging in if needed.
// Anonymous clients get "".
func (c *Client) authorization(ctx context.Context) (Secret, error) {
	if !c.Authenticated() {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bearer != "" {
		return c.bearer, nil
	}
	// Holding the lock through the login makes concurrent first requests
	// share one login instead of racing to make several.
	b, err := c.login(ctx)
	if err != nil {
		return "", err
	}
	c.bearer = b
	return b, nil
}

// dropBearer forgets a rejected token, unless another request already
// replaced it.
func (c *Client) dropBearer(rejected Secret) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bearer == rejected {
		c.bearer = ""
	}
}

// login exchanges the personal access token for a short-lived access token.
// Called with c.mu held.
func (c *Client) login(ctx context.Context) (Secret, error) {
	payload, err := json.Marshal(struct {
		Identifier string `json:"identifier"`
		Secret     string `json:"secret"`
	}{c.username, c.token.Reveal()})
	if err != nil {
		return "", fmt.Errorf("encoding login: %w", err)
	}

	status, header, body, err := c.do(ctx, http.MethodPost, c.baseURL.JoinPath("v2", "auth", "token"), payload, "")
	if err != nil {
		return "", fmt.Errorf("logging in as %s: %w", c.username, err)
	}
	if status == http.StatusTooManyRequests {
		wait := retryAfter(header, c.now())
		if wait <= 0 {
			wait = defaultRateLimitWait
		}
		c.blockedUntil = c.now().Add(wait)
		return "", &RateLimitError{RetryAfter: wait}
	}
	switch status {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", fmt.Errorf("logging in as %s: %w: %s", c.username, ErrAuth, errorMessage(body))
	default:
		// Hub being down is not a credentials problem; telling the user to
		// check their token would send them the wrong way. Not an *APIError
		// either: a 404 here must not read as "repository not found".
		return "", fmt.Errorf("logging in as %s: docker hub: %d %s: %s", c.username, status, http.StatusText(status), errorMessage(body))
	}

	var res struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &res); err != nil || res.AccessToken == "" {
		return "", fmt.Errorf("logging in as %s: unexpected response: no access token", c.username)
	}
	return Secret(res.AccessToken), nil
}

// do sends one request and reads the whole (bounded) body. Error messages
// carry the URL but never a header, so credentials cannot leak through them.
func (c *Client) do(ctx context.Context, method string, u *url.URL, payload []byte, bearer Secret) (int, http.Header, []byte, error) {
	var reqBody io.Reader
	if payload != nil {
		reqBody = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer.Reveal())
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("reading response: %w", err)
	}
	// Every response counts against the quota, errors included.
	c.recordRateLimit(resp.Header)
	if len(body) > maxBodyBytes {
		return 0, nil, nil, fmt.Errorf("response exceeds %d bytes", maxBodyBytes)
	}
	return resp.StatusCode, resp.Header, body, nil
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
