package api

import (
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Endpoints of the web UI screens of phase 7 lot 3: calendar, home dashboard,
// onboarding, general settings, people search, command palette search,
// history export, pause and resume of the whole queue, per-indexer retry of an
// interactive search.

func decisionProtocol(p string) decision.Protocol { return decision.Protocol(p) }

// ----------------------------------------------------------------- calendar

// CalendarMovie is the movie of a calendar event.
type CalendarMovie struct {
	ID                  int64   `json:"id"`
	TmdbID              int64   `json:"tmdbId"`
	Title               string  `json:"title"`
	Year                int64   `json:"year"`
	PosterURL           *string `json:"posterUrl" doc:"w342"`
	DominantColor       *string `json:"dominantColor"`
	Monitored           bool    `json:"monitored"`
	HasFile             bool    `json:"hasFile"`
	State               string  `json:"state" enum:"available,missing,downloading,upgradable" doc:"Library state"`
	Status              string  `json:"status" enum:"announced,in_cinemas,released,deleted" doc:"Release stage"`
	MinimumAvailability string  `json:"minimumAvailability" enum:"announced,in_cinemas,released"`
	Quality             *string `json:"quality" doc:"Quality id of the file; null without a file"`
	QualityName         *string `json:"qualityName"`
}

// CalendarEvent is one release date of a library movie.
type CalendarEvent struct {
	Date       string        `json:"date" doc:"YYYY-MM-DD"`
	Type       string        `json:"type" enum:"cinema,digital,physical"`
	Searchable bool          `json:"searchable" doc:"This release meets the movie's minimum availability: Gonema searches from this date (monitored movies)"`
	Note       string        `json:"note" doc:"What Gonema does about it, in plain language"`
	Movie      CalendarMovie `json:"movie"`
}

// CalendarPage is the calendar of a date range.
type CalendarPage struct {
	Start  string          `json:"start"`
	End    string          `json:"end"`
	Events []CalendarEvent `json:"events" doc:"By date, then cinema, digital, physical, then title"`
}

func (s *Server) calendarEventOf(e *library.CalendarEvent) CalendarEvent {
	m := e.Movie
	return CalendarEvent{Date: e.Date, Type: e.Type, Searchable: e.Searchable, Note: e.Note, Movie: CalendarMovie{
		ID: m.ID, TmdbID: m.TmdbID, Title: m.Title, Year: m.Year, PosterURL: s.imageURL(imagecache.Poster, gridPosterSize, m.PosterPath),
		DominantColor: m.DominantColor, Monitored: m.Monitored, HasFile: m.FileQuality != nil, State: m.State, Status: m.Status,
		MinimumAvailability: m.MinimumAvailability, Quality: m.FileQuality, QualityName: qualityName(m.FileQuality),
	}}
}

func (s *Server) calendarRoute() {
	huma.Register(s.api, huma.Operation{
		OperationID: "get-calendar", Method: http.MethodGet, Path: "/calendar", Summary: "Release calendar",
		Description: "Cinema, digital and physical release dates of the library movies in a range (both ends included, at most 400 days). " +
			"Monitored movies only by default; unmonitored=true adds the unmonitored movies not released yet. Served from the in-memory index.",
		Tags: []string{"Calendar"},
	}, func(ctx context.Context, in *struct {
		Start       string   `query:"start" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"YYYY-MM-DD; default the first day of the current month"`
		End         string   `query:"end" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"YYYY-MM-DD, included; default start + 41 days (a 6-week grid)"`
		Unmonitored bool     `query:"unmonitored" default:"false" doc:"Add the announced unmonitored movies"`
		Types       []string `query:"types" maxItems:"3" enum:"cinema,digital,physical" doc:"Release types, comma-separated; all when absent"`
	}) (*struct{ Body CalendarPage }, error) {
		start, end := in.Start, in.End
		if start == "" {
			now := time.Now()
			start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).Format(time.DateOnly)
		}
		if end == "" {
			t, err := time.Parse(time.DateOnly, start)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("start must be a date (YYYY-MM-DD).")
			}
			end = t.AddDate(0, 0, 41).Format(time.DateOnly)
		}
		evs, err := s.d.Library.Calendar(ctx, library.CalendarQuery{Start: start, End: end, Unmonitored: in.Unmonitored, Types: in.Types})
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := CalendarPage{Start: start, End: end, Events: make([]CalendarEvent, 0, len(evs))}
		for i := range evs {
			out.Events = append(out.Events, s.calendarEventOf(&evs[i]))
		}
		return &struct{ Body CalendarPage }{out}, nil
	})
}

// ----------------------------------------------------------------- dashboard

// DashboardWanted splits the monitored movies without a file.
type DashboardWanted struct {
	Released       int64 `json:"released" doc:"Released enough for their minimum availability: Gonema searches for them"`
	NotYetReleased int64 `json:"notYetReleased" doc:"Waiting for their release"`
	CutoffUnmet    int64 `json:"cutoffUnmet" doc:"Have a file below the profile's cutoff"`
}

