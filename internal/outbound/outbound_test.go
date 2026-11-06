package outbound

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateURL(t *testing.T) {
	for _, tc := range []struct {
		in, want, errHas string
	}{
		{in: "http://plex:32400", want: "http://plex:32400"},
		{in: " https://ntfy.example.org/base/ ", want: "https://ntfy.example.org/base/"},
		{in: "https://hooks.example.com/x?thread_id=4#frag", want: "https://hooks.example.com/x?thread_id=4"},
		{in: "http://[::1]:8096", want: "http://[::1]:8096"},
		{in: "", errHas: "empty"},
		{in: "ftp://example.org", errHas: "http:// or https://"},
		{in: "file:///etc/passwd", errHas: "http:// or https://"},
		{in: "javascript:alert(1)", errHas: "http:// or https://"},
		{in: "http://", errHas: "no host"},
		{in: "http://:80", errHas: "no host"},
		{in: "http://user:pass@example.org", errHas: "own fields"},
		{in: "http://example.org:0", errHas: "port"},
		{in: "http://example.org:99999", errHas: "port"},
		{in: "://bad", errHas: "parsed"},
	} {
		u, err := ValidateURL(tc.in)
		if tc.errHas != "" {
			if err == nil || !errors.Is(err, ErrInvalidURL) || !strings.Contains(err.Error(), tc.errHas) {
				t.Errorf("ValidateURL(%q) = %v, %v; want error containing %q", tc.in, u, err, tc.errHas)
			}
			continue
		}
		if err != nil || u.String() != tc.want {
			t.Errorf("ValidateURL(%q) = %v, %v; want %s", tc.in, u, err, tc.want)
		}
	}
}

