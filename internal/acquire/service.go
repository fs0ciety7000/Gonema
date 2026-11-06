// Package acquire is the acquisition service of Gonema: indexer and download
// client settings, automatic and interactive searches, grabs, the download
// queue tracker, failure handling (blocklist and automatic retries).
//
// Decisions come from the pure engine (internal/decision); this package does
// the I/O around them. Every automatic action writes a history row with its
// reasons, in the same transaction as the change it records.
//
// Secrets (indexer API keys, client passwords) are encrypted at rest with the
// secret box, bound to "indexers/<id>" or "download-clients/<id>", and never
// returned. Download links and magnet URIs of releases never leave the server:
// API views and events carry an opaque release key instead, and logs carry
// release titles and indexer names only.
package acquire

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/match"
	"github.com/fs0ciety7000/gonema/internal/netx"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/search"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Event types published by the service (SSE "event:" names).
const (
	EventSearchBatch         events.Type = "search.batch"
	EventSearchIndexerDone   events.Type = "search.indexer_done"
	EventSearchIndexerFailed events.Type = "search.indexer_failed"
	EventSearchFinished      events.Type = "search.finished"

	EventDownloadGrabbed  events.Type = "download.grabbed"
	EventDownloadUpdated  events.Type = "download.updated"
	EventDownloadImported events.Type = "download.imported"
	EventDownloadFailed   events.Type = "download.failed"
	EventDownloadRemoved  events.Type = "download.removed"
)

// History events written by the service (history.event).
const (
	HistoryGrabbed          = "grabbed"
	HistoryGrabOverridden   = "grab_overridden"
	HistoryGrabFailed       = "grab_failed"
	HistoryDownloadFailed   = "download_failed"
	HistoryImported         = "imported"
	HistoryUpgraded         = "upgraded"
	HistoryImportFailed     = "import_failed"
	HistoryBlocklisted      = "blocklisted"
	HistoryRetryScheduled   = "retry_scheduled"
	HistoryRetriesExhausted = "retries_exhausted"
	HistoryDownloadRemoved  = "download_removed"
)

// Settings read by the service (settings table, JSON values).
const (
	// SettingMaxAttempts is the number of releases tried per movie and per day
	// (first grab included) before automatic retries stop. Default 6.
	SettingMaxAttempts = "downloads.max_attempts_per_day"
	// SettingBlocklistGroup: when true, a failed download also blocks its release
	// group for the movie. Default false.
	SettingBlocklistGroup = "downloads.blocklist_group_on_failure"
	// SettingRSSInterval is the RSS sync period in minutes (10 to 120). Default 15.
	SettingRSSInterval = "indexers.rss_interval_min"
)

// Defaults.
const (
	DefaultMaxAttempts = 6
	DefaultRSSInterval = 15 * time.Minute
)

// Job kinds handled by the service (see RegisterJobs). JobMovieSearch is the
// kind the library enqueues for "search on add" (library.JobMovieSearch).
const (
	JobMovieSearch    = "movie.search"
	JobSearchMissing  = "movies.search_missing"
	JobRSSSync        = "rss.sync"
	JobDownloadRetry  = "download.retry"
	SearchMissingTick = 24 * time.Hour
)

// Matcher finds the library movies a release name is about (library.Service).
type Matcher interface {
	MatchRelease(p parser.ParsedRelease) []match.Candidate
	MatchIMDb(imdbID string) []match.Candidate
}

// JobQueue is the part of the job runner the service uses.
type JobQueue interface {
	Enqueue(ctx context.Context, kind string, payload any, opts jobs.EnqueueOptions) (db.Job, bool, error)
	Has(kind string) bool
}

// ImportRequest asks the importer to import a completed download.
type ImportRequest struct {
	DownloadID   int64
	MovieID      int64
	LocalPath    string // output path as seen by Gonema (remote path mapping applied)
	ReleaseTitle string
	Protocol     decision.Protocol
	// ClientSeeding is true when the client keeps the data (torrent seeding):
	// the importer must copy or hardlink, never move.
	ClientSeeding bool
}

// ImportResult is the outcome of an import. Imported false with a nil error
// means the content was refused (fake, sample, corrupt...): the release is
// blocklisted and the next one tried. An error means the import could not run
// (disk, permissions): the download is kept with a warning.
type ImportResult struct {
	Imported bool
	Upgraded bool
	FileID   int64
	Reason   string
}

// ImportFunc imports completed downloads (internal/importer, through an adapter).
type ImportFunc interface {
	Import(ctx context.Context, req ImportRequest) (ImportResult, error)
}

// Options configures a Service. Store and Box are required.
type Options struct {
	Store    *store.Store
	Box      *secretbox.Box
	Matcher  Matcher              // required by the RSS sync
	Search   *search.Orchestrator // default search.New with the logger and clock
	Jobs     JobQueue             // optional: without it, follow-up work runs inline
	Bus      *events.Bus          // optional
	Importer ImportFunc           // optional: without it, completed downloads wait
	// ImporterWritesHistory tells that the importer records its own outcome in
	// history (imported or upgraded with the file, import_failed for a
	// refusal): the tracker then only records what the importer does not
	// (import errors, and the failure handling of refused downloads).
	ImporterWritesHistory bool
	Logger                *slog.Logger     // default discards
	Now                   func() time.Time // default time.Now
	// HTTPClient fetches .nzb and .torrent files from indexers; default a client
	// with TLS verification and no global timeout (each fetch has its own).
	HTTPClient *http.Client
	// Factories build indexer and download clients from settings; defaults use
	// the newznab/torznab packages and the five download clients.
	NewIndexer IndexerFactory
	NewClient  ClientFactory
	Version    string // for the indexer User-Agent
}