// DashboardMonth is the activity since the first day of the month.
type DashboardMonth struct {
	Since    time.Time `json:"since"`
	Added    int64     `json:"added" doc:"Movies added to the library"`
	Imported int64     `json:"imported" doc:"Files imported (first file of a movie)"`
	Upgraded int64     `json:"upgraded" doc:"Files replaced by a better one"`
}

// DashboardQueueItem is an active download.
type DashboardQueueItem struct {
	ID            int64   `json:"id" doc:"Queue entry id"`
	MovieID       int64   `json:"movieId"`
	Title         string  `json:"title" doc:"Movie title"`
	Year          int64   `json:"year"`
	PosterURL     *string `json:"posterUrl"`
	DominantColor *string `json:"dominantColor"`
	Release       string  `json:"release"`
	Status        string  `json:"status" enum:"queued,downloading,paused,completed,importing,imported,failed,warning,removed"`
	Progress      float64 `json:"progress"`
	SpeedBps      int64   `json:"speedBps"`
	EtaSeconds    *int64  `json:"etaSeconds"`
	Client        string  `json:"client"`
	Size          int64   `json:"size"`
}

// DashboardQueue sums up the queue.
type DashboardQueue struct {
	QueueSummary
	RemainingBytes int64                `json:"remainingBytes"`
	Items          []DashboardQueueItem `json:"items" doc:"Up to 5: downloading first, then importing, queued, paused, warnings"`
}

// DiskUsage is a root folder with its space and contents.
type DiskUsage struct {
	RootFolderID    int64  `json:"rootFolderId"`
	Path            string `json:"path"`
	FreeSpace       *int64 `json:"freeSpace"`
	TotalSpace      *int64 `json:"totalSpace"`
	Accessible      bool   `json:"accessible"`
	Error           string `json:"error,omitempty"`
	Movies          int64  `json:"movies"`
	Files           int64  `json:"files"`
	SizeOnDisk      int64  `json:"sizeOnDisk" doc:"Bytes of the library files in this folder"`
	AverageFileSize *int64 `json:"averageFileSize" doc:"null without files"`
	RoomForFilms    *int64 `json:"roomForFilms" doc:"Free space / average file size; null when unknown"`
}

// DashboardMovie is a recently added movie.
type DashboardMovie struct {
	ID            int64     `json:"id"`
	Title         string    `json:"title"`
	Year          int64     `json:"year"`
	PosterURL     *string   `json:"posterUrl"`
	DominantColor *string   `json:"dominantColor"`
	State         string    `json:"state"`
	Monitored     bool      `json:"monitored"`
	AddedAt       time.Time `json:"addedAt"`
}

// DashboardHealth sums up the health issues.
type DashboardHealth struct {
	Issues   int                  `json:"issues"`
	Errors   int                  `json:"errors"`
	Warnings int                  `json:"warnings"`
	Top      []events.HealthIssue `json:"top" doc:"The 3 most urgent issues"`
}

// ServiceStatus is the state of an indexer or a download client.
type ServiceStatus struct {
	ID       int64      `json:"id"`
	Name     string     `json:"name"`
	Kind     string     `json:"kind"`
	Protocol string     `json:"protocol" enum:"usenet,torrent"`
	Enabled  bool       `json:"enabled" doc:"Indexers: used by at least one of RSS, automatic and interactive searches"`
	State    string     `json:"state" enum:"ok,failing,paused,off" doc:"paused: an indexer skipped after repeated failures until 'until'; off: disabled by the user"`
	Message  string     `json:"message,omitempty" doc:"Last error"`
	Until    *time.Time `json:"until,omitempty"`
}

// Dashboard is everything the home screen shows, in one call without network
// access.
type Dashboard struct {
	GeneratedAt    time.Time        `json:"generatedAt"`
	Library        LibraryStats     `json:"library"`
	Wanted         DashboardWanted  `json:"wanted"`
	ThisMonth      DashboardMonth   `json:"thisMonth"`
	Queue          DashboardQueue   `json:"queue"`
	Upcoming       []CalendarEvent  `json:"upcoming" doc:"Next releases of monitored movies (60 days, at most 6)"`
	Disks          []DiskUsage      `json:"disks"`
	RecentActivity []HistoryRow     `json:"recentActivity" doc:"Latest grabs, imports, upgrades and failures (8)"`
	RecentlyAdded  []DashboardMovie `json:"recentlyAdded" doc:"Latest additions (12)"`
	Health         DashboardHealth  `json:"health"`
	Indexers       []ServiceStatus  `json:"indexers"`
	Clients        []ServiceStatus  `json:"clients"`
	LastRSSSync    *time.Time       `json:"lastRssSync" doc:"null before the first sync"`
}

var activityEvents = []string{
	acquire.HistoryGrabbed, acquire.HistoryGrabOverridden, acquire.HistoryImported, acquire.HistoryUpgraded,
	acquire.HistoryImportFailed, acquire.HistoryDownloadFailed, acquire.HistoryRetriesExhausted,
}

