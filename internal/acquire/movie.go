package acquire

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/indexer"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// shared holds what every movie of a batch (RSS sync) needs: loaded once.
type shared struct {
	rules    []*quality.Rule
	sizes    quality.Sizes
	indexers []db.Indexer
	profiles map[int64]quality.Profile
}

func (s *Service) loadShared(ctx context.Context) (*shared, error) {
	sh := &shared{profiles: make(map[int64]quality.Profile)}
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		var err error
		if sh.rules, err = library.LoadScoringRules(ctx, q); err != nil {
			return err
		}
		if sh.sizes, err = library.LoadQualitySizes(ctx, q); err != nil {
			return err
		}
		if sh.indexers, err = q.ListIndexers(ctx); err != nil {
			return fmt.Errorf("list indexers: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("acquire: load decision settings: %w", err)
	}
	return sh, nil
}

// movieContext is a movie with everything a decision on its releases needs.
type movieContext struct {
	row       db.Movie
	tags      []string
	downloads []db.Download // newest first
	cfg       decision.Config
}

// loadMovie reads a movie and builds the decision configuration of a search.
func (s *Service) loadMovie(ctx context.Context, movieID int64, sh *shared, opts decision.Options) (*movieContext, error) {
	if sh == nil {
		var err error
		if sh, err = s.loadShared(ctx); err != nil {
			return nil, err
		}
	}
	mc := &movieContext{}
	var blocks []db.Blocklist
	var file *db.MovieFile
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		var err error
		if mc.row, err = q.GetMovie(ctx, movieID); err != nil {
			return mapNotFound(err, "Movie %d is not in the library.", movieID)
		}
		tags, err := q.ListMovieTags(ctx, movieID)
		if err != nil {
			return fmt.Errorf("list tags: %w", err)
		}
		for _, t := range tags {
			mc.tags = append(mc.tags, t.Label)
		}
		alts, err := q.ListAlternateTitles(ctx, movieID)
		if err != nil {
			return fmt.Errorf("list alternate titles: %w", err)
		}
		for _, a := range alts {
			mc.cfg.Movie.AlternateTitles = append(mc.cfg.Movie.AlternateTitles, a.Title)
		}
		if f, err := q.GetCurrentMovieFile(ctx, movieID); err == nil {
			file = &f
		} else if !errors.Is(store.MapError(err), store.ErrNotFound) {
			return fmt.Errorf("get movie file: %w", err)
		}
		if blocks, err = q.ListBlocklistForMovie(ctx, &movieID); err != nil {
			return fmt.Errorf("list blocklist: %w", err)
		}
		if mc.downloads, err = q.ListMovieDownloads(ctx, movieID); err != nil {
			return fmt.Errorf("list downloads: %w", err)
		}
		if _, ok := sh.profiles[mc.row.QualityProfileID]; !ok {
			p, err := library.LoadQualityProfile(ctx, q, mc.row.QualityProfileID)
			if err != nil {
				return err
			}
			sh.profiles[mc.row.QualityProfileID] = p
		}
		return nil
	})
	if err != nil {
		var ae *Error
		if errors.As(err, &ae) {
			return nil, err
		}
		return nil, fmt.Errorf("acquire: load movie %d: %w", movieID, err)
	}
	m := &mc.row
	if m.OriginalTitle != "" && !strings.EqualFold(m.OriginalTitle, m.Title) {
		mc.cfg.Movie.AlternateTitles = append(mc.cfg.Movie.AlternateTitles, m.OriginalTitle)
	}
	mc.cfg.Movie.ID, mc.cfg.Movie.Title, mc.cfg.Movie.Year = m.ID, m.Title, int(m.Year)
	mc.cfg.Movie.RuntimeMinutes, mc.cfg.Movie.Monitored = int(m.RuntimeMin), m.Monitored
	var lang parser.Language
	if lang.UnmarshalText([]byte(m.OriginalLanguage)) == nil {
		mc.cfg.Movie.OriginalLanguage = lang
	}
	mc.cfg.Movie.Status = availability(m.Status)
	mc.cfg.Movie.MinimumAvailability = availability(m.MinimumAvailability)
	mc.cfg.Movie.InCinemas = date(m.ReleaseCinema)
	mc.cfg.Movie.DigitalRelease = date(m.ReleaseDigital)
	mc.cfg.Movie.PhysicalRelease = date(m.ReleasePhysical)

	mc.cfg.Profile = sh.profiles[m.QualityProfileID]
	mc.cfg.Rules, mc.cfg.Sizes = sh.rules, sh.sizes
	if file != nil {
		cur := &decision.CurrentFile{FileQuality: decision.FileQuality{
			Quality: quality.ID(file.Quality), Revision: parser.Revision{Version: int(file.Revision)},
			CustomScore: int(file.CustomScore),
		}}
		for _, code := range decodeArray[string](file.Languages) {
			var l parser.Language
			if l.UnmarshalText([]byte(code)) == nil {
				cur.Languages = append(cur.Languages, l)
			}
		}
		mc.cfg.Current = cur
	}
	for _, b := range blocks {
		mc.cfg.Blocklist = append(mc.cfg.Blocklist, decision.BlocklistEntry{
			Kind: decision.BlocklistKind(b.Kind), SourceTitle: b.SourceTitle, InfoHash: deref(b.InfoHash),
			ReleaseGroup: deref(b.ReleaseGroup),
		})
	}
	mc.cfg.Indexers = make(map[int64]decision.IndexerSettings)
	for i := range sh.indexers {
		if ix := &sh.indexers[i]; ix.MinSeeders != nil {
			mc.cfg.Indexers[ix.ID] = decision.IndexerSettings{MinSeeders: int(*ix.MinSeeders)}
		}
	}
	mc.cfg.Options = opts
	if mc.cfg.Options.Now.IsZero() {
		mc.cfg.Options.Now = s.now()
	}
	return mc, nil
}