// Service is the acquisition service. It is safe for concurrent use.
type Service struct {
	st       *store.Store
	box      *secretbox.Box
	matcher  Matcher
	orch     *search.Orchestrator
	jobs     JobQueue
	bus      *events.Bus
	importer ImportFunc
	impHist  bool
	log      *slog.Logger
	now      func() time.Time
	http     *http.Client
	newIx    IndexerFactory
	newCl    ClientFactory
	version  string

	// base is canceled by Close: background searches and imports stop.
	base   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	cacheMu sync.Mutex
	ixCache map[int64]cachedIndexer
	clCache map[int64]cachedClient

	healthMu sync.Mutex
	health   map[int64]search.Health // last persisted breaker state

	resched Rescheduler // set by RegisterJobs; nil: intervals apply at the next run

	searches *searchCache
	recent   *recentSearches

	track trackerState

	onImportDone func(downloadID int64) // test hook, called after each import attempt
}

// New returns a service. Call Init before use and Close at shutdown.
func New(opts Options) (*Service, error) {
	if opts.Store == nil || opts.Box == nil {
		return nil, errors.New("acquire: store and secret box are required")
	}
	s := &Service{
		st: opts.Store, box: opts.Box, matcher: opts.Matcher, orch: opts.Search, jobs: opts.Jobs,
		bus: opts.Bus, importer: opts.Importer, impHist: opts.ImporterWritesHistory, log: opts.Logger, now: opts.Now, http: opts.HTTPClient,
		newIx: opts.NewIndexer, newCl: opts.NewClient, version: opts.Version,
		ixCache: make(map[int64]cachedIndexer), clCache: make(map[int64]cachedClient),
		health: make(map[int64]search.Health),
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.orch == nil {
		s.orch = search.New(search.Options{Logger: s.log, Now: s.now})
	}
	if s.http == nil {
		// Release downloads follow the links indexers return: destinations
		// checked at dial time (netx integration policy).
		s.http = netx.NewClient(netx.Integration)
	}
	if s.newIx == nil {
		s.newIx = defaultIndexerFactory(s.version, s.log)
	}
	if s.newCl == nil {
		s.newCl = defaultClientFactory
	}
	if s.version == "" {
		s.version = "dev"
	}
	s.base, s.cancel = context.WithCancel(context.Background())
	s.searches = newSearchCache(s.now)
	s.recent = newRecentSearches(s.now)
	s.track.init()
	return s, nil
}

// Init restores the indexer health persisted by the previous run. It does no
// network access.
func (s *Service) Init(ctx context.Context) error {
	return s.restoreHealth(ctx)
}

// Close stops the background searches and imports and waits for them.
func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

// Search returns the orchestrator (indexer health).
func (s *Service) Search() *search.Orchestrator { return s.orch }

func (s *Service) publish(e events.Event) {
	if s.bus != nil {
		s.bus.Publish(e)
	}
}

// go runs fn in the background, tracked by Close.
func (s *Service) goBackground(fn func(ctx context.Context)) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		fn(s.base)
	}()
}

// historyEntry is one history row to append.
type historyEntry struct {
	movieID    int64
	event      string
	title      string
	quality    string
	score      *int64
	downloadID int64
	data       map[string]any
}

// appendHistory writes a history row with q (a transaction, or the writer).
func (s *Service) appendHistory(ctx context.Context, q *db.Queries, h historyEntry) error {
	raw, err := marshalObject(h.data)
	if err != nil {
		return fmt.Errorf("encode history data: %w", err)
	}
	p := db.AppendHistoryParams{
		Event: h.event, SourceTitle: h.title, CustomScore: h.score, Data: raw, CreatedAt: store.Millis(s.now()),
	}
	if h.movieID != 0 {
		p.MovieID = &h.movieID
	}
	if h.quality != "" {
		p.Quality = &h.quality
	}
	if h.downloadID != 0 {
		p.DownloadID = &h.downloadID
	}
	if _, err := q.AppendHistory(ctx, p); err != nil {
		return fmt.Errorf("append %s history: %w", h.event, err)
	}
	return nil
}

// writeHistory appends a history row outside a transaction, logging failures:
// a history write must never undo the action it records.
func (s *Service) writeHistory(ctx context.Context, h historyEntry) {
	if err := s.appendHistory(context.WithoutCancel(ctx), s.st.Write(), h); err != nil {
		s.log.Error("history write failed", slog.String("event", h.event), slog.Int64("movie_id", h.movieID),
			slog.Any("error", store.MapError(err)))
	}
}

// settingInt reads an integer setting, def when unset or invalid.
func (s *Service) settingInt(ctx context.Context, key string, def int) int {
	v, err := store.GetSetting[int](ctx, s.st, key)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Warn("invalid setting, using the default", slog.String("key", key), slog.Any("error", err))
		}
		return def
	}
	return v
}

func (s *Service) settingBool(ctx context.Context, key string) bool {
	v, err := store.GetSetting[bool](ctx, s.st, key)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("invalid setting, using the default", slog.String("key", key), slog.Any("error", err))
	}
	return err == nil && v
}

// tagsApply reports whether an indexer or client restricted to tags serves a
// movie with movieTags: no tag means every movie.
func tagsApply(tags, movieTags []string) bool {
	if len(tags) == 0 {
		return true
	}
	for _, t := range tags {
		for _, m := range movieTags {
			if strings.EqualFold(t, m) {
				return true
			}
		}
	}
	return false
}
