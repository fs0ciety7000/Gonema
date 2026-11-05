package mediaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// EventFileImported is the event the importer publishes after an import
// (importer.EventFileImported; mirrored to keep this package free of the
// importer's dependencies, checked by a test). Its data has "upgraded".
const EventFileImported events.Type = "moviefile.imported"

// Start subscribes to the bus and starts the workers. Without a bus it only
// starts the workers (tests call Refresh directly).
func (s *Service) Start() {
	for range s.workers {
		s.wg.Add(1)
		go s.worker()
	}
	if s.bus == nil {
		return
	}
	s.sub = s.bus.Subscribe(eventBuffer, func(e *events.Event) bool {
		switch e.Type {
		case EventFileImported, events.MovieFileRenamed, events.MovieFileDeleted:
			return true
		}
		return false
	})
	s.wg.Add(1)
	go s.listen()
}

// Close stops following the bus, drops the pending refreshes and waits for
// the running ones (bounded by their timeout). Safe to call more than once.
func (s *Service) Close() {
	s.once.Do(func() {
		if s.sub != nil {
			s.sub.Close()
		}
		if n := s.deb.close(); n > 0 {
			s.log.Info("media server refreshes dropped at shutdown", slog.Int("count", n))
		}
		s.cancel()
		s.wg.Wait()
	})
}

func (s *Service) listen() {
	defer s.wg.Done()
	var reported uint64
	for e := range s.sub.C() {
		if d := s.sub.Dropped(); d > reported {
			s.log.Warn("media server refresher fell behind: some refreshes were skipped", slog.Uint64("dropped", d-reported))
			reported = d
		}
		s.handle(&e)
	}
}

// Request asks every enabled server whose refresh_on includes reason to rescan
// folder (after the debounce window).
func (s *Service) Request(ctx context.Context, folder string, reason Reason) error {
	if folder == "" || !reason.Valid() {
		return nil
	}
	rows, err := s.st.Read().ListEnabledMediaServers(ctx)
	if err != nil {
		return store.MapError(fmt.Errorf("mediaserver: list enabled: %w", err))
	}
	for i := range rows {
		if slices.Contains(decodeJSON[[]Reason](rows[i].RefreshOn), reason) {
			s.deb.add(target{server: rows[i].ID, folder: folder}, reason)
		}
	}
	return nil
}

func (s *Service) handle(e *events.Event) {
	data := dataMap(e.Data)
	var reason Reason
	switch e.Type {
	case EventFileImported:
		reason = ReasonAdd
		if up, _ := data["upgraded"].(bool); up {
			reason = ReasonUpgrade
		}
	case events.MovieFileRenamed:
		reason = ReasonRename
	case events.MovieFileDeleted:
		reason = ReasonDelete
	default:
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	folder, _ := data["path"].(string)
	if !filepath.IsAbs(folder) {
		var err error
		if folder, err = s.movieFolder(ctx, e.MovieID); err != nil {
			s.log.Warn("media server refresh skipped: movie folder unknown", slog.Int64("movie_id", e.MovieID), slog.Any("error", err))
			return
		}
	}
	if err := s.Request(ctx, folder, reason); err != nil {
		s.log.Error("media server refresh not scheduled", slog.Int64("movie_id", e.MovieID), slog.Any("error", err))
	}
}

// movieFolder returns the folder of a movie as Gonema sees it.
func (s *Service) movieFolder(ctx context.Context, movieID int64) (string, error) {
	if movieID == 0 {
		return "", fmt.Errorf("no movie")
	}
	m, err := s.st.Read().GetMovie(ctx, movieID)
	if err != nil {
		return "", store.MapError(err)
	}
	rf, err := s.st.Read().GetRootFolder(ctx, m.RootFolderID)
	if err != nil {
		return "", store.MapError(err)
	}
	return joinUnder(rf.Path, m.Path)
}

// joinUnder joins a relative movie path to its root folder; the result must
// stay inside the root.
func joinUnder(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("movie path %q is not relative", rel)
	}
	p := filepath.Join(root, rel)
	if r, err := filepath.Rel(root, p); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("movie path %q leaves its root folder", rel)
	}
	return p, nil
}

