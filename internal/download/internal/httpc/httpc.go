// Package httpc is the HTTP plumbing shared by the download clients: one
// timeout per request, bounded bodies, error classification in plain language
// and redaction of credentials from every error message.
package httpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/netx"
)

// MaxBodyBytes bounds a response body.
const MaxBodyBytes = 32 << 20

const redacted = "REDACTED"

// Requester sends requests to one download client.
type Requester struct {
	Kind      download.Kind
	HTTP      *http.Client
	Base      *url.URL // scheme://host:port/urlbase
	Timeout   time.Duration
	UserAgent string

	mu      sync.RWMutex
	secrets []string
}

// New builds a Requester from cfg; defaultPort is the kind's default port.
// Every non-empty credential of cfg is redacted from errors.
func New(kind download.Kind, cfg *download.Config, defaultPort int) (*Requester, error) {
	base, err := cfg.BaseURL(defaultPort)
	if err != nil {
		return nil, err
	}
	hc, err := cfg.Client()
	if err != nil {
		return nil, err
	}
	r := &Requester{Kind: kind, HTTP: hc, Base: base, Timeout: cfg.RequestTimeout(), UserAgent: "Gonema"}
	r.AddSecret(cfg.APIKey)
	r.AddSecret(cfg.Password)
	return r, nil
}

// AddSecret registers a value to redact from errors (session ids, tokens).
func (r *Requester) AddSecret(s string) {
	if s == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range []string{s, url.QueryEscape(s), url.PathEscape(s)} {
		if !slices.Contains(r.secrets, v) {
			r.secrets = append(r.secrets, v)
		}
	}
}

// Redact replaces every registered secret in s.
func (r *Requester) Redact(s string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, v := range r.secrets {
		if v != "" {
			s = strings.ReplaceAll(s, v, redacted)
		}
	}
	return s
}

