package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/flight"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/ratelimit"
)

// DefaultBaseURL is the TMDB API v3 root.
const DefaultBaseURL = "https://api.themoviedb.org/3"

const (
	providerName     = "tmdb"
	maxBodyBytes     = 8 << 20  // largest accepted response body
	maxErrBodyBytes  = 64 << 10 // largest error body read for its message
	configurationTTL = 24 * time.Hour
	redacted         = "REDACTED"
)

// Options configures a Client. The zero value of every field selects its default.
type Options struct {
	HTTPClient *http.Client // default: a client without global timeout (per-attempt timeouts apply)
	BaseURL    string       // default DefaultBaseURL
	Language   string       // default "en-US"
	Region     string       // ISO 3166-1, default "US": release dates, certification, upcoming/now playing
	Version    string       // Gonema version for the User-Agent, default "dev"

	Timeout       time.Duration // per HTTP attempt, default 10 s
	MaxAttempts   int           // per request, default 4
	BackoffBase   time.Duration // first retry delay before jitter, default 500 ms
	BackoffMax    time.Duration // longest retry delay, default 8 s
	MaxRetryAfter time.Duration // longest Retry-After honored; longer fails with RateLimitError. Default 30 s

	RateLimit float64            // requests per second, default 20
	Burst     int                // default 10
	Limiter   *ratelimit.Limiter // shared limiter; overrides RateLimit and Burst

	CacheTTL        time.Duration // default 1 h; negative disables the cache
	CacheMaxEntries int           // default 1024
	CacheMaxBytes   int           // default 64 MiB

	Logger *slog.Logger // retries at debug level; default discards
}

// Client is a TMDB API client. It is safe for concurrent use.
type Client struct {
	http        *http.Client
	base        string
	lang        string
	region      string
	userAgent   string
	cred        credential
	timeout     time.Duration
	maxAttempts int
	backoffBase time.Duration
	backoffMax  time.Duration
	maxRetry    time.Duration
	cacheTTL    time.Duration
	limiter     *ratelimit.Limiter
	cache       *respCache
	flight      flight.Group[[]byte]
	log         *slog.Logger

	// Replaced in tests.
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error

	clockMu sync.Mutex
	clock   ServerClock // last Date header seen
}

// ServerClock is the Date header of the last TMDB answer and the local time
// it was received, to detect a wrong system clock. The header has a one
// second resolution.
type ServerClock struct {
	Server time.Time
	Local  time.Time
}

// LastServerClock returns the clock of the last answer; ok is false before any
// answer with a valid Date header.
func (c *Client) LastServerClock() (clock ServerClock, ok bool) {
	c.clockMu.Lock()
	defer c.clockMu.Unlock()
	return c.clock, !c.clock.Server.IsZero()
}

func (c *Client) observeDate(h string) {
	if h == "" {
		return
	}
	t, err := http.ParseTime(h)
	if err != nil {
		return
	}
	c.clockMu.Lock()
	c.clock = ServerClock{Server: t.UTC(), Local: c.now().UTC()}
	c.clockMu.Unlock()
}

var _ metadata.Provider = (*Client)(nil)

type credKind uint8

const (
	credBearer credKind = iota + 1 // API Read Access Token (v4 JWT)
	credAPIKey                     // v3 API key
)

type credential struct {
	kind  credKind
	value string
}

// ErrKeyFormat is returned by New when the credential has neither the shape of an
// API Read Access Token nor of an API Key. It matches metadata.ErrUnauthorized.
var ErrKeyFormat = fmt.Errorf("%w: TMDB key format not recognized: paste the \"API Read Access Token\" "+
	"(long, starts with eyJ) or the \"API Key\" (32 hexadecimal characters) from https://www.themoviedb.org/settings/api",
	metadata.ErrUnauthorized)

