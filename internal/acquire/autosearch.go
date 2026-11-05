package acquire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/indexer"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/search"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Automatic search tuning.
const (
	// researchAfter skips movies searched more recently by the missing search.
	researchAfter = 12 * time.Hour
	// searchSpacing spreads the per-movie searches of the missing search, so
	// indexers see a trickle instead of a burst.
	searchSpacing = 10 * time.Second
	// maxSearchesPerRun bounds the movies searched by one missing search.
	maxSearchesPerRun = 500
	// recentTTL is how long the ranked results of an automatic search are kept
	// for retries after a failed download.
	recentTTL     = 6 * time.Hour
	maxRecent     = 256
	searchTimeout = 2 * time.Minute
)

// purpose selects the indexers of a search.
type purpose int

const (
	forAuto purpose = iota
	forInteractive
	forRSS
)

// MoviePayload is the payload of per-movie jobs.
type MoviePayload struct {
	MovieID int64 `json:"movieId"`
}

// DownloadPayload is the payload of per-download jobs.
type DownloadPayload struct {
	DownloadID int64 `json:"downloadId"`
}

// RegisterJobs registers the job handlers and the recurring schedules: the
// missing search daily, the RSS sync every SettingRSSInterval minutes.
func (s *Service) RegisterJobs(ctx context.Context, r interface {
	Handle(kind string, fn jobs.Handler, opts jobs.HandlerOptions)
	Every(kind string, interval, jitter time.Duration, payload any)
}) {
	r.Handle(JobMovieSearch, s.handleMovieSearch, jobs.HandlerOptions{Timeout: 5 * time.Minute})
	r.Handle(JobSearchMissing, func(ctx context.Context, _ db.Job) error { return s.SearchMissing(ctx) },
		jobs.HandlerOptions{Timeout: 10 * time.Minute})
	r.Handle(JobRSSSync, func(ctx context.Context, _ db.Job) error { return s.SyncRSS(ctx) },
		jobs.HandlerOptions{Timeout: 10 * time.Minute})
	r.Handle(JobDownloadRetry, s.handleRetry, jobs.HandlerOptions{Timeout: 5 * time.Minute})
	r.Every(JobSearchMissing, SearchMissingTick, time.Hour, nil)
	r.Every(JobRSSSync, s.rssInterval(ctx), time.Minute, nil)
}

// rssInterval reads SettingRSSInterval (minutes, 10 to 120).
func (s *Service) rssInterval(ctx context.Context) time.Duration {
	m := s.settingInt(ctx, SettingRSSInterval, int(DefaultRSSInterval/time.Minute))
	return time.Duration(min(max(m, 10), 120)) * time.Minute
}

func (s *Service) handleMovieSearch(ctx context.Context, job db.Job) error {
	var p MoviePayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil || p.MovieID <= 0 {
		return fmt.Errorf("acquire: invalid %s payload", job.Kind)
	}
	_, err := s.SearchMovie(ctx, p.MovieID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil // movie removed since: nothing to do
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrInvalid):
		// Settings problem (no indexer, no client, invalid profile): retrying
		// the job cannot help; the reason is in the log and the history.
		s.log.Warn("automatic search not possible", slog.Int64("movie_id", p.MovieID), slog.String("reason", err.Error()))
		return nil
	}
	return err
}

