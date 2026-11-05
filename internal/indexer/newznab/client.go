package newznab

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/flight"
	"github.com/fs0ciety7000/gonema/internal/indexer"
	"github.com/fs0ciety7000/gonema/internal/ratelimit"
)

// Defaults of Options.
const (
	DefaultAPIPath      = "/api"
	DefaultTimeout      = 30 * time.Second
	DefaultMaxBodyBytes = 10 << 20
	DefaultCapsTTL      = 6 * time.Hour
	defaultPageLimit    = 100
	maxErrBodyBytes     = 64 << 10
	redacted            = "REDACTED"
)

// Options configures a Client. The zero value of every field selects its default.
type Options struct {
	// Info identifies the indexer; its fields are stamped on every release. An
	// empty Protocol means Usenet (torznab.New sets BitTorrent).
	Info indexer.Info
	// BaseURL is the indexer root, e.g. "https://indexer.example". Required.
	BaseURL string
	// APIPath is appended to BaseURL; default "/api".
	APIPath string
	// APIKey is sent as the apikey query parameter; may be empty for public
	// Torznab feeds.
	APIKey string
	// Categories searched; empty uses the movie categories of the capabilities,
	// or 2000 when they announce none.
	Categories []int

	HTTPClient   *http.Client       // default: a client without global timeout
	Timeout      time.Duration      // per HTTP request, default 30 s
	MaxBodyBytes int64              // largest accepted response, default 10 MiB
	CapsTTL      time.Duration      // capabilities cache, default 6 h
	Limiter      *ratelimit.Limiter // optional, waited before every request
	Version      string             // Gonema version for the User-Agent, default "dev"
	Logger       *slog.Logger       // requests at debug level; default discards
}

// Client is a Newznab (or Torznab) API client. It implements indexer.Indexer and
// is safe for concurrent use.
type Client struct {
	info       indexer.Info
	endpoint   string // base URL + API path, no query
	apiKey     string
	categories []int
	http       *http.Client
	timeout    time.Duration
	maxBody    int64
	capsTTL    time.Duration
	limiter    *ratelimit.Limiter
	userAgent  string
	log        *slog.Logger

	capsFlight flight.Group[indexer.Caps]
	mu         sync.Mutex
	caps       indexer.Caps
	capsAt     time.Time

	now func() time.Time // replaced in tests
}

var _ indexer.Indexer = (*Client)(nil)

