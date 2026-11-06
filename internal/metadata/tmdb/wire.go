package tmdb

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/metadata"
)

// Wire types mirror the JSON documented at https://developer.themoviedb.org/reference.
// null strings decode as "".

type wireSummary struct {
	ID               int     `json:"id"`
	Title            string  `json:"title"`
	Name             string  `json:"name"` // collection parts are documented with name/original_name
	OriginalTitle    string  `json:"original_title"`
	OriginalName     string  `json:"original_name"`
	OriginalLanguage string  `json:"original_language"`
	ReleaseDate      string  `json:"release_date"`
	Overview         string  `json:"overview"`
	PosterPath       string  `json:"poster_path"`
	BackdropPath     string  `json:"backdrop_path"`
	Popularity       float64 `json:"popularity"`
	VoteAverage      float64 `json:"vote_average"`
	VoteCount        int     `json:"vote_count"`
	Adult            bool    `json:"adult"`
	GenreIDs         []int   `json:"genre_ids"`
	MediaType        string  `json:"media_type"`
}

type wirePage struct {
	Page         int           `json:"page"`
	TotalPages   int           `json:"total_pages"`
	TotalResults int           `json:"total_results"`
	Results      []wireSummary `json:"results"`
}

type wireMovie struct {
	wireSummary
	IMDbID              string `json:"imdb_id"`
	Runtime             int    `json:"runtime"`
	Status              string `json:"status"`
	Tagline             string `json:"tagline"`
	Homepage            string `json:"homepage"`
	BelongsToCollection *struct {
		ID           int    `json:"id"`
		Name         string `json:"name"`
		PosterPath   string `json:"poster_path"`
		BackdropPath string `json:"backdrop_path"`
	} `json:"belongs_to_collection"`
	Genres []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"genres"`
	ProductionCompanies []struct {
		ID            int    `json:"id"`
		Name          string `json:"name"`
		LogoPath      string `json:"logo_path"`
		OriginCountry string `json:"origin_country"`
	} `json:"production_companies"`

	// append_to_response
	Credits *struct {
		Cast []wireCredit `json:"cast"`
		Crew []wireCredit `json:"crew"`
	} `json:"credits"`
	Videos *struct {
		Results []wireVideo `json:"results"`
	} `json:"videos"`
	AlternativeTitles *struct {
		Titles []struct {
			Country string `json:"iso_3166_1"`
			Title   string `json:"title"`
			Type    string `json:"type"`
		} `json:"titles"`
	} `json:"alternative_titles"`
	ReleaseDates *struct {
		Results []struct {
			Country      string `json:"iso_3166_1"`
			ReleaseDates []struct {
				Certification string `json:"certification"`
				Language      string `json:"iso_639_1"`
				Note          string `json:"note"`
				ReleaseDate   string `json:"release_date"`
				Type          int    `json:"type"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
	ExternalIDs *struct {
		IMDbID string `json:"imdb_id"`
	} `json:"external_ids"`
	Keywords *struct {
		Keywords []struct {
			Name string `json:"name"`
		} `json:"keywords"`
	} `json:"keywords"`
}

type wireCredit struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	ProfilePath        string `json:"profile_path"`
	KnownForDepartment string `json:"known_for_department"`
	Character          string `json:"character"`
	Order              int    `json:"order"`
	Department         string `json:"department"`
	Job                string `json:"job"`
}

type wireVideo struct {
	Language    string `json:"iso_639_1"`
	Region      string `json:"iso_3166_1"`
	Name        string `json:"name"`
	Key         string `json:"key"`
	Site        string `json:"site"`
	Size        int    `json:"size"`
	Type        string `json:"type"`
	Official    bool   `json:"official"`
	PublishedAt string `json:"published_at"`
}

type wireCollection struct {
	ID           int           `json:"id"`
	Name         string        `json:"name"`
	Overview     string        `json:"overview"`
	PosterPath   string        `json:"poster_path"`
	BackdropPath string        `json:"backdrop_path"`
	Parts        []wireSummary `json:"parts"`
}