func (s *Service) handleRetry(ctx context.Context, job db.Job) error {
	var p DownloadPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil || p.DownloadID <= 0 {
		return fmt.Errorf("acquire: invalid %s payload", job.Kind)
	}
	err := s.Retry(ctx, p.DownloadID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

// SearchOutcome summarizes an automatic search.
type SearchOutcome struct {
	Searched bool       `json:"searched"`
	Results  int        `json:"results"`
	Accepted int        `json:"accepted"`
	Grabbed  *QueueItem `json:"grabbed,omitempty"`
	Reason   string     `json:"reason"`
}

// SearchMovie searches the indexers for a monitored movie and grabs the best
// accepted release, unless a download at least as good is already queued.
func (s *Service) SearchMovie(ctx context.Context, movieID int64) (SearchOutcome, error) {
	return s.autoSearch(ctx, movieID, 0, "")
}

func (s *Service) autoSearch(ctx context.Context, movieID, retryOf int64, reason string) (SearchOutcome, error) {
	var out SearchOutcome
	mc, err := s.loadMovie(ctx, movieID, nil, decision.Options{})
	if err != nil {
		return out, err
	}
	log := s.log.With(slog.Int64("movie_id", movieID), slog.String("movie", mc.row.Title))
	if !mc.row.Monitored {
		out.Reason = "The movie is not monitored."
		return out, nil
	}
	if ok, at := decision.Available(&mc.cfg.Movie, mc.cfg.Options.Now, 0); !ok {
		out.Reason = "The movie is not available yet (minimum availability " + mc.cfg.Movie.MinimumAvailability.String() + ")."
		if !at.IsZero() {
			out.Reason = fmt.Sprintf("The movie is not available before %s.", at.Format(time.DateOnly))
		}
		log.Debug("search skipped", slog.String("reason", out.Reason))
		return out, nil
	}
	rels, err := s.collect(ctx, mc, forAuto)
	if err != nil {
		return out, err
	}
	out.Searched = true
	s.markSearched(ctx, movieID)
	ranked, byKey, err := s.evaluate(mc, rels)
	if err != nil {
		return out, err
	}
	s.recent.put(movieID, ranked, byKey)
	out.Results = len(ranked)
	for i := range ranked {
		if ranked[i].Decision.Accepted {
			out.Accepted++
		}
	}
	if out.Accepted == 0 {
		out.Reason = fmt.Sprintf("No acceptable release among %d results.", len(ranked))
		log.Debug("automatic search: nothing to grab", slog.Int("results", len(ranked)))
		for i := range min(len(ranked), 5) {
			log.Debug("rejected release", slog.String("release", ranked[i].Candidate.Title),
				slog.String("reasons", rejectionText(ranked[i].Decision.Rejections)))
		}
		return out, nil
	}
	best := ranked[0]
	if d, covered := mc.queuedCovers(best); covered {
		out.Reason = fmt.Sprintf("A download at least as good is already queued: %s.", d.ReleaseTitle)
		log.Debug("automatic search: queued download is as good", slog.String("queued", d.ReleaseTitle))
		return out, nil
	}
	rel, ok := byKey[candKey(&best.Candidate)]
	if !ok {
		return out, fmt.Errorf("acquire: ranked release %q lost its source", best.Candidate.Title)
	}
	item, err := s.grab(ctx, grabRequest{movieID: movieID, rel: rel, dec: best.Decision, retryOf: retryOf, reason: reason})
	if err != nil {
		return out, err
	}
	out.Grabbed = &item
	out.Reason = "Grabbed " + rel.Title + "."
	return out, nil
}

func (s *Service) markSearched(ctx context.Context, movieID int64) {
	if _, err := s.st.Write().SetMovieLastSearched(context.WithoutCancel(ctx), db.SetMovieLastSearchedParams{
		ID: movieID, LastSearchedAt: store.Millis(s.now()),
	}); err != nil {
		s.log.Error("record last search", slog.Int64("movie_id", movieID), slog.Any("error", err))
	}
}

// indexersFor returns the enabled indexers of a purpose that serve a movie
// with these tags (nil tags: every indexer of the purpose).
func (s *Service) indexersFor(rows []db.Indexer, p purpose, movieTags []string, anyTags bool) (ixs []indexer.Indexer, used []db.Indexer) {
	for i := range rows {
		r := &rows[i]
		enabled := r.EnableAutoSearch
		switch p {
		case forInteractive:
			enabled = r.EnableInteractive
		case forRSS:
			enabled = r.EnableRss
		}
		if !enabled || (!anyTags && !tagsApply(decodeArray[string](r.Tags), movieTags)) {
			continue
		}
		ix, err := s.indexerClient(r)
		if err != nil {
			s.log.Error("indexer unusable", slog.String("indexer", r.Name), slog.Any("error", err))
			continue
		}
		ixs = append(ixs, ix)
		used = append(used, *r)
	}
	return ixs, used
}

// collect runs a search on the indexers of a purpose and returns the merged
// results. Indexer failures are logged and recorded in their health; they do
// not fail the search.
func (s *Service) collect(ctx context.Context, mc *movieContext, p purpose) ([]indexer.Release, error) {
	rows, err := s.st.Read().ListIndexers(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("acquire: list indexers: %w", err))
	}
	ixs, used := s.indexersFor(rows, p, mc.tags, false)
	if len(ixs) == 0 {
		return nil, newError(ErrUnavailable, nil, "No enabled indexer can search for this movie: add one in Settings > Indexers.")
	}
	sctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	var fin *search.Finished
	for ev := range s.orch.Search(sctx, mc.query(), ixs) {
		switch e := ev.(type) {
		case *search.IndexerFailed:
			s.log.Warn("indexer search failed", slog.String("indexer", e.Indexer.Name), slog.String("error", e.Err.Error()))
		case *search.Finished:
			fin = e
		}
	}
	s.persistHealth(ctx, used)
	if fin == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("acquire: search did not finish in time")
	}
	return fin.Results, nil
}

