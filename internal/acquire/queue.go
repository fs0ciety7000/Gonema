package acquire

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Queue tracker timing.
const (
	TrackActiveInterval = 10 * time.Second // while downloads are in progress
	TrackIdleInterval   = 60 * time.Second // otherwise
	// persistEvery bounds how often the progress of a download is written; a
	// status change is written at once.
	persistEvery = time.Minute
	// vanishGrace: a download missing from its client right after the grab is
	// not yet listed (the client fetches it); after it, two consecutive misses
	// mark it removed.
	vanishGrace  = 2 * time.Minute
	vanishMisses = 2
	importTimout = 6 * time.Hour
)

// QueueItem is a download of the queue as shown to the user. It carries no
// download link.
type QueueItem struct {
	ID          int64      `json:"id"`
	MovieID     int64      `json:"movieId"`
	Title       string     `json:"title" doc:"Release title"`
	Protocol    string     `json:"protocol" enum:"usenet,torrent"`
	Size        int64      `json:"size"`
	Quality     string     `json:"quality"`
	QualityName string     `json:"qualityName"`
	CustomScore int64      `json:"customScore"`
	Status      string     `json:"status" enum:"queued,downloading,paused,completed,importing,imported,failed,warning,removed"`
	Progress    float64    `json:"progress" doc:"0 to 1"`
	SpeedBps    int64      `json:"speedBps"`
	EtaSeconds  *int64     `json:"etaSeconds"`
	ClientID    *int64     `json:"clientId"`
	Client      string     `json:"client"`
	IndexerID   *int64     `json:"indexerId"`
	Messages    []string   `json:"messages"`
	Manual      bool       `json:"manual"`
	RetryOf     *int64     `json:"retryOf"`
	GrabbedAt   time.Time  `json:"grabbedAt"`
	CompletedAt *time.Time `json:"completedAt"`
	ImportedAt  *time.Time `json:"importedAt"`
}

// live is the in-memory state of a tracked download.
type live struct {
	status      string
	progress    float64
	speed       int64
	eta         *int64
	message     string
	persistedAt time.Time
	misses      int
	clientDown  string // last client error, "" when reachable
}

type trackerState struct {
	mu        sync.Mutex // guards the maps below
	live      map[int64]*live
	importing map[int64]bool
	runMu     sync.Mutex // one TrackOnce at a time
	wakeCh    chan struct{}
	noImport  bool // "no importer" logged once
}

func (t *trackerState) init() {
	t.live = make(map[int64]*live)
	t.importing = make(map[int64]bool)
	t.wakeCh = make(chan struct{}, 1)
}

func (t *trackerState) wake() {
	select {
	case t.wakeCh <- struct{}{}:
	default:
	}
}

func (t *trackerState) get(id int64) *live {
	t.mu.Lock()
	defer t.mu.Unlock()
	l, ok := t.live[id]
	if !ok {
		l = &live{}
		t.live[id] = l
	}
	return l
}

func (t *trackerState) snapshot(id int64) (live, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l, ok := t.live[id]
	if !ok {
		return live{}, false
	}
	return *l, true
}

func (t *trackerState) forget(id int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.live, id)
}

// RunTracker polls the download clients until ctx ends: every
// TrackActiveInterval while downloads are active, TrackIdleInterval otherwise,
// and right after a grab.
func (s *Service) RunTracker(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-s.track.wakeCh:
		}
		n, err := s.TrackOnce(ctx)
		if err != nil && ctx.Err() == nil {
			s.log.Error("queue tracking failed", slog.Any("error", err))
		}
		d := TrackIdleInterval
		if n > 0 {
			d = TrackActiveInterval
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(d)
	}
}