type wireFilmCredit struct {
	wireSummary
	Character  string `json:"character"`
	Department string `json:"department"`
	Job        string `json:"job"`
}

type wirePersonCredits struct {
	ID   int              `json:"id"`
	Cast []wireFilmCredit `json:"cast"`
	Crew []wireFilmCredit `json:"crew"`
}

type wirePerson struct {
	ID                 int           `json:"id"`
	IMDbID             string        `json:"imdb_id"`
	Name               string        `json:"name"`
	ProfilePath        string        `json:"profile_path"`
	KnownForDepartment string        `json:"known_for_department"`
	Biography          string        `json:"biography"`
	Birthday           string        `json:"birthday"`
	Deathday           string        `json:"deathday"`
	PlaceOfBirth       string        `json:"place_of_birth"`
	Popularity         float64       `json:"popularity"`
	Adult              bool          `json:"adult"`
	KnownFor           []wireSummary `json:"known_for"`
}

type wirePeoplePage struct {
	Page         int          `json:"page"`
	TotalPages   int          `json:"total_pages"`
	TotalResults int          `json:"total_results"`
	Results      []wirePerson `json:"results"`
}

type wireFind struct {
	MovieResults []wireSummary `json:"movie_results"`
}

type wireConfiguration struct {
	Images struct {
		BaseURL       string   `json:"base_url"`
		SecureBaseURL string   `json:"secure_base_url"`
		BackdropSizes []string `json:"backdrop_sizes"`
		PosterSizes   []string `json:"poster_sizes"`
		ProfileSizes  []string `json:"profile_sizes"`
	} `json:"images"`
}

// date parses a TMDB date leniently: an invalid date is unknown.
func date(s string) metadata.Date {
	d, err := metadata.ParseDate(s)
	if err != nil {
		return metadata.Date{}
	}
	return d
}

func (w *wireSummary) toSummary() metadata.MovieSummary {
	d := date(w.ReleaseDate)
	s := metadata.MovieSummary{
		TMDBID:           w.ID,
		Title:            cmp.Or(w.Title, w.Name),
		OriginalTitle:    cmp.Or(w.OriginalTitle, w.OriginalName),
		OriginalLanguage: w.OriginalLanguage,
		Year:             d.Year,
		ReleaseDate:      d,
		Overview:         w.Overview,
		PosterPath:       w.PosterPath,
		BackdropPath:     w.BackdropPath,
		Popularity:       w.Popularity,
		Adult:            w.Adult,
		GenreIDs:         w.GenreIDs,
	}
	if w.VoteCount > 0 {
		s.Rating = &metadata.Rating{Value: w.VoteAverage, Votes: w.VoteCount}
	}
	return s
}

func (p *wirePage) toPage() metadata.Page[metadata.MovieSummary] {
	out := metadata.Page[metadata.MovieSummary]{
		Page: p.Page, TotalPages: p.TotalPages, TotalResults: p.TotalResults,
		Results: make([]metadata.MovieSummary, 0, len(p.Results)),
	}
	for i := range p.Results {
		if p.Results[i].MediaType != "" && p.Results[i].MediaType != "movie" {
			continue
		}
		out.Results = append(out.Results, p.Results[i].toSummary())
	}
	return out
}