// candKey is search.Key for a candidate.
func candKey(c *decision.Candidate) string {
	if c.InfoHash != "" {
		return "btih:" + c.InfoHash
	}
	return "guid:" + c.GUID
}

// evaluate decides on releases and ranks them. byKey maps candKey to releases.
func (s *Service) evaluate(mc *movieContext, rels []indexer.Release) (ranked []decision.Evaluated, byKey map[string]indexer.Release, err error) {
	ev, err := decision.NewEvaluator(mc.cfg)
	if err != nil {
		return nil, nil, unusableProfile(err)
	}
	cands := make([]decision.Candidate, 0, len(rels))
	byKey = make(map[string]indexer.Release, len(rels))
	for i := range rels {
		c := rels[i].Candidate()
		c.AlreadyGrabbed = mc.tried(rels[i].Title)
		cands = append(cands, c)
		byKey[search.Key(&rels[i])] = rels[i]
	}
	return ev.EvaluateAll(cands), byKey, nil
}

// SearchMissing enqueues an automatic search for each monitored movie that
// misses a file or can be upgraded, least recently searched first, spaced out
// so the indexers are not hammered. Without a job queue the searches run
// inline, one after the other.
func (s *Service) SearchMissing(ctx context.Context) error {
	ixRows, err := s.st.Read().ListIndexers(ctx)
	if err != nil {
		return store.MapError(fmt.Errorf("acquire: list indexers: %w", err))
	}
	if !slices.ContainsFunc(ixRows, func(r db.Indexer) bool { return r.EnableAutoSearch }) {
		s.log.Info("missing search skipped: no indexer is enabled for automatic search")
		return nil
	}
	rows, err := s.st.Read().ListSearchableMovies(ctx)
	if err != nil {
		return store.MapError(fmt.Errorf("acquire: list searchable movies: %w", err))
	}
	now := s.now()
	n := 0
	for _, r := range rows {
		if n >= maxSearchesPerRun || ctx.Err() != nil {
			break
		}
		if r.LastSearchedAt != nil && now.Sub(store.FromMillis(*r.LastSearchedAt)) < researchAfter {
			continue
		}
		if s.jobs != nil && s.jobs.Has(JobMovieSearch) {
			if _, _, err := s.jobs.Enqueue(ctx, JobMovieSearch, MoviePayload{MovieID: r.ID}, jobs.EnqueueOptions{
				DedupeKey: fmt.Sprintf("%s:%d", JobMovieSearch, r.ID), Priority: -5, MaxAttempts: 2,
				RunAt: now.Add(time.Duration(n) * searchSpacing),
			}); err != nil {
				return fmt.Errorf("acquire: enqueue search of movie %d: %w", r.ID, err)
			}
		} else if _, err := s.SearchMovie(ctx, r.ID); err != nil && !errors.Is(err, ErrUnavailable) {
			s.log.Warn("missing search failed", slog.Int64("movie_id", r.ID), slog.Any("error", err))
		}
		n++
	}
	if n > 0 {
		s.log.Info("missing and upgradable movies queued for search", slog.Int("count", n))
	}
	return nil
}