// parseCredential tells the two TMDB credential kinds apart by shape. A pasted
// "Bearer " prefix is tolerated.
func parseCredential(s string) (credential, error) {
	s = strings.TrimSpace(s)
	if len(s) > 7 && strings.EqualFold(s[:7], "bearer ") {
		s = strings.TrimSpace(s[7:])
	}
	switch {
	case len(s) == 32 && strings.Trim(s, "0123456789abcdefABCDEF") == "":
		return credential{kind: credAPIKey, value: s}, nil
	case len(s) >= 40 && len(s) <= 4096 && strings.HasPrefix(s, "eyJ") && strings.Count(s, ".") == 2 &&
		strings.Trim(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.") == "":
		return credential{kind: credBearer, value: s}, nil
	}
	return credential{}, ErrKeyFormat
}

// New returns a client for the given credential (API Read Access Token or API Key).
func New(key string, opts Options) (*Client, error) {
	cred, err := parseCredential(key)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(opts.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("tmdb: invalid base URL %q", base)
	}
	c := &Client{
		http:        opts.HTTPClient,
		base:        base,
		lang:        orDefault(opts.Language, "en-US"),
		region:      strings.ToUpper(orDefault(opts.Region, metadata.FallbackRegion)),
		userAgent:   "Gonema/" + orDefault(opts.Version, "dev") + " (+https://github.com/fs0ciety7000/gonema)",
		cred:        cred,
		timeout:     orDefault(opts.Timeout, 10*time.Second),
		maxAttempts: orDefault(opts.MaxAttempts, 4),
		backoffBase: orDefault(opts.BackoffBase, 500*time.Millisecond),
		backoffMax:  orDefault(opts.BackoffMax, 8*time.Second),
		maxRetry:    orDefault(opts.MaxRetryAfter, 30*time.Second),
		cacheTTL:    orDefault(opts.CacheTTL, time.Hour),
		limiter:     opts.Limiter,
		log:         opts.Logger,
		now:         time.Now,
		sleep:       sleepCtx,
	}
	if c.http == nil {
		c.http = &http.Client{}
	}
	if c.limiter == nil {
		c.limiter = ratelimit.New(orDefault(opts.RateLimit, 20), orDefault(opts.Burst, 10))
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	c.cache = newRespCache(orDefault(opts.CacheMaxEntries, 1024), orDefault(opts.CacheMaxBytes, 64<<20))
	return c, nil
}

func orDefault[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// get fetches path with query q and decodes the JSON body into dst. ttl > 0
// caches the body; ttl <= 0 bypasses the cache (but not singleflight).
func (c *Client) get(ctx context.Context, path string, q url.Values, ttl time.Duration, dst any) error {
	key := path + "?" + q.Encode() // credentials are added later, never part of the key
	body, ok := c.cache.get(key, c.now())
	if !ok {
		var err error
		body, _, err = c.flight.Do(ctx, key, func(fctx context.Context) ([]byte, error) {
			b, err := c.fetch(fctx, path, q)
			if err == nil {
				c.cache.put(key, b, ttl, c.now())
			}
			return b, err
		})
		if err != nil {
			return c.wrap(path, err)
		}
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("tmdb: GET %s: decode response: %w", path, err)
	}
	return nil
}

// wrap adds the request path to err unless it is already a tmdb error.
func (c *Client) wrap(path string, err error) error {
	if strings.HasPrefix(err.Error(), "tmdb: ") {
		return err
	}
	return fmt.Errorf("tmdb: GET %s: %w", path, err)
}

// attemptError is the outcome of one failed HTTP attempt.
type attemptError struct {
	err        error
	retry      bool
	retryAfter time.Duration // from a 429 Retry-After header
	rateLimit  bool
}

// fetch performs the request with rate limiting and retries.
func (c *Client) fetch(ctx context.Context, path string, q url.Values) ([]byte, error) {
	for attempt := 1; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("tmdb: GET %s: %w", path, err)
		}
		body, ae := c.once(ctx, path, q)
		if ae == nil {
			return body, nil
		}
		if !ae.retry || attempt >= c.maxAttempts || ctx.Err() != nil {
			return nil, c.final(path, ae)
		}
		wait := c.backoff(attempt)
		if ae.rateLimit && ae.retryAfter > 0 {
			if ae.retryAfter > c.maxRetry {
				return nil, c.final(path, ae)
			}
			wait = ae.retryAfter
		}
		c.log.DebugContext(ctx, "tmdb request retry", "path", path, "attempt", attempt, "wait", wait, "error", ae.err.Error())
		if err := c.sleep(ctx, wait); err != nil {
			return nil, fmt.Errorf("tmdb: GET %s: %w (last error: %w)", path, err, ae.err)
		}
	}
}

func (c *Client) final(path string, ae *attemptError) error {
	if ae.rateLimit {
		return fmt.Errorf("tmdb: GET %s: %w", path, &metadata.RateLimitError{RetryAfter: ae.retryAfter})
	}
	return fmt.Errorf("tmdb: GET %s: %w", path, ae.err)
}

// backoff returns the delay before retry n (1-based): exponential with equal
// jitter, in [d/2, d) where d = min(BackoffMax, BackoffBase*2^(n-1)).
func (c *Client) backoff(n int) time.Duration {
	d := c.backoffBase << min(n-1, 20)
	if d <= 0 || d > c.backoffMax {
		d = c.backoffMax
	}
	half := d / 2
	return half + rand.N(half+1) // jitter only, not security sensitive
}

// once performs a single HTTP attempt.
func (c *Client) once(ctx context.Context, path string, q url.Values) ([]byte, *attemptError) {
	actx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	rq := q
	if c.cred.kind == credAPIKey {
		rq = make(url.Values, len(q)+1)
		for k, v := range q {
			rq[k] = v
		}
		rq.Set("api_key", c.cred.value)
	}
	u := c.base + path
	if len(rq) > 0 {
		u += "?" + rq.Encode()
	}
	req, err := http.NewRequestWithContext(actx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, &attemptError{err: errors.New(c.redact(err.Error()))}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.cred.kind == credBearer {
		req.Header.Set("Authorization", "Bearer "+c.cred.value)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		te := c.transportError(err)
		// The caller's own cancellation is final; a per-attempt timeout or a
		// network failure is worth another attempt.
		return nil, &attemptError{err: te, retry: ctx.Err() == nil}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrBodyBytes))
		_ = resp.Body.Close()
	}()
	c.observeDate(resp.Header.Get("Date"))

	if resp.StatusCode == http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
		if err != nil {
			return nil, &attemptError{err: c.transportError(err), retry: ctx.Err() == nil}
		}
		if len(body) > maxBodyBytes {
			return nil, &attemptError{err: fmt.Errorf("response larger than %d bytes", maxBodyBytes)}
		}
		return body, nil
	}

	code, msg := c.errorBody(resp.Body)
	switch s := resp.StatusCode; {
	case s == http.StatusUnauthorized:
		return nil, &attemptError{err: metadata.NewAPIError(providerName, s, code, msg, metadata.ErrUnauthorized)}
	case s == http.StatusNotFound:
		return nil, &attemptError{err: metadata.NewAPIError(providerName, s, code, msg, metadata.ErrNotFound)}
	case s == http.StatusTooManyRequests:
		return nil, &attemptError{
			err:        metadata.NewAPIError(providerName, s, code, msg, metadata.ErrRateLimited),
			retry:      true,
			rateLimit:  true,
			retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), c.now()),
		}
	case s >= 500:
		return nil, &attemptError{err: metadata.NewAPIError(providerName, s, code, msg, nil), retry: s != http.StatusNotImplemented}
	default:
		return nil, &attemptError{err: metadata.NewAPIError(providerName, s, code, msg, nil)}
	}
}

