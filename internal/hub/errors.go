package hub

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrNotFound means the repository or tag does not exist.
	ErrNotFound = errors.New("not found")
	// ErrPageLimit means Docker Hub refused to page further. Anonymous
	// requests stop after 1000 tags or 200 search results.
	ErrPageLimit = errors.New("docker hub pagination limit reached")
	// ErrAuth means Docker Hub rejected the configured credentials.
	ErrAuth = errors.New("docker hub login failed")
)

// APIError is a non-success response from Docker Hub. Use errors.Is with
// ErrNotFound or ErrPageLimit to classify it.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("docker hub: %d %s: %s", e.StatusCode, http.StatusText(e.StatusCode), e.Message)
}

// Is lets callers match on the sentinel errors without inspecting status codes.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.StatusCode == http.StatusNotFound
	case ErrPageLimit:
		// Hub uses a plain 403 for this; the message is the only discriminator.
		return e.StatusCode == http.StatusForbidden && strings.Contains(e.Message, "pagination")
	}
	return false
}

// RateLimitError is returned for HTTP 429, and for any request made before
// the wait it announced is over. The client never retries on its own.
type RateLimitError struct {
	// RetryAfter is how long to wait: the server's hint, or a default
	// backoff when it gave none.
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("docker hub rate limit reached; retry in %s", e.RetryAfter.Round(time.Second))
	}
	return "docker hub rate limit reached"
}

// retryAfter prefers the standard Retry-After header (seconds or HTTP date)
// and falls back to Hub's X-RateLimit-Reset (unix seconds).
func retryAfter(h http.Header, now time.Time) time.Duration {
	var d time.Duration
	if v := h.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			d = time.Duration(secs) * time.Second
		} else if t, err := http.ParseTime(v); err == nil {
			d = t.Sub(now)
		}
	} else if v := h.Get("X-RateLimit-Reset"); v != "" {
		if unix, err := strconv.ParseInt(v, 10, 64); err == nil {
			d = time.Unix(unix, 0).Sub(now)
		}
	}
	return max(d, 0)
}
