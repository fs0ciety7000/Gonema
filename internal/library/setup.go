package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Onboarding status, interface preferences, people search and the counts of
// the home dashboard.

// Settings keys.
const (
	settingUIPreferences   = "ui.preferences"
	settingSetupDismissed  = "ui.onboarding_dismissed"
	maxPeopleQueryRunes    = 200
	defaultWeekStartMonday = 1
)

// Setup step ids, in the order of the onboarding screens.
const (
	StepAccount        = "account"
	StepTMDB           = "tmdb"
	StepRootFolder     = "root_folder"
	StepDownloadClient = "download_client"
	StepIndexer        = "indexer"
	StepQualityProfile = "quality_profile"
	StepFirstMovie     = "first_movie"
)

// SetupStep is one onboarding step.
type SetupStep struct {
	ID string
	// Done: the step needs nothing more.
	Done bool
	// Available is false for steps that need a later version.
	Available bool
	// Count is the number of items the step created (folders, clients...).
	Count int64
}

// SetupStatus is the progress of the onboarding.
type SetupStatus struct {
	Steps []SetupStep
	// Complete: every available step is done.
	Complete bool
	// Dismissed: the user chose "skip for now".
	Dismissed bool
	// Next is the first available step not done ("" when complete).
	Next string
}

// SetupStatus reads the onboarding progress from what is configured. It does
// no network access (the TMDB key is checked when it is saved).
func (s *Service) SetupStatus(ctx context.Context) (SetupStatus, error) {
	c, err := s.st.Read().GetSetupCounts(ctx)
	if err != nil {
		return SetupStatus{}, store.MapError(fmt.Errorf("library: setup counts: %w", err))
	}
	prefs, err := s.UIPreferences(ctx)
	if err != nil {
		return SetupStatus{}, err
	}
	dismissed, err := store.GetSetting[bool](ctx, s.st, settingSetupDismissed)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return SetupStatus{}, fmt.Errorf("library: read %s: %w", settingSetupDismissed, err)
	}
	tmdb := s.TMDBConfigured()
	users, err := s.st.Read().CountUsers(ctx)
	if err != nil {
		return SetupStatus{}, store.MapError(fmt.Errorf("library: count accounts: %w", err))
	}
	st := SetupStatus{Dismissed: dismissed, Steps: []SetupStep{
		// The admin account (POST /auth/setup); done once it exists.
		{ID: StepAccount, Available: true, Done: users > 0, Count: users},
		{ID: StepTMDB, Available: true, Done: tmdb, Count: boolCount(tmdb)},
		{ID: StepRootFolder, Available: true, Done: c.RootFolders > 0, Count: c.RootFolders},
		{ID: StepDownloadClient, Available: true, Done: c.DownloadClients > 0, Count: c.DownloadClients},
		{ID: StepIndexer, Available: true, Done: c.Indexers > 0, Count: c.Indexers},
		// Starter profiles always exist: the step is the choice of the default one.
		{ID: StepQualityProfile, Available: true, Done: prefs.DefaultQualityProfileID != nil || c.Movies > 0, Count: c.QualityProfiles},
		{ID: StepFirstMovie, Available: true, Done: c.Movies > 0, Count: c.Movies},
	}}
	st.Complete = true
	for _, step := range st.Steps {
		if step.Available && !step.Done {
			st.Complete = false
			if st.Next == "" {
				st.Next = step.ID
			}
		}
	}
	return st, nil
}

