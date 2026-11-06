package api

import (
	"encoding/json"
	"time"

	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// API types: what the API exposes, decoupled from the database rows. None of
// them carries a secret.

func ts(ms int64) time.Time { return store.FromMillis(ms) }

func tsPtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := store.FromMillis(*ms)
	return &t
}

func raw(s string) json.RawMessage {
	if s == "" {
		return json.RawMessage("null")
	}
	return json.RawMessage(s)
}

func genresOf(s string) []string {
	var g []string
	if err := json.Unmarshal([]byte(s), &g); err != nil || g == nil {
		return []string{}
	}
	return g
}

// AlternateTitle is another title of a movie.
type AlternateTitle struct {
	Title   string `json:"title"`
	Country string `json:"country,omitempty"`
	Source  string `json:"source" enum:"tmdb,user"`
}

// Movie is a library movie with its details.
type Movie struct {
	ID                  int64            `json:"id"`
	TmdbID              int64            `json:"tmdbId"`
	ImdbID              *string          `json:"imdbId"`
	Title               string           `json:"title"`
	OriginalTitle       string           `json:"originalTitle"`
	SortTitle           string           `json:"sortTitle"`
	OriginalLanguage    string           `json:"originalLanguage"`
	Year                int64            `json:"year" doc:"0 when unknown"`
	Overview            string           `json:"overview"`
	RuntimeMin          int64            `json:"runtimeMin"`
	Status              string           `json:"status" enum:"announced,in_cinemas,released,deleted"`
	State               string           `json:"state" doc:"Library state derived from files and downloads: missing, downloading, available, upgradable..."`
	ReleaseCinema       *string          `json:"releaseCinema" doc:"YYYY-MM-DD"`
	ReleaseDigital      *string          `json:"releaseDigital"`
	ReleasePhysical     *string          `json:"releasePhysical"`
	Certification       string           `json:"certification"`
	Genres              []string         `json:"genres"`
	Ratings             json.RawMessage  `json:"ratings" doc:"{tmdb:{value,votes}, imdb:{value,votes}}"`
	PosterPath          *string          `json:"posterPath"`
	BackdropPath        *string          `json:"backdropPath"`
	DominantColor       *string          `json:"dominantColor" doc:"#rrggbb from the poster"`
	Extra               json.RawMessage  `json:"extra" doc:"Display-only data: cast, crew, videos, keywords, statusReason"`
	CollectionID        *int64           `json:"collectionId"`
	Monitored           bool             `json:"monitored"`
	QualityProfileID    int64            `json:"qualityProfileId"`
	MinimumAvailability string           `json:"minimumAvailability" enum:"announced,in_cinemas,released"`
	RootFolderID        int64            `json:"rootFolderId"`
	Path                string           `json:"path" doc:"Movie folder, relative to the root folder"`
	AddedAt             time.Time        `json:"addedAt"`
	MetadataRefreshedAt *time.Time       `json:"metadataRefreshedAt"`
	LastSearchedAt      *time.Time       `json:"lastSearchedAt"`
	Tags                []string         `json:"tags"`
	AlternateTitles     []AlternateTitle `json:"alternateTitles"`
}

func movieOf(m *db.Movie) Movie {
	return Movie{
		ID: m.ID, TmdbID: m.TmdbID, ImdbID: m.ImdbID, Title: m.Title, OriginalTitle: m.OriginalTitle,
		SortTitle: m.SortTitle, OriginalLanguage: m.OriginalLanguage, Year: m.Year, Overview: m.Overview,
		RuntimeMin: m.RuntimeMin, Status: m.Status, ReleaseCinema: m.ReleaseCinema,
		ReleaseDigital: m.ReleaseDigital, ReleasePhysical: m.ReleasePhysical, Certification: m.Certification,
		Genres: genresOf(m.Genres), Ratings: raw(m.Ratings), PosterPath: m.PosterPath,
		BackdropPath: m.BackdropPath, DominantColor: m.DominantColor, Extra: raw(m.Extra),
		CollectionID: m.CollectionID, Monitored: m.Monitored, QualityProfileID: m.QualityProfileID,
		MinimumAvailability: m.MinimumAvailability, RootFolderID: m.RootFolderID, Path: m.Path,
		AddedAt: ts(m.AddedAt), MetadataRefreshedAt: tsPtr(m.MetadataRefreshedAt),
		LastSearchedAt: tsPtr(m.LastSearchedAt), Tags: []string{}, AlternateTitles: []AlternateTitle{},
	}
}

func movieDetailsOf(d *library.MovieDetails) Movie {
	m := movieOf(&d.Movie)
	m.State = d.State
	m.Tags = d.Tags
	for _, a := range d.AlternateTitles {
		m.AlternateTitles = append(m.AlternateTitles, AlternateTitle{Title: a.Title, Country: a.Country, Source: a.Source})
	}
	return m
}

// LookupResult is a TMDB search result.
type LookupResult struct {
	metadata.MovieSummary
	InLibrary bool  `json:"inLibrary"`
	MovieID   int64 `json:"movieId,omitempty" doc:"Library movie id when inLibrary"`
}

