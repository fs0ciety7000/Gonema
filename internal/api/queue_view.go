package api

import (
	"context"
	"encoding/json"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// QueueMovie is the movie of a queue entry.
type QueueMovie struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	Year          int64   `json:"year"`
	PosterURL     *string `json:"posterUrl" doc:"w342"`
	DominantColor *string `json:"dominantColor"`
}

// QueueRetry says which failed download an entry replaces: "release #2 of 6".
type QueueRetry struct {
	Attempt            int64  `json:"attempt" doc:"This release's number among the movie's grabs of the last 24 hours"`
	Of                 int64  `json:"of" doc:"Attempts allowed per day (Settings > Acquisition)"`
	PreviousDownloadID int64  `json:"previousDownloadId"`
	PreviousRelease    string `json:"previousRelease"`
	PreviousReason     string `json:"previousReason" doc:"Why the previous release failed"`
}

// QueueActions are the actions the entry allows now.
type QueueActions struct {
	Pause       bool `json:"pause"`
	Resume      bool `json:"resume"`
	Remove      bool `json:"remove" doc:"Remove (optionally blocklist and retry)"`
	RetryImport bool `json:"retryImport" doc:"The import failed: POST /queue/{id}/import"`
}

// QueueEntry is a row of the queue screen.
type QueueEntry struct {
	acquire.QueueItem
	Movie           QueueMovie   `json:"movie"`
	Indexer         string       `json:"indexer" doc:"Indexer name, empty when unknown"`
	Seeders         *int64       `json:"seeders" doc:"Torrents: seeders when grabbed"`
	DownloadedBytes int64        `json:"downloadedBytes" doc:"size x progress"`
	UpgradeFrom     *string      `json:"upgradeFrom" doc:"Quality name of the file on disk it would replace; null without a file"`
	StatusMessage   string       `json:"statusMessage" doc:"Latest message from the client or the importer"`
	Importing       bool         `json:"importing"`
	ImportFailed    bool         `json:"importFailed" doc:"Completed but the import failed; failedReason says why"`
	FailedReason    *string      `json:"failedReason"`
	Retry           *QueueRetry  `json:"retry" doc:"Set when this download replaces a failed one"`
	Actions         QueueActions `json:"actions"`
}

// queueEntries adds what the queue screen shows to the live queue items.
func (s *Server) queueEntries(ctx context.Context, items []acquire.QueueItem, maxAttempts int) ([]QueueEntry, error) {
	rows, err := s.d.Store.Read().ListQueueContext(ctx)
	if err != nil {
		return nil, store.MapError(err)
	}
	byID := make(map[int64]*db.ListQueueContextRow, len(rows))
	for i := range rows {
		byID[rows[i].ID] = &rows[i]
	}
	out := make([]QueueEntry, 0, len(items))
	for i := range items {
		out = append(out, s.queueEntryOf(&items[i], byID[items[i].ID], int64(maxAttempts)))
	}
	return out, nil
}

func lastMessage(m []string) string {
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1]
}

func (s *Server) queueEntryOf(it *acquire.QueueItem, c *db.ListQueueContextRow, maxAttempts int64) QueueEntry {
	e := QueueEntry{
		QueueItem: *it, Movie: QueueMovie{ID: it.MovieID}, StatusMessage: lastMessage(it.Messages),
		DownloadedBytes: int64(float64(it.Size) * it.Progress),
		Importing:       it.Status == "importing",
		ImportFailed:    it.Status == "warning" && it.CompletedAt != nil,
	}
	if e.Messages == nil {
		e.Messages = []string{}
	}
	if e.ImportFailed {
		e.FailedReason = optStr(e.StatusMessage)
	}
	hasClient := it.ClientID != nil
	e.Actions = QueueActions{
		Pause:       hasClient && !e.ImportFailed && (it.Status == "queued" || it.Status == "downloading" || it.Status == "warning"),
		Resume:      hasClient && it.Status == "paused",
		Remove:      it.Status != "importing",
		RetryImport: e.ImportFailed,
	}
	if c == nil {
		return e
	}
	e.Movie = QueueMovie{
		ID: c.MovieID, Title: c.MovieTitle, Year: c.MovieYear,
		PosterURL: s.imageURL(imagecache.Poster, gridPosterSize, c.MoviePosterPath), DominantColor: c.MovieDominantColor,
	}
	e.Indexer = c.IndexerName
	if it.Protocol == "torrent" {
		seeders := c.Seeders
		e.Seeders = &seeders
	}
	e.UpgradeFrom = qualityName(c.FileQuality)
	if c.PreviousID != nil {
		r := &QueueRetry{Attempt: c.Attempt, Of: max(maxAttempts, c.Attempt), PreviousDownloadID: *c.PreviousID, PreviousRelease: deref(c.PreviousTitle)}
		if c.PreviousMessages != nil {
			var msgs []string
			if json.Unmarshal([]byte(*c.PreviousMessages), &msgs) == nil {
				r.PreviousReason = lastMessage(msgs)
			}
		}
		e.Retry = r
	}
	return e
}