// URL returns the absolute URL of path (relative to the base) with query q.
func (r *Requester) URL(path string, q url.Values) string {
	u := *r.Base
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	if len(q) > 0 {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// Request is one HTTP call.
type Request struct {
	Op          string // for errors: test, add, items...
	Method      string
	Path        string // relative to the base URL
	Query       url.Values
	Body        []byte
	ContentType string
	Header      http.Header
}

// Response is a fully read response.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Do sends req. Transport failures are returned as *download.Error; the HTTP
// status is not interpreted (see StatusError).
func (r *Requester) Do(ctx context.Context, req *Request) (*Response, error) {
	rctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	var body io.Reader
	if req.Body != nil {
		body = bytes.NewReader(req.Body)
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	hr, err := http.NewRequestWithContext(rctx, method, r.URL(req.Path, req.Query), body)
	if err != nil {
		return nil, r.Err(req.Op, download.ErrInvalidRequest, "", err)
	}
	for k, vs := range req.Header {
		for _, v := range vs {
			hr.Header.Add(k, v)
		}
	}
	if req.ContentType != "" {
		hr.Header.Set("Content-Type", req.ContentType)
	}
	if hr.Header.Get("User-Agent") == "" {
		hr.Header.Set("User-Agent", r.UserAgent)
	}
	resp, err := r.HTTP.Do(hr)
	if err != nil {
		return nil, r.transportErr(ctx, req.Op, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, r.transportErr(ctx, req.Op, err)
	}
	if len(b) > MaxBodyBytes {
		return nil, r.Err(req.Op, download.ErrUnexpected, "response too large", nil)
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: b}, nil
}

// StatusError maps a non-2xx status to a *download.Error; nil for 2xx.
func (r *Requester) StatusError(op string, resp *Response) error {
	s := resp.Status
	switch {
	case s >= 200 && s < 300:
		return nil
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		return r.Err(op, download.ErrAuth, fmt.Sprintf("HTTP %d", s), nil)
	case s == http.StatusNotFound:
		return r.Err(op, download.ErrURLBase, "HTTP 404", nil)
	case s >= 300 && s < 400:
		return r.Err(op, download.ErrURLBase, fmt.Sprintf("HTTP %d redirect: a login page or a wrong URL base", s), nil)
	case s >= 500:
		return r.Err(op, download.ErrUnexpected, fmt.Sprintf("HTTP %d: the client reported an internal error", s), nil)
	}
	return r.Err(op, download.ErrUnexpected, fmt.Sprintf("HTTP %d", s), nil)
}

// Err builds a *download.Error whose detail and cause message are redacted.
func (r *Requester) Err(op string, kind error, detail string, cause error) error {
	var c error
	if cause != nil {
		c = &redactedError{msg: r.Redact(cause.Error()), err: cause}
	}
	return download.NewError(r.Kind, op, kind, r.Redact(detail), c)
}

// transportErr classifies a failed round trip.
func (r *Requester) transportErr(parent context.Context, op string, err error) error {
	if perr := parent.Err(); perr != nil {
		// The caller gave up: report its context error, not the client.
		return download.NewError(r.Kind, op, perr, "", nil)
	}
	detail, kind := classify(err)
	if errors.Is(err, context.DeadlineExceeded) {
		kind, detail = download.ErrUnreachable, fmt.Sprintf("no answer within %s", r.Timeout)
	}
	return r.Err(op, kind, detail, err)
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
		return blocked.Error(), download.ErrInvalidRequest
	case errors.As(err, &unknownCA), errors.As(err, &verifyErr):
		return "certificate not trusted (self-signed?)", download.ErrTLS
	case errors.As(err, &hostErr):
		return "certificate issued for another host name", download.ErrTLS
	case errors.As(err, &invalidErr):
		return "certificate invalid or expired", download.ErrTLS
	case errors.As(err, &recordErr):
		return "the client does not speak HTTPS on this port: disable TLS", download.ErrTLS
	case strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		return "the client does not speak HTTPS on this port: disable TLS", download.ErrTLS
	case errors.As(err, &dnsErr):
		return "host name not found", download.ErrUnreachable
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused", download.ErrUnreachable
	}
	return "", download.ErrUnreachable
}

// redactedError carries a redacted message. Is matches the original error
// chain (context errors, net errors) without exposing its text.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }

func (e *redactedError) Is(target error) bool { return errors.Is(e.err, target) }

// BasicAuth returns the value of an Authorization header for user and pass.
func BasicAuth(user, pass string) string {
	req := http.Request{Header: http.Header{}}
	req.SetBasicAuth(user, pass)
	return req.Header.Get("Authorization")
}

// Field is a multipart form field.
type Field struct{ Name, Value string }

// File is a multipart file part.
type File struct {
	Field, Filename, ContentType string
	Data                         []byte
}

// Multipart encodes fields and files as multipart/form-data and returns the
// body and its content type.
func Multipart(fields []Field, files ...File) (body []byte, contentType string, err error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, f := range fields {
		if err := w.WriteField(f.Name, f.Value); err != nil {
			return nil, "", fmt.Errorf("httpc: multipart field: %w", err)
		}
	}
	for _, f := range files {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, f.Field, sanitizeFilename(f.Filename)))
		ct := f.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		h.Set("Content-Type", ct)
		pw, err := w.CreatePart(h)
		if err != nil {
			return nil, "", fmt.Errorf("httpc: multipart file: %w", err)
		}
		if _, err := pw.Write(f.Data); err != nil {
			return nil, "", fmt.Errorf("httpc: multipart file: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("httpc: multipart close: %w", err)
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// sanitizeFilename keeps the base name and drops characters that would break
// the Content-Disposition header.
func sanitizeFilename(s string) string {
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' {
			return -1
		}
		return r
	}, s)
	if s == "" {
		s = "release"
	}
	return s
}

// Num decodes a JSON number that some APIs send as a string ("1277.65").
// Unparsable or null values decode as 0.
type Num float64

// UnmarshalJSON implements json.Unmarshaler.
func (n *Num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		*n = 0
		return nil //nolint:nilerr // lenient on purpose: one odd field must not fail a whole listing
	}
	*n = Num(f)
	return nil
}

// Int64 converts n, saturating at the int64 bounds.
func (n Num) Int64() int64 {
	switch f := float64(n); {
	case f >= math.MaxInt64:
		return math.MaxInt64
	case f <= math.MinInt64:
		return math.MinInt64
	default:
		return int64(f)
	}
}