// recentSearches keeps the ranked results of the last automatic search of each
// movie, to retry with the next release after a failed download.
type recentSearches struct {
	now   func() time.Time
	mu    sync.Mutex
	m     map[int64]*rankedSearch
	order []int64 // insertion order, oldest first
}

type rankedSearch struct {
	at       time.Time
	ranked   []decision.Evaluated
	releases map[string]indexer.Release
}

func newRecentSearches(now func() time.Time) *recentSearches {
	return &recentSearches{now: now, m: make(map[int64]*rankedSearch)}
}

func (r *recentSearches) put(movieID int64, ranked []decision.Evaluated, rels map[string]indexer.Release) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.m[movieID]; !ok {
		r.order = append(r.order, movieID)
	}
	r.m[movieID] = &rankedSearch{at: r.now(), ranked: ranked, releases: rels}
	for len(r.order) > maxRecent {
		delete(r.m, r.order[0])
		r.order = r.order[1:]
	}
}

func (r *recentSearches) get(movieID int64) (*rankedSearch, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rs, ok := r.m[movieID]
	if !ok || r.now().Sub(rs.at) > recentTTL {
		return nil, false
	}
	return rs, true
}

// ----------------------------------------------------------------- RSS

// SyncRSS reads the RSS feed of every RSS-enabled indexer, matches the new
// releases to library movies (confident matches only), and grabs the best
// accepted release of each matched movie. Rejections are logged at debug level.
func (s *Service) SyncRSS(ctx context.Context) error {
	if s.matcher == nil {
		return errors.New("acquire: RSS sync needs the release matcher")
	}
	sh, err := s.loadShared(ctx)
	if err != nil {
		return err
	}
	ixs, used := s.indexersFor(sh.indexers, forRSS, nil, true)
	if len(ixs) == 0 {
		s.log.Debug("RSS sync skipped: no RSS-enabled indexer")
		return nil
	}
	type feedState struct {
		id    int64
		state search.RSSState
	}
	var states []feedState
	byMovie := make(map[int64][]indexer.Release)
	matched := 0
	for i, ix := range ixs {
		r := &used[i]
		st := s.rssState(ctx, r.ID)
		res, err := s.orch.SyncRSS(ctx, ix, st)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log.Warn("RSS sync failed", slog.String("indexer", r.Name), slog.String("error", err.Error()))
			continue
		}
		if res.Gap {
			s.log.Warn("RSS feed shorter than the sync interval: releases may have been missed", slog.String("indexer", r.Name))
		}
		states = append(states, feedState{id: r.ID, state: res.State})
		for j := range res.Releases {
			if id, ok := s.matchMovie(&res.Releases[j]); ok {
				byMovie[id] = append(byMovie[id], res.Releases[j])
				matched++
			}
		}
	}
	s.persistHealth(ctx, used)
	grabbed := 0
	for movieID, rels := range byMovie {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ok, err := s.rssDecide(ctx, sh, movieID, rels)
		if err != nil {
			s.log.Warn("RSS decision failed", slog.Int64("movie_id", movieID), slog.Any("error", err))
			continue
		}
		if ok {
			grabbed++
		}
	}
	for _, f := range states {
		s.saveRSSState(ctx, f.id, f.state)
	}
	s.log.Info("RSS sync done", slog.Int("indexers", len(ixs)), slog.Int("matched", matched), slog.Int("grabbed", grabbed))
	return nil
}