// TrackOnce polls the clients of the active downloads once and reconciles the
// queue. It returns the number of active downloads.
func (s *Service) TrackOnce(ctx context.Context) (int, error) {
	s.track.runMu.Lock()
	defer s.track.runMu.Unlock()
	rows, err := s.st.Read().ListActiveDownloads(ctx)
	if err != nil {
		return 0, store.MapError(fmt.Errorf("acquire: list active downloads: %w", err))
	}
	if len(rows) == 0 {
		return 0, nil
	}
	clients, err := s.st.Read().ListDownloadClients(ctx)
	if err != nil {
		return 0, store.MapError(fmt.Errorf("acquire: list download clients: %w", err))
	}
	byID := make(map[int64]*db.DownloadClient, len(clients))
	for i := range clients {
		byID[clients[i].ID] = &clients[i]
	}
	mapper, err := s.pathMapper(ctx)
	if err != nil {
		return 0, err
	}
	groups := make(map[int64][]db.Download)
	var order []int64
	for i := range rows {
		cid := deref(rows[i].ClientID)
		if _, ok := groups[cid]; !ok {
			order = append(order, cid)
		}
		groups[cid] = append(groups[cid], rows[i])
	}
	for _, cid := range order {
		dls := groups[cid]
		c := byID[cid]
		if c == nil {
			for i := range dls {
				s.markRemoved(ctx, &dls[i], "The download client of this download was deleted: it is no longer tracked.")
			}
			continue
		}
		if !c.Enabled {
			continue
		}
		s.trackClient(ctx, c, dls, mapper)
	}
	return len(rows), nil
}

