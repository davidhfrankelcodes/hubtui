package hub

import (
	"net/http"
	"strconv"
	"time"
)

// RateLimit is Docker Hub's request quota as of the last response.
//
// Hub's window rolls: every response carries X-RateLimit-Reset one minute
// ahead, and the quota is whole again once that passes. Verified live
// 2026-10-03: 180 anonymous, 600 signed in.
type RateLimit struct {
	Limit     int
	Remaining int
	Reset     time.Time
}

// At returns the quota as it stands at now: full again after Reset.
func (r RateLimit) At(now time.Time) RateLimit {
	if !r.Reset.IsZero() && !now.Before(r.Reset) {
		r.Remaining = r.Limit
	}
	return r
}

// parseRateLimit reads the X-RateLimit-* headers. It reports false when
// they are missing or malformed, as on errors from the CDN in front of Hub.
func parseRateLimit(h http.Header) (RateLimit, bool) {
	limit, err1 := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	remaining, err2 := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	if err1 != nil || err2 != nil || limit <= 0 || remaining < 0 {
		return RateLimit{}, false
	}
	r := RateLimit{Limit: limit, Remaining: min(remaining, limit)}
	if unix, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		r.Reset = time.Unix(unix, 0)
	}
	return r, true
}

// RateLimit returns the quota from the most recent response that reported
// one, adjusted to now. It reports false before any such response.
func (c *Client) RateLimit() (RateLimit, bool) {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	if !c.rateKnown {
		return RateLimit{}, false
	}
	return c.rate.At(c.now()), true
}

func (c *Client) recordRateLimit(h http.Header) {
	r, ok := parseRateLimit(h)
	if !ok {
		return
	}
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	c.rate, c.rateKnown = r, true
}