func queueOrder(status string) int {
	switch status {
	case "downloading":
		return 0
	case "importing":
		return 1
	case "queued", "completed":
		return 2
	case "paused":
		return 3
	}
	return 4
}

func (s *Server) dashboard(ctx context.Context) (Dashboard, error) {
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	out := Dashboard{GeneratedAt: now.UTC(), Upcoming: []CalendarEvent{}, Disks: []DiskUsage{}, RecentActivity: []HistoryRow{},
		RecentlyAdded: []DashboardMovie{}, Indexers: []ServiceStatus{}, Clients: []ServiceStatus{},
		Health: DashboardHealth{Top: []events.HealthIssue{}}, Queue: DashboardQueue{Items: []DashboardQueueItem{}}}
	lib := s.d.Library

	st, err := lib.Stats(ctx)
	if err != nil {
		return out, err
	}
	out.Library = LibraryStats{
		Total: st.Total, OnDisk: st.OnDisk, TotalSize: st.TotalSize, Monitored: st.Monitored, Unmonitored: st.Unmonitored,
		Available: st.Available, Missing: st.Missing, Downloading: st.Downloading, Upgradable: st.Upgradable,
		Wanted: st.Wanted, CutoffUnmet: st.CutoffUnmet,
	}
	ov, err := lib.Overview(ctx, monthStart)
	if err != nil {
		return out, err
	}
	out.Wanted = DashboardWanted{Released: st.Wanted, NotYetReleased: ov.NotYetReleased, CutoffUnmet: st.CutoffUnmet}
	counts, err := lib.HistoryCounts(ctx, monthStart, []string{acquire.HistoryImported, acquire.HistoryUpgraded})
	if err != nil {
		return out, err
	}
	out.ThisMonth = DashboardMonth{Since: monthStart.UTC(), Added: ov.AddedSince, Imported: counts[acquire.HistoryImported], Upgraded: counts[acquire.HistoryUpgraded]}

	if qr := s.queueReader(); qr != nil {
		items, err := qr.Queue(ctx)
		if err != nil {
			return out, err
		}
		out.Queue.QueueSummary = summarize(items)
		entries, err := s.queueEntries(ctx, items, 0)
		if err != nil {
			return out, err
		}
		slices.SortStableFunc(entries, func(a, b QueueEntry) int { return cmp.Compare(queueOrder(a.Status), queueOrder(b.Status)) })
		for i := range entries {
			e := &entries[i]
			out.Queue.RemainingBytes += max(e.Size-e.DownloadedBytes, 0)
			if len(out.Queue.Items) < 5 {
				out.Queue.Items = append(out.Queue.Items, DashboardQueueItem{
					ID: e.ID, MovieID: e.MovieID, Title: e.Movie.Title, Year: e.Movie.Year, PosterURL: e.Movie.PosterURL,
					DominantColor: e.Movie.DominantColor, Release: e.Title, Status: e.Status, Progress: e.Progress,
					SpeedBps: e.SpeedBps, EtaSeconds: e.EtaSeconds, Client: e.Client, Size: e.Size,
				})
			}
		}
	}

	up, err := lib.Upcoming(ctx, 60, 6)
	if err != nil {
		return out, err
	}
	for i := range up {
		out.Upcoming = append(out.Upcoming, s.calendarEventOf(&up[i]))
	}

	folders, err := lib.ListRootFolders(ctx)
	if err != nil {
		return out, err
	}
	for i := range folders {
		rf := &folders[i]
		u := ov.Folders[rf.ID]
		d := DiskUsage{
			RootFolderID: rf.ID, Path: rf.Path, FreeSpace: rf.FreeSpace, TotalSpace: rf.TotalSpace, Accessible: rf.Accessible,
			Error: rf.Error, Movies: u.Movies, Files: u.Files, SizeOnDisk: u.SizeOnDisk,
		}
		if u.Files > 0 {
			avg := u.SizeOnDisk / u.Files
			d.AverageFileSize = &avg
			if rf.FreeSpace != nil && avg > 0 {
				room := *rf.FreeSpace / avg
				d.RoomForFilms = &room
			}
		}
		out.Disks = append(out.Disks, d)
	}

	evRaw, err := json.Marshal(activityEvents)
	if err != nil {
		return out, err
	}
	evs := string(evRaw)
	rows, err := s.d.Store.Read().ListHistoryFiltered(ctx, db.ListHistoryFilteredParams{BeforeID: math.MaxInt64, Limit: 8, Events: &evs})
	if err != nil {
		return out, store.MapError(err)
	}
	chains, err := s.downloadChains(ctx, rows)
	if err != nil {
		return out, err
	}
	for i := range rows {
		out.RecentActivity = append(out.RecentActivity, s.historyRowOf(&rows[i], chains))
	}

	page, err := lib.ListMovies(ctx, library.ListQuery{Sort: library.SortAdded, Limit: 12})
	if err != nil {
		return out, err
	}
	for _, m := range page.Movies {
		out.RecentlyAdded = append(out.RecentlyAdded, DashboardMovie{
			ID: m.ID, Title: m.Title, Year: m.Year, PosterURL: s.imageURL(imagecache.Poster, gridPosterSize, m.PosterPath),
			DominantColor: m.DominantColor, State: m.State, Monitored: m.Monitored, AddedAt: ts(m.AddedAt),
		})
	}

	if s.health != nil {
		issues := s.health.Issues() // most urgent first
		out.Health.Issues = len(issues)
		for i := range issues {
			switch issues[i].Severity {
			case events.SeverityError:
				out.Health.Errors++
			case events.SeverityWarning:
				out.Health.Warnings++
			}
		}
		out.Health.Top = append(out.Health.Top, issues[:min(3, len(issues))]...)
	}

	if a := s.d.Acquire; a != nil {
		ixs, err := a.ListIndexers(ctx)
		if err != nil {
			return out, err
		}
		for i := range ixs {
			ix := &ixs[i]
			st := ServiceStatus{ID: ix.ID, Name: ix.Name, Kind: ix.Kind, Protocol: ix.Protocol, Message: ix.Health.LastError,
				Enabled: ix.EnableRSS || ix.EnableAutoSearch || ix.EnableInteractive, State: "ok"}
			switch {
			case !st.Enabled:
				st.State = "off"
			case ix.Health.State == "disabled":
				st.State, st.Until = "paused", ix.Health.DisabledUntil
			case ix.Health.State == "failing":
				st.State = "failing"
			}
			out.Indexers = append(out.Indexers, st)
		}
		cls, err := a.ListDownloadClients(ctx)
		if err != nil {
			return out, err
		}
		for i := range cls {
			c := &cls[i]
			st := ServiceStatus{ID: c.ID, Name: c.Name, Kind: c.Kind, Protocol: c.Protocol, Enabled: c.Enabled, Message: c.Health.LastError, State: "ok"}
			switch {
			case !c.Enabled:
				st.State = "off"
			case c.Health.FailureCount > 0:
				st.State = "failing"
			}
			out.Clients = append(out.Clients, st)
		}
	}
	last, err := s.d.Store.Read().GetLastRSSSync(ctx)
	if err != nil {
		return out, store.MapError(err)
	}
	if last > 0 {
		t := ts(last)
		out.LastRSSSync = &t
	}
	return out, nil
}