func (s *Service) trackClient(ctx context.Context, c *db.DownloadClient, dls []db.Download, mapper *download.PathMapper) {
	cl, err := s.downloadClient(c)
	if err == nil {
		var items []download.Item
		if items, err = cl.Items(ctx); err == nil {
			s.recordClientHealth(ctx, c, nil)
			byRef := make(map[string]*download.Item, len(items))
			for i := range items {
				byRef[strings.ToLower(items[i].Ref)] = &items[i]
			}
			for i := range dls {
				d := &dls[i]
				s.reconcile(ctx, c, cl, d, byRef[strings.ToLower(deref(d.ClientRef))], mapper)
			}
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	// Unreachable: keep the downloads as they are, with a warning.
	s.recordClientHealth(ctx, c, err)
	msg := fmt.Sprintf("%s cannot be reached: %s", c.Name, err.Error())
	s.log.Warn("download client unreachable", slog.String("client", c.Name), slog.Any("error", err))
	for i := range dls {
		l := s.track.get(dls[i].ID)
		s.track.mu.Lock()
		l.clientDown = msg
		s.track.mu.Unlock()
		// A completed download can still be imported without its client.
		if dls[i].Status == string(download.StatusCompleted) && dls[i].OutputPath != nil {
			s.startImport(ctx, c, &dls[i], *dls[i].OutputPath)
		}
	}
}

// reconcile applies the client state of one download.
func (s *Service) reconcile(ctx context.Context, c *db.DownloadClient, cl download.Client, d *db.Download, it *download.Item, mapper *download.PathMapper) {
	l := s.track.get(d.ID)
	s.track.mu.Lock()
	l.clientDown = ""
	s.track.mu.Unlock()
	if importFailed(d) {
		// The import failed (disk, permissions, path mapping): it runs again on
		// request only (RetryImport). Retrying at each poll would repeat the
		// failure and its history row every few seconds.
		s.track.mu.Lock()
		l.misses = 0
		s.track.mu.Unlock()
		return
	}
	if d.Status == string(download.StatusImporting) || (d.Status == string(download.StatusCompleted) && d.OutputPath != nil) {
		// Waiting for (or resuming) the import; the client state no longer matters.
		if d.OutputPath != nil {
			s.startImport(ctx, c, d, *d.OutputPath)
		}
		return
	}
	if it == nil {
		s.track.mu.Lock()
		l.misses++
		misses := l.misses
		s.track.mu.Unlock()
		if misses >= vanishMisses && s.now().Sub(store.FromMillis(d.GrabbedAt)) > vanishGrace {
			s.markRemoved(ctx, d, fmt.Sprintf("The download disappeared from %s (removed outside Gonema).", c.Name))
		}
		return
	}
	s.track.mu.Lock()
	l.misses = 0
	l.progress, l.speed, l.message = download.Clamp01(it.Progress), max(it.Speed, 0), it.Message
	l.eta = nil
	if it.ETA > 0 {
		l.eta = ptr(int64(it.ETA / time.Second))
	}
	l.status = string(it.Status)
	s.track.mu.Unlock()

	switch it.Status {
	case download.StatusFailed:
		reason := it.Message
		if reason == "" {
			reason = c.Name + " reported the download as failed."
		}
		s.handleFailed(ctx, c, cl, d, reason)
		return
	case download.StatusCompleted:
		if it.OutputPath == "" {
			s.persistState(ctx, d, string(download.StatusCompleted), l, "Waiting for "+c.Name+" to report where the files are.", false)
			return
		}
		local, mapped := mapper.Map(c.Host, it.OutputPath)
		msg := ""
		if !mapped {
			msg = "No remote path mapping applies: using the path reported by " + c.Name + " as is."
		}
		d.OutputPath = &local
		d.CompletedAt = ptr(store.Millis(s.now()))
		l.progress = 1
		s.persistState(ctx, d, string(download.StatusCompleted), l, msg, true)
		s.startImport(ctx, c, d, local)
		return
	}
	persist := string(it.Status) != d.Status || s.now().Sub(l.persistedAt) >= persistEvery
	if persist {
		s.persistState(ctx, d, string(it.Status), l, it.Message, string(it.Status) != d.Status)
	}
	s.publish(events.Event{Type: events.DownloadProgress, MovieID: d.MovieID, Data: s.queueItemLive(d, c.Name, l)})
}

// importFailed reports whether a download waits in warning after a failed
// import (completed in its client), as opposed to a warning reported by the
// client while downloading (stalled, unknown state).
func importFailed(d *db.Download) bool {
	return d.Status == string(download.StatusWarning) && d.CompletedAt != nil
}

// persistState writes the status and progress of a download. changed publishes
// download.updated.
func (s *Service) persistState(ctx context.Context, d *db.Download, status string, l *live, msg string, changed bool) {
	s.track.mu.Lock()
	p := db.UpdateDownloadStateParams{
		ID: d.ID, Status: status, Progress: l.progress, SpeedBps: l.speed, EtaS: l.eta, OutputPath: d.OutputPath,
		StatusMessages: jsonArray(messages(msg)), CompletedAt: d.CompletedAt, ImportedAt: d.ImportedAt,
	}
	l.persistedAt = s.now()
	s.track.mu.Unlock()
	row, err := s.st.Write().UpdateDownloadState(context.WithoutCancel(ctx), p)
	if err != nil {
		if err = store.MapError(err); errors.Is(err, store.ErrNotFound) {
			s.log.Debug("download left the queue meanwhile", slog.Int64("download_id", d.ID))
			return
		}
		s.log.Error("persist download state", slog.Int64("download_id", d.ID), slog.Any("error", err))
		return
	}
	*d = row
	if changed {
		s.publish(events.Event{Type: EventDownloadUpdated, MovieID: d.MovieID, Data: s.queueItem(d, "")})
	}
}

func messages(msg string) []string {
	if msg == "" {
		return []string{}
	}
	return []string{msg}
}

// setStatus writes a final status with a message and forgets the live state.
func (s *Service) setStatus(ctx context.Context, q *db.Queries, d *db.Download, status, msg string) error {
	p := db.UpdateDownloadStateParams{
		ID: d.ID, Status: status, Progress: d.Progress, SpeedBps: 0, OutputPath: d.OutputPath,
		StatusMessages: jsonArray(messages(msg)), CompletedAt: d.CompletedAt, ImportedAt: d.ImportedAt,
	}
	if status == string(download.StatusImported) {
		p.Progress = 1
	}
	row, err := q.UpdateDownloadState(ctx, p)
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return notFound("Download %d left the queue meanwhile.", d.ID)
		}
		return fmt.Errorf("update download %d: %w", d.ID, err)
	}
	*d = row
	return nil
}

