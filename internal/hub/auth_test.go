package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testUser  = "alice"
	testToken = Secret("dckr_pat_SUPERSECRET")
)

// authServer imitates Hub: /v2/auth/token trades the PAT for an access token,
// and the tags endpoint wants the current access token.
type authServer struct {
	mu        sync.Mutex
	logins    int
	issued    string // the access token currently accepted
	authSeen  []string
	loginCode int // non-zero overrides the login response
	loginBody []string
}

func (s *authServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path == "/v2/auth/token" {
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("login: %s with content type %q", r.Method, r.Header.Get("Content-Type"))
			}
			var body struct{ Identifier, Secret string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("login body: %v", err)
			}
			s.loginBody = append(s.loginBody, body.Identifier+":"+body.Secret)
			s.logins++
			if s.loginCode != 0 {
				w.WriteHeader(s.loginCode)
				_, _ = w.Write([]byte(`{"message":"unauthorized","errinfo":{}}`))
				return
			}
			if body.Identifier != testUser || body.Secret != testToken.Reveal() {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"unauthorized","errinfo":{}}`))
				return
			}
			s.issued = fmt.Sprintf("access-%d", s.logins)
			_, _ = fmt.Fprintf(w, `{"access_token":%q}`, s.issued)
			return
		}
		auth := r.Header.Get("Authorization")
		s.authSeen = append(s.authSeen, auth)
		if auth != "Bearer "+s.issued {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"token expired"}`))
			return
		}
		_, _ = w.Write([]byte(`{"count":0,"next":null,"results":[]}`))
	}
}

func newAuthClient(t *testing.T, s *authServer, token Secret) *Client {
	t.Helper()
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)
	c, err := NewClient(Options{BaseURL: srv.URL, HTTPClient: srv.Client(), Username: testUser, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func tags(c *Client) error {
	_, err := c.Tags(context.Background(), Repo{"library", "nginx"}, TagsOptions{})
	return err
}

func TestAuthLogsInOnceAndSendsBearer(t *testing.T) {
	s := &authServer{}
	c := newAuthClient(t, s, testToken)
	for range 3 {
		if err := tags(c); err != nil {
			t.Fatal(err)
		}
	}
	if s.logins != 1 {
		t.Errorf("logged in %d times, want once", s.logins)
	}
	if s.loginBody[0] != testUser+":"+testToken.Reveal() {
		t.Errorf("login sent %q", s.loginBody[0])
	}
	for _, a := range s.authSeen {
		if a != "Bearer access-1" {
			t.Errorf("Authorization = %q", a)
		}
	}
}

func TestAuthConcurrentFirstRequestsShareOneLogin(t *testing.T) {
	s := &authServer{}
	c := newAuthClient(t, s, testToken)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := tags(c); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if s.logins != 1 {
		t.Errorf("logged in %d times, want once", s.logins)
	}
}

func TestAuthExpiredTokenReloginsOnce(t *testing.T) {
	s := &authServer{}
	c := newAuthClient(t, s, testToken)
	if err := tags(c); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.issued = "rotated-server-side" // the old access token is now rejected
	s.mu.Unlock()

	// Hub would issue a new token on login; ours issues access-2.
	if err := tags(c); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	if s.logins != 2 {
		t.Errorf("logins = %d, want a second one after the 401", s.logins)
	}
	if last := s.authSeen[len(s.authSeen)-1]; last != "Bearer access-2" {
		t.Errorf("retried with %q", last)
	}
}

func TestAuthRejected(t *testing.T) {
	s := &authServer{}
	c := newAuthClient(t, s, Secret("dckr_pat_WRONG"))
	err := tags(c)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("error = %v, want ErrAuth", err)
	}
	if !strings.Contains(err.Error(), "alice") || !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("error %q should name the user and Hub's reason", err)
	}
	if strings.Contains(err.Error(), "WRONG") {
		t.Errorf("error leaks the token: %q", err)
	}
	if len(s.authSeen) != 0 {
		t.Error("made an API request after the login failed")
	}
}

func TestAnonymousSendsNoCredentials(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/auth/token" {
			t.Error("anonymous client tried to log in")
		}
		auth = append(auth, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
	}))
	defer srv.Close()
	c, err := NewClient(Options{BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := tags(c); err != nil {
		t.Fatal(err)
	}
	if auth[0] != "" {
		t.Errorf("Authorization = %q", auth[0])
	}
	if c.Authenticated() {
		t.Error("Authenticated() = true without credentials")
	}
}

func TestNewClientNeedsBothCredentials(t *testing.T) {
	for _, o := range []Options{{Username: "alice"}, {Token: testToken}} {
		if _, err := NewClient(o); err == nil {
			t.Errorf("NewClient(%+v) accepted half the credentials", o)
		}
	}
}

func TestSecretNeverFormats(t *testing.T) {
	c, err := NewClient(Options{Username: testUser, Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	c.bearer = Secret("access-xyz")
	opts := Options{Username: testUser, Token: testToken}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		out := fmt.Sprintf(verb, c) + fmt.Sprintf(verb, opts) + fmt.Sprintf(verb, &opts) + fmt.Sprintf(verb, testToken)
		if strings.Contains(out, "SUPERSECRET") || strings.Contains(out, "access-xyz") {
			t.Errorf("%s leaks a secret: %s", verb, out)
		}
	}
	b, err := json.Marshal(struct{ T Secret }{testToken})
	if err != nil || strings.Contains(string(b), "SUPERSECRET") {
		t.Errorf("JSON leaks the secret: %s %v", b, err)
	}
}

func TestRateLimitBlocksFurtherRequests(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits == 1 {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
	}))
	defer srv.Close()
	c, err := NewClient(Options{BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	var rl *RateLimitError
	if err := tags(c); !errors.As(err, &rl) || rl.RetryAfter != time.Minute {
		t.Fatalf("first request: %v", err)
	}

	// Inside the window: no network, and the remaining time counts down.
	now = now.Add(45 * time.Second)
	for range 10 {
		if err := tags(c); !errors.As(err, &rl) || rl.RetryAfter != 15*time.Second {
			t.Fatalf("blocked request: %v", err)
		}
		if _, err := c.Search(context.Background(), "x", PageOptions{}); !errors.As(err, &rl) {
			t.Fatalf("search during the block: %v", err)
		}
	}
	if hits != 1 {
		t.Errorf("server saw %d requests during the block, want 1", hits)
	}

	now = now.Add(15 * time.Second)
	if err := tags(c); err != nil {
		t.Fatalf("after the window: %v", err)
	}
	if hits != 2 {
		t.Errorf("hits = %d, want the request to go through", hits)
	}
}