func boolCount(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// SetSetupDismissed records that the user skipped (or resumed) the onboarding.
func (s *Service) SetSetupDismissed(ctx context.Context, dismissed bool) error {
	if err := store.SetSetting(ctx, s.st, settingSetupDismissed, dismissed); err != nil {
		return fmt.Errorf("library: save %s: %w", settingSetupDismissed, err)
	}
	return nil
}

// UIPreferences are the interface settings shared by every browser (until
// users arrive in phase 8).
type UIPreferences struct {
	Theme   string `json:"theme" enum:"system,light,dark" doc:"Color theme"`
	Density string `json:"density" enum:"airy,compact" doc:"Default density of lists"`
	// DefaultQualityProfileID, DefaultRootFolderID and
	// DefaultMinimumAvailability preselect the add-film dialog.
	DefaultQualityProfileID    *int64 `json:"defaultQualityProfileId" doc:"Preselected profile when adding a film; null for none"`
	DefaultRootFolderID        *int64 `json:"defaultRootFolderId" doc:"Preselected root folder; null for none"`
	DefaultMinimumAvailability string `json:"defaultMinimumAvailability" enum:"announced,in_cinemas,released"`
	CalendarShowUnmonitored    bool   `json:"calendarShowUnmonitored" doc:"The calendar shows announced unmonitored films by default"`
	WeekStartsOn               int    `json:"weekStartsOn" minimum:"0" maximum:"1" doc:"0 Sunday, 1 Monday"`
}

// DefaultUIPreferences returns the preferences of a new installation.
func DefaultUIPreferences() UIPreferences {
	return UIPreferences{Theme: "system", Density: "airy", DefaultMinimumAvailability: AvailReleased, WeekStartsOn: defaultWeekStartMonday}
}

// UIPreferences returns the stored preferences, defaults filled in. A default
// profile or folder that no longer exists reads as none.
func (s *Service) UIPreferences(ctx context.Context) (UIPreferences, error) {
	p, err := store.GetSetting[UIPreferences](ctx, s.st, settingUIPreferences)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return DefaultUIPreferences(), nil
	case err != nil:
		s.log.Warn("invalid interface preferences, using the defaults", slog.Any("error", err))
		return DefaultUIPreferences(), nil
	}
	def := DefaultUIPreferences()
	if p.Theme == "" {
		p.Theme = def.Theme
	}
	if p.Density == "" {
		p.Density = def.Density
	}
	if p.DefaultMinimumAvailability == "" {
		p.DefaultMinimumAvailability = def.DefaultMinimumAvailability
	}
	if id := p.DefaultQualityProfileID; id != nil {
		if _, err := s.st.Read().GetQualityProfile(ctx, *id); err != nil {
			p.DefaultQualityProfileID = nil
		}
	}
	if id := p.DefaultRootFolderID; id != nil {
		if _, err := s.st.Read().GetRootFolder(ctx, *id); err != nil {
			p.DefaultRootFolderID = nil
		}
	}
	return p, nil
}

// SetUIPreferences validates and saves the preferences.
func (s *Service) SetUIPreferences(ctx context.Context, p UIPreferences) (UIPreferences, error) {
	switch p.Theme {
	case "system", "light", "dark":
	default:
		return UIPreferences{}, invalid("theme", "the theme must be system, light or dark")
	}
	switch p.Density {
	case "airy", "compact":
	default:
		return UIPreferences{}, invalid("density", "the density must be airy or compact")
	}
	if !validAvailability(p.DefaultMinimumAvailability) {
		return UIPreferences{}, invalid("defaultMinimumAvailability", "minimum availability must be announced, in_cinemas or released")
	}
	if p.WeekStartsOn != 0 && p.WeekStartsOn != 1 {
		return UIPreferences{}, invalid("weekStartsOn", "the week starts on Sunday (0) or Monday (1)")
	}
	if id := p.DefaultQualityProfileID; id != nil {
		if _, err := s.st.Read().GetQualityProfile(ctx, *id); err != nil {
			return UIPreferences{}, invalid("defaultQualityProfileId", "quality profile %d does not exist", *id)
		}
	}
	if id := p.DefaultRootFolderID; id != nil {
		if _, err := s.st.Read().GetRootFolder(ctx, *id); err != nil {
			return UIPreferences{}, invalid("defaultRootFolderId", "root folder %d does not exist", *id)
		}
	}
	if err := store.SetSetting(ctx, s.st, settingUIPreferences, p); err != nil {
		return UIPreferences{}, fmt.Errorf("library: save interface preferences: %w", err)
	}
	s.publish(events.Event{Type: events.SettingsChanged, Data: map[string]any{"setting": "ui"}})
	return p, nil
}

// ----------------------------------------------------------------- people

// PersonHit is a provider person with the library flags of the movies they
// are known for.
type PersonHit struct {
	metadata.PersonSummary
	KnownFor []DiscoverItem
}

// PeoplePage is a page of a people search.
type PeoplePage struct {
	Page, TotalPages, TotalResults int
	Results                        []PersonHit
}