// markRemoved records a download gone from its client.
func (s *Service) markRemoved(ctx context.Context, d *db.Download, reason string) {
	ctx = context.WithoutCancel(ctx)
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		if err := s.setStatus(ctx, q, d, string(download.StatusRemoved), reason); err != nil {
			return err
		}
		return s.appendHistory(ctx, q, historyEntry{
			movieID: d.MovieID, event: HistoryDownloadRemoved, title: d.ReleaseTitle, quality: d.Quality, downloadID: d.ID,
			data: map[string]any{"reason": reason},
		})
	})
	if err != nil {
		s.log.Error("mark download removed", slog.Int64("download_id", d.ID), slog.Any("error", err))
		return
	}
	s.track.forget(d.ID)
	s.log.Info("download removed", slog.Int64("download_id", d.ID), slog.String("release", d.ReleaseTitle), slog.String("reason", reason))
	s.publish(events.Event{Type: EventDownloadRemoved, MovieID: d.MovieID, Data: s.queueItem(d, "")})
}

// handleFailed records a failed download, blocklists the release (and its
// group when the setting asks for it), removes it from the client if
// configured, then retries with the next release.
func (s *Service) handleFailed(ctx context.Context, c *db.DownloadClient, cl download.Client, d *db.Download, reason string) {
	ctx = context.WithoutCancel(ctx)
	group := parser.Parse(d.ReleaseTitle).ReleaseGroup
	blockGroup := group != "" && s.settingBool(ctx, SettingBlocklistGroup)
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		if err := s.setStatus(ctx, q, d, string(download.StatusFailed), reason); err != nil {
			return err
		}
		if err := s.appendHistory(ctx, q, historyEntry{
			movieID: d.MovieID, event: HistoryDownloadFailed, title: d.ReleaseTitle, quality: d.Quality, downloadID: d.ID,
			data: map[string]any{"reason": reason, "client": clientName(c)},
		}); err != nil {
			return err
		}
		return s.blocklistDownload(ctx, q, d, reason, blockGroup)
	})
	if err != nil {
		s.log.Error("record failed download", slog.Int64("download_id", d.ID), slog.Any("error", err))
		return
	}
	s.track.forget(d.ID)
	s.log.Warn("download failed, release blocklisted", slog.Int64("movie_id", d.MovieID), slog.String("release", d.ReleaseTitle),
		slog.String("reason", reason))
	s.publish(events.Event{Type: EventDownloadFailed, MovieID: d.MovieID, Data: s.queueItem(d, clientName(c))})
	if c != nil && cl != nil && c.RemoveFailed && d.ClientRef != nil {
		if err := cl.Remove(ctx, *d.ClientRef, false); err != nil && !errors.Is(err, download.ErrNotFound) {
			s.log.Warn("remove failed download from client", slog.String("client", c.Name), slog.Any("error", err))
		}
	}
	s.scheduleRetry(ctx, d)
}

func clientName(c *db.DownloadClient) string {
	if c == nil {
		return ""
	}
	return c.Name
}

// blocklistDownload adds the release of a download to the blocklist of its
// movie, and its group when asked, with history rows.
func (s *Service) blocklistDownload(ctx context.Context, q *db.Queries, d *db.Download, reason string, group bool) error {
	p := db.InsertBlocklistParams{
		MovieID: &d.MovieID, Kind: string(decision.BlockRelease), SourceTitle: d.ReleaseTitle, IndexerID: d.IndexerID,
		Protocol: &d.Protocol, Reason: reason, CreatedAt: store.Millis(s.now()),
	}
	g := parser.Parse(d.ReleaseTitle).ReleaseGroup
	if g != "" {
		p.ReleaseGroup = &g
	}
	if d.Protocol == string(decision.Torrent) && d.ClientRef != nil && *d.ClientRef != "" {
		p.InfoHash = d.ClientRef
	}
	if _, err := q.InsertBlocklist(ctx, p); err != nil {
		return fmt.Errorf("blocklist release: %w", err)
	}
	data := map[string]any{"reason": reason, "kind": "release"}
	if group {
		gp := p
		gp.Kind, gp.InfoHash = string(decision.BlockGroup), nil
		if _, err := q.InsertBlocklist(ctx, gp); err != nil {
			return fmt.Errorf("blocklist group: %w", err)
		}
		data["group"] = g
		data["kind"] = "release and group"
	}
	return s.appendHistory(ctx, q, historyEntry{
		movieID: d.MovieID, event: HistoryBlocklisted, title: d.ReleaseTitle, quality: d.Quality, downloadID: d.ID, data: data,
	})
}

