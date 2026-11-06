package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// The calendar feed key: a read-only key that only opens the iCal feed
// (GET /api/v1/calendar.ics?key=...). Calendar apps cannot send headers, so
// this key travels in the URL, the one documented exception to the "never a
// key in the URL" rule. It is separate from the API key, grants nothing else,
// is stored hashed (SHA-256, last four characters kept for display), shown
// once at creation, and can be regenerated or disabled at any time.

// CalendarKeyPrefix starts every calendar key.
const CalendarKeyPrefix = "gnc_"

// Release types of the feed (same names as the library calendar).
var calendarTypes = []string{"cinema", "digital", "physical"}

// Bounds of the feed options.
const (
	MaxCalendarPastDays   = 365
	MaxCalendarFutureDays = 730
)

// CalendarOptions select the events of the feed.
type CalendarOptions struct {
	PastDays    int      `json:"pastDays" minimum:"0" maximum:"365" doc:"Days before today included"`
	FutureDays  int      `json:"futureDays" minimum:"1" maximum:"730" doc:"Days after today included"`
	Unmonitored bool     `json:"unmonitored" doc:"Also the unmonitored movies that are not released yet (like the calendar screen)"`
	Types       []string `json:"releaseTypes" minItems:"1" maxItems:"3" doc:"cinema, digital and/or physical"`
}

// DefaultCalendarOptions returns the defaults: 30 days back, 180 ahead, monitored movies, every
// release type.
func DefaultCalendarOptions() CalendarOptions {
	return CalendarOptions{PastDays: 30, FutureDays: 180, Types: slices.Clone(calendarTypes)}
}

// Validate checks the options and normalizes the types (sorted, unique).
func (o *CalendarOptions) Validate() error {
	switch {
	case o.PastDays < 0 || o.PastDays > MaxCalendarPastDays:
		return &InvalidError{Field: "pastDays", Msg: fmt.Sprintf("the feed can look back 0 to %d days", MaxCalendarPastDays)}
	case o.FutureDays < 1 || o.FutureDays > MaxCalendarFutureDays:
		return &InvalidError{Field: "futureDays", Msg: fmt.Sprintf("the feed can look ahead 1 to %d days", MaxCalendarFutureDays)}
	case len(o.Types) == 0:
		return &InvalidError{Field: "releaseTypes", Msg: "choose at least one release type: cinema, digital or physical"}
	}
	var types []string
	for _, t := range o.Types {
		t = strings.TrimSpace(t)
		if !slices.Contains(calendarTypes, t) {
			return &InvalidError{Field: "releaseTypes", Msg: "release types must be cinema, digital or physical"}
		}
		if !slices.Contains(types, t) {
			types = append(types, t)
		}
	}
	slices.SortFunc(types, func(a, b string) int { return slices.Index(calendarTypes, a) - slices.Index(calendarTypes, b) })
	o.Types = types
	return nil
}

// CalendarFeed describes the feed of an account, without its key.
type CalendarFeed struct {
	Enabled    bool
	Last4      string
	CreatedAt  *time.Time
	LastUsedAt *time.Time
	Options    CalendarOptions
}

func calendarFeedOf(r *db.CalendarFeed) CalendarFeed {
	f := CalendarFeed{Enabled: r.KeyHash != nil, Last4: r.Last4, Options: CalendarOptions{
		PastDays: int(r.PastDays), FutureDays: int(r.FutureDays), Unmonitored: r.IncludeUnmonitored,
	}}
	if err := json.Unmarshal([]byte(r.ReleaseTypes), &f.Options.Types); err != nil || len(f.Options.Types) == 0 {
		f.Options.Types = slices.Clone(calendarTypes)
	}
	if f.Enabled && r.CreatedAt != nil {
		t := store.FromMillis(*r.CreatedAt)
		f.CreatedAt = &t
	}
	if f.Enabled && r.LastUsedAt != nil {
		t := store.FromMillis(*r.LastUsedAt)
		f.LastUsedAt = &t
	}
	return f
}