// toMovie converts a details response; region picks release dates, now derives the status.
func (w *wireMovie) toMovie(region string, now time.Time) *metadata.Movie {
	s := w.toSummary()
	m := &metadata.Movie{
		TMDBID:           s.TMDBID,
		IMDbID:           w.IMDbID,
		Title:            s.Title,
		OriginalTitle:    s.OriginalTitle,
		OriginalLanguage: s.OriginalLanguage,
		Year:             s.Year,
		ReleaseDate:      s.ReleaseDate,
		Overview:         s.Overview,
		RuntimeMin:       w.Runtime,
		Adult:            w.Adult,
		Popularity:       w.Popularity,
		ProviderStatus:   w.Status,
		PosterPath:       w.PosterPath,
		BackdropPath:     w.BackdropPath,
		Extra:            metadata.Extra{Tagline: w.Tagline, Homepage: w.Homepage},
	}
	if m.IMDbID == "" && w.ExternalIDs != nil {
		m.IMDbID = w.ExternalIDs.IMDbID
	}
	if s.Rating != nil {
		m.Ratings.TMDB = s.Rating
	}
	for _, g := range w.Genres {
		m.Genres = append(m.Genres, g.Name)
	}
	for _, c := range w.ProductionCompanies {
		if name := strings.TrimSpace(c.Name); name != "" {
			m.Extra.Companies = append(m.Extra.Companies, metadata.Company{
				TMDBID: c.ID, Name: name, LogoPath: c.LogoPath, Country: strings.ToUpper(c.OriginCountry),
			})
		}
	}
	if c := w.BelongsToCollection; c != nil && c.ID != 0 {
		m.Collection = &metadata.CollectionRef{TMDBID: c.ID, Name: c.Name, PosterPath: c.PosterPath, BackdropPath: c.BackdropPath}
	}
	if w.AlternativeTitles != nil {
		seen := make(map[string]bool)
		for _, t := range w.AlternativeTitles.Titles {
			title := strings.TrimSpace(t.Title)
			k := t.Country + "\x00" + title
			if title == "" || seen[k] {
				continue
			}
			seen[k] = true
			m.AlternateTitles = append(m.AlternateTitles, metadata.AlternateTitle{Title: title, Country: t.Country, Type: t.Type})
		}
	}
	if w.ReleaseDates != nil {
		for _, r := range w.ReleaseDates.Results {
			rd := metadata.ReleaseDates{Region: strings.ToUpper(r.Country)}
			for _, d := range r.ReleaseDates {
				rd.Dates = append(rd.Dates, metadata.ReleaseDate{
					Type: metadata.ReleaseType(d.Type), Date: date(d.ReleaseDate),
					Certification: strings.TrimSpace(d.Certification), Language: d.Language, Note: d.Note,
				})
			}
			m.ReleaseDates = append(m.ReleaseDates, rd)
		}
		metadata.SortReleaseDates(m.ReleaseDates)
	}
	m.Releases = metadata.SelectReleases(m.ReleaseDates, region)
	if m.Year == 0 {
		m.Year = m.Releases.Cinema.Date.Year
	}
	m.Status, m.StatusReason = metadata.DeriveStatus(m.Releases, m.ReleaseDate, now)

	if w.Credits != nil {
		m.Extra.Cast = credits(w.Credits.Cast)
		slices.SortStableFunc(m.Extra.Cast, func(a, b metadata.Credit) int { return a.Order - b.Order })
		m.Extra.Crew = credits(w.Credits.Crew)
	}
	if w.Videos != nil {
		m.Extra.Videos = videos(w.Videos.Results)
	}
	if w.Keywords != nil {
		for _, k := range w.Keywords.Keywords {
			m.Extra.Keywords = append(m.Extra.Keywords, k.Name)
		}
	}
	return m
}

func credits(in []wireCredit) []metadata.Credit {
	out := make([]metadata.Credit, 0, len(in))
	for i := range in {
		c := &in[i]
		out = append(out, metadata.Credit{
			Person:    metadata.Person{TMDBID: c.ID, Name: c.Name, ProfilePath: c.ProfilePath, KnownForDepartment: c.KnownForDepartment},
			Character: c.Character, Job: c.Job, Department: c.Department, Order: c.Order,
		})
	}
	return out
}