// startImport runs the import of a completed download in the background (one
// at a time per download).
func (s *Service) startImport(ctx context.Context, c *db.DownloadClient, d *db.Download, local string) {
	if s.importer == nil {
		s.track.mu.Lock()
		logged := s.track.noImport
		s.track.noImport = true
		s.track.mu.Unlock()
		if !logged {
			s.log.Warn("completed downloads wait: no importer is configured")
		}
		return
	}
	s.track.mu.Lock()
	if s.track.importing[d.ID] {
		s.track.mu.Unlock()
		return
	}
	s.track.importing[d.ID] = true
	s.track.mu.Unlock()

	dl := *d
	if dl.Status != string(download.StatusImporting) {
		l := s.track.get(dl.ID)
		s.track.mu.Lock()
		l.progress, l.speed, l.eta, l.status = 1, 0, nil, string(download.StatusImporting)
		s.track.mu.Unlock()
		s.persistState(ctx, &dl, string(download.StatusImporting), l, "", true)
	}
	client := *c
	s.goBackground(func(base context.Context) {
		defer func() {
			s.track.mu.Lock()
			delete(s.track.importing, dl.ID)
			s.track.mu.Unlock()
		}()
		ictx, cancel := context.WithTimeout(base, importTimout)
		defer cancel()
		s.runImport(ictx, &client, &dl, local)
		if s.onImportDone != nil {
			s.onImportDone(dl.ID)
		}
	})
}

func (s *Service) runImport(ctx context.Context, c *db.DownloadClient, d *db.Download, local string) {
	res, err := s.importer.Import(ctx, ImportRequest{
		DownloadID: d.ID, MovieID: d.MovieID, LocalPath: local, ReleaseTitle: d.ReleaseTitle,
		Protocol: decision.Protocol(d.Protocol), ClientSeeding: d.Protocol == string(decision.Torrent),
	})
	if ctx.Err() != nil && err != nil {
		return // shutdown: the import resumes at the next start (status importing)
	}
	switch {
	case err != nil:
		msg := "Import failed: " + err.Error()
		werr := s.st.WithTx(context.WithoutCancel(ctx), func(q *db.Queries) error {
			if err := s.setStatus(ctx, q, d, string(download.StatusWarning), msg); err != nil {
				return err
			}
			return s.appendHistory(ctx, q, historyEntry{
				movieID: d.MovieID, event: HistoryImportFailed, title: d.ReleaseTitle, quality: d.Quality, downloadID: d.ID,
				data: map[string]any{"reason": err.Error(), "path": local},
			})
		})
		if werr != nil {
			s.log.Error("record import failure", slog.Int64("download_id", d.ID), slog.Any("error", werr))
		}
		s.log.Warn("import failed", slog.Int64("download_id", d.ID), slog.String("release", d.ReleaseTitle), slog.Any("error", err))
		s.publish(events.Event{Type: EventDownloadUpdated, MovieID: d.MovieID, Data: s.queueItem(d, c.Name)})
	case !res.Imported:
		reason := res.Reason
		if reason == "" {
			reason = "the importer refused the files"
		}
		cl, _ := s.downloadClient(c)
		s.handleFailed(ctx, c, cl, d, "Import refused: "+reason)
	default:
		event := HistoryImported
		if res.Upgraded {
			event = HistoryUpgraded
		}
		d.ImportedAt = ptr(store.Millis(s.now()))
		werr := s.st.WithTx(context.WithoutCancel(ctx), func(q *db.Queries) error {
			if err := s.setStatus(ctx, q, d, string(download.StatusImported), ""); err != nil {
				return err
			}
			if s.impHist {
				return nil // the importer wrote the imported or upgraded row with the file
			}
			score := d.CustomScore
			return s.appendHistory(ctx, q, historyEntry{
				movieID: d.MovieID, event: event, title: d.ReleaseTitle, quality: d.Quality, score: &score, downloadID: d.ID,
				data: map[string]any{"fileId": res.FileID, "reason": res.Reason, "path": local, "upgraded": res.Upgraded},
			})
		})
		if werr != nil {
			s.log.Error("record import", slog.Int64("download_id", d.ID), slog.Any("error", werr))
			return
		}
		s.track.forget(d.ID)
		s.log.Info("download imported", slog.Int64("movie_id", d.MovieID), slog.String("release", d.ReleaseTitle), slog.Bool("upgrade", res.Upgraded))
		s.publish(events.Event{Type: EventDownloadImported, MovieID: d.MovieID, Data: s.queueItem(d, c.Name)})
		if c.RemoveCompleted && d.Protocol == string(decision.Usenet) && d.ClientRef != nil {
			if cl, err := s.downloadClient(c); err == nil {
				if err := cl.Remove(context.WithoutCancel(ctx), *d.ClientRef, false); err != nil && !errors.Is(err, download.ErrNotFound) {
					s.log.Warn("remove imported download from client", slog.String("client", c.Name), slog.Any("error", err))
				}
			}
		}
	}
}