func (s *Server) dashboardRoute() {
	huma.Register(s.api, huma.Operation{
		OperationID: "get-dashboard", Method: http.MethodGet, Path: "/dashboard", Summary: "Home dashboard",
		Description: "Library counts, wanted and upgradable, this month's activity, the queue, upcoming releases, disk space per root folder, " +
			"recent activity, recently added films, health issues and the state of indexers and download clients. " +
			"One call, no network access (free space is measured on the local disks).",
		Tags: []string{"Dashboard"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body Dashboard }, error) {
		d, err := s.dashboard(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body Dashboard }{d}, nil
	})
}

// ----------------------------------------------------------------- onboarding and general settings

// SetupStep is one onboarding step.
type SetupStep struct {
	ID        string `json:"id" enum:"account,tmdb,root_folder,download_client,indexer,quality_profile,first_movie"`
	Done      bool   `json:"done"`
	Available bool   `json:"available" doc:"false: arrives in a later version (the admin account comes with authentication)"`
	Count     int64  `json:"count" doc:"Items configured (folders, clients, indexers, profiles, films)"`
}

// SetupStatus is the onboarding progress, resumable at any time.
type SetupStatus struct {
	Complete  bool        `json:"complete" doc:"Every available step is done"`
	Dismissed bool        `json:"dismissed" doc:"The user chose to skip the onboarding"`
	Next      string      `json:"next" doc:"First step to do; empty when complete"`
	Steps     []SetupStep `json:"steps" doc:"In the order of the onboarding screens"`
}

// GeneralSettings are the server settings (read-only: flags and environment
// variables, applied at start) and the interface preferences.
type GeneralSettings struct {
	Server struct {
		Bind     string `json:"bind" doc:"Listening address (GONEMA_BIND)"`
		BaseURL  string `json:"baseUrl" doc:"URL base path (GONEMA_BASE_URL)"`
		DataDir  string `json:"dataDir"`
		LogLevel string `json:"logLevel" doc:"GONEMA_LOG_LEVEL"`
		Version  string `json:"version"`
	} `json:"server" doc:"Read-only here: change them in the environment and restart"`
	UI library.UIPreferences `json:"ui"`
}

func (s *Server) setupStatus(ctx context.Context) (*struct{ Body SetupStatus }, error) {
	st, err := s.d.Library.SetupStatus(ctx)
	if err != nil {
		return nil, s.problem(ctx, err, false)
	}
	out := SetupStatus{Complete: st.Complete, Dismissed: st.Dismissed, Next: st.Next, Steps: make([]SetupStep, 0, len(st.Steps))}
	for _, step := range st.Steps {
		out.Steps = append(out.Steps, SetupStep{ID: step.ID, Done: step.Done, Available: step.Available, Count: step.Count})
	}
	return &struct{ Body SetupStatus }{out}, nil
}

