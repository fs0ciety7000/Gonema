package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/fs0ciety7000/gonema/internal/auth"
	"github.com/fs0ciety7000/gonema/internal/ical"
	"github.com/fs0ciety7000/gonema/internal/library"
)

// The iCal feed (GET /calendar.ics?key=...) and its settings. The feed is
// authenticated by its own read-only key in the query string: calendar apps
// cannot send headers. This is the only key Gonema accepts in a URL; it opens
// this feed and nothing else (see docs/security.md).

// calendarPath is the feed path, without the base URL.
const calendarPath = Prefix + "/calendar.ics"

// CalendarFeedView is Settings > Calendar feed. The key is never returned,
// except by POST /settings/calendar-feed/regenerate, once.
type CalendarFeedView struct {
	Enabled    bool       `json:"enabled"`
	Last4      string     `json:"last4,omitempty" doc:"Last four characters of the key"`
	CreatedAt  *time.Time `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt" doc:"Last fetch by a calendar app (updated at most every 5 minutes)"`
	auth.CalendarOptions
	Key  *string `json:"key,omitempty" doc:"Only in the answer of regenerate: the key, shown once"`
	URL  *string `json:"url,omitempty" doc:"Only in the answer of regenerate: the feed URL with its key, as seen from this request (behind a reverse proxy, check the host)"`
	Path *string `json:"path,omitempty" doc:"Only in the answer of regenerate: the feed path with its key, to prefix with the public origin"`
}

func calendarView(f *auth.CalendarFeed) CalendarFeedView {
	return CalendarFeedView{Enabled: f.Enabled, Last4: f.Last4, CreatedAt: f.CreatedAt, LastUsedAt: f.LastUsedAt, CalendarOptions: f.Options}
}

// Feed rate limits, per client address.
const (
	calendarWindow       = 10 * time.Minute
	calendarRequests     = 60 // requests per window
	calendarFailures     = 10 // wrong keys per window
	calendarLimiterMax   = 10000
	calendarRefresh      = time.Hour
	calendarCacheControl = "private, max-age=900"
)

// windowLimiter counts events per key in fixed windows. Memory is bounded: the
// map is cleared when it grows past max (a burst of addresses resets counts).
type windowLimiter struct {
	mu     sync.Mutex
	window time.Duration
	limit  int
	max    int
	m      map[string]*windowCount
	now    func() time.Time
}

type windowCount struct {
	start time.Time
	n     int
}

func newWindowLimiter(window time.Duration, limit, max int) *windowLimiter {
	return &windowLimiter{window: window, limit: limit, max: max, m: map[string]*windowCount{}, now: time.Now}
}

// count records an event; it returns 0 when under the limit, else how long to
// wait.
func (l *windowLimiter) count(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	c, ok := l.m[key]
	if !ok || now.Sub(c.start) >= l.window {
		if !ok && len(l.m) >= l.max {
			clear(l.m)
		}
		c = &windowCount{start: now}
		l.m[key] = c
	}
	c.n++
	if c.n > l.limit {
		return c.start.Add(l.window).Sub(now)
	}
	return 0
}

// over reports how long to wait when the key is already over the limit,
// without counting.
func (l *windowLimiter) over(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.m[key]
	if !ok || l.now().Sub(c.start) >= l.window || c.n < l.limit {
		return 0
	}
	return c.start.Add(l.window).Sub(l.now())
}

type calendarInput struct {
	Key string `query:"key" maxLength:"128" doc:"The calendar feed key (Settings > Calendar feed); read-only, opens this feed only"`
}

func retryAfter(d time.Duration) http.Header {
	return http.Header{"Retry-After": {strconv.Itoa(int(d.Seconds()) + 1)}}
}