func availability(s string) decision.Availability {
	switch s {
	case "in_cinemas":
		return decision.InCinemas
	case "released", "deleted":
		return decision.Released
	}
	return decision.Announced
}

func date(s *string) time.Time {
	if s == nil {
		return time.Time{}
	}
	t, err := time.Parse(time.DateOnly, *s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// query builds the indexer query of a movie.
func (mc *movieContext) query() indexer.Query {
	q := indexer.Query{TMDbID: mc.row.TmdbID, Title: mc.row.Title, Year: int(mc.row.Year)}
	if mc.row.ImdbID != nil {
		q.IMDbID = *mc.row.ImdbID
	}
	return q
}

// tried reports downloads already made of a release title (any status): a
// release is not grabbed twice for the same movie.
func (mc *movieContext) tried(title string) bool {
	for i := range mc.downloads {
		if strings.EqualFold(mc.downloads[i].ReleaseTitle, title) {
			return true
		}
	}
	return false
}

// activeDownloads returns the downloads in progress.
func (mc *movieContext) activeDownloads() []db.Download {
	var out []db.Download
	for i := range mc.downloads {
		if isActive(mc.downloads[i].Status) {
			out = append(out, mc.downloads[i])
		}
	}
	return out
}

func isActive(status string) bool {
	switch status {
	case "queued", "downloading", "paused", "completed", "importing", "warning":
		return true
	}
	return false
}

// failedEntries lists the releases tried for the movie (failed, removed or
// still in the queue) and its blocklist, for decision.NextBest.
func (mc *movieContext) failedEntries() []decision.BlocklistEntry {
	out := append([]decision.BlocklistEntry(nil), mc.cfg.Blocklist...)
	for i := range mc.downloads {
		d := &mc.downloads[i]
		e := decision.BlocklistEntry{Kind: decision.BlockRelease, SourceTitle: d.ReleaseTitle}
		if d.Protocol == string(decision.Torrent) {
			e.InfoHash = deref(d.ClientRef)
		}
		out = append(out, e)
	}
	return out
}

// queuedCovers reports whether a download in progress is at least as good as
// cand: a new grab would not improve on it.
func (mc *movieContext) queuedCovers(cand decision.Evaluated) (db.Download, bool) {
	fq := decision.FileQuality{Quality: cand.Decision.Quality, Revision: cand.Candidate.Parsed.Revision, CustomScore: cand.Decision.Score.Total}
	active := mc.activeDownloads()
	for i := range active {
		d := &active[i]
		cur := decision.FileQuality{Quality: quality.ID(d.Quality), Revision: parser.Revision{Version: int(d.Revision)}, CustomScore: int(d.CustomScore)}
		if _, better := decision.IsUpgrade(cur, fq, &mc.cfg.Profile); !better {
			return *d, true
		}
	}
	return db.Download{}, false
}