func (s *Server) setupRoutes() {
	huma.Register(s.api, huma.Operation{
		OperationID: "get-onboarding", Method: http.MethodGet, Path: "/onboarding", Summary: "Onboarding progress",
		Description: "Each setup step (TMDB key, root folder, download client, indexer, default quality profile, first film) with done or not, " +
			"so the interface can guide and resume. No network access.",
		Tags: []string{"Onboarding"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body SetupStatus }, error) {
		return s.setupStatus(ctx)
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "set-onboarding", Method: http.MethodPut, Path: "/onboarding", Summary: "Skip or resume the onboarding",
		Tags: []string{"Onboarding"},
	}, func(ctx context.Context, in *struct {
		Body struct {
			Dismissed bool `json:"dismissed"`
		}
	}) (*struct{ Body SetupStatus }, error) {
		if err := s.d.Library.SetSetupDismissed(ctx, in.Body.Dismissed); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return s.setupStatus(ctx)
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "get-general-settings", Method: http.MethodGet, Path: "/settings/general", Summary: "Server settings and interface preferences",
		Tags: []string{"Settings"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body GeneralSettings }, error) {
		var out GeneralSettings
		out.Server.Bind, out.Server.BaseURL, out.Server.DataDir = s.d.Bind, s.d.BaseURL, s.d.DataDir
		out.Server.LogLevel, out.Server.Version = s.d.LogLevel, s.d.Version
		ui, err := s.d.Library.UIPreferences(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out.UI = ui
		return &struct{ Body GeneralSettings }{out}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "set-ui-preferences", Method: http.MethodPut, Path: "/settings/ui", Summary: "Change the interface preferences",
		Description: "Theme, density, defaults of the add-film dialog, calendar and week start. Shared by every browser until users arrive.",
		Tags:        []string{"Settings"},
	}, func(ctx context.Context, in *struct{ Body library.UIPreferences }) (*struct{ Body library.UIPreferences }, error) {
		p, err := s.d.Library.SetUIPreferences(ctx, in.Body)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body library.UIPreferences }{p}, nil
	})
}

// ----------------------------------------------------------------- people

// PersonResult is a TMDB person.
type PersonResult struct {
	TmdbID             int             `json:"tmdbId"`
	Name               string          `json:"name"`
	KnownForDepartment string          `json:"knownForDepartment,omitempty" doc:"Acting, Directing..."`
	ProfileURL         *string         `json:"profileUrl" doc:"Portrait at w185, through the image cache"`
	Popularity         float64         `json:"popularity"`
	KnownFor           []DiscoverMovie `json:"knownFor" doc:"Movies they are known for, flagged when in the library"`
}

// PeoplePage is a page of people.
type PeoplePage struct {
	Page         int            `json:"page"`
	TotalPages   int            `json:"totalPages"`
	TotalResults int            `json:"totalResults"`
	Results      []PersonResult `json:"results"`
}

// PersonProfile is the profile of a person.
type PersonProfile struct {
	TmdbID             int     `json:"tmdbId"`
	ImdbID             string  `json:"imdbId,omitempty"`
	Name               string  `json:"name"`
	ProfileURL         *string `json:"profileUrl" doc:"w185"`
	KnownForDepartment string  `json:"knownForDepartment,omitempty"`
	Biography          string  `json:"biography,omitempty"`
	Birthday           *string `json:"birthday" doc:"YYYY-MM-DD"`
	Deathday           *string `json:"deathday"`
	PlaceOfBirth       string  `json:"placeOfBirth,omitempty"`
}

func (s *Server) personResultOf(h *library.PersonHit) PersonResult {
	out := PersonResult{
		TmdbID: h.TMDBID, Name: h.Name, KnownForDepartment: h.KnownForDepartment, Popularity: h.Popularity,
		ProfileURL: s.imageURLOf(imagecache.Profile, profileSize, h.ProfilePath), KnownFor: make([]DiscoverMovie, 0, len(h.KnownFor)),
	}
	for i := range h.KnownFor {
		out.KnownFor = append(out.KnownFor, s.discoverMovieOf(&h.KnownFor[i].MovieSummary, h.KnownFor[i].Flags))
	}
	return out
}

func (s *Server) personProfileOf(d *metadata.PersonDetails) *PersonProfile {
	return &PersonProfile{
		TmdbID: d.TMDBID, ImdbID: d.IMDbID, Name: d.Name, ProfileURL: s.imageURLOf(imagecache.Profile, profileSize, d.ProfilePath),
		KnownForDepartment: d.KnownForDepartment, Biography: d.Biography, Birthday: optStr(d.Birthday.String()),
		Deathday: optStr(d.Deathday.String()), PlaceOfBirth: d.PlaceOfBirth,
	}
}

