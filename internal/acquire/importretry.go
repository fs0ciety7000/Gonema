package acquire

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// retryClientTimeout bounds the client call asking where the files are.
const retryClientTimeout = 15 * time.Second

// RetryImport runs the import of a completed download again, typically after
// the user fixed what made it fail (a remote path mapping, permissions, free
// space). The output path is asked again from the client, with the current
// path mappings; without the client, the stored path is used. movieID > 0
// imports the download for that movie instead of the one it was grabbed for.
//
// The import runs in the background like an automatic one; the returned item
// is in "importing". A history row records the manual retry (and the movie
// change), in the same transaction.
func (s *Service) RetryImport(ctx context.Context, id, movieID int64) (QueueItem, error) {
	d, err := s.st.Read().GetDownload(ctx, id)
	if err != nil {
		return QueueItem{}, mapNotFound(err, "Download %d does not exist.", id)
	}
	if s.importer == nil {
		return QueueItem{}, newError(ErrUnavailable, nil, "Imports are not available on this server.")
	}
	if d.Status != string(download.StatusWarning) && d.Status != string(download.StatusCompleted) {
		return QueueItem{}, invalid("This download is %s: only a completed download, or one whose import needs attention, can be imported again.", d.Status)
	}
	c := db.DownloadClient{Name: "the download client"}
	if d.ClientID != nil {
		row, err := s.st.Read().GetDownloadClient(ctx, *d.ClientID)
		switch err = store.MapError(err); {
		case err == nil:
			c = row
		case !errors.Is(err, store.ErrNotFound):
			return QueueItem{}, fmt.Errorf("acquire: get client of download %d: %w", id, err)
		}
	}
	local, note, err := s.retryPath(ctx, &d, &c)
	if err != nil {
		return QueueItem{}, err
	}
	if local == "" {
		return QueueItem{}, invalid("%s has not reported where the files of this download are yet; try again once it shows the download as completed.", c.Name)
	}

	from := d.MovieID
	if movieID > 0 && movieID != d.MovieID {
		if _, err := s.st.Read().GetMovie(ctx, movieID); err != nil {
			return QueueItem{}, mapNotFound(err, "Movie %d is not in the library.", movieID)
		}
	} else {
		movieID = d.MovieID
	}
	reason := "Import retried by the user."
	data := map[string]any{"reason": reason, "manual": true, "kind": "import", "path": local}
	if note != "" {
		data["note"] = note
	}
	if movieID != from {
		reason = fmt.Sprintf("Import retried by the user for another movie (%d instead of %d).", movieID, from)
		data["reason"], data["previousMovieId"] = reason, from
	}
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		if movieID != from {
			row, err := q.ReassignDownloadMovie(ctx, db.ReassignDownloadMovieParams{MovieID: movieID, ID: d.ID})
			if err != nil {
				if errors.Is(store.MapError(err), store.ErrNotFound) {
					return notFound("Download %d left the queue meanwhile.", d.ID)
				}
				return fmt.Errorf("reassign download %d: %w", d.ID, err)
			}
			d = row
		}
		return s.appendHistory(ctx, q, historyEntry{
			movieID: movieID, event: HistoryRetryScheduled, title: d.ReleaseTitle, quality: d.Quality, downloadID: d.ID, data: data,
		})
	})
	if err != nil {
		var ae *Error
		if errors.As(err, &ae) {
			return QueueItem{}, err
		}
		return QueueItem{}, fmt.Errorf("acquire: retry import of download %d: %w", id, err)
	}
	s.log.Info("import retried by the user", slog.Int64("download_id", d.ID), slog.Int64("movie_id", movieID),
		slog.String("release", d.ReleaseTitle))

	d.OutputPath = &local
	if d.CompletedAt == nil {
		d.CompletedAt = ptr(store.Millis(s.now()))
	}
	s.startImport(ctx, &c, &d, local)
	s.track.wake()
	item := s.queueItem(&d, clientName(&c))
	item.Status = string(download.StatusImporting)
	return item, nil
}

// retryPath returns the output path of a download as seen by Gonema: asked
// again from the client with the current path mappings, else the stored path.
// note explains a fallback, for the history row.
func (s *Service) retryPath(ctx context.Context, d *db.Download, c *db.DownloadClient) (local, note string, err error) {
	stored := deref(d.OutputPath)
	if c.ID == 0 || !c.Enabled || d.ClientRef == nil {
		return stored, "path as last reported (the download client is not available)", nil
	}
	cl, err := s.downloadClient(c)
	if err != nil {
		return stored, "path as last reported (the download client cannot be used)", nil
	}
	cctx, cancel := context.WithTimeout(ctx, retryClientTimeout)
	defer cancel()
	items, err := cl.Items(cctx)
	if err != nil {
		s.log.Warn("import retry: download client unreachable, using the stored path", slog.String("client", c.Name),
			slog.Any("error", err))
		return stored, "path as last reported (" + c.Name + " cannot be reached)", nil
	}
	for i := range items {
		if !strings.EqualFold(items[i].Ref, *d.ClientRef) || items[i].OutputPath == "" {
			continue
		}
		mapper, err := s.pathMapper(ctx)
		if err != nil {
			return "", "", err
		}
		local, mapped := mapper.Map(c.Host, items[i].OutputPath)
		if !mapped {
			return local, "no remote path mapping applies: path used as reported by " + c.Name, nil
		}
		return local, "", nil
	}
	return stored, "path as last reported (" + c.Name + " no longer lists the download)", nil
}