// New returns a client. The URL is validated here; reachability and the API
// key are checked by Test.
func New(o Options) (*Client, error) {
	info := o.Info
	if info.Protocol == "" {
		info.Protocol = decision.Usenet
	}
	if info.Priority == 0 {
		info.Priority = indexer.DefaultPriority
	}
	base := strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" {
		return nil, &indexer.Error{Kind: indexer.ErrBadURL, Indexer: info.Name}
	}
	path := strings.TrimSpace(o.APIPath)
	if path == "" {
		path = DefaultAPIPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if strings.ContainsAny(path, "?#") {
		return nil, &indexer.Error{Kind: indexer.ErrBadURL, Indexer: info.Name, Message: "the API path must not contain ? or #"}
	}
	c := &Client{
		info:       info,
		endpoint:   base + path,
		apiKey:     strings.TrimSpace(o.APIKey),
		categories: append([]int(nil), o.Categories...),
		http:       o.HTTPClient,
		timeout:    orDefault(o.Timeout, DefaultTimeout),
		maxBody:    orDefault(o.MaxBodyBytes, DefaultMaxBodyBytes),
		capsTTL:    orDefault(o.CapsTTL, DefaultCapsTTL),
		limiter:    o.Limiter,
		userAgent:  "Gonema/" + orDefault(o.Version, "dev") + " (+https://github.com/fs0ciety7000/gonema)",
		log:        o.Logger,
		now:        time.Now,
	}
	if c.http == nil {
		c.http = &http.Client{}
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	return c, nil
}

func orDefault[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// Info returns the indexer identity.
func (c *Client) Info() indexer.Info { return c.info }

// Caps returns the capabilities, cached for Options.CapsTTL. Concurrent calls
// share one request.
func (c *Client) Caps(ctx context.Context) (indexer.Caps, error) {
	c.mu.Lock()
	if !c.capsAt.IsZero() && c.now().Sub(c.capsAt) < c.capsTTL {
		caps := c.caps
		c.mu.Unlock()
		return caps, nil
	}
	c.mu.Unlock()
	caps, _, err := c.capsFlight.Do(ctx, "caps", c.fetchCaps)
	return caps, err
}

func (c *Client) fetchCaps(ctx context.Context) (indexer.Caps, error) {
	body, err := c.get(ctx, url.Values{"t": {"caps"}})
	if err != nil {
		return indexer.Caps{}, err
	}
	caps, err := ParseCaps(body)
	if err != nil {
		return indexer.Caps{}, c.decorate(err)
	}
	c.mu.Lock()
	c.caps, c.capsAt = caps, c.now()
	c.mu.Unlock()
	return caps, nil
}

// Test fetches the capabilities (bypassing the cache) and runs a one-result
// search, since many indexers answer t=caps without checking the key.
func (c *Client) Test(ctx context.Context) error {
	caps, err := c.fetchCaps(ctx)
	if err != nil {
		return err
	}
	v := url.Values{"t": {"search"}, "limit": {"1"}}
	if cats := c.searchCategories(&caps, nil); len(cats) > 0 {
		v.Set("cat", joinInts(cats))
	}
	_, err = c.feed(ctx, v)
	return err
}

// Search runs a movie search: t=movie with imdbid/tmdbid when the capabilities
// support them, else a text search (t=movie or t=search with q=title year). Up
// to q.MaxPages pages are fetched. On error, the releases of the pages already
// fetched are returned with it.
func (c *Client) Search(ctx context.Context, q indexer.Query) ([]indexer.Release, error) {
	caps, err := c.Caps(ctx)
	if errors.Is(err, indexer.ErrUnsupported) {
		// t=caps is optional on some old servers: assume the spec defaults.
		caps, err = indexer.Caps{Search: indexer.SearchMode{Available: true, Params: []string{"q"}}}, nil
	}
	if err != nil {
		return nil, err
	}
	params, err := searchParams(&caps, &q)
	if err != nil {
		return nil, c.decorate(err)
	}
	if cats := c.searchCategories(&caps, q.Categories); len(cats) > 0 {
		params.Set("cat", joinInts(cats))
	}
	limit := pageLimit(&caps, q.Limit)
	pages := max(q.MaxPages, 1)
	offset := max(q.Offset, 0)
	var out []indexer.Release
	seen := make(map[string]struct{})
	for range pages {
		params.Set("offset", strconv.Itoa(offset))
		params.Set("limit", strconv.Itoa(limit))
		page, err := c.feed(ctx, params)
		if err != nil {
			return out, err
		}
		for i := range page.Releases {
			if _, dup := seen[page.Releases[i].GUID]; dup {
				continue
			}
			seen[page.Releases[i].GUID] = struct{}{}
			out = append(out, page.Releases[i])
		}
		offset += page.Count
		if page.Count == 0 || page.Count < limit || (page.Total >= 0 && offset >= page.Total) {
			break
		}
	}
	return out, nil
}

// RSS returns the latest releases of the movie categories (t=search without
// query, one page of the largest allowed size).
func (c *Client) RSS(ctx context.Context) ([]indexer.Release, error) {
	caps, err := c.Caps(ctx)
	if errors.Is(err, indexer.ErrUnsupported) {
		caps, err = indexer.Caps{}, nil
	}
	if err != nil {
		return nil, err
	}
	v := url.Values{"t": {"search"}, "limit": {strconv.Itoa(pageLimit(&caps, 0))}}
	if cats := c.searchCategories(&caps, nil); len(cats) > 0 {
		v.Set("cat", joinInts(cats))
	}
	page, err := c.feed(ctx, v)
	if err != nil {
		return nil, err
	}
	return page.Releases, nil
}

// searchParams chooses the search function and its parameters for q.
func searchParams(caps *indexer.Caps, q *indexer.Query) (url.Values, error) {
	imdb := indexer.IMDbNumber(q.IMDbID)
	ms := caps.MovieSearch
	useIMDb := imdb != "" && ms.Supports("imdbid")
	useTMDb := q.TMDbID > 0 && ms.Supports("tmdbid")
	if useIMDb || useTMDb {
		// Several ids in one call are ORed by the server (spec revision 1.1); they
		// are only sent together when supportedParams lists them both.
		v := url.Values{"t": {"movie"}}
		if useIMDb {
			v.Set("imdbid", imdb)
		}
		if useTMDb {
			v.Set("tmdbid", strconv.FormatInt(q.TMDbID, 10))
		}
		return v, nil
	}
	text := searchText(q.Title, q.Year)
	if text == "" {
		return nil, &indexer.Error{Kind: indexer.ErrBadRequest, Message: "the search has no id supported by the indexer and no title"}
	}
	if ms.Supports("q") {
		return url.Values{"t": {"movie"}, "q": {text}}, nil
	}
	return url.Values{"t": {"search"}, "q": {text}}, nil
}

// searchText builds the free-text query: apostrophes are dropped ("Ocean's" ->
// "Oceans"), other punctuation becomes a space, the year is appended.
func searchText(title string, year int) string {
	var b strings.Builder
	space := false
	for _, r := range title {
		switch {
		case r == '\'' || r == '’':
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
		if b.Len() > 200 {
			break
		}
	}
	if b.Len() == 0 {
		return ""
	}
	if year > 1800 && year < 3000 {
		b.WriteByte(' ')
		b.WriteString(strconv.Itoa(year))
	}
	return b.String()
}

func (c *Client) searchCategories(caps *indexer.Caps, override []int) []int {
	switch {
	case len(override) > 0:
		return override
	case len(c.categories) > 0:
		return c.categories
	}
	if cats := caps.MovieCategories(); len(cats) > 0 {
		return cats
	}
	return []int{indexer.MoviesCategory}
}

func pageLimit(caps *indexer.Caps, want int) int {
	limit := want
	if limit <= 0 {
		limit = caps.LimitMax
	}
	if limit <= 0 {
		limit = defaultPageLimit
	}
	if caps.LimitMax > 0 && limit > caps.LimitMax {
		limit = caps.LimitMax
	}
	return limit
}

func joinInts(ns []int) string {
	var b strings.Builder
	for i, n := range ns {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(n))
	}
	return b.String()
}

// feed fetches and decodes one RSS response.
func (c *Client) feed(ctx context.Context, v url.Values) (Page, error) {
	body, err := c.get(ctx, v)
	if err != nil {
		return Page{}, err
	}
	page, err := ParseFeed(body, c.info)
	if err != nil {
		return Page{}, c.decorate(err)
	}
	return page, nil
}

// get performs one GET with the API key and returns the body of a 200 answer.
// Errors are *indexer.Error with the credential redacted, or the context error
// when ctx was canceled.
func (c *Client) get(ctx context.Context, v url.Values) ([]byte, error) {
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("%s: %w", c.info.Name, err)
		}
	}
	rctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	q := make(url.Values, len(v)+1)
	for k, vs := range v {
		q[k] = vs
	}
	if c.apiKey != "" {
		q.Set("apikey", c.apiKey)
	}
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, c.endpoint+"?"+q.Encode(), http.NoBody)
	if err != nil {
		return nil, &indexer.Error{Kind: indexer.ErrBadURL, Indexer: c.info.Name, Message: c.redact(err.Error())}
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.1")
	req.Header.Set("User-Agent", c.userAgent)

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.transportError(ctx, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrBodyBytes))
		_ = resp.Body.Close()
	}()
	c.log.DebugContext(ctx, "indexer request", "indexer", c.info.Name, "function", v.Get("t"),
		"status", resp.StatusCode, "duration", time.Since(start))

	if resp.StatusCode != http.StatusOK {
		return nil, c.statusError(resp)
	}
	if resp.ContentLength > c.maxBody {
		return nil, c.tooLarge()
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return nil, c.transportError(ctx, err)
	}
	if int64(len(body)) > c.maxBody {
		return nil, c.tooLarge()
	}
	return body, nil
}