func (s *Server) peopleRoute() {
	huma.Register(s.api, huma.Operation{
		OperationID: "discover-people", Method: http.MethodGet, Path: "/discover/people", Summary: "Search people on TMDB",
		Description: "By name, with the movies they are known for (flagged when in the library). Cached and rate limited like every TMDB call. " +
			"The filmography is at /discover/person/{tmdbId}.",
		Tags: []string{"Discover"},
	}, func(ctx context.Context, in *struct {
		Query string `query:"query" required:"true" minLength:"1" maxLength:"200"`
		Page  int    `query:"page" minimum:"1" maximum:"500" default:"1"`
	}) (*struct{ Body PeoplePage }, error) {
		p, err := s.d.Library.SearchPeople(ctx, in.Query, in.Page)
		if err != nil {
			return nil, s.problem(ctx, err, true)
		}
		out := PeoplePage{Page: p.Page, TotalPages: p.TotalPages, TotalResults: p.TotalResults, Results: make([]PersonResult, 0, len(p.Results))}
		for i := range p.Results {
			out.Results = append(out.Results, s.personResultOf(&p.Results[i]))
		}
		return &struct{ Body PeoplePage }{out}, nil
	})
}

// ----------------------------------------------------------------- command palette

// PaletteFilm is a library movie matching the palette query.
type PaletteFilm struct {
	ID            int64   `json:"id"`
	TmdbID        int64   `json:"tmdbId"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"originalTitle"`
	Year          int64   `json:"year"`
	PosterURL     *string `json:"posterUrl" doc:"w342"`
	DominantColor *string `json:"dominantColor"`
	State         string  `json:"state" enum:"available,missing,downloading,upgradable"`
	Monitored     bool    `json:"monitored"`
	Quality       *string `json:"quality"`
	QualityName   *string `json:"qualityName"`
	Field         string  `json:"field" enum:"title,originalTitle" doc:"Field that matched"`
	Match         string  `json:"match" enum:"prefix,word,substring,words,fuzzy" doc:"How it matched, best first"`
	Ranges        [][]int `json:"ranges" doc:"Matched characters of field as [start, end) pairs in Unicode code points, to highlight"`
}

// PaletteRemote are the TMDB results of the palette.
type PaletteRemote struct {
	Movies []DiscoverMovie `json:"movies" doc:"TMDB movies, flagged when in the library"`
	People []PersonResult  `json:"people"`
	Error  string          `json:"error,omitempty" doc:"Why TMDB results are missing (no key, timeout...); library hits are still returned"`
}

// PaletteResult is the answer of the palette search.
type PaletteResult struct {
	Query      string         `json:"query"`
	Films      []PaletteFilm  `json:"films" doc:"Library movies, best match first"`
	FilmsTotal int            `json:"filmsTotal" doc:"Library movies matching, beyond the limit"`
	Remote     *PaletteRemote `json:"remote" doc:"TMDB results; null unless remote=true and the query has 3 characters or more"`
	TookMicros int64          `json:"tookMicros" doc:"Time spent on the library hits"`
}

// paletteRemoteTimeout bounds the TMDB part of the palette search.
const paletteRemoteTimeout = 4 * time.Second

func (s *Server) paletteRemote(ctx context.Context, q string, limit int) *PaletteRemote {
	ctx, cancel := context.WithTimeout(ctx, paletteRemoteTimeout)
	defer cancel()
	out := &PaletteRemote{Movies: []DiscoverMovie{}, People: []PersonResult{}}
	var wg sync.WaitGroup
	var movies []library.LookupResult
	var people library.PeoplePage
	var errM, errP error
	wg.Go(func() { movies, errM = s.d.Library.Lookup(ctx, q, 0) })
	wg.Go(func() { people, errP = s.d.Library.SearchPeople(ctx, q, 1) })
	wg.Wait()
	for i := range movies[:min(len(movies), limit)] {
		r := &movies[i]
		out.Movies = append(out.Movies, s.discoverMovieOf(&r.MovieSummary, library.Flags{MovieID: r.MovieID, Monitored: r.Monitored, State: r.State, Excluded: r.Excluded}))
	}
	for i := range people.Results[:min(len(people.Results), limit)] {
		out.People = append(out.People, s.personResultOf(&people.Results[i]))
	}
	if err := cmp.Or(errM, errP); err != nil {
		out.Error = remoteMessage(err)
		s.log.Debug("palette: TMDB part failed", slog.String("request_id", RequestID(ctx)), slog.Any("error", err))
	}
	return out
}

// remoteMessage is the plain-language reason of a TMDB failure.
func remoteMessage(err error) string {
	var rl *metadata.RateLimitError
	switch {
	case errors.Is(err, library.ErrNoProvider):
		return "No TMDB key is configured yet."
	case errors.As(err, &rl):
		return "TMDB is rate limiting requests; try again in a moment."
	case errors.Is(err, context.DeadlineExceeded):
		return "TMDB did not answer in time."
	case errors.Is(err, metadata.ErrUnauthorized):
		return "TMDB rejected the configured key."
	}
	return "TMDB could not be reached."
}

