package library

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/fs0ciety7000/gonema/internal/quality"
)

// Release types of the calendar.
const (
	ReleaseCinema   = "cinema"
	ReleaseDigital  = "digital"
	ReleasePhysical = "physical"
)

// MaxCalendarDays bounds the range of one calendar request.
const MaxCalendarDays = 400

// dateLayout is the layout of the release dates (movies.release_*).
const dateLayout = "2006-01-02"

// CalendarQuery selects calendar events. Start and End are dates
// (YYYY-MM-DD), both included.
type CalendarQuery struct {
	Start, End string
	// Unmonitored adds the unmonitored movies that are not released yet
	// (announced or in cinemas); by default the calendar shows monitored
	// movies only.
	Unmonitored bool
	// Types limits the release types (ReleaseCinema...); empty means all.
	Types []string
}

// CalendarEvent is one release date of a library movie.
type CalendarEvent struct {
	Date  string // YYYY-MM-DD
	Type  string // ReleaseCinema, ReleaseDigital or ReleasePhysical
	Movie *MovieSummary
	// Searchable: this release satisfies the movie's minimum availability, so
	// Gonema searches for the movie from this date (when monitored).
	Searchable bool
	// Note says in plain language what Gonema does about this release.
	Note string
}

// searchable reports whether a release type satisfies a minimum availability.
func searchable(typ, minimum string) bool {
	switch minimum {
	case AvailAnnounced, AvailInCinemas:
		return true
	}
	return typ == ReleaseDigital || typ == ReleasePhysical
}

// eventNote is the plain-language note of an event; today is YYYY-MM-DD.
func eventNote(e *CalendarEvent, today string) string {
	m := e.Movie
	q := ""
	if m.FileQuality != nil {
		q = quality.ID(*m.FileQuality).Name()
	}
	switch {
	case !m.Monitored:
		if m.Status == "announced" {
			return "Announced; not monitored"
		}
		return "Not monitored"
	case m.State == StateDownloading:
		return "Downloading"
	case m.State == StateAvailable:
		return "Already have " + q
	case m.State == StateUpgradable:
		if e.Searchable {
			return "May upgrade your " + q
		}
		return "Have " + q + ", below the cutoff"
	case !e.Searchable:
		return "Gonema waits for the digital or physical release"
	case e.Date > today:
		return "Will search on release day"
	}
	return "Released; Gonema is searching for it"
}

// Calendar returns the release events of the library movies in a date range,
// by date, then type (cinema, digital, physical), then title. Rows come from
// the in-memory index: no database scan per request.
func (s *Service) Calendar(ctx context.Context, cq CalendarQuery) ([]CalendarEvent, error) {
	start, err := time.Parse(dateLayout, cq.Start)
	if err != nil {
		return nil, invalid("start", "start must be a date (YYYY-MM-DD)")
	}
	end, err := time.Parse(dateLayout, cq.End)
	if err != nil {
		return nil, invalid("end", "end must be a date (YYYY-MM-DD)")
	}
	if end.Before(start) {
		return nil, invalid("end", "the range is empty: end is before start")
	}
	if end.Sub(start) > MaxCalendarDays*24*time.Hour {
		return nil, invalid("end", "the range is longer than %d days", MaxCalendarDays)
	}
	want := map[string]bool{ReleaseCinema: true, ReleaseDigital: true, ReleasePhysical: true}
	if len(cq.Types) > 0 {
		clear(want)
		for _, t := range cq.Types {
			switch t {
			case ReleaseCinema, ReleaseDigital, ReleasePhysical:
				want[t] = true
			default:
				return nil, invalid("types", "release types must be cinema, digital or physical")
			}
		}
	}
	idx, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	today := s.now().Format(dateLayout)
	var out []CalendarEvent
	for i := range idx.rows {
		m := &idx.rows[i]
		if m.Status == "deleted" {
			continue
		}
		if !m.Monitored && (!cq.Unmonitored || m.Status == "released") {
			continue
		}
		for _, d := range [...]struct {
			typ  string
			date *string
		}{{ReleaseCinema, m.ReleaseCinema}, {ReleaseDigital, m.ReleaseDigital}, {ReleasePhysical, m.ReleasePhysical}} {
			if d.date == nil || !want[d.typ] || *d.date < cq.Start || *d.date > cq.End {
				continue
			}
			e := CalendarEvent{Date: *d.date, Type: d.typ, Movie: m, Searchable: searchable(d.typ, m.MinimumAvailability)}
			e.Note = eventNote(&e, today)
			out = append(out, e)
		}
	}
	order := map[string]int{ReleaseCinema: 0, ReleaseDigital: 1, ReleasePhysical: 2}
	slices.SortFunc(out, func(a, b CalendarEvent) int {
		return cmp.Or(cmp.Compare(a.Date, b.Date), cmp.Compare(order[a.Type], order[b.Type]),
			cmp.Compare(a.Movie.SortTitle, b.Movie.SortTitle), cmp.Compare(a.Movie.ID, b.Movie.ID))
	})
	if out == nil {
		out = []CalendarEvent{}
	}
	return out, nil
}

// Upcoming returns the next release events of monitored movies, from today on
// (at most limit, within days).
func (s *Service) Upcoming(ctx context.Context, days, limit int) ([]CalendarEvent, error) {
	now := s.now()
	evs, err := s.Calendar(ctx, CalendarQuery{Start: now.Format(dateLayout), End: now.AddDate(0, 0, days).Format(dateLayout)})
	if err != nil {
		return nil, err
	}
	return evs[:min(len(evs), limit)], nil
}