func (c *Client) tooLarge() error {
	return &indexer.Error{Kind: indexer.ErrBadResponse, Indexer: c.info.Name,
		Message: fmt.Sprintf("response larger than %d MiB", c.maxBody>>20)}
}

// statusError maps a non-200 answer. A Newznab <error> body takes precedence
// over the HTTP status.
func (c *Client) statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodyBytes))
	if _, err := ParseFeed(body, c.info); err != nil {
		var ie *indexer.Error
		if errors.As(err, &ie) && ie.Code != 0 {
			ie.HTTPStatus = resp.StatusCode
			if errors.Is(ie.Kind, indexer.ErrRateLimited) {
				ie.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), c.now())
			}
			return c.decorate(ie)
		}
	}
	e := &indexer.Error{Kind: indexer.ErrBadResponse, HTTPStatus: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	switch s := resp.StatusCode; {
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		e.Kind = indexer.ErrUnauthorized
	case s == http.StatusNotFound:
		e.Kind = indexer.ErrNotIndexer
	case s == http.StatusTooManyRequests:
		e.Kind = indexer.ErrRateLimited
		e.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), c.now())
	case s >= 500:
		e.Kind = indexer.ErrServer
		e.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), c.now())
	}
	return c.decorate(e)
}

// decorate stamps the indexer name on an *indexer.Error and redacts its message.
func (c *Client) decorate(err error) error {
	var e *indexer.Error
	if !errors.As(err, &e) {
		return err
	}
	e.Indexer = c.info.Name
	e.Message = c.redact(e.Message)
	return e
}