func (s *Server) paletteRoute() {
	huma.Register(s.api, huma.Operation{
		OperationID: "search-everything", Method: http.MethodGet, Path: "/search", Summary: "Command palette search",
		Description: "Library films matched fuzzily from the in-memory index (prefix, word start, substring, all words, letters in order; " +
			"a 4-digit word selects the year), in about a millisecond. With remote=true and a query of 3 characters or more, " +
			"also TMDB movies and people (at most 4 s; a TMDB failure is reported in remote.error, never as an error status). " +
			"Call it without remote on each keystroke and with remote=true debounced. Actions are defined by the interface.",
		Tags: []string{"Search"},
	}, func(ctx context.Context, in *struct {
		Q      string `query:"q" maxLength:"200"`
		Limit  int    `query:"limit" minimum:"1" maximum:"50" default:"8" doc:"Per group"`
		Remote bool   `query:"remote" default:"false" doc:"Also search TMDB (q of 3 characters or more)"`
	}) (*struct{ Body PaletteResult }, error) {
		start := time.Now()
		hits, total, err := s.d.Library.SearchLibrary(ctx, in.Q, in.Limit)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := PaletteResult{Query: in.Q, Films: make([]PaletteFilm, 0, len(hits)), FilmsTotal: total}
		for i := range hits {
			h := &hits[i]
			m := h.Movie
			f := PaletteFilm{
				ID: m.ID, TmdbID: m.TmdbID, Title: m.Title, OriginalTitle: m.OriginalTitle, Year: m.Year,
				PosterURL: s.imageURL(imagecache.Poster, gridPosterSize, m.PosterPath), DominantColor: m.DominantColor,
				State: m.State, Monitored: m.Monitored, Quality: m.FileQuality, QualityName: qualityName(m.FileQuality),
				Field: h.Field, Match: h.Kind, Ranges: make([][]int, 0, len(h.Ranges)),
			}
			for _, r := range h.Ranges {
				f.Ranges = append(f.Ranges, []int{r[0], r[1]})
			}
			out.Films = append(out.Films, f)
		}
		out.TookMicros = time.Since(start).Microseconds()
		if in.Remote && len([]rune(in.Q)) >= 3 {
			out.Remote = s.paletteRemote(ctx, in.Q, in.Limit)
		}
		return &struct{ Body PaletteResult }{out}, nil
	})
}

// ----------------------------------------------------------------- history export

// historyExportPage is the number of rows read per query of an export, and
// historyExportPageTimeout the time allowed to write one page.
const (
	historyExportPage        = 500
	historyExportPageTimeout = 30 * time.Second
)

var historyCSVHeader = []string{
	"id", "createdAt", "event", "movieId", "movieTitle", "movieYear", "sourceTitle", "quality", "customScore",
	"downloadId", "chainId", "reason", "data",
}

func csvRow(r *HistoryRow) []string {
	opt := func(p *int64) string {
		if p == nil {
			return ""
		}
		return strconv.FormatInt(*p, 10)
	}
	title, year := "", ""
	if r.Movie != nil {
		title, year = r.Movie.Title, strconv.FormatInt(r.Movie.Year, 10)
	}
	return []string{
		strconv.FormatInt(r.ID, 10), r.CreatedAt.Format(time.RFC3339), r.Event, opt(r.MovieID), title, year,
		r.SourceTitle, deref(r.Quality), opt(r.CustomScore), opt(r.DownloadID), opt(r.ChainID), r.Reason, string(r.Data),
	}
}

// csvSafe neutralizes cells a spreadsheet would read as a formula.
func csvSafe(cells []string) []string {
	for i, c := range cells {
		if c != "" && (c[0] == '=' || c[0] == '+' || c[0] == '-' || c[0] == '@' || c[0] == '\t' || c[0] == '\r') {
			if _, err := strconv.ParseFloat(c, 64); err != nil {
				cells[i] = "'" + c
			}
		}
	}
	return cells
}

func (s *Server) historyExportRoute() {
	huma.Register(s.api, huma.Operation{
		OperationID: "export-history", Method: http.MethodGet, Path: "/history/export", Summary: "Export the history",
		Description: "Every entry matching the filters of GET /history, newest first, streamed as CSV (one header row; cells that a " +
			"spreadsheet would read as a formula are prefixed with ') or as a JSON array of history rows.",
		Tags: []string{"History"},
		Responses: map[string]*huma.Response{
			"200": {Description: "The export", Content: map[string]*huma.MediaType{
				"text/csv":         {Schema: &huma.Schema{Type: huma.TypeString}},
				"application/json": {Schema: &huma.Schema{Type: huma.TypeArray, Items: &huma.Schema{Ref: "#/components/schemas/HistoryRow"}}},
			}},
		},
	}, func(_ context.Context, in *struct {
		HistoryFilterParams
		Format string `query:"format" enum:"csv,json" default:"csv"`
	}) (*huma.StreamResponse, error) {
		if _, err := in.params(math.MaxInt64, 1); err != nil {
			return nil, err
		}
		filters := in.HistoryFilterParams
		format := in.Format
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			r, w := humago.Unwrap(hctx)
			s.streamHistory(r, w, &filters, format)
		}}, nil
	})
}

