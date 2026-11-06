// Package outbound is the HTTP plumbing shared by the integrations that call
// user-configured services (media servers, notification services): URL
// validation, one timeout per request, size-capped responses, no redirects,
// plain-language errors with every registered secret redacted, Retry-After
// parsing and a retry loop with backoff.
//
// The download clients keep their own helper (internal/download/internal/httpc).
// Both connect through package netx: SSRF policy checked at dial time.
package outbound

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fs0ciety7000/gonema/internal/netx"
)

// Defaults.
const (
	// MaxBodyBytes bounds a response body: the services answer with small JSON
	// documents; anything larger is refused.
	MaxBodyBytes = 4 << 20
	// DefaultTimeout bounds one request.
	DefaultTimeout = 15 * time.Second
	// MaxRetryAfter caps the wait a server may ask for before a retry.
	MaxRetryAfter = 5 * time.Minute
)

const redacted = "REDACTED"

// Error kinds, matched with errors.Is.
var (
	// ErrInvalidURL: the configured URL is not usable (scheme, host, credentials).
	ErrInvalidURL = errors.New("outbound: invalid URL")
	// ErrUnreachable: no answer (DNS, refused, timeout).
	ErrUnreachable = errors.New("outbound: unreachable")
	// ErrTLS: the TLS handshake failed.
	ErrTLS = errors.New("outbound: TLS failure")
	// ErrAuth: the service refused the credentials (401, 403).
	ErrAuth = errors.New("outbound: authentication refused")
	// ErrStatus: any other unexpected HTTP status.
	ErrStatus = errors.New("outbound: unexpected status")
	// ErrResponse: the response could not be understood (too large, bad JSON).
	ErrResponse = errors.New("outbound: unexpected response")
)

// ValidateURL parses a user-configured base URL: http or https only, a host is
// required, user info is refused (credentials go in the secret fields) and the
// fragment is dropped. The message of the error is a plain sentence.
func ValidateURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: the URL is empty", ErrInvalidURL)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: the URL cannot be parsed", ErrInvalidURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: the URL must start with http:// or https://", ErrInvalidURL)
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, fmt.Errorf("%w: the URL has no host", ErrInvalidURL)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: put the user name and password in their own fields, not in the URL", ErrInvalidURL)
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("%w: the port must be between 1 and 65535", ErrInvalidURL)
		}
	}
	u.Fragment, u.RawFragment = "", ""
	return u, nil
}

// Redactor replaces registered secrets in text. The zero value is ready to use
// and safe for concurrent use.
type Redactor struct {
	mu      sync.RWMutex
	secrets []string
}

// Add registers secrets; their URL-escaped forms are registered too. Values
// shorter than 4 characters are ignored (they would mangle every message).
func (r *Redactor) Add(secrets ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range secrets {
		if len(s) < 4 {
			continue
		}
		for _, v := range []string{s, url.QueryEscape(s), url.PathEscape(s)} {
			if !slices.Contains(r.secrets, v) {
				r.secrets = append(r.secrets, v)
			}
		}
	}
	// Longest first: a secret that contains another is replaced whole.
	slices.SortFunc(r.secrets, func(a, b string) int { return len(b) - len(a) })
}