// videos keeps YouTube trailers and teasers: trailers first, official first,
// then higher resolution, then most recent, then key (deterministic).
func videos(in []wireVideo) []metadata.Video {
	var out []metadata.Video
	for i := range in {
		v := &in[i]
		if !strings.EqualFold(v.Site, "YouTube") || v.Key == "" {
			continue
		}
		var t metadata.VideoType
		switch strings.ToLower(v.Type) {
		case "trailer":
			t = metadata.VideoTrailer
		case "teaser":
			t = metadata.VideoTeaser
		default:
			continue
		}
		out = append(out, metadata.Video{
			Name: v.Name, Key: v.Key, Type: t, Official: v.Official,
			Language: v.Language, Region: v.Region, Size: v.Size, PublishedAt: v.PublishedAt,
		})
	}
	slices.SortStableFunc(out, func(a, b metadata.Video) int {
		if a.Type != b.Type {
			if a.Type == metadata.VideoTrailer {
				return -1
			}
			return 1
		}
		if a.Official != b.Official {
			if a.Official {
				return -1
			}
			return 1
		}
		if a.Size != b.Size {
			return b.Size - a.Size
		}
		if c := strings.Compare(b.PublishedAt, a.PublishedAt); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
	return out
}

func (w *wireCollection) toCollection() *metadata.Collection {
	c := &metadata.Collection{
		TMDBID: w.ID, Name: w.Name, Overview: w.Overview, PosterPath: w.PosterPath, BackdropPath: w.BackdropPath,
		Parts: make([]metadata.MovieSummary, 0, len(w.Parts)),
	}
	for i := range w.Parts {
		c.Parts = append(c.Parts, w.Parts[i].toSummary())
	}
	slices.SortStableFunc(c.Parts, func(a, b metadata.MovieSummary) int {
		if c := a.ReleaseDate.Compare(b.ReleaseDate); c != 0 {
			return c
		}
		return a.TMDBID - b.TMDBID
	})
	return c
}

func (w *wirePersonCredits) toFilmography(personID int) *metadata.Filmography {
	f := &metadata.Filmography{PersonID: personID, Cast: filmCredits(w.Cast), Crew: filmCredits(w.Crew)}
	return f
}

// filmCredits converts and orders credits: undated (upcoming) first, then newest first.
func filmCredits(in []wireFilmCredit) []metadata.FilmCredit {
	out := make([]metadata.FilmCredit, 0, len(in))
	for i := range in {
		c := &in[i]
		out = append(out, metadata.FilmCredit{Movie: c.toSummary(), Character: c.Character, Job: c.Job, Department: c.Department})
	}
	slices.SortStableFunc(out, func(a, b metadata.FilmCredit) int {
		da, db := a.Movie.ReleaseDate, b.Movie.ReleaseDate
		switch {
		case da.IsZero() != db.IsZero():
			if da.IsZero() {
				return -1
			}
			return 1
		case da != db:
			return db.Compare(da)
		}
		return a.Movie.TMDBID - b.Movie.TMDBID
	})
	return out
}

func (p *wirePeoplePage) toPage() metadata.Page[metadata.PersonSummary] {
	out := metadata.Page[metadata.PersonSummary]{
		Page: p.Page, TotalPages: p.TotalPages, TotalResults: p.TotalResults,
		Results: make([]metadata.PersonSummary, 0, len(p.Results)),
	}
	for i := range p.Results {
		w := &p.Results[i]
		ps := metadata.PersonSummary{
			TMDBID: w.ID, Name: w.Name, ProfilePath: w.ProfilePath, KnownForDepartment: w.KnownForDepartment,
			Popularity: w.Popularity, Adult: w.Adult,
		}
		for j := range w.KnownFor {
			if k := &w.KnownFor[j]; k.MediaType == "" || k.MediaType == "movie" {
				ps.KnownFor = append(ps.KnownFor, k.toSummary())
			}
		}
		out.Results = append(out.Results, ps)
	}
	return out
}

func (w *wirePerson) toDetails() *metadata.PersonDetails {
	return &metadata.PersonDetails{
		TMDBID: w.ID, IMDbID: w.IMDbID, Name: w.Name, ProfilePath: w.ProfilePath,
		KnownForDepartment: w.KnownForDepartment, Biography: w.Biography, Birthday: date(w.Birthday),
		Deathday: date(w.Deathday), PlaceOfBirth: w.PlaceOfBirth, Adult: w.Adult,
	}
}