// matchMovie returns the library movie a release is confidently about.
func (s *Service) matchMovie(rel *indexer.Release) (int64, bool) {
	if rel.IMDbID != "" {
		if c := s.matcher.MatchIMDb(rel.IMDbID); len(c) == 1 {
			return c[0].MovieID, true
		}
	}
	c := rel.Candidate()
	cands := s.matcher.MatchRelease(c.Parsed)
	if len(cands) > 0 && cands[0].Confident() {
		return cands[0].MovieID, true
	}
	return 0, false
}

// rssDecide evaluates the RSS releases of one movie and grabs the best.
func (s *Service) rssDecide(ctx context.Context, sh *shared, movieID int64, rels []indexer.Release) (bool, error) {
	mc, err := s.loadMovie(ctx, movieID, sh, decision.Options{})
	if err != nil {
		return false, err
	}
	if !mc.row.Monitored {
		return false, nil
	}
	ranked, byKey, err := s.evaluate(mc, rels)
	if err != nil {
		return false, err
	}
	for i := range ranked {
		if !ranked[i].Decision.Accepted {
			s.log.Debug("RSS release rejected", slog.Int64("movie_id", movieID), slog.String("release", ranked[i].Candidate.Title),
				slog.String("reasons", rejectionText(ranked[i].Decision.Rejections)))
		}
	}
	if len(ranked) == 0 || !ranked[0].Decision.Accepted {
		return false, nil
	}
	best := ranked[0]
	if _, covered := mc.queuedCovers(best); covered {
		return false, nil
	}
	rel := byKey[candKey(&best.Candidate)]
	if _, err := s.grab(ctx, grabRequest{movieID: movieID, rel: rel, dec: best.Decision,
		reason: fmt.Sprintf("New RSS release from %s accepted: %s, score %d", rel.IndexerName, best.Decision.Quality.Name(), best.Decision.Score.Total),
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) rssState(ctx context.Context, indexerID int64) search.RSSState {
	r, err := s.st.Read().GetRSSState(ctx, indexerID)
	if err != nil {
		if !errors.Is(store.MapError(err), store.ErrNotFound) {
			s.log.Error("read RSS state", slog.Int64("indexer_id", indexerID), slog.Any("error", err))
		}
		return search.RSSState{}
	}
	st := search.RSSState{LastGUID: deref(r.LastGuid)}
	if r.LastPubAt != nil {
		st.LastPubAt = store.FromMillis(*r.LastPubAt)
	}
	if r.LastSyncAt != nil {
		st.LastSyncAt = store.FromMillis(*r.LastSyncAt)
	}
	return st
}

func (s *Service) saveRSSState(ctx context.Context, indexerID int64, st search.RSSState) {
	p := db.UpsertRSSStateParams{IndexerID: indexerID, LastPubAt: millisPtr(st.LastPubAt), LastSyncAt: millisPtr(st.LastSyncAt)}
	if st.LastGUID != "" {
		g := st.LastGUID
		p.LastGuid = &g
	}
	if err := s.st.Write().UpsertRSSState(context.WithoutCancel(ctx), p); err != nil {
		s.log.Error("save RSS state", slog.Int64("indexer_id", indexerID), slog.Any("error", err))
	}
}

// ----------------------------------------------------------------- retries

// scheduleRetry tries the next release of a movie after a failed download:
// through the job queue when there is one, inline otherwise.
func (s *Service) scheduleRetry(ctx context.Context, dl *db.Download) {
	if s.jobs != nil && s.jobs.Has(JobDownloadRetry) {
		if _, _, err := s.jobs.Enqueue(ctx, JobDownloadRetry, DownloadPayload{DownloadID: dl.ID}, jobs.EnqueueOptions{
			DedupeKey: fmt.Sprintf("%s:%d", JobDownloadRetry, dl.ID), Priority: 5, MaxAttempts: 3,
		}); err != nil {
			s.log.Error("enqueue retry", slog.Int64("download_id", dl.ID), slog.Any("error", err))
		}
		return
	}
	if err := s.Retry(ctx, dl.ID); err != nil {
		s.log.Warn("retry failed", slog.Int64("download_id", dl.ID), slog.Any("error", err))
	}
}

// Retry grabs the next best release of the movie of a failed download: from
// the results of the last automatic search when they are recent, else from a
// new search. Each step is written to history. Retries stop once the movie
// used its daily attempts (SettingMaxAttempts, first grab included).
func (s *Service) Retry(ctx context.Context, downloadID int64) error {
	dl, err := s.st.Read().GetDownload(ctx, downloadID)
	if err != nil {
		return mapNotFound(err, "Download %d does not exist.", downloadID)
	}
	maxAttempts := max(s.settingInt(ctx, SettingMaxAttempts, DefaultMaxAttempts), 1)
	used, err := s.st.Read().CountGrabsSince(ctx, db.CountGrabsSinceParams{
		MovieID: dl.MovieID, Since: store.Millis(s.now().Add(-24 * time.Hour)),
	})
	if err != nil {
		return store.MapError(fmt.Errorf("acquire: count attempts: %w", err))
	}
	if int(used) >= maxAttempts {
		s.writeHistory(ctx, historyEntry{
			movieID: dl.MovieID, event: HistoryRetriesExhausted, title: dl.ReleaseTitle, downloadID: dl.ID,
			data: map[string]any{"reason": fmt.Sprintf("Tried %d releases in the last 24 hours: automatic retries stop for today; RSS and the daily search keep looking.", used), "attempts": used, "max": maxAttempts},
		})
		return nil
	}
	attempt := int(used) + 1
	mc, err := s.loadMovie(ctx, dl.MovieID, nil, decision.Options{})
	if err != nil {
		return err
	}
	if !mc.row.Monitored {
		s.log.Info("movie not monitored: no retry", slog.Int64("movie_id", dl.MovieID))
		return nil
	}
	msg := fmt.Sprintf("Trying release #%d of %d", attempt, maxAttempts)
	reason := fmt.Sprintf("%s after the failure of %s", msg, dl.ReleaseTitle)
	if rs, ok := s.recent.get(dl.MovieID); ok {
		if next, found := decision.NextBest(rs.ranked, mc.failedEntries()); found {
			if rel, ok := rs.releases[candKey(&next.Candidate)]; ok {
				s.writeHistory(ctx, historyEntry{
					movieID: dl.MovieID, event: HistoryRetryScheduled, title: next.Candidate.Title, downloadID: dl.ID,
					data: map[string]any{"reason": msg, "attempt": attempt, "max": maxAttempts, "failed": dl.ReleaseTitle, "source": "cached search"},
				})
				_, err := s.grab(ctx, grabRequest{movieID: dl.MovieID, rel: rel, dec: next.Decision, retryOf: dl.ID, reason: reason})
				return err
			}
		}
	}
	s.writeHistory(ctx, historyEntry{
		movieID: dl.MovieID, event: HistoryRetryScheduled, title: dl.ReleaseTitle, downloadID: dl.ID,
		data: map[string]any{"reason": msg + ": searching the indexers again", "attempt": attempt, "max": maxAttempts, "failed": dl.ReleaseTitle, "source": "new search"},
	})
	out, err := s.autoSearch(ctx, dl.MovieID, dl.ID, reason)
	if err != nil {
		return err
	}
	if out.Grabbed == nil {
		s.writeHistory(ctx, historyEntry{
			movieID: dl.MovieID, event: HistoryRetriesExhausted, title: dl.ReleaseTitle, downloadID: dl.ID,
			data: map[string]any{"reason": "No other acceptable release found: " + strings.TrimSuffix(out.Reason, ".") + ". RSS and the daily search keep looking.", "attempt": attempt, "max": maxAttempts},
		})
	}
	return nil
}
