package tmdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/fs0ciety7000/gonema/internal/metadata"
)

// detailsAppend is the append_to_response of Movie.
const detailsAppend = "credits,videos,alternative_titles,release_dates,external_ids,keywords"

// maxPage is the highest page TMDB serves (error code 22 beyond).
const maxPage = 500

// ErrInvalidArgument is returned for arguments rejected before any request.
var ErrInvalidArgument = errors.New("tmdb: invalid argument")

func (c *Client) langQuery() url.Values {
	return url.Values{"language": {c.lang}}
}

func pageQuery(q url.Values, page int) (url.Values, error) {
	if page <= 0 {
		page = 1
	}
	if page > maxPage {
		return nil, fmt.Errorf("%w: page %d (max %d)", ErrInvalidArgument, page, maxPage)
	}
	q.Set("page", strconv.Itoa(page))
	return q, nil
}

func validID(kind string, id int) error {
	if id <= 0 {
		return fmt.Errorf("%w: %s id %d", ErrInvalidArgument, kind, id)
	}
	return nil
}

// ValidateKey checks the credential against TMDB. It returns nil when TMDB
// accepts it, an error matching metadata.ErrUnauthorized (with a message fit for
// the onboarding screen) when TMDB rejects it, or another error when TMDB could
// not be reached. The result is never cached.
func (c *Client) ValidateKey(ctx context.Context) error {
	var out struct {
		Success bool `json:"success"`
	}
	err := c.get(ctx, "/authentication", url.Values{}, 0, &out)
	if errors.Is(err, metadata.ErrUnauthorized) {
		return fmt.Errorf("%w: TMDB rejected this key; check it on https://www.themoviedb.org/settings/api (%w)", metadata.ErrUnauthorized, err)
	}
	if err != nil {
		return err
	}
	if !out.Success {
		return fmt.Errorf("%w: TMDB did not confirm this key", metadata.ErrUnauthorized)
	}
	return nil
}

// ImageConfig is the image part of /configuration.
type ImageConfig struct {
	SecureBaseURL string   // e.g. "https://image.tmdb.org/t/p/"
	PosterSizes   []string // e.g. w92 ... w780, original
	BackdropSizes []string
	ProfileSizes  []string
}

// Configuration returns the image configuration (cached for a day).
func (c *Client) Configuration(ctx context.Context) (ImageConfig, error) {
	var w wireConfiguration
	if err := c.get(ctx, "/configuration", url.Values{}, configurationTTL, &w); err != nil {
		return ImageConfig{}, err
	}
	base := w.Images.SecureBaseURL
	if base == "" {
		return ImageConfig{}, errors.New("tmdb: configuration has no secure_base_url")
	}
	return ImageConfig{
		SecureBaseURL: base, PosterSizes: w.Images.PosterSizes,
		BackdropSizes: w.Images.BackdropSizes, ProfileSizes: w.Images.ProfileSizes,
	}, nil
}

// ImageBaseURL returns the secure image base URL; it satisfies the image cache's
// base URL source.
func (c *Client) ImageBaseURL(ctx context.Context) (string, error) {
	cfg, err := c.Configuration(ctx)
	if err != nil {
		return "", err
	}
	return cfg.SecureBaseURL, nil
}

// Search implements metadata.Provider. Adult movies are excluded.
func (c *Client) Search(ctx context.Context, query string, year, page int) (metadata.Page[metadata.MovieSummary], error) {
	query = strings.TrimSpace(query)
	if query == "" || utf8.RuneCountInString(query) > 200 {
		return metadata.Page[metadata.MovieSummary]{}, fmt.Errorf("%w: search query must be 1 to 200 characters", ErrInvalidArgument)
	}
	q := c.langQuery()
	q.Set("query", query)
	q.Set("include_adult", "false")
	if year >= 1870 && year <= 2200 {
		q.Set("year", strconv.Itoa(year))
	}
	return c.list(ctx, "/search/movie", q, page)
}

func (c *Client) list(ctx context.Context, path string, q url.Values, page int) (metadata.Page[metadata.MovieSummary], error) {
	q, err := pageQuery(q, page)
	if err != nil {
		return metadata.Page[metadata.MovieSummary]{}, err
	}
	var w wirePage
	if err := c.get(ctx, path, q, c.cacheTTL, &w); err != nil {
		return metadata.Page[metadata.MovieSummary]{}, err
	}
	return w.toPage(), nil
}