// ----------------------------------------------------------------- views and actions

func (s *Service) queueItem(d *db.Download, client string) QueueItem {
	it := QueueItem{
		ID: d.ID, MovieID: d.MovieID, Title: d.ReleaseTitle, Protocol: d.Protocol, Size: d.Size, Quality: d.Quality,
		QualityName: quality.ID(d.Quality).Name(), CustomScore: d.CustomScore, Status: d.Status, Progress: d.Progress,
		SpeedBps: d.SpeedBps, EtaSeconds: d.EtaS, ClientID: d.ClientID, Client: client, IndexerID: d.IndexerID,
		Messages: decodeArray[string](d.StatusMessages), Manual: d.Manual, RetryOf: d.RetryOf,
		GrabbedAt: store.FromMillis(d.GrabbedAt), CompletedAt: timePtr(d.CompletedAt), ImportedAt: timePtr(d.ImportedAt),
	}
	return it
}

func (s *Service) queueItemLive(d *db.Download, client string, l *live) QueueItem {
	it := s.queueItem(d, client)
	s.track.mu.Lock()
	defer s.track.mu.Unlock()
	if l.status != "" && isActive(d.Status) && d.Status != string(download.StatusImporting) {
		it.Status = l.status
	}
	it.Progress, it.SpeedBps, it.EtaSeconds = l.progress, l.speed, l.eta
	if l.message != "" && !containsString(it.Messages, l.message) {
		it.Messages = append(it.Messages, l.message)
	}
	if l.clientDown != "" {
		it.Messages = append(it.Messages, l.clientDown)
	}
	return it
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Queue returns the active downloads with their live progress, oldest first.
func (s *Service) Queue(ctx context.Context) ([]QueueItem, error) {
	rows, err := s.st.Read().ListActiveDownloads(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("acquire: list queue: %w", err))
	}
	names := s.clientNames(ctx)
	out := make([]QueueItem, 0, len(rows))
	for i := range rows {
		d := &rows[i]
		if l, ok := s.track.snapshot(d.ID); ok {
			out = append(out, s.queueItemLive(d, names[deref(d.ClientID)], &l))
		} else {
			out = append(out, s.queueItem(d, names[deref(d.ClientID)]))
		}
	}
	return out, nil
}

func (s *Service) clientNames(ctx context.Context) map[int64]string {
	rows, err := s.st.Read().ListDownloadClients(ctx)
	if err != nil {
		return map[int64]string{}
	}
	m := make(map[int64]string, len(rows))
	for i := range rows {
		m[rows[i].ID] = rows[i].Name
	}
	return m
}