// CalendarFeed returns the feed of the user (off, with the default options,
// when it was never set up).
func (s *Service) CalendarFeed(ctx context.Context, userID int64) (CalendarFeed, error) {
	if userID == 0 {
		return CalendarFeed{}, ErrNeedsSetup
	}
	r, err := s.st.Read().GetCalendarFeed(ctx, userID)
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return CalendarFeed{Options: DefaultCalendarOptions()}, nil
	}
	if err != nil {
		return CalendarFeed{}, store.MapError(fmt.Errorf("auth: read calendar feed: %w", err))
	}
	return calendarFeedOf(&r), nil
}

// RegenerateCalendarKey turns the feed on with a new key, returned once; the
// previous key stops working at once. The options are kept.
func (s *Service) RegenerateCalendarKey(ctx context.Context, userID int64) (string, CalendarFeed, error) {
	if userID == 0 {
		return "", CalendarFeed{}, ErrNeedsSetup
	}
	key, h := newSecret(CalendarKeyPrefix)
	r, err := s.st.Write().SetCalendarFeedKey(ctx, db.SetCalendarFeedKeyParams{
		UserID: userID, KeyHash: h, Last4: last4(key), CreatedAt: ptrTo(store.Millis(s.now())),
	})
	if err != nil {
		return "", CalendarFeed{}, store.MapError(fmt.Errorf("auth: set calendar key: %w", err))
	}
	s.log.Info("calendar feed key regenerated", slog.Int64("user_id", userID))
	return key, calendarFeedOf(&r), nil
}

// DisableCalendarFeed forgets the key: the feed URL stops working. The
// options are kept. ErrNotFound when the feed is already off.
func (s *Service) DisableCalendarFeed(ctx context.Context, userID int64) error {
	if userID == 0 {
		return ErrNeedsSetup
	}
	n, err := s.st.Write().DisableCalendarFeed(ctx, userID)
	if err != nil {
		return store.MapError(fmt.Errorf("auth: disable calendar feed: %w", err))
	}
	if n == 0 {
		return ErrNotFound
	}
	s.log.Info("calendar feed disabled", slog.Int64("user_id", userID))
	return nil
}

// SetCalendarOptions changes the options of the feed (the key is unchanged).
func (s *Service) SetCalendarOptions(ctx context.Context, userID int64, o CalendarOptions) (CalendarFeed, error) {
	if userID == 0 {
		return CalendarFeed{}, ErrNeedsSetup
	}
	if err := o.Validate(); err != nil {
		return CalendarFeed{}, err
	}
	types, err := json.Marshal(o.Types)
	if err != nil {
		return CalendarFeed{}, fmt.Errorf("auth: encode release types: %w", err)
	}
	r, err := s.st.Write().SetCalendarFeedOptions(ctx, db.SetCalendarFeedOptionsParams{
		UserID: userID, PastDays: int64(o.PastDays), FutureDays: int64(o.FutureDays),
		IncludeUnmonitored: o.Unmonitored, ReleaseTypes: string(types),
	})
	if err != nil {
		return CalendarFeed{}, store.MapError(fmt.Errorf("auth: set calendar options: %w", err))
	}
	return calendarFeedOf(&r), nil
}

// CalendarKeyOptions returns the options of the feed a calendar key opens,
// ErrUnauthenticated when the key is unknown or the feed is off. The key opens
// nothing else. Its last use is recorded at most every touchEvery.
func (s *Service) CalendarKeyOptions(ctx context.Context, key string) (CalendarOptions, error) {
	if !strings.HasPrefix(key, CalendarKeyPrefix) || len(key) > 128 {
		return CalendarOptions{}, ErrUnauthenticated
	}
	r, err := s.st.Read().GetCalendarFeedByKey(ctx, hashOf(key))
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return CalendarOptions{}, ErrUnauthenticated
	}
	if err != nil {
		return CalendarOptions{}, store.MapError(fmt.Errorf("auth: read calendar key: %w", err))
	}
	now := store.Millis(s.now())
	if r.LastUsedAt == nil || now-*r.LastUsedAt >= touchEvery.Milliseconds() {
		if err := s.st.Write().TouchCalendarFeed(ctx, db.TouchCalendarFeedParams{LastUsedAt: &now, ID: r.ID}); err != nil {
			s.log.Warn("cannot record calendar feed use", slog.Any("error", err))
		}
	}
	return calendarFeedOf(&r).Options, nil
}

func ptrTo[T any](v T) *T { return &v }