// SearchPeople looks people up by name at the provider (cached and rate
// limited by it). Adult profiles and adult movies are dropped.
func (s *Service) SearchPeople(ctx context.Context, query string, page int) (PeoplePage, error) {
	if page == 0 {
		page = 1
	}
	if page < 1 || page > MaxDiscoverPage {
		return PeoplePage{}, invalid("page", "page must be between 1 and %d", MaxDiscoverPage)
	}
	if n := len([]rune(query)); n == 0 || n > maxPeopleQueryRunes {
		return PeoplePage{}, invalid("query", "the name must be 1 to %d characters long", maxPeopleQueryRunes)
	}
	p, err := s.Provider()
	if err != nil {
		return PeoplePage{}, err
	}
	res, err := p.SearchPeople(ctx, query, page)
	if err != nil {
		return PeoplePage{}, fmt.Errorf("library: search people: %w", err)
	}
	var all []metadata.MovieSummary
	for i := range res.Results {
		all = append(all, res.Results[i].KnownFor...)
	}
	flagged, err := s.flagSummaries(ctx, all)
	if err != nil {
		return PeoplePage{}, err
	}
	byID := make(map[int]DiscoverItem, len(flagged))
	for i := range flagged {
		byID[flagged[i].TMDBID] = flagged[i]
	}
	out := PeoplePage{Page: res.Page, TotalPages: res.TotalPages, TotalResults: res.TotalResults, Results: []PersonHit{}}
	for i := range res.Results {
		r := &res.Results[i]
		if r.Adult {
			continue
		}
		h := PersonHit{PersonSummary: *r, KnownFor: []DiscoverItem{}}
		for j := range r.KnownFor {
			if f, ok := byID[r.KnownFor[j].TMDBID]; ok {
				h.KnownFor = append(h.KnownFor, f)
			}
		}
		out.Results = append(out.Results, h)
	}
	return out, nil
}

// Person returns the profile of a provider person.
func (s *Service) Person(ctx context.Context, personID int) (*metadata.PersonDetails, error) {
	if personID <= 0 {
		return nil, invalid("tmdbId", "the TMDB id must be a positive number")
	}
	p, err := s.Provider()
	if err != nil {
		return nil, err
	}
	d, err := p.Person(ctx, personID)
	if errors.Is(err, metadata.ErrNotFound) {
		return nil, notFound("TMDB has no person with id %d", personID)
	}
	if err != nil {
		return nil, fmt.Errorf("library: person %d: %w", personID, err)
	}
	return d, nil
}

// ----------------------------------------------------------------- dashboard

// FolderUsage is what the library stores in a root folder.
type FolderUsage struct {
	Movies     int64
	Files      int64
	SizeOnDisk int64 // bytes of the current files
}

// DashboardCounts are the counts of the home dashboard, from the in-memory
// index.
type DashboardCounts struct {
	// AddedSince counts the movies added at or after the given time.
	AddedSince int64
	// NotYetReleased counts the monitored missing movies not released enough
	// for their minimum availability (wanted later).
	NotYetReleased int64
	Folders        map[int64]FolderUsage
}

// Overview returns the dashboard counts.
func (s *Service) Overview(ctx context.Context, since time.Time) (DashboardCounts, error) {
	idx, err := s.index(ctx)
	if err != nil {
		return DashboardCounts{}, err
	}
	out := DashboardCounts{Folders: map[int64]FolderUsage{}}
	sinceMS := store.Millis(since)
	for i := range idx.rows {
		m := &idx.rows[i]
		if m.AddedAt >= sinceMS {
			out.AddedSince++
		}
		if m.Monitored && m.State == StateMissing && !matchesWanted(m, WantedMissing) {
			out.NotYetReleased++
		}
		u := out.Folders[m.RootFolderID]
		u.Movies++
		if m.FileSize != nil {
			u.Files++
			u.SizeOnDisk += *m.FileSize
		}
		out.Folders[m.RootFolderID] = u
	}
	return out, nil
}

// HistoryCounts counts history events at or after since, by event.
func (s *Service) HistoryCounts(ctx context.Context, since time.Time, kinds []string) (map[string]int64, error) {
	raw := "[]"
	if len(kinds) > 0 {
		b, err := jsonMarshal(kinds)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	rows, err := s.st.Read().CountHistoryEventsSince(ctx, db.CountHistoryEventsSinceParams{Since: store.Millis(since), Events: raw})
	if err != nil {
		return nil, store.MapError(fmt.Errorf("library: count history: %w", err))
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Event] = r.N
	}
	return out, nil
}

func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("library: encode: %w", err)
	}
	return string(b), nil
}