func (s *Server) calendarRoutes() {
	tags := []string{"Calendar"}
	requests := newWindowLimiter(calendarWindow, calendarRequests, calendarLimiterMax)
	failures := newWindowLimiter(calendarWindow, calendarFailures, calendarLimiterMax)

	huma.Register(s.api, huma.Operation{
		OperationID: "get-calendar-feed-ics", Method: http.MethodGet, Path: "/calendar.ics", Tags: tags,
		Summary: "iCal feed of the release dates",
		Description: "An RFC 5545 calendar of the release dates of the library movies (all-day events), for calendar apps. " +
			"Authenticated by the calendar feed key in the key query parameter, the one key accepted in a URL: it is read-only " +
			"and opens this feed only (no session, no API key). Options are set in Settings > Calendar feed. " +
			"Limited to 60 requests and 10 wrong keys per 10 minutes per client address (429).",
		Responses: map[string]*huma.Response{
			"200": {Description: "The calendar", Content: map[string]*huma.MediaType{
				"text/calendar": {Schema: &huma.Schema{Type: huma.TypeString}},
			}},
		},
		Errors: []int{http.StatusUnauthorized, http.StatusTooManyRequests},
	}, func(ctx context.Context, in *calendarInput) (*huma.StreamResponse, error) {
		c := clientOf(ctx)
		if w := failures.over(c.IP); w > 0 {
			return nil, huma.ErrorWithHeaders(huma.Error429TooManyRequests("Too many wrong calendar keys: wait a few minutes."), retryAfter(w))
		}
		if w := requests.count(c.IP); w > 0 {
			return nil, huma.ErrorWithHeaders(huma.Error429TooManyRequests("Too many calendar requests: wait a few minutes."), retryAfter(w))
		}
		if s.d.Auth == nil {
			return nil, huma.Error401Unauthorized("The calendar feed is not available on this server.")
		}
		opts, err := s.d.Auth.CalendarKeyOptions(ctx, strings.TrimSpace(in.Key))
		if errors.Is(err, auth.ErrUnauthenticated) {
			failures.count(c.IP)
			s.log.Warn("calendar feed: wrong or disabled key", slog.String("ip", c.IP))
			return nil, huma.Error401Unauthorized("This calendar key is not valid or the feed is turned off: copy the feed URL again from Settings > Calendar feed.")
		}
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		cal, err := s.buildCalendar(ctx, &opts, &c)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			_, w := humago.Unwrap(hctx)
			h := w.Header()
			h.Set("Content-Type", "text/calendar; charset=utf-8")
			h.Set("Content-Disposition", `inline; filename="gonema.ics"`)
			h.Set("Cache-Control", calendarCacheControl)
			w.WriteHeader(http.StatusOK)
			if err := cal.Write(w); err != nil {
				s.log.Debug("calendar feed write interrupted", slog.Any("error", err))
			}
		}}, nil
	})

	setTags := []string{"Settings"}
	huma.Register(s.api, huma.Operation{
		OperationID: "get-calendar-feed", Method: http.MethodGet, Path: "/settings/calendar-feed", Tags: setTags,
		Summary:     "Calendar feed settings",
		Description: "Whether the iCal feed is on, the last four characters of its key and its options. The key and the feed URL are shown only by regenerate.",
	}, func(ctx context.Context, _ *struct{}) (*noStore[CalendarFeedView], error) {
		a, p, err := s.caller(ctx)
		if err != nil {
			return nil, err
		}
		f, err := a.CalendarFeed(ctx, p.UserID)
		if err != nil {
			return nil, s.authProblem(ctx, err)
		}
		return &noStore[CalendarFeedView]{CacheControl: "no-store", Body: calendarView(&f)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "set-calendar-feed", Method: http.MethodPut, Path: "/settings/calendar-feed", Tags: setTags,
		Summary:     "Change the calendar feed options",
		Description: "Days back and ahead, unmonitored movies, release types. The key is unchanged; the feed stays on or off.",
	}, func(ctx context.Context, in *struct{ Body auth.CalendarOptions }) (*noStore[CalendarFeedView], error) {
		a, p, err := s.caller(ctx)
		if err != nil {
			return nil, err
		}
		f, err := a.SetCalendarOptions(ctx, p.UserID, in.Body)
		if err != nil {
			return nil, s.authProblem(ctx, err)
		}
		return &noStore[CalendarFeedView]{CacheControl: "no-store", Body: calendarView(&f)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "regenerate-calendar-feed", Method: http.MethodPost, Path: "/settings/calendar-feed/regenerate", Tags: setTags,
		Summary: "Turn the calendar feed on with a new key",
		Description: "Creates a new read-only key and returns it once, with the feed URL. The previous key (and every calendar " +
			"subscribed with it) stops working at once.",
	}, func(ctx context.Context, _ *struct{}) (*noStore[CalendarFeedView], error) {
		a, p, err := s.caller(ctx)
		if err != nil {
			return nil, err
		}
		key, f, err := a.RegenerateCalendarKey(ctx, p.UserID)
		if err != nil {
			return nil, s.authProblem(ctx, err)
		}
		v := calendarView(&f)
		c := clientOf(ctx)
		path := s.d.BaseURL + calendarPath + "?key=" + key
		url := c.origin() + path
		v.Key, v.Path, v.URL = &key, &path, &url
		return &noStore[CalendarFeedView]{CacheControl: "no-store", Body: v}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "disable-calendar-feed", Method: http.MethodDelete, Path: "/settings/calendar-feed", Tags: setTags,
		Summary:       "Turn the calendar feed off",
		Description:   "Requires confirm=true (422 otherwise). The key is forgotten: subscribed calendars stop updating. The options are kept. 404 when the feed is already off.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		Confirm bool `query:"confirm" doc:"Must be true"`
	}) (*struct{}, error) {
		if !in.Confirm {
			return nil, huma.Error422UnprocessableEntity("Turning the calendar feed off breaks every calendar subscribed to it: confirm with confirm=true.")
		}
		a, p, err := s.caller(ctx)
		if err != nil {
			return nil, err
		}
		if err := a.DisableCalendarFeed(ctx, p.UserID); err != nil {
			if errors.Is(err, auth.ErrNotFound) {
				return nil, huma.Error404NotFound("The calendar feed is already off.")
			}
			return nil, s.authProblem(ctx, err)
		}
		return nil, nil
	})
}

