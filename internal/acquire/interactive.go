package acquire

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/indexer"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/search"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// Interactive search cache bounds.
const (
	searchTTL         = 15 * time.Minute
	maxSearches       = 32
	maxSessionResults = 5000
)

// IndexerStatus is the progress of one indexer in an interactive search.
type IndexerStatus struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Status      string     `json:"status" enum:"pending,done,failed"`
	Count       int        `json:"count"`
	Error       string     `json:"error,omitempty"`
	WillRetryAt *time.Time `json:"willRetryAt,omitempty" doc:"When a paused indexer will be tried again"`
	DurationMS  int64      `json:"durationMs"`
}

// SearchResult is one release of an interactive search. It carries no
// download link: grab it with its key.
type SearchResult struct {
	Key         string                `json:"key" doc:"Opaque key of the release in this search, to grab it"`
	Title       string                `json:"title"`
	Size        int64                 `json:"size" doc:"Bytes, 0 if unknown"`
	Protocol    decision.Protocol     `json:"protocol" enum:"usenet,torrent"`
	IndexerID   int64                 `json:"indexerId"`
	Indexer     string                `json:"indexer"`
	Sources     []string              `json:"sources" doc:"Every indexer offering the release"`
	Seeders     int                   `json:"seeders" doc:"-1 if unknown"`
	Leechers    int                   `json:"leechers" doc:"-1 if unknown"`
	Grabs       int                   `json:"grabs" doc:"-1 if unknown"`
	PublishDate *time.Time            `json:"publishDate"`
	Flags       []string              `json:"flags"`
	InfoURL     string                `json:"infoUrl,omitempty" doc:"Details page on the indexer, credentials removed"`
	Quality     quality.ID            `json:"quality"`
	QualityName string                `json:"qualityName"`
	Parsed      parser.ParsedRelease  `json:"parsed"`
	Score       quality.Breakdown     `json:"score"`
	Accepted    bool                  `json:"accepted"`
	Rejections  []decision.Rejection  `json:"rejections"`
	Upgrade     *decision.UpgradeInfo `json:"upgrade,omitempty"`
	Notes       []string              `json:"notes,omitempty"`
	Rank        int                   `json:"rank" doc:"1 is the best release; ranks change while results arrive"`
}

