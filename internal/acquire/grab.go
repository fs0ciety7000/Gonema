package acquire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/indexer"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// addTimeout bounds the call that hands a release to a download client.
const addTimeout = 60 * time.Second

// grabRequest is one grab.
type grabRequest struct {
	movieID int64
	rel     indexer.Release
	dec     decision.Decision
	manual  bool  // started by the user (interactive search)
	retryOf int64 // the failed download this grab replaces, 0 if none
	reason  string
}

// decisionSnapshot is the decision stored with a download (downloads.decision).
// It holds no link.
type decisionSnapshot struct {
	decision.Decision
	Parsed      parser.ParsedRelease `json:"parsed"`
	IndexerID   int64                `json:"indexerId"`
	Indexer     string               `json:"indexer"`
	Protocol    decision.Protocol    `json:"protocol"`
	Seeders     int                  `json:"seeders"`
	PublishDate time.Time            `json:"publishDate"`
	Flags       []string             `json:"flags,omitempty"`
	Manual      bool                 `json:"manual"`
}

// Grab sends a release to a download client and tracks it. A rejected release
// is refused (ErrRefused with the reasons) unless manual is set: the grab is
// then an explicit override, recorded as such. The .nzb or .torrent is fetched
// by the server: its link never leaves it.
func (s *Service) Grab(ctx context.Context, movieID int64, rel indexer.Release, dec decision.Decision, manual bool) (QueueItem, error) {
	return s.grab(ctx, grabRequest{movieID: movieID, rel: rel, dec: dec, manual: manual})
}

func (s *Service) grab(ctx context.Context, g grabRequest) (QueueItem, error) {
	if !g.dec.Accepted {
		if !g.manual {
			return QueueItem{}, &RefusedError{Title: g.rel.Title, Rejections: g.dec.Rejections}
		}
		g.dec.Overridden = true
	}
	movie, err := s.st.Read().GetMovie(ctx, g.movieID)
	if err != nil {
		return QueueItem{}, mapNotFound(err, "Movie %d is not in the library.", g.movieID)
	}
	tagRows, err := s.st.Read().ListMovieTags(ctx, g.movieID)
	if err != nil {
		return QueueItem{}, store.MapError(fmt.Errorf("acquire: movie tags: %w", err))
	}
	var tags []string
	for _, t := range tagRows {
		tags = append(tags, t.Label)
	}
	clients, err := s.clientsFor(ctx, g.rel.Protocol, tags)
	if err != nil {
		return QueueItem{}, err
	}
	if len(clients) == 0 {
		err := newError(ErrUnavailable, nil, "No enabled download client handles %s releases for this movie: add one in Settings > Download clients.", protocolName(g.rel.Protocol))
		s.grabFailed(ctx, &g, err.Msg)
		return QueueItem{}, err
	}
	pl, err := s.fetchRelease(ctx, &g.rel)
	if err != nil {
		var ae *Error
		msg := "the release file could not be downloaded"
		if errors.As(err, &ae) {
			msg = ae.Msg
		}
		s.grabFailed(ctx, &g, msg)
		return QueueItem{}, err
	}
	seedRatio, seedTime := s.seedLimits(ctx, g.rel.IndexerID)

	var (
		ref     string
		used    *db.DownloadClient
		reasons []string
	)
	for i := range clients {
		c := &clients[i]
		cl, err := s.downloadClient(c)
		if err != nil {
			reasons = append(reasons, err.Error())
			continue
		}
		req := download.AddRequest{Protocol: g.rel.Protocol, Priority: download.PriorityNormal, SeedRatio: seedRatio, SeedTime: seedTime}
		if pl.magnet != "" {
			req.URL = pl.magnet
		} else {
			req.Data, req.Filename = pl.data, pl.filename
		}
		if g.rel.Protocol == decision.Usenet {
			req.Name = g.rel.Title
		}
		actx, cancel := context.WithTimeout(ctx, addTimeout)
		ref, err = cl.Add(actx, req)
		cancel()
		if !errors.Is(err, download.ErrRejected) && !errors.Is(err, download.ErrInvalidRequest) {
			s.recordClientHealth(ctx, c, err)
		}
		if err == nil {
			used = c
			break
		}
		reasons = append(reasons, err.Error())
		s.log.Warn("download client refused a release", slog.String("client", c.Name), slog.String("release", g.rel.Title),
			slog.Any("error", err))
	}
	if used == nil {
		msg := "every download client failed: " + strings.Join(reasons, "; ")
		s.grabFailed(ctx, &g, msg)
		return QueueItem{}, newError(ErrUpstream, nil, "The download client could not add the release: %s", strings.Join(reasons, "; "))
	}
	if ref == "" {
		ref = pl.infoHash
	}
	ref = strings.ToLower(ref)
	dl, err := s.recordGrab(ctx, &g, &movie, used, ref)
	if err != nil {
		return QueueItem{}, err
	}
	item := s.queueItem(&dl, used.Name)
	s.log.Info("release grabbed", slog.Int64("movie_id", movie.ID), slog.String("movie", movie.Title),
		slog.String("release", g.rel.Title), slog.String("indexer", g.rel.IndexerName), slog.String("client", used.Name),
		slog.String("quality", string(g.dec.Quality)), slog.Int("score", g.dec.Score.Total),
		slog.Bool("manual", g.manual), slog.Bool("overridden", g.dec.Overridden), slog.Int64("retry_of", g.retryOf))
	s.publish(events.Event{Type: EventDownloadGrabbed, MovieID: movie.ID, Data: item})
	s.track.wake()
	return item, nil
}