// queueEntry returns an active download with its client.
func (s *Service) queueEntry(ctx context.Context, id int64) (db.Download, *db.DownloadClient, download.Client, error) {
	d, err := s.st.Read().GetDownload(ctx, id)
	if err != nil {
		return d, nil, nil, mapNotFound(err, "Download %d is not in the queue.", id)
	}
	if !isActive(d.Status) {
		return d, nil, nil, notFound("Download %d is no longer in the queue (status %s).", id, d.Status)
	}
	if d.ClientID == nil {
		return d, nil, nil, nil
	}
	c, err := s.st.Read().GetDownloadClient(ctx, *d.ClientID)
	if err != nil {
		return d, nil, nil, mapNotFound(err, "The download client of download %d no longer exists.", id)
	}
	cl, err := s.downloadClient(&c)
	if err != nil {
		return d, &c, nil, newError(ErrUpstream, err, "The download client %s cannot be used: %s", c.Name, err.Error())
	}
	return d, &c, cl, nil
}

// SetPaused pauses or resumes a download in its client.
func (s *Service) SetPaused(ctx context.Context, id int64, paused bool) (QueueItem, error) {
	d, c, cl, err := s.queueEntry(ctx, id)
	if err != nil {
		return QueueItem{}, err
	}
	if cl == nil || d.ClientRef == nil {
		return QueueItem{}, invalid("This download has no client to pause or resume it.")
	}
	op, status := cl.Resume, download.StatusDownloading
	if paused {
		op, status = cl.Pause, download.StatusPaused
	}
	if err := op(ctx, *d.ClientRef); err != nil {
		if errors.Is(err, download.ErrNotFound) {
			return QueueItem{}, notFound("%s no longer has this download.", c.Name)
		}
		return QueueItem{}, newError(ErrUpstream, err, "%s did not accept the request: %s", c.Name, err.Error())
	}
	l := s.track.get(d.ID)
	s.track.mu.Lock()
	l.status = string(status)
	s.track.mu.Unlock()
	s.persistState(ctx, &d, string(status), l, "", true)
	s.track.wake()
	return s.queueItemLive(&d, c.Name, l), nil
}

// RemoveOptions configures RemoveFromQueue.
type RemoveOptions struct {
	Blocklist  bool // blocklist the release
	Retry      bool // grab the next release
	DeleteData bool // ask the client to delete the downloaded data (explicit user request only)
}

// RemoveFromQueue removes a download from its client and from the queue.
func (s *Service) RemoveFromQueue(ctx context.Context, id int64, opts RemoveOptions) error {
	d, c, cl, err := s.queueEntry(ctx, id)
	if err != nil {
		return err
	}
	if cl != nil && d.ClientRef != nil {
		if err := cl.Remove(ctx, *d.ClientRef, opts.DeleteData); err != nil && !errors.Is(err, download.ErrNotFound) {
			return newError(ErrUpstream, err, "%s could not remove the download: %s", c.Name, err.Error())
		}
	}
	reason := "Removed from the queue by the user."
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		if err := s.setStatus(ctx, q, &d, string(download.StatusRemoved), reason); err != nil {
			return err
		}
		if err := s.appendHistory(ctx, q, historyEntry{
			movieID: d.MovieID, event: HistoryDownloadRemoved, title: d.ReleaseTitle, quality: d.Quality, downloadID: d.ID,
			data: map[string]any{"reason": reason, "blocklist": opts.Blocklist, "retry": opts.Retry, "deleteData": opts.DeleteData},
		}); err != nil {
			return err
		}
		if opts.Blocklist {
			return s.blocklistDownload(ctx, q, &d, "Removed from the queue and blocklisted by the user.", false)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("acquire: remove download %d: %w", id, err)
	}
	s.track.forget(d.ID)
	s.log.Info("download removed by the user", slog.Int64("download_id", d.ID), slog.String("release", d.ReleaseTitle),
		slog.Bool("blocklist", opts.Blocklist), slog.Bool("retry", opts.Retry), slog.Bool("delete_data", opts.DeleteData))
	s.publish(events.Event{Type: EventDownloadRemoved, MovieID: d.MovieID, Data: s.queueItem(&d, clientName(c))})
	if opts.Retry {
		s.scheduleRetry(ctx, &d)
	}
	return nil
}
