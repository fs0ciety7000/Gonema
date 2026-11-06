package metadata

import (
	"context"
)

// Status is the release stage of a movie (movies.status).
type Status string

// Statuses. StatusDeleted is set by the library when the provider no longer
// knows the movie (ErrNotFound on refresh); providers never return it.
const (
	StatusAnnounced Status = "announced"
	StatusInCinemas Status = "in_cinemas"
	StatusReleased  Status = "released"
	StatusDeleted   Status = "deleted"
)

// Movie is the full metadata of one movie.
type Movie struct {
	TMDBID           int     `json:"tmdbId"`
	IMDbID           string  `json:"imdbId,omitempty"`
	Title            string  `json:"title"`
	OriginalTitle    string  `json:"originalTitle,omitempty"`
	OriginalLanguage string  `json:"originalLanguage,omitempty"` // ISO 639-1
	Year             int     `json:"year,omitempty"`             // year of ReleaseDate
	ReleaseDate      Date    `json:"releaseDate,omitzero"`       // primary release date
	Overview         string  `json:"overview,omitempty"`
	RuntimeMin       int     `json:"runtimeMin,omitempty"`
	Adult            bool    `json:"adult,omitempty"`
	Popularity       float64 `json:"popularity,omitempty"`

	// ProviderStatus is the raw provider status ("Rumored", "Planned",
	// "In Production", "Post Production", "Released", "Canceled").
	ProviderStatus string `json:"providerStatus,omitempty"`
	// Status and StatusReason are derived by DeriveStatus at fetch time.
	Status       Status `json:"status"`
	StatusReason string `json:"statusReason,omitempty"`

	// Releases holds the dates chosen for the library (release_cinema,
	// release_digital, release_physical, certification); see SelectReleases.
	Releases ReleaseSelection `json:"releases"`
	// ReleaseDates lists the dates of every region the provider knows, by region code.
	ReleaseDates []ReleaseDates `json:"releaseDates,omitempty"`

	Genres  []string `json:"genres,omitempty"`
	Ratings Ratings  `json:"ratings"`

	PosterPath   string `json:"posterPath,omitempty"`   // provider path, e.g. "/abc.jpg"
	BackdropPath string `json:"backdropPath,omitempty"` // provider path

	Collection      *CollectionRef   `json:"collection,omitempty"`
	AlternateTitles []AlternateTitle `json:"alternateTitles,omitempty"`

	// Extra is display-only data, stored as movies.extra JSON.
	Extra Extra `json:"extra"`
}

// Trailer returns the preferred trailer, if any (Extra.Videos is ordered by preference).
func (m *Movie) Trailer() (Video, bool) {
	for _, v := range m.Extra.Videos {
		if v.Type == VideoTrailer {
			return v, true
		}
	}
	return Video{}, false
}

// Extra is the display-only part of a movie (movies.extra).
type Extra struct {
	Tagline  string   `json:"tagline,omitempty"`
	Homepage string   `json:"homepage,omitempty"`
	Cast     []Credit `json:"cast,omitempty"` // by billing order
	Crew     []Credit `json:"crew,omitempty"`
	Videos   []Video  `json:"videos,omitempty"` // YouTube trailers and teasers, preferred first
	Keywords []string `json:"keywords,omitempty"`
	// Companies are the production companies (studios), in provider order.
	Companies []Company `json:"companies,omitempty"`
}

// Company is a production company.
type Company struct {
	TMDBID   int    `json:"tmdbId"`
	Name     string `json:"name"`
	LogoPath string `json:"logoPath,omitempty"` // provider path
	Country  string `json:"country,omitempty"`  // ISO 3166-1
}

// MovieSummary is a movie as returned by searches, lists and collections.
type MovieSummary struct {
	TMDBID           int     `json:"tmdbId"`
	Title            string  `json:"title"`
	OriginalTitle    string  `json:"originalTitle,omitempty"`
	OriginalLanguage string  `json:"originalLanguage,omitempty"`
	Year             int     `json:"year,omitempty"`
	ReleaseDate      Date    `json:"releaseDate,omitzero"`
	Overview         string  `json:"overview,omitempty"`
	PosterPath       string  `json:"posterPath,omitempty"`
	BackdropPath     string  `json:"backdropPath,omitempty"`
	Popularity       float64 `json:"popularity,omitempty"`
	Rating           *Rating `json:"rating,omitempty"` // provider rating; nil when no votes
	Adult            bool    `json:"adult,omitempty"`
	GenreIDs         []int   `json:"genreIds,omitempty"`
}

// CollectionRef is the collection a movie belongs to.
type CollectionRef struct {
	TMDBID       int    `json:"tmdbId"`
	Name         string `json:"name"`
	PosterPath   string `json:"posterPath,omitempty"`
	BackdropPath string `json:"backdropPath,omitempty"`
}