// Redact returns s with every registered secret replaced by REDACTED.
func (r *Redactor) Redact(s string) string {
	if r == nil {
		return s
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, v := range r.secrets {
		s = strings.ReplaceAll(s, v, redacted)
	}
	return s
}

// Error is a failed call. Its message is plain language and redacted.
type Error struct {
	Service    string        // "Plex", "Discord"...
	Kind       error         // one of the Err* values, or a context error
	Status     int           // HTTP status, 0 for transport failures
	RetryAfter time.Duration // asked by the server (429, 503), 0 if none
	Detail     string        // redacted detail
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Service)
	switch {
	case errors.Is(e.Kind, context.Canceled):
		b.WriteString(": the request was canceled")
	case errors.Is(e.Kind, context.DeadlineExceeded):
		b.WriteString(": no answer in time")
	case errors.Is(e.Kind, ErrUnreachable):
		b.WriteString(" could not be reached")
	case errors.Is(e.Kind, ErrTLS):
		b.WriteString(": secure connection failed")
	case errors.Is(e.Kind, ErrAuth):
		b.WriteString(" refused the credentials")
	case errors.Is(e.Kind, ErrResponse):
		b.WriteString(" sent an answer Gonema does not understand")
	case errors.Is(e.Kind, ErrInvalidURL):
		b.WriteString(": invalid address")
	default:
		b.WriteString(" answered with an error")
	}
	if e.Status != 0 {
		fmt.Fprintf(&b, " (HTTP %d)", e.Status)
	}
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

// Is matches the kind.
func (e *Error) Is(target error) bool { return errors.Is(e.Kind, target) }

// Temporary reports whether a retry may succeed: rate limiting, server errors,
// transport failures and timeouts. Authentication and request errors are final.
func (e *Error) Temporary() bool {
	switch {
	case errors.Is(e.Kind, context.Canceled):
		return false
	case errors.Is(e.Kind, ErrUnreachable), errors.Is(e.Kind, context.DeadlineExceeded):
		return true
	case e.Status == http.StatusTooManyRequests, e.Status == http.StatusRequestTimeout, e.Status >= 500:
		return true
	}
	return false
}

// Temporary reports whether err is worth retrying: an *Error, or any error
// with a Temporary() bool method saying so (SMTP 4xx replies).
func Temporary(err error) bool {
	var t interface{ Temporary() bool }
	return errors.As(err, &t) && t.Temporary()
}

// RetryAfterOf returns the wait the server asked for in err, 0 if none.
func RetryAfterOf(err error) time.Duration {
	var e *Error
	if errors.As(err, &e) {
		return e.RetryAfter
	}
	return 0
}

// Client sends requests to one service.
type Client struct {
	Service   string // name used in error messages
	HTTP      *http.Client
	Timeout   time.Duration
	UserAgent string
	Redactor  *Redactor
	Now       func() time.Time // Retry-After dates; default time.Now
}

// NewHTTPClient returns the HTTP client the integrations share: no redirects
// (a redirect is reported, never followed with the credentials), proxy from
// the environment, TLS 1.2 minimum, destinations checked by netx with the
// integration policy (loopback and LAN allowed by default).
func NewHTTPClient() *http.Client { return newHTTPClient(netx.Integration) }

// NewWebhookHTTPClient is NewHTTPClient with the webhook policy of netx:
// public addresses only, unless the operator allowed private ones.
func NewWebhookHTTPClient() *http.Client { return newHTTPClient(netx.Webhook) }

func newHTTPClient(class netx.Class) *http.Client {
	tr := netx.NewTransport(class)
	tr.MaxIdleConnsPerHost = 2
	tr.ResponseHeaderTimeout = DefaultTimeout
	return &http.Client{Transport: netx.Wrap(tr), CheckRedirect: netx.NoRedirect}
}

// Response is a fully read response.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Do sends req with the client timeout. The body is read up to MaxBodyBytes.
// Transport failures are *Error values; the status is not interpreted (see
// Check).
func (c *Client) Do(ctx context.Context, method, rawURL string, header http.Header, body []byte) (*Response, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(rctx, method, rawURL, rd)
	if err != nil {
		return nil, &Error{Service: c.Service, Kind: ErrInvalidURL, Detail: "the request could not be built"}
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if req.Header.Get("User-Agent") == "" && c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	hc := c.HTTP
	if hc == nil {
		hc = NewHTTPClient()
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, c.transportErr(ctx, err, timeout)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, c.transportErr(ctx, err, timeout)
	}
	if len(b) > MaxBodyBytes {
		return nil, &Error{Service: c.Service, Kind: ErrResponse, Status: resp.StatusCode, Detail: "the response is too large"}
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: b}, nil
}

// Check maps a non-2xx response to an *Error (nil for 2xx). detail, when not
// empty, is a message extracted from the body by the caller; it is redacted.
func (c *Client) Check(resp *Response, detail string) error {
	s := resp.Status
	if s >= 200 && s < 300 {
		return nil
	}
	e := &Error{Service: c.Service, Kind: ErrStatus, Status: s, Detail: c.clip(detail)}
	switch {
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		e.Kind = ErrAuth
	case s >= 300 && s < 400:
		e.Detail = "redirected elsewhere: check the URL (the redirect is not followed)"
	case s == http.StatusNotFound && e.Detail == "":
		e.Detail = "not found: check the URL"
	}
	if s == http.StatusTooManyRequests || s == http.StatusServiceUnavailable {
		e.RetryAfter = ParseRetryAfter(resp.Header.Get("Retry-After"), c.now())
	}
	return e
}

// Fail builds an *Error of kind with a redacted detail.
func (c *Client) Fail(kind error, status int, detail string) error {
	return &Error{Service: c.Service, Kind: kind, Status: status, Detail: c.clip(detail)}
}

// Redact redacts s with the client's redactor.
func (c *Client) Redact(s string) string { return c.Redactor.Redact(s) }

func (c *Client) clip(s string) string {
	s = strings.TrimSpace(c.Redact(s))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// transportErr classifies a failed round trip. The *url.Error wrapper (which
// quotes the URL, and so possibly a token) is never part of the message.
func (c *Client) transportErr(parent context.Context, err error, timeout time.Duration) error {
	if perr := parent.Err(); perr != nil {
		return &Error{Service: c.Service, Kind: perr}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Service: c.Service, Kind: context.DeadlineExceeded, Detail: fmt.Sprintf("nothing within %s", timeout)}
	}
	detail, kind := classify(err)
	return &Error{Service: c.Service, Kind: kind, Detail: c.clip(detail)}
}

func classify(err error) (detail string, kind error) {
	var (
		unknownCA  x509.UnknownAuthorityError
		hostErr    x509.HostnameError
		invalidErr x509.CertificateInvalidError
		verifyErr  *tls.CertificateVerificationError
		recordErr  tls.RecordHeaderError
		dnsErr     *net.DNSError
	)
	var blocked *netx.BlockedError
	switch {
	case errors.As(err, &blocked):
		return blocked.Error(), ErrInvalidURL
	case errors.Is(err, netx.ErrTooLarge):
		return "the response is too large", ErrResponse
	case errors.As(err, &unknownCA), errors.As(err, &verifyErr):
		return "certificate not trusted (self-signed?)", ErrTLS
	case errors.As(err, &hostErr):
		return "certificate issued for another host name", ErrTLS
	case errors.As(err, &invalidErr):
		return "certificate invalid or expired", ErrTLS
	case errors.As(err, &recordErr), strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		return "the service does not speak HTTPS on this port: use http://", ErrTLS
	case errors.As(err, &dnsErr):
		return "host name not found", ErrUnreachable
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused", ErrUnreachable
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return "connection closed by the service", ErrUnreachable
	}
	return "network error", ErrUnreachable
}

// ParseRetryAfter reads a Retry-After value (seconds or HTTP date), capped at
// MaxRetryAfter; 0 when absent or invalid.
func ParseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		if !(secs > 0) { // also NaN
			return 0
		}
		if secs >= MaxRetryAfter.Seconds() {
			return MaxRetryAfter
		}
		d = time.Duration(secs * float64(time.Second))
	} else if t, err := http.ParseTime(v); err == nil {
		d = t.Sub(now)
	}
	if d <= 0 {
		return 0
	}
	return min(d, MaxRetryAfter)
}
