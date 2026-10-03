package hub

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestClientRateLimit(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	reset := now.Add(time.Minute)
	quota := func(limit, remaining string) http.Header {
		return http.Header{
			"X-Ratelimit-Limit":     {limit},
			"X-Ratelimit-Remaining": {remaining},
			"X-Ratelimit-Reset":     {strconv.FormatInt(reset.Unix(), 10)},
		}
	}
	tests := []struct {
		name    string
		status  int
		header  http.Header
		body    string
		at      time.Time
		want    RateLimit
		wantOK  bool
		wantErr bool
	}{
		{name: "anonymous", status: 200, header: quota("180", "175"), body: `{"results":[]}`, at: now, want: RateLimit{180, 175, reset}, wantOK: true},
		{name: "signed in", status: 200, header: quota("600", "599"), body: `{"results":[]}`, at: now, want: RateLimit{600, 599, reset}, wantOK: true},
		{name: "errors count too", status: 404, header: quota("180", "12"), body: `{"message":"object not found"}`, at: now, want: RateLimit{180, 12, reset}, wantOK: true, wantErr: true},
		{name: "429 reports zero", status: 429, header: quota("180", "0"), body: ``, at: now, want: RateLimit{180, 0, reset}, wantOK: true, wantErr: true},
		{name: "full again after reset", status: 200, header: quota("180", "3"), body: `{"results":[]}`, at: reset, want: RateLimit{180, 180, reset}, wantOK: true},
		{name: "no headers", status: 200, header: nil, body: `{"results":[]}`, at: now, wantOK: false},
		{name: "malformed", status: 200, header: quota("lots", "175"), body: `{"results":[]}`, at: now, wantOK: false},
		{name: "remaining above limit is clamped", status: 200, header: quota("180", "900"), body: `{"results":[]}`, at: now, want: RateLimit{180, 180, reset}, wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, raw(tt.status, tt.header, tt.body))
			c.now = func() time.Time { return now }
			if _, ok := c.RateLimit(); ok {
				t.Fatal("quota known before any request")
			}
			_, err := c.Search(context.Background(), "nginx", PageOptions{})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			c.now = func() time.Time { return tt.at }
			got, ok := c.RateLimit()
			if ok != tt.wantOK || (ok && (got.Limit != tt.want.Limit || got.Remaining != tt.want.Remaining || !got.Reset.Equal(tt.want.Reset))) {
				t.Errorf("RateLimit() = %+v, %v; want %+v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// A later response without headers (a CDN error page) keeps the last quota.
func TestClientRateLimitKeepsLastKnown(t *testing.T) {
	calls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			raw(200, http.Header{"X-Ratelimit-Limit": {"180"}, "X-Ratelimit-Remaining": {"170"}}, `{"results":[]}`)(w, r)
			return
		}
		raw(502, nil, "<html>bad gateway</html>")(w, r)
	})
	_, _ = c.Search(context.Background(), "a", PageOptions{})
	_, err := c.Search(context.Background(), "b", PageOptions{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("second search err = %v", err)
	}
	if got, ok := c.RateLimit(); !ok || got.Remaining != 170 {
		t.Errorf("RateLimit() = %+v, %v; want the first response's 170", got, ok)
	}
}