// origin is the scheme and host of the request, as the client sees them when
// GONEMA_TRUSTED_PROXIES is right.
func (c *client) origin() string {
	scheme := "http"
	if c.Secure {
		scheme = "https"
	}
	return scheme + "://" + c.Host
}

var releaseLabels = map[string]string{
	library.ReleaseCinema: "In cinemas", library.ReleaseDigital: "Digital release", library.ReleasePhysical: "Physical release",
}

// buildCalendar reads the release events of the feed window (today minus
// PastDays to today plus FutureDays, in chunks the library calendar accepts).
func (s *Server) buildCalendar(ctx context.Context, o *auth.CalendarOptions, c *client) (*ical.Calendar, error) {
	now := time.Now().UTC()
	start, end := now.AddDate(0, 0, -o.PastDays), now.AddDate(0, 0, o.FutureDays)
	cal := &ical.Calendar{
		ProdID: "-//Gonema//Release calendar " + s.d.Version + "//EN", Name: "Gonema releases",
		Description: "Release dates of the films in your Gonema library", Refresh: calendarRefresh,
	}
	for from := start; !from.After(end); from = from.AddDate(0, 0, library.MaxCalendarDays) {
		to := from.AddDate(0, 0, library.MaxCalendarDays-1)
		if to.After(end) {
			to = end
		}
		evs, err := s.d.Library.Calendar(ctx, library.CalendarQuery{
			Start: from.Format(time.DateOnly), End: to.Format(time.DateOnly), Unmonitored: o.Unmonitored, Types: o.Types,
		})
		if err != nil {
			return nil, err
		}
		for i := range evs {
			e := &evs[i]
			day, err := time.Parse(time.DateOnly, e.Date)
			if err != nil {
				continue
			}
			m := e.Movie
			title := m.Title
			if m.Year > 0 {
				title = fmt.Sprintf("%s (%d)", m.Title, m.Year)
			}
			label := releaseLabels[e.Type]
			cal.Events = append(cal.Events, ical.Event{
				// Stable across fetches, keys and restarts: movie and release type.
				UID:         fmt.Sprintf("gonema-movie-%d-%s-tmdb%d@gonema", m.ID, e.Type, m.TmdbID),
				Date:        day,
				Summary:     title + " · " + label,
				Description: e.Note + ".",
				URL:         c.origin() + s.d.BaseURL + "/films/" + strconv.FormatInt(m.ID, 10),
				Categories:  []string{label},
				Stamp:       now,
			})
		}
	}
	return cal, nil
}