// transportError classifies a failed request. The caller's own cancellation is
// returned as is (wrapped): it is not an indexer failure. The caller's deadline
// is reported as ErrTimeout.
func (c *Client) transportError(ctx context.Context, err error) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &indexer.Error{Kind: indexer.ErrTimeout, Indexer: c.info.Name, Message: "the search deadline passed", Err: ctx.Err()}
	case ctx.Err() != nil:
		return fmt.Errorf("%s: %w", c.info.Name, ctx.Err())
	}
	inner := err
	var ue *url.Error
	if errors.As(err, &ue) {
		inner = ue.Err
	}
	e := &indexer.Error{Kind: indexer.ErrUnreachable, Indexer: c.info.Name, Message: c.redact(inner.Error()), Err: inner}
	var (
		dns     *net.DNSError
		netErr  net.Error
		unknown x509.UnknownAuthorityError
		invalid x509.CertificateInvalidError
		host    x509.HostnameError
		verify  *tls.CertificateVerificationError
		record  tls.RecordHeaderError
	)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		e.Kind, e.Message = indexer.ErrTimeout, fmt.Sprintf("no answer within %s", c.timeout)
	case errors.As(err, &verify), errors.As(err, &unknown), errors.As(err, &invalid), errors.As(err, &host), errors.As(err, &record),
		strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		e.Kind = indexer.ErrTLS
	case errors.As(err, &dns):
		e.Message = "host name not found"
	}
	return e
}

// redact removes the API key from s, raw and URL-encoded, and blanks any apikey
// query value.
func (c *Client) redact(s string) string {
	if c.apiKey != "" {
		s = strings.ReplaceAll(s, c.apiKey, redacted)
		if esc := url.QueryEscape(c.apiKey); esc != c.apiKey {
			s = strings.ReplaceAll(s, esc, redacted)
		}
	}
	return redactParam(s, "apikey=")
}

func redactParam(s, param string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, param)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i+len(param)])
		s = s[i+len(param):]
		end := strings.IndexAny(s, "&\"' #;")
		if end < 0 {
			end = len(s)
		}
		if end > 0 {
			b.WriteString(redacted)
		}
		s = s[end:]
	}
}

// parseRetryAfter reads delta-seconds or an HTTP date; 0 if absent or invalid.
// The result is capped at a day.
func parseRetryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	var d time.Duration
	if n, err := strconv.Atoi(h); err == nil {
		d = time.Duration(n) * time.Second
	} else if t, err := http.ParseTime(h); err == nil {
		d = t.Sub(now)
	}
	if d <= 0 {
		return 0
	}
	return min(d, 24*time.Hour)
}