// Movie implements metadata.Provider.
func (c *Client) Movie(ctx context.Context, tmdbID int) (*metadata.Movie, error) {
	if err := validID("movie", tmdbID); err != nil {
		return nil, err
	}
	q := c.langQuery()
	q.Set("append_to_response", detailsAppend)
	var w wireMovie
	if err := c.get(ctx, "/movie/"+strconv.Itoa(tmdbID), q, c.cacheTTL, &w); err != nil {
		return nil, err
	}
	if w.ID == 0 {
		return nil, fmt.Errorf("tmdb: GET /movie/%d: response has no id", tmdbID)
	}
	return w.toMovie(c.region, c.now()), nil
}

// FindByIMDb implements metadata.Provider.
func (c *Client) FindByIMDb(ctx context.Context, imdbID string) (metadata.MovieSummary, error) {
	if !validIMDbID(imdbID) {
		return metadata.MovieSummary{}, fmt.Errorf("%w: IMDb id %q", ErrInvalidArgument, imdbID)
	}
	q := c.langQuery()
	q.Set("external_source", "imdb_id")
	var w wireFind
	if err := c.get(ctx, "/find/"+imdbID, q, c.cacheTTL, &w); err != nil {
		return metadata.MovieSummary{}, err
	}
	if len(w.MovieResults) == 0 {
		return metadata.MovieSummary{}, fmt.Errorf("tmdb: no movie with IMDb id %s: %w", imdbID, metadata.ErrNotFound)
	}
	return w.MovieResults[0].toSummary(), nil
}

// validIMDbID accepts "tt" followed by 7 to 10 digits.
func validIMDbID(s string) bool {
	if len(s) < 9 || len(s) > 12 || s[0] != 't' || s[1] != 't' {
		return false
	}
	for i := 2; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Collection implements metadata.Provider.
func (c *Client) Collection(ctx context.Context, tmdbID int) (*metadata.Collection, error) {
	if err := validID("collection", tmdbID); err != nil {
		return nil, err
	}
	var w wireCollection
	if err := c.get(ctx, "/collection/"+strconv.Itoa(tmdbID), c.langQuery(), c.cacheTTL, &w); err != nil {
		return nil, err
	}
	return w.toCollection(), nil
}

// PersonFilmography implements metadata.Provider.
func (c *Client) PersonFilmography(ctx context.Context, personID int) (*metadata.Filmography, error) {
	if err := validID("person", personID); err != nil {
		return nil, err
	}
	var w wirePersonCredits
	if err := c.get(ctx, "/person/"+strconv.Itoa(personID)+"/movie_credits", c.langQuery(), c.cacheTTL, &w); err != nil {
		return nil, err
	}
	return w.toFilmography(personID), nil
}

// Popular implements metadata.Provider.
func (c *Client) Popular(ctx context.Context, page int) (metadata.Page[metadata.MovieSummary], error) {
	return c.list(ctx, "/movie/popular", c.langQuery(), page)
}

// Trending implements metadata.Provider.
func (c *Client) Trending(ctx context.Context, window metadata.TrendingWindow, page int) (metadata.Page[metadata.MovieSummary], error) {
	if window != metadata.TrendingDay && window != metadata.TrendingWeek {
		return metadata.Page[metadata.MovieSummary]{}, fmt.Errorf("%w: trending window %q", ErrInvalidArgument, window)
	}
	return c.list(ctx, "/trending/movie/"+string(window), c.langQuery(), page)
}

// Upcoming implements metadata.Provider (configured region).
func (c *Client) Upcoming(ctx context.Context, page int) (metadata.Page[metadata.MovieSummary], error) {
	q := c.langQuery()
	q.Set("region", c.region)
	return c.list(ctx, "/movie/upcoming", q, page)
}

// NowPlaying implements metadata.Provider (configured region).
func (c *Client) NowPlaying(ctx context.Context, page int) (metadata.Page[metadata.MovieSummary], error) {
	q := c.langQuery()
	q.Set("region", c.region)
	return c.list(ctx, "/movie/now_playing", q, page)
}
