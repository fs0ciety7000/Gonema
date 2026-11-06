package acquire

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// History events of the retry chain.
const (
	HistoryRetriesStopped = "retries_stopped"
	HistoryRetriesResumed = "retries_resumed"
)

// retryChain returns the downloads of the retry chain of a download (linked
// by retry_of, in both directions), oldest first, and the download itself.
func (s *Service) retryChain(ctx context.Context, id int64) (db.Download, []db.Download, error) {
	d, err := s.st.Read().GetDownload(ctx, id)
	if err != nil {
		return db.Download{}, nil, mapNotFound(err, "Download %d does not exist.", id)
	}
	all, err := s.st.Read().ListMovieDownloads(ctx, d.MovieID)
	if err != nil {
		return db.Download{}, nil, store.MapError(fmt.Errorf("acquire: downloads of movie %d: %w", d.MovieID, err))
	}
	return d, chainOf(all, id), nil
}

// chainOf returns the downloads of all linked by retry_of to id (id included),
// oldest first. Pure: the links are followed in memory; cycles cannot loop.
func chainOf(all []db.Download, id int64) []db.Download {
	byID := make(map[int64]*db.Download, len(all))
	children := make(map[int64][]int64, len(all))
	for i := range all {
		d := &all[i]
		byID[d.ID] = d
		if d.RetryOf != nil {
			children[*d.RetryOf] = append(children[*d.RetryOf], d.ID)
		}
	}
	root := id
	seen := map[int64]bool{root: true}
	for {
		d, ok := byID[root]
		if !ok || d.RetryOf == nil || seen[*d.RetryOf] || byID[*d.RetryOf] == nil {
			break
		}
		root = *d.RetryOf
		seen[root] = true
	}
	var out []db.Download
	visited := map[int64]bool{}
	queue := []int64{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		if d, ok := byID[cur]; ok {
			out = append(out, *d)
		}
		queue = append(queue, children[cur]...)
	}
	slices.SortFunc(out, func(a, b db.Download) int {
		return cmp.Or(cmp.Compare(a.GrabbedAt, b.GrabbedAt), cmp.Compare(a.ID, b.ID))
	})
	return out
}

func chainIDs(chain []db.Download) (string, error) {
	ids := make([]int64, 0, len(chain))
	for i := range chain {
		ids = append(ids, chain[i].ID)
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return "", fmt.Errorf("acquire: encode chain ids: %w", err)
	}
	return string(raw), nil
}

// StopRetrying stops the automatic next-best retries of the retry chain of a
// download: when a download of the chain fails, the next release is no longer
// grabbed automatically (RSS and the scheduled searches still run). The
// download itself is left alone. Written to history; ResumeRetrying undoes it.
func (s *Service) StopRetrying(ctx context.Context, id int64) (QueueItem, error) {
	return s.setRetryStopped(ctx, id, true)
}

// ResumeRetrying undoes StopRetrying. When the newest download of the chain
// failed while retries were stopped and nothing is downloading for the movie,
// the next release is tried now.
func (s *Service) ResumeRetrying(ctx context.Context, id int64) (QueueItem, error) {
	return s.setRetryStopped(ctx, id, false)
}

func (s *Service) setRetryStopped(ctx context.Context, id int64, stop bool) (QueueItem, error) {
	d, chain, err := s.retryChain(ctx, id)
	if err != nil {
		return QueueItem{}, err
	}
	stopped := d.RetryStoppedAt != nil
	if stopped == stop {
		return s.queueItem(&d, s.clientNames(ctx)[deref(d.ClientID)]), nil // nothing to change
	}
	ids, err := chainIDs(chain)
	if err != nil {
		return QueueItem{}, err
	}
	var at *int64
	event, reason := HistoryRetriesResumed, "Automatic retries resumed by the user: a failed download of this chain is replaced by the next best release again."
	if stop {
		now := store.Millis(s.now())
		at = &now
		event, reason = HistoryRetriesStopped, "Automatic retries stopped by the user: if a download of this chain fails, Gonema does not grab the next release (RSS and the scheduled searches still run)."
	}
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.SetDownloadsRetryStopped(ctx, db.SetDownloadsRetryStoppedParams{RetryStoppedAt: at, Ids: ids}); err != nil {
			return fmt.Errorf("set retry flag: %w", err)
		}
		return s.appendHistory(ctx, q, historyEntry{
			movieID: d.MovieID, event: event, title: d.ReleaseTitle, quality: d.Quality, downloadID: d.ID,
			data: map[string]any{"reason": reason, "chain": len(chain)},
		})
	})
	if err != nil {
		return QueueItem{}, store.MapError(fmt.Errorf("acquire: retries of download %d: %w", id, err))
	}
	s.dlog.Info("automatic retries changed by the user", slog.Int64("download_id", id), slog.Int64("movie_id", d.MovieID),
		slog.Bool("stopped", stop), slog.Int("chain", len(chain)))
	d.RetryStoppedAt = at
	item := s.queueItem(&d, s.clientNames(ctx)[deref(d.ClientID)])
	s.publish(events.Event{Type: EventDownloadUpdated, MovieID: d.MovieID, Data: item})
	if !stop {
		s.retryAfterResume(ctx, chain)
	}
	return item, nil
}

// retryAfterResume tries the next release when the newest download of the
// chain failed and no download of the movie is active.
func (s *Service) retryAfterResume(ctx context.Context, chain []db.Download) {
	if len(chain) == 0 {
		return
	}
	last := chain[len(chain)-1]
	if last.Status != "failed" {
		return
	}
	all, err := s.st.Read().ListMovieDownloads(ctx, last.MovieID)
	if err != nil {
		s.dlog.Error("resume retries: list downloads", slog.Int64("movie_id", last.MovieID), slog.Any("error", store.MapError(err)))
		return
	}
	for i := range all {
		if isActive(all[i].Status) {
			return
		}
	}
	last.RetryStoppedAt = nil
	s.scheduleRetry(ctx, &last)
}