// recordGrab inserts the download and its history row in one transaction.
func (s *Service) recordGrab(ctx context.Context, g *grabRequest, movie *db.Movie, c *db.DownloadClient, ref string) (db.Download, error) {
	snap, err := json.Marshal(decisionSnapshot{
		Decision: g.dec, Parsed: parser.Parse(g.rel.Title), IndexerID: g.rel.IndexerID, Indexer: g.rel.IndexerName,
		Protocol: g.rel.Protocol, Seeders: g.rel.Seeders, PublishDate: g.rel.PublishDate, Flags: g.rel.Flags, Manual: g.manual,
	})
	if err != nil {
		return db.Download{}, fmt.Errorf("acquire: encode decision: %w", err)
	}
	q := g.dec.Quality
	if q == "" {
		q = quality.Unknown
	}
	p := db.InsertDownloadParams{
		MovieID: movie.ID, ClientID: &c.ID, ClientRef: &ref, ReleaseTitle: g.rel.Title,
		ReleaseGuid: RedactURL(g.rel.GUID, true), InfoUrl: RedactURL(g.rel.InfoURL, false),
		Protocol: string(g.rel.Protocol), Size: max(g.rel.Size, 0), Quality: string(q),
		Revision: int64(max(parser.Parse(g.rel.Title).Revision.Version, 1)), CustomScore: int64(g.dec.Score.Total),
		Decision: string(snap), GrabbedAt: store.Millis(s.now()), Manual: g.manual,
	}
	if g.rel.IndexerID > 0 {
		p.IndexerID = &g.rel.IndexerID
	}
	if g.retryOf > 0 {
		p.RetryOf = &g.retryOf
	}
	event, reason := HistoryGrabbed, g.reason
	if g.dec.Overridden {
		event = HistoryGrabOverridden
		reason = "Grabbed on explicit request despite: " + rejectionText(g.dec.Rejections)
	}
	if reason == "" {
		reason = fmt.Sprintf("Best accepted release: %s, score %d", q.Name(), g.dec.Score.Total)
		if g.manual {
			reason = fmt.Sprintf("Grabbed from an interactive search: %s, score %d", q.Name(), g.dec.Score.Total)
		}
	}
	var dl db.Download
	err = s.st.WithTx(ctx, func(tx *db.Queries) error {
		var err error
		if dl, err = tx.InsertDownload(ctx, p); err != nil {
			if errors.Is(store.MapError(err), store.ErrConflict) {
				return conflict("This release is already in the queue of %s.", c.Name)
			}
			return fmt.Errorf("insert download: %w", err)
		}
		score := int64(g.dec.Score.Total)
		return s.appendHistory(ctx, tx, historyEntry{
			movieID: movie.ID, event: event, title: g.rel.Title, quality: string(q), score: &score, downloadID: dl.ID,
			data: map[string]any{
				"reason": reason, "indexer": g.rel.IndexerName, "indexerId": g.rel.IndexerID, "client": c.Name,
				"clientId": c.ID, "protocol": g.rel.Protocol, "size": g.rel.Size, "score": g.dec.Score,
				"rejections": g.dec.Rejections, "upgrade": g.dec.Upgrade, "manual": g.manual, "retryOf": g.retryOf,
			},
		})
	})
	if err != nil {
		var ae *Error
		if errors.As(err, &ae) {
			return db.Download{}, err
		}
		return db.Download{}, fmt.Errorf("acquire: record grab of %q: %w", g.rel.Title, err)
	}
	return dl, nil
}

// grabFailed records a failed grab in history.
func (s *Service) grabFailed(ctx context.Context, g *grabRequest, reason string) {
	s.log.Warn("grab failed", slog.Int64("movie_id", g.movieID), slog.String("release", g.rel.Title),
		slog.String("indexer", g.rel.IndexerName), slog.String("reason", reason))
	s.writeHistory(ctx, historyEntry{
		movieID: g.movieID, event: HistoryGrabFailed, title: g.rel.Title, quality: string(g.dec.Quality),
		data: map[string]any{"reason": reason, "indexer": g.rel.IndexerName, "manual": g.manual, "retryOf": g.retryOf},
	})
}

func rejectionText(rs []decision.Rejection) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, r.Message)
	}
	return strings.Join(parts, "; ")
}

func protocolName(p decision.Protocol) string {
	if p == decision.Torrent {
		return "BitTorrent"
	}
	return "Usenet"
}

// clientsFor returns the enabled clients of a protocol that serve a movie with
// these tags, preferred first.
func (s *Service) clientsFor(ctx context.Context, p decision.Protocol, movieTags []string) ([]db.DownloadClient, error) {
	rows, err := s.st.Read().ListDownloadClients(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("acquire: list download clients: %w", err))
	}
	var out []db.DownloadClient
	for i := range rows {
		if r := &rows[i]; r.Enabled && r.Protocol == string(p) && tagsApply(decodeArray[string](r.Tags), movieTags) {
			out = append(out, *r)
		}
	}
	return out, nil
}

// seedLimits returns the seeding limits of the indexer of a torrent.
func (s *Service) seedLimits(ctx context.Context, indexerID int64) (ratio float64, seedTime time.Duration) {
	if indexerID <= 0 {
		return 0, 0
	}
	r, err := s.st.Read().GetIndexer(ctx, indexerID)
	if err != nil {
		return 0, 0
	}
	if r.SeedRatio != nil {
		ratio = *r.SeedRatio
	}
	if r.SeedTimeMin != nil {
		seedTime = time.Duration(*r.SeedTimeMin) * time.Minute
	}
	return ratio, seedTime
}