// dataMap returns event data as a map: maps are used as is, structs go
// through their JSON form.
func dataMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// enqueue hands a debounced refresh to the workers; it never blocks (the
// debouncer's timer goroutine calls it).
func (s *Service) enqueue(t target, r Reason) {
	select {
	case s.work <- refreshJob{t: t, reason: r}:
	default:
		s.log.Warn("media server refresh dropped: too many pending", slog.Int64("media_server_id", t.server))
	}
}

func (s *Service) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case j := <-s.work:
			s.run(j)
		}
	}
}

// run performs one refresh with retries, then records the outcome.
func (s *Service) run(j refreshJob) {
	ctx, cancel := context.WithTimeout(s.ctx, refreshTimeout)
	defer cancel()
	r, err := s.st.Read().GetMediaServer(ctx, j.t.server)
	if err != nil {
		return // removed meanwhile
	}
	if !r.Enabled || !slices.Contains(decodeJSON[[]Reason](r.RefreshOn), j.reason) {
		return
	}
	cfg, err := s.config(&r)
	if err == nil {
		var srv Server
		if srv, err = s.newServer(cfg, s.sopts); err == nil {
			err = s.retry.Do(ctx, func(ctx context.Context) error {
				return srv.Refresh(ctx, RefreshRequest{Path: j.t.folder, Reason: j.reason})
			})
		}
	}
	if s.ctx.Err() != nil {
		return // shutdown: not a server failure
	}
	if err != nil {
		s.recordFailure(&r, err)
		return
	}
	s.log.Info("media server refreshed", slog.String("server", r.Name), slog.String("folder", j.t.folder), slog.String("reason", string(j.reason)))
	s.recordOK(ctx, &r)
}

// recordOK clears the failures of a server and publishes its recovery.
func (s *Service) recordOK(ctx context.Context, r *db.MediaServer) {
	ctx = context.WithoutCancel(ctx)
	if err := store.RequireRow(s.st.Write().RecordMediaServerOK(ctx, db.RecordMediaServerOKParams{ID: r.ID, At: nowMillis(s.now)})); err != nil {
		s.log.Error("record media server health", slog.Int64("media_server_id", r.ID), slog.Any("error", err))
		return
	}
	if r.FailureCount > 0 {
		s.publishHealth(r, nil)
	}
}

// recordFailure counts a failed refresh. The first failure after a success
// raises a health issue (the refresh was retried already).
func (s *Service) recordFailure(r *db.MediaServer, cause error) {
	msg := cause.Error() // redacted by the implementations
	s.log.Warn("media server refresh failed", slog.String("server", r.Name), slog.String("kind", r.Kind), slog.String("error", msg))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := s.st.Write().RecordMediaServerFailure(ctx, db.RecordMediaServerFailureParams{ID: r.ID, LastError: &msg, At: nowMillis(s.now)})
	if err != nil {
		s.log.Error("record media server health", slog.Int64("media_server_id", r.ID), slog.Any("error", store.MapError(err)))
		return
	}
	if n == 1 {
		s.publishHealth(r, cause)
	}
}

func (s *Service) publishHealth(r *db.MediaServer, cause error) {
	if s.bus == nil {
		return
	}
	ev := HealthEvent{Source: HealthSource, ID: r.ID, Name: r.Name, OK: cause == nil}
	if cause != nil {
		ev.Message = fmt.Sprintf("%s could not refresh its library: %s. Gonema keeps importing; the server catches up at its next scan.",
			r.Name, cause.Error())
	}
	s.bus.Publish(events.Event{Type: events.HealthChanged, Data: ev})
}

func nowMillis(now func() time.Time) *int64 {
	ms := store.Millis(now())
	return &ms
}
