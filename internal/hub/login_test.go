package hub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newLoginClient serves login with loginHandler and counts every request.
func newLoginClient(t *testing.T, loginHandler http.HandlerFunc) (*Client, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/v2/auth/token" {
			loginHandler(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"count":0,"next":null,"results":[]}`))
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(Options{BaseURL: srv.URL, HTTPClient: srv.Client(), Username: testUser, Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	return c, &hits
}

func TestLoginFailures(t *testing.T) {
	tests := []struct {
		name      string
		login     http.HandlerFunc
		wantAuth  bool
		wantText  string
		wantRetry time.Duration
	}{
		{name: "bad credentials", login: raw(401, nil, `{"message":"unauthorized","errinfo":{}}`), wantAuth: true},
		{name: "forbidden", login: raw(403, nil, `{"message":"account locked"}`), wantAuth: true},
		// Hub misbehaving says nothing about the credentials.
		{name: "server error", login: raw(500, nil, `{"message":"internal error"}`), wantText: "docker hub: 500 Internal Server Error: internal error"},
		{name: "CDN error page", login: raw(502, nil, `<html>bad gateway</html>`), wantText: "502 Bad Gateway: unexpected response"},
		{name: "login endpoint missing", login: raw(404, nil, `{"message":"not found"}`), wantText: "404 Not Found"},
		{name: "no access token", login: raw(200, nil, `{"token":"wrong-field"}`), wantText: "unexpected response: no access token"},
		{name: "malformed response", login: raw(200, nil, `<html>`), wantText: "unexpected response: no access token"},
		{name: "rate limited", login: raw(429, http.Header{"Retry-After": {"45"}}, ""), wantRetry: 45 * time.Second},
		{name: "rate limited without a hint", login: raw(429, nil, ""), wantRetry: defaultRateLimitWait},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, hits := newLoginClient(t, tt.login)
			now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
			c.now = func() time.Time { return now }

			err := tags(c)
			if err == nil {
				t.Fatal("request succeeded despite failed login")
			}
			if strings.Contains(err.Error(), testToken.Reveal()) {
				t.Fatalf("error leaks the token: %v", err)
			}
			if got := errors.Is(err, ErrAuth); got != tt.wantAuth {
				t.Errorf("errors.Is(ErrAuth) = %v, want %v (%v)", got, tt.wantAuth, err)
			}
			if hits.Load() != 1 {
				t.Errorf("%d requests, want only the login", hits.Load())
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantText)
			}
			// Whatever went wrong, it was not the repository or the paging.
			for _, sentinel := range []error{ErrNotFound, ErrPageLimit} {
				if errors.Is(err, sentinel) {
					t.Errorf("login failure matches %v: %v", sentinel, err)
				}
			}

			var rl *RateLimitError
			if errors.As(err, &rl) != (tt.wantRetry > 0) {
				t.Fatalf("RateLimitError = %v, want %v", rl, tt.wantRetry > 0)
			}
			if rl == nil {
				return
			}
			if rl.RetryAfter != tt.wantRetry {
				t.Errorf("RetryAfter = %v, want %v", rl.RetryAfter, tt.wantRetry)
			}
			// A rate-limited login blocks the client like any other 429.
			if err := tags(c); !errors.As(err, &rl) || hits.Load() != 1 {
				t.Errorf("second request: %v after %d requests, want blocked without one", err, hits.Load())
			}
			now = now.Add(tt.wantRetry)
			_ = tags(c)
			if hits.Load() == 1 {
				t.Error("still blocked after the wait")
			}
		})
	}
}

// An expired access token gets one fresh login; if that fails too the error
// is an auth error, and the client does not keep trying.
func TestReloginFails(t *testing.T) {
	s := &authServer{}
	c := newAuthClient(t, s, testToken)
	if err := tags(c); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.issued = "revoked"
	s.loginCode = http.StatusUnauthorized
	s.mu.Unlock()

	err := tags(c)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if strings.Contains(err.Error(), testToken.Reveal()) {
		t.Fatalf("error leaks the token: %v", err)
	}
	if s.logins != 2 {
		t.Errorf("%d logins, want the first and one retry", s.logins)
	}
}

func TestRateLimitErrorText(t *testing.T) {
	if got := (&RateLimitError{}).Error(); got != "docker hub rate limit reached" {
		t.Errorf("no wait: %q", got)
	}
	if got := (&RateLimitError{RetryAfter: 1500 * time.Millisecond}).Error(); got != "docker hub rate limit reached; retry in 2s" {
		t.Errorf("with wait: %q", got)
	}
}

func TestTagPushedFallsBackToLastUpdated(t *testing.T) {
	body := `{"count":1,"next":null,"results":[{"name":"old","digest":"sha256:aa","last_updated":"2019-05-01T10:00:00Z","tag_last_pushed":null,"images":[]}]}`
	c, _ := newTestClient(t, raw(200, nil, body))
	p, err := c.Tags(context.Background(), Repo{"library", "nginx"}, TagsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2019, 5, 1, 10, 0, 0, 0, time.UTC); !p.Tags[0].Pushed.Equal(want) {
		t.Errorf("Pushed = %v, want last_updated %v", p.Tags[0].Pushed, want)
	}
}