// errorBody reads TMDB's {"status_code":N,"status_message":"..."} error body.
func (c *Client) errorBody(r io.Reader) (code int, msg string) {
	b, err := io.ReadAll(io.LimitReader(r, maxErrBodyBytes))
	if err != nil {
		return 0, ""
	}
	var e struct {
		StatusCode    int    `json:"status_code"`
		StatusMessage string `json:"status_message"`
	}
	if json.Unmarshal(b, &e) != nil {
		return 0, ""
	}
	msg = c.redact(e.StatusMessage)
	if len(msg) > 300 {
		msg = strings.ToValidUTF8(msg[:300], "")
	}
	return e.StatusCode, msg
}

// redactedError hides credentials from the message of a transport error while
// keeping the underlying error (net.Error, context errors) reachable.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

func (c *Client) transportError(err error) error {
	inner := err
	var ue *url.Error
	if errors.As(err, &ue) {
		inner = ue.Err
	}
	return &redactedError{msg: c.redact(err.Error()), err: inner}
}

// redact removes the credential from s, both raw and URL-encoded, and blanks any
// api_key query value.
func (c *Client) redact(s string) string {
	if c.cred.value != "" {
		s = strings.ReplaceAll(s, c.cred.value, redacted)
		s = strings.ReplaceAll(s, url.QueryEscape(c.cred.value), redacted)
	}
	return redactAPIKeyParam(s)
}

func redactAPIKeyParam(s string) string {
	const p = "api_key="
	var b strings.Builder
	for {
		i := strings.Index(s, p)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i+len(p)])
		s = s[i+len(p):]
		end := strings.IndexAny(s, "&\" #")
		if end < 0 {
			end = len(s)
		}
		if s[:end] != redacted {
			b.WriteString(redacted)
		} else {
			b.WriteString(s[:end])
		}
		s = s[end:]
	}
}

// parseRetryAfter reads delta-seconds or an HTTP date; 0 if absent or invalid.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n < 0 {
			return 0
		}
		return time.Duration(min(n, 24*3600)) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