// Collection is a saga.
type Collection struct {
	ID                  int64                   `json:"id"`
	TmdbID              int64                   `json:"tmdbId"`
	Name                string                  `json:"name"`
	Overview            string                  `json:"overview"`
	PosterPath          *string                 `json:"posterPath"`
	BackdropPath        *string                 `json:"backdropPath"`
	Monitored           bool                    `json:"monitored" doc:"Add the collection's movies automatically"`
	QualityProfileID    *int64                  `json:"qualityProfileId"`
	RootFolderID        *int64                  `json:"rootFolderId"`
	MinimumAvailability string                  `json:"minimumAvailability"`
	RefreshedAt         *time.Time              `json:"refreshedAt"`
	Movies              []Movie                 `json:"movies" doc:"Movies of the collection in the library"`
	Parts               []metadata.MovieSummary `json:"parts" doc:"Every movie of the collection according to TMDB; null when TMDB is unavailable"`
	PartsError          string                  `json:"partsError,omitempty"`
}

func collectionOf(c *db.Collection) Collection {
	return Collection{
		ID: c.ID, TmdbID: c.TmdbID, Name: c.Name, Overview: c.Overview, PosterPath: c.PosterPath,
		BackdropPath: c.BackdropPath, Monitored: c.Monitored, QualityProfileID: c.QualityProfileID,
		RootFolderID: c.RootFolderID, MinimumAvailability: c.MinimumAvailability,
		RefreshedAt: tsPtr(c.RefreshedAt), Movies: []Movie{},
	}
}

// RootFolder is a folder movies are stored in.
type RootFolder struct {
	ID         int64      `json:"id"`
	Path       string     `json:"path"`
	FreeSpace  *int64     `json:"freeSpace" doc:"Bytes available; null when unknown"`
	TotalSpace *int64     `json:"totalSpace"`
	CheckedAt  *time.Time `json:"checkedAt"`
	Accessible bool       `json:"accessible"`
	Error      string     `json:"error,omitempty" doc:"Why the folder is not accessible"`
}

func rootFolderOf(rf *library.RootFolder) RootFolder {
	return RootFolder{
		ID: rf.ID, Path: rf.Path, FreeSpace: rf.FreeSpace, TotalSpace: rf.TotalSpace,
		CheckedAt: tsPtr(rf.CheckedAt), Accessible: rf.Accessible, Error: rf.Error,
	}
}

// QualityProfileItem is one quality of a profile; members of a group share
// position and group name.
type QualityProfileItem struct {
	Quality   string  `json:"quality"`
	Position  int64   `json:"position"`
	GroupName *string `json:"groupName"`
	Allowed   bool    `json:"allowed"`
}

// QualityProfile is a quality profile.
type QualityProfile struct {
	ID             int64                `json:"id"`
	Name           string               `json:"name"`
	UpgradeAllowed bool                 `json:"upgradeAllowed"`
	CutoffQuality  string               `json:"cutoffQuality"`
	MinScore       int64                `json:"minScore"`
	CutoffScore    int64                `json:"cutoffScore"`
	MinUpgradeStep int64                `json:"minUpgradeStep"`
	Languages      json.RawMessage      `json:"languages"`
	Items          []QualityProfileItem `json:"items"`
}

func qualityProfileOf(p *library.QualityProfile) QualityProfile {
	out := QualityProfile{
		ID: p.ID, Name: p.Name, UpgradeAllowed: p.UpgradeAllowed, CutoffQuality: p.CutoffQuality,
		MinScore: p.MinScore, CutoffScore: p.CutoffScore, MinUpgradeStep: p.MinUpgradeStep,
		Languages: raw(p.Languages), Items: make([]QualityProfileItem, 0, len(p.Items)),
	}
	for _, it := range p.Items {
		out.Items = append(out.Items, QualityProfileItem{Quality: it.Quality, Position: it.Position, GroupName: it.GroupName, Allowed: it.Allowed})
	}
	return out
}

// Job is a background job.
type Job struct {
	ID          int64      `json:"id"`
	Kind        string     `json:"kind"`
	Payload     any        `json:"payload"`
	Status      string     `json:"status" enum:"pending,running,done,failed,cancelled"`
	Priority    int64      `json:"priority"`
	RunAt       time.Time  `json:"runAt"`
	Attempts    int64      `json:"attempts"`
	MaxAttempts int64      `json:"maxAttempts"`
	LastError   *string    `json:"lastError"`
	StartedAt   *time.Time `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
}

func jobOf(j *db.Job) Job {
	return Job{
		ID: j.ID, Kind: j.Kind, Payload: raw(j.Payload), Status: j.Status, Priority: j.Priority,
		RunAt: ts(j.RunAt), Attempts: j.Attempts, MaxAttempts: j.MaxAttempts, LastError: j.LastError,
		StartedAt: tsPtr(j.StartedAt), FinishedAt: tsPtr(j.FinishedAt), CreatedAt: ts(j.CreatedAt),
	}
}

// HistoryEntry is one history row.
type HistoryEntry struct {
	ID          int64           `json:"id"`
	MovieID     *int64          `json:"movieId"`
	Event       string          `json:"event"`
	SourceTitle string          `json:"sourceTitle"`
	Quality     *string         `json:"quality"`
	CustomScore *int64          `json:"customScore"`
	DownloadID  *int64          `json:"downloadId"`
	Data        json.RawMessage `json:"data" doc:"Reasons and details of the event"`
	CreatedAt   time.Time       `json:"createdAt"`
}

func historyOf(h *db.History) HistoryEntry {
	return HistoryEntry{
		ID: h.ID, MovieID: h.MovieID, Event: h.Event, SourceTitle: h.SourceTitle, Quality: h.Quality,
		CustomScore: h.CustomScore, DownloadID: h.DownloadID, Data: raw(h.Data), CreatedAt: ts(h.CreatedAt),
	}
}