// SearchSnapshot is the state of an interactive search.
type SearchSnapshot struct {
	ID         string          `json:"id"`
	MovieID    int64           `json:"movieId"`
	Status     string          `json:"status" enum:"running,finished,failed"`
	Error      string          `json:"error,omitempty"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	Indexers   []IndexerStatus `json:"indexers"`
	Results    []SearchResult  `json:"results" doc:"Best first"`
}

// Event data of the interactive search events.
type (
	// SearchBatchEvent is the data of search.batch.
	SearchBatchEvent struct {
		SearchID   string           `json:"searchId"`
		MovieID    int64            `json:"movieId"`
		Indexer    string           `json:"indexer"`
		Results    []SearchResult   `json:"results"`
		Duplicates []DuplicateEvent `json:"duplicates,omitempty"`
		Ranks      map[string]int   `json:"ranks" doc:"Current rank of every result of the search, by key"`
	}
	// DuplicateEvent tells that a release already sent is offered by another indexer.
	DuplicateEvent struct {
		Key     string `json:"key"`
		Indexer string `json:"indexer"`
	}
	// SearchIndexerEvent is the data of search.indexer_done and search.indexer_failed.
	SearchIndexerEvent struct {
		SearchID string        `json:"searchId"`
		MovieID  int64         `json:"movieId"`
		Indexer  IndexerStatus `json:"indexer"`
	}
	// SearchFinishedEvent is the data of search.finished.
	SearchFinishedEvent struct {
		SearchID   string `json:"searchId"`
		MovieID    int64  `json:"movieId"`
		Status     string `json:"status"`
		Total      int    `json:"total"`
		Accepted   int    `json:"accepted"`
		Succeeded  int    `json:"succeeded"`
		Failed     int    `json:"failed"`
		DurationMS int64  `json:"durationMs"`
	}
)

type sessionResult struct {
	rel     indexer.Release
	ev      decision.Evaluated
	sources []string
	rank    int
}

type session struct {
	id      string
	movieID int64
	started time.Time

	mu       sync.Mutex
	status   string
	errMsg   string
	finished time.Time
	indexers []IndexerStatus
	results  map[string]*sessionResult // by opaque key
	byCand   map[string]string         // candKey -> opaque key
	order    []string                  // opaque keys, best first
}

// searchCache holds the recent interactive searches.
type searchCache struct {
	now   func() time.Time
	mu    sync.Mutex
	m     map[string]*session
	order []string
}

func newSearchCache(now func() time.Time) *searchCache {
	return &searchCache{now: now, m: make(map[string]*session)}
}

func (c *searchCache) add(s *session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeLocked()
	c.m[s.id] = s
	c.order = append(c.order, s.id)
	for len(c.order) > maxSearches {
		delete(c.m, c.order[0])
		c.order = c.order[1:]
	}
}

func (c *searchCache) get(id string) (*session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeLocked()
	s, ok := c.m[id]
	return s, ok
}

func (c *searchCache) purgeLocked() {
	now := c.now()
	for len(c.order) > 0 {
		s := c.m[c.order[0]]
		if s != nil && now.Sub(s.started) <= searchTTL {
			return
		}
		delete(c.m, c.order[0])
		c.order = c.order[1:]
	}
}

func newSearchID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails (Go 1.24+)
	return hex.EncodeToString(b[:])
}

// releaseKey is the opaque key of a release in API views: a hash, so that a
// GUID holding a download link never leaves the server.
func releaseKey(searchID, candKey string) string {
	h := sha256.Sum256([]byte(searchID + "\x00" + candKey))
	return hex.EncodeToString(h[:12])
}

// StartInteractive starts an interactive search of a movie and returns at
// once. Results stream as search.* events and are kept for GetSearch.
func (s *Service) StartInteractive(ctx context.Context, movieID int64) (SearchSnapshot, error) {
	mc, err := s.loadMovie(ctx, movieID, nil, decision.Options{Interactive: true})
	if err != nil {
		return SearchSnapshot{}, err
	}
	ev, err := decision.NewEvaluator(mc.cfg)
	if err != nil {
		return SearchSnapshot{}, unusableProfile(err)
	}
	rows, err := s.st.Read().ListIndexers(ctx)
	if err != nil {
		return SearchSnapshot{}, store.MapError(fmt.Errorf("acquire: list indexers: %w", err))
	}
	ixs, used := s.indexersFor(rows, forInteractive, mc.tags, false)
	if len(ixs) == 0 {
		return SearchSnapshot{}, newError(ErrUnavailable, nil, "No enabled indexer can search for this movie: add one in Settings > Indexers.")
	}
	sess := &session{
		id: newSearchID(), movieID: movieID, started: s.now(), status: "running",
		results: make(map[string]*sessionResult), byCand: make(map[string]string),
	}
	for i := range used {
		sess.indexers = append(sess.indexers, IndexerStatus{ID: used[i].ID, Name: used[i].Name, Status: "pending"})
	}
	s.searches.add(sess)
	s.log.Info("interactive search started", slog.Int64("movie_id", movieID), slog.String("search_id", sess.id),
		slog.Int("indexers", len(ixs)))
	q := mc.query()
	s.goBackground(func(base context.Context) {
		sctx, cancel := context.WithTimeout(base, searchTimeout)
		defer cancel()
		s.runInteractive(sctx, sess, mc, ev, q, ixs)
		s.persistHealth(base, used)
	})
	return sess.snapshot(), nil
}

func (s *Service) runInteractive(ctx context.Context, sess *session, mc *movieContext, ev *decision.Evaluator, q indexer.Query, ixs []indexer.Indexer) {
	start := time.Now()
	for e := range s.orch.Search(ctx, q, ixs) {
		switch e := e.(type) {
		case *search.ResultBatch:
			s.publish(events.Event{Type: EventSearchBatch, MovieID: sess.movieID, Data: sess.addBatch(e, mc, ev)})
		case *search.IndexerDone:
			st := sess.indexerDone(e.Indexer.ID, IndexerStatus{Status: "done", Count: e.Count, DurationMS: e.Duration.Milliseconds()})
			s.publish(events.Event{Type: EventSearchIndexerDone, MovieID: sess.movieID, Data: SearchIndexerEvent{SearchID: sess.id, MovieID: sess.movieID, Indexer: st}})
		case *search.IndexerFailed:
			st := IndexerStatus{Status: "failed", Error: e.Err.Error(), DurationMS: e.Duration.Milliseconds()}
			if !e.WillRetryAt.IsZero() {
				t := e.WillRetryAt.UTC()
				st.WillRetryAt = &t
			}
			st = sess.indexerDone(e.Indexer.ID, st)
			s.publish(events.Event{Type: EventSearchIndexerFailed, MovieID: sess.movieID, Data: SearchIndexerEvent{SearchID: sess.id, MovieID: sess.movieID, Indexer: st}})
		case *search.Finished:
			fin := sess.finish(s.now(), "finished", "")
			fin.Succeeded, fin.Failed, fin.DurationMS = e.Succeeded, e.Failed, time.Since(start).Milliseconds()
			s.publish(events.Event{Type: EventSearchFinished, MovieID: sess.movieID, Data: fin})
			s.log.Info("interactive search finished", slog.String("search_id", sess.id), slog.Int("results", fin.Total),
				slog.Int("accepted", fin.Accepted), slog.Int("failed_indexers", e.Failed))
			return
		}
	}
	fin := sess.finish(s.now(), "failed", "The search was interrupted before every indexer answered.")
	fin.DurationMS = time.Since(start).Milliseconds()
	s.publish(events.Event{Type: EventSearchFinished, MovieID: sess.movieID, Data: fin})
}

func (sess *session) addBatch(b *search.ResultBatch, mc *movieContext, ev *decision.Evaluator) SearchBatchEvent {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	out := SearchBatchEvent{SearchID: sess.id, MovieID: sess.movieID, Indexer: b.Indexer.Name, Results: []SearchResult{}}
	var added []string
	for i := range b.Releases {
		if len(sess.results) >= maxSessionResults {
			break
		}
		rel := b.Releases[i]
		c := rel.Candidate()
		c.AlreadyGrabbed = mc.tried(rel.Title)
		ck := candKey(&c)
		k := releaseKey(sess.id, ck)
		if _, dup := sess.results[k]; dup {
			continue
		}
		sess.results[k] = &sessionResult{rel: rel, ev: decision.Evaluated{Candidate: c, Decision: ev.Evaluate(&c)}, sources: []string{rel.IndexerName}}
		sess.byCand[ck] = k
		added = append(added, k)
	}
	for _, d := range b.Duplicates {
		if k, ok := sess.byCand[d.Key]; ok {
			r := sess.results[k]
			r.sources = append(r.sources, d.Source.IndexerName)
			out.Duplicates = append(out.Duplicates, DuplicateEvent{Key: k, Indexer: d.Source.IndexerName})
		}
	}
	sess.rerankLocked(ev)
	for _, k := range added {
		out.Results = append(out.Results, sess.results[k].view(k))
	}
	out.Ranks = make(map[string]int, len(sess.order))
	for i, k := range sess.order {
		out.Ranks[k] = i + 1
	}
	return out
}

func (sess *session) rerankLocked(ev *decision.Evaluator) {
	evs := make([]decision.Evaluated, 0, len(sess.results))
	for _, r := range sess.results {
		evs = append(evs, r.ev)
	}
	ranked := ev.Rank(evs)
	sess.order = sess.order[:0]
	for i := range ranked {
		k := sess.byCand[candKey(&ranked[i].Candidate)]
		sess.results[k].rank = i + 1
		sess.order = append(sess.order, k)
	}
}

func (sess *session) indexerDone(id int64, st IndexerStatus) IndexerStatus {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	for i := range sess.indexers {
		if sess.indexers[i].ID == id {
			st.ID, st.Name = id, sess.indexers[i].Name
			sess.indexers[i] = st
			return st
		}
	}
	return st
}

func (sess *session) finish(now time.Time, status, msg string) SearchFinishedEvent {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.status, sess.errMsg, sess.finished = status, msg, now
	fin := SearchFinishedEvent{SearchID: sess.id, MovieID: sess.movieID, Status: status, Total: len(sess.results)}
	for _, r := range sess.results {
		if r.ev.Decision.Accepted {
			fin.Accepted++
		}
	}
	return fin
}

func (sess *session) snapshot() SearchSnapshot {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	snap := SearchSnapshot{
		ID: sess.id, MovieID: sess.movieID, Status: sess.status, Error: sess.errMsg, StartedAt: sess.started.UTC(),
		Indexers: append([]IndexerStatus{}, sess.indexers...), Results: make([]SearchResult, 0, len(sess.order)),
	}
	if !sess.finished.IsZero() {
		t := sess.finished.UTC()
		snap.FinishedAt = &t
	}
	for _, k := range sess.order {
		snap.Results = append(snap.Results, sess.results[k].view(k))
	}
	return snap
}

func (r *sessionResult) view(key string) SearchResult {
	d := &r.ev.Decision
	v := SearchResult{
		Key: key, Title: r.rel.Title, Size: r.rel.Size, Protocol: r.rel.Protocol, IndexerID: r.rel.IndexerID,
		Indexer: r.rel.IndexerName, Sources: append([]string{}, r.sources...), Seeders: r.rel.Seeders,
		Leechers: r.rel.Leechers, Grabs: r.rel.Grabs, Flags: append([]string{}, r.rel.Flags...),
		InfoURL: RedactURL(r.rel.InfoURL, false), Quality: d.Quality, QualityName: d.Quality.Name(),
		Parsed: r.ev.Candidate.Parsed, Score: d.Score, Accepted: d.Accepted,
		Rejections: append([]decision.Rejection{}, d.Rejections...), Upgrade: d.Upgrade, Notes: d.Notes, Rank: r.rank,
	}
	if !r.rel.PublishDate.IsZero() {
		t := r.rel.PublishDate.UTC()
		v.PublishDate = &t
	}
	return v
}

// GetSearch returns the state of an interactive search.
func (s *Service) GetSearch(id string) (SearchSnapshot, error) {
	sess, ok := s.searches.get(id)
	if !ok {
		return SearchSnapshot{}, notFound("This search has expired or does not exist: search again.")
	}
	return sess.snapshot(), nil
}

// GrabFromSearch grabs a release of an interactive search. A rejected release
// needs override: the grab is then recorded as an override with its reasons.
func (s *Service) GrabFromSearch(ctx context.Context, searchID, key string, override bool) (QueueItem, error) {
	sess, ok := s.searches.get(searchID)
	if !ok {
		return QueueItem{}, notFound("This search has expired or does not exist: search again.")
	}
	sess.mu.Lock()
	r, ok := sess.results[key]
	var rel indexer.Release
	var dec decision.Decision
	if ok {
		rel, dec = r.rel, r.ev.Decision
	}
	sess.mu.Unlock()
	if !ok {
		return QueueItem{}, notFound("This release is not part of the search.")
	}
	if !dec.Accepted && !override {
		return QueueItem{}, &RefusedError{Title: rel.Title, Rejections: dec.Rejections}
	}
	return s.grab(ctx, grabRequest{movieID: sess.movieID, rel: rel, dec: dec, manual: true})
}