func (s *Server) streamHistory(r *http.Request, w http.ResponseWriter, f *HistoryFilterParams, format string) {
	ctx := r.Context()
	name := "gonema-history-" + time.Now().UTC().Format("20060102-150405")
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	if format == "json" {
		h.Set("Content-Type", "application/json")
		h.Set("Content-Disposition", `attachment; filename="`+name+`.json"`)
	} else {
		h.Set("Content-Type", "text/csv; charset=utf-8")
		h.Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
	}
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	cw := csv.NewWriter(w)
	enc := json.NewEncoder(w)
	if format == "json" {
		_, _ = w.Write([]byte("["))
	} else if err := cw.Write(historyCSVHeader); err != nil {
		return
	}
	before, first, n := int64(math.MaxInt64), true, 0
	for {
		// A large export outlives the server-wide write deadline: each page
		// gets its own.
		_ = rc.SetWriteDeadline(time.Now().Add(historyExportPageTimeout))
		p, err := f.params(before, historyExportPage)
		if err != nil {
			return // checked before streaming
		}
		rows, err := s.d.Store.Read().ListHistoryFiltered(ctx, p)
		if err == nil {
			var chains map[int64]int64
			if chains, err = s.downloadChains(ctx, rows); err == nil {
				for i := range rows {
					row := s.historyRowOf(&rows[i], chains)
					if format == "json" {
						if !first {
							_, _ = w.Write([]byte(","))
						}
						err = enc.Encode(&row)
					} else {
						err = cw.Write(csvSafe(csvRow(&row)))
					}
					if err != nil {
						break
					}
					first = false
					n++
				}
			}
		}
		if err != nil {
			// Headers are sent: the truncated body is the only signal left.
			s.log.Warn("history export interrupted", slog.String("request_id", RequestID(ctx)), slog.Int("rows", n), slog.Any("error", err))
			return
		}
		cw.Flush()
		_ = rc.Flush()
		if len(rows) < historyExportPage {
			break
		}
		before = rows[len(rows)-1].ID
	}
	if format == "json" {
		_, _ = w.Write([]byte("]\n"))
	}
	cw.Flush()
	s.log.Info("history exported", slog.String("request_id", RequestID(ctx)), slog.String("format", format), slog.Int("rows", n))
}

// ----------------------------------------------------------------- queue and searches

func (s *Server) lot3AcquireRoutes(a *acquire.Service) {
	s.previewRoute(a)
	for _, op := range []struct {
		path, id, summary string
		paused            bool
	}{
		{"/queue/pause", "pause-queue", "Pause every download", true},
		{"/queue/resume", "resume-queue", "Resume every paused download", false},
	} {
		huma.Register(s.api, huma.Operation{
			OperationID: op.id, Method: http.MethodPost, Path: op.path, Summary: op.summary,
			Description: "Pauses (or resumes) each of Gonema's active downloads in its client; the clients' other downloads are not touched. " +
				"The result says per client what it supports and what was done; a failing client does not stop the others.",
			Tags: []string{"Queue"},
		}, func(ctx context.Context, _ *struct{}) (*struct{ Body acquire.PauseAllResult }, error) {
			res, err := a.SetAllPaused(ctx, op.paused)
			if err != nil {
				return nil, s.problem(ctx, err, false)
			}
			return &struct{ Body acquire.PauseAllResult }{res}, nil
		})
	}
	huma.Register(s.api, huma.Operation{
		OperationID: "retry-search-indexer", Method: http.MethodPost, Path: "/searches/{searchId}/indexers/{indexerId}/retry",
		Summary: "Ask one indexer of an interactive search again",
		Description: "Typically after it failed or timed out. Returns at once (202) with the indexer pending; its results join the same search " +
			"and stream as search.batch, search.indexer_done or search.indexer_failed, then search.finished, on /events. " +
			"force=true first clears the indexer's pause after repeated failures. 409 while the indexer has not answered yet.",
		Tags: []string{"Search"}, DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *struct {
		SearchID  string `path:"searchId" minLength:"32" maxLength:"32" pattern:"^[0-9a-f]+$"`
		IndexerID int64  `path:"indexerId" minimum:"1"`
		Force     bool   `query:"force" default:"false" doc:"Try even if the indexer is paused after repeated failures"`
	}) (*struct {
		Status int
		Body   acquire.SearchSnapshot
	}, error) {
		snap, err := a.RetryIndexer(ctx, in.SearchID, in.IndexerID, in.Force)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct {
			Status int
			Body   acquire.SearchSnapshot
		}{http.StatusAccepted, snap}, nil
	})
}