func TestRedactor(t *testing.T) {
	var r Redactor
	r.Add("abc", "s3cr3t/token+x", "s3cr3t")
	got := r.Redact("a s3cr3t/token+x b s3cr3t%2Ftoken%2Bx c s3cr3t abc")
	if strings.Contains(got, "s3cr3t") || !strings.Contains(got, "abc") {
		t.Errorf("Redact = %q", got)
	}
	var nilR *Redactor
	if nilR.Redact("x") != "x" {
		t.Error("nil redactor must return its input")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"", 0}, {"0", 0}, {"-3", 0}, {"NaN", 0}, {"abc", 0},
		{"7", 7 * time.Second}, {"1.5", 1500 * time.Millisecond},
		{"86400", MaxRetryAfter}, {"Inf", MaxRetryAfter},
		{"Thu, 05 Nov 2026 10:00:30 GMT", 30 * time.Second},
		{"Thu, 05 Nov 2026 09:00:00 GMT", 0},
	} {
		if got := ParseRetryAfter(tc.in, now); got != tc.want {
			t.Errorf("ParseRetryAfter(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func newClient(red *Redactor) *Client {
	return &Client{Service: "Test", HTTP: NewHTTPClient(), Timeout: 5 * time.Second, UserAgent: "Gonema/test", Redactor: red}
}

func TestClientStatusMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "Gonema/test" {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		switch r.URL.Path {
		case "/auth":
			w.WriteHeader(http.StatusUnauthorized)
		case "/limited":
			w.Header().Set("Retry-After", "12")
			w.WriteHeader(http.StatusTooManyRequests)
		case "/redirect":
			http.Redirect(w, r, "http://169.254.169.254/latest", http.StatusFound)
		case "/big":
			_, _ = w.Write(make([]byte, MaxBodyBytes+10))
		case "/boom":
			w.WriteHeader(http.StatusBadGateway)
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer srv.Close()
	c := newClient(nil)
	ctx := context.Background()

	resp, err := c.Do(ctx, http.MethodGet, srv.URL+"/ok", nil, nil)
	if err != nil || c.Check(resp, "") != nil || string(resp.Body) != "ok" {
		t.Fatalf("ok: %v %v", resp, err)
	}
	resp, _ = c.Do(ctx, http.MethodGet, srv.URL+"/auth", nil, nil)
	if err := c.Check(resp, ""); !errors.Is(err, ErrAuth) || Temporary(err) {
		t.Errorf("auth: %v", err)
	}
	resp, _ = c.Do(ctx, http.MethodGet, srv.URL+"/limited", nil, nil)
	err = c.Check(resp, "slow down")
	if !Temporary(err) || RetryAfterOf(err) != 12*time.Second || !strings.Contains(err.Error(), "HTTP 429") {
		t.Errorf("limited: %v (retry after %s)", err, RetryAfterOf(err))
	}
	resp, err = c.Do(ctx, http.MethodGet, srv.URL+"/redirect", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Check(resp, ""); err == nil || !strings.Contains(err.Error(), "not followed") {
		t.Errorf("redirect must not be followed: %v", err)
	}
	if _, err := c.Do(ctx, http.MethodGet, srv.URL+"/big", nil, nil); !errors.Is(err, ErrResponse) {
		t.Errorf("big body: %v", err)
	}
	resp, _ = c.Do(ctx, http.MethodGet, srv.URL+"/boom", nil, nil)
	if err := c.Check(resp, ""); !Temporary(err) {
		t.Errorf("502 must be temporary: %v", err)
	}
}

// TestTransportErrorRedaction: a transport error quotes the URL (*url.Error);
// the token in its path must never reach the message.
func TestTransportErrorRedaction(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close() // refused from now on
	const token = "123456:AAH-supersecret-token-value-xyz"
	red := &Redactor{}
	red.Add(token)
	c := newClient(red)
	_, err := c.Do(context.Background(), http.MethodPost, addr+"/bot"+token+"/sendMessage", nil, []byte("{}"))
	if err == nil || !errors.Is(err, ErrUnreachable) || !Temporary(err) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "supersecret") || strings.Contains(err.Error(), "/bot") {
		t.Errorf("error leaks the URL: %q", err)
	}
}

func TestClientTimeoutAndCancel(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	c := newClient(nil)
	c.Timeout = 50 * time.Millisecond
	_, err := c.Do(context.Background(), http.MethodGet, srv.URL, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) || !Temporary(err) {
		t.Errorf("timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Do(ctx, http.MethodGet, srv.URL, nil, nil)
	if !errors.Is(err, context.Canceled) || Temporary(err) {
		t.Errorf("canceled: %v", err)
	}
}

func TestRetry(t *testing.T) {
	var slept []time.Duration
	sleep := func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	r := Retry{Attempts: 4, Backoff: []time.Duration{time.Second, 5 * time.Second}, Sleep: sleep}

	calls := 0
	err := r.Do(context.Background(), func(context.Context) error {
		calls++
		switch calls {
		case 1:
			return &Error{Service: "x", Kind: ErrStatus, Status: 429, RetryAfter: 30 * time.Second}
		case 2:
			return &Error{Service: "x", Kind: ErrStatus, Status: 503}
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("calls = %d, err = %v", calls, err)
	}
	if len(slept) != 2 || slept[0] != 30*time.Second || slept[1] != 5*time.Second {
		t.Errorf("slept = %v, want [30s 5s] (Retry-After wins over the backoff step)", slept)
	}

	slept, calls = nil, 0
	err = r.Do(context.Background(), func(context.Context) error {
		calls++
		return &Error{Service: "x", Kind: ErrAuth, Status: 401}
	})
	if calls != 1 || !errors.Is(err, ErrAuth) || len(slept) != 0 {
		t.Errorf("final error retried: calls %d, %v", calls, err)
	}

	calls = 0
	err = r.Do(context.Background(), func(context.Context) error {
		calls++
		return &Error{Service: "x", Kind: ErrUnreachable}
	})
	if calls != 4 || !errors.Is(err, ErrUnreachable) {
		t.Errorf("attempts: calls %d, %v", calls, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls = 0
	err = Retry{Attempts: 3}.Do(ctx, func(context.Context) error {
		calls++
		return &Error{Service: "x", Kind: ErrUnreachable}
	})
	if calls != 1 || err == nil {
		t.Errorf("canceled context must stop the retries: calls %d, %v", calls, err)
	}
}