// Collection is a saga (collections table) and its movies.
type Collection struct {
	TMDBID       int            `json:"tmdbId"`
	Name         string         `json:"name"`
	Overview     string         `json:"overview,omitempty"`
	PosterPath   string         `json:"posterPath,omitempty"`
	BackdropPath string         `json:"backdropPath,omitempty"`
	Parts        []MovieSummary `json:"parts"` // by release date, undated last
}

// AlternateTitle is another title of a movie (alternate_titles).
type AlternateTitle struct {
	Title   string `json:"title"`
	Country string `json:"country,omitempty"` // ISO 3166-1
	Type    string `json:"type,omitempty"`    // provider note: "romanization", "working title"...
}

// Person is a cast or crew member.
type Person struct {
	TMDBID             int    `json:"tmdbId"`
	Name               string `json:"name"`
	ProfilePath        string `json:"profilePath,omitempty"`
	KnownForDepartment string `json:"knownForDepartment,omitempty"`
}

// Credit is a person's part in a movie: Character for cast, Job and Department
// for crew.
type Credit struct {
	Person     Person `json:"person"`
	Character  string `json:"character,omitempty"`
	Job        string `json:"job,omitempty"`
	Department string `json:"department,omitempty"`
	Order      int    `json:"order,omitempty"` // cast billing order
}

// FilmCredit is one movie in a person's filmography.
type FilmCredit struct {
	Movie      MovieSummary `json:"movie"`
	Character  string       `json:"character,omitempty"`
	Job        string       `json:"job,omitempty"`
	Department string       `json:"department,omitempty"`
}

// Filmography is a person's movie credits.
type Filmography struct {
	PersonID int          `json:"personId"`
	Cast     []FilmCredit `json:"cast"` // by release date, newest first, undated first (upcoming)
	Crew     []FilmCredit `json:"crew"`
}

// VideoType is the kind of a video.
type VideoType string

// Video types kept by providers.
const (
	VideoTrailer VideoType = "trailer"
	VideoTeaser  VideoType = "teaser"
)

// Video is a trailer or teaser hosted on YouTube.
type Video struct {
	Name        string    `json:"name"`
	Key         string    `json:"key"` // YouTube video id
	Type        VideoType `json:"type"`
	Official    bool      `json:"official,omitempty"`
	Language    string    `json:"language,omitempty"` // ISO 639-1
	Region      string    `json:"region,omitempty"`   // ISO 3166-1
	Size        int       `json:"size,omitempty"`     // vertical resolution
	PublishedAt string    `json:"publishedAt,omitempty"`
}

// URL returns the YouTube watch URL of v.
func (v Video) URL() string { return "https://www.youtube.com/watch?v=" + v.Key }

// Rating is an average score out of 10 and its number of votes.
type Rating struct {
	Value float64 `json:"value"`
	Votes int     `json:"votes"`
}

// Ratings matches movies.ratings: {tmdb:{value,votes}, imdb:{value,votes}}.
//
// IMDb ratings are not published by TMDB: IMDb stays nil unless another source
// fills it (not in this phase).
type Ratings struct {
	TMDB *Rating `json:"tmdb,omitempty"`
	IMDb *Rating `json:"imdb,omitempty"`
}

// TrendingWindow is the period of a trending list.
type TrendingWindow string

// Trending windows.
const (
	TrendingDay  TrendingWindow = "day"
	TrendingWeek TrendingWindow = "week"
)

// Page is one page of a paginated result. Page numbers start at 1.
type Page[T any] struct {
	Page         int `json:"page"`
	TotalPages   int `json:"totalPages"`
	TotalResults int `json:"totalResults"`
	Results      []T `json:"results"`
}

// Provider is a source of movie metadata. Implementations are safe for
// concurrent use. page is 1-based; 0 means 1.
type Provider interface {
	// Search looks movies up by title; year (0 = any) narrows the search.
	Search(ctx context.Context, query string, year, page int) (Page[MovieSummary], error)
	// Movie returns the full metadata of a movie.
	Movie(ctx context.Context, tmdbID int) (*Movie, error)
	// FindByIMDb resolves an IMDb id ("tt0137523"); ErrNotFound if unknown.
	FindByIMDb(ctx context.Context, imdbID string) (MovieSummary, error)
	// Collection returns a collection and its movies.
	Collection(ctx context.Context, tmdbID int) (*Collection, error)
	// PersonFilmography returns the movies a person played in or worked on.
	PersonFilmography(ctx context.Context, personID int) (*Filmography, error)

	Popular(ctx context.Context, page int) (Page[MovieSummary], error)
	Trending(ctx context.Context, window TrendingWindow, page int) (Page[MovieSummary], error)
	Upcoming(ctx context.Context, page int) (Page[MovieSummary], error)
	NowPlaying(ctx context.Context, page int) (Page[MovieSummary], error)
}
