package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Event types the dispatcher follows. The acquisition and import ones mirror
// acquire.Event* and importer.EventFileImported (this package does not import
// them; a test in cmd/gonema checks the names).
const (
	EventDownloadGrabbed events.Type = "download.grabbed"
	EventDownloadFailed  events.Type = "download.failed"
	EventDownloadUpdated events.Type = "download.updated"
	EventFileImported    events.Type = "moviefile.imported"
)

// Settings keys read by the builder.
const (
	// SettingAppURL is the address Gonema is reached at from outside
	// ("https://gonema.example.lan", base path included): message links open
	// the movie there. Empty: no links.
	SettingAppURL = "notifications.app_url"
	// SettingMaxAttempts and DefaultMaxAttempts mirror acquire's daily retry
	// budget (checked by a test in cmd/gonema).
	SettingMaxAttempts = "downloads.max_attempts_per_day"
	DefaultMaxAttempts = 6
)

// ImageBaseURLSource gives the TMDB image base URL ("https://image.tmdb.org/t/p/").
type ImageBaseURLSource interface {
	ImageBaseURL(ctx context.Context) (string, error)
}

// posterSize is the TMDB poster width used in messages.
const posterSize = "w342"

// builder turns bus events into messages.
type builder struct {
	st     *store.Store
	images ImageBaseURLSource
	now    func() time.Time

	mu     sync.Mutex
	health map[string]bool  // "source/id" -> last ok state
	warned map[int64]string // download id -> last import failure notified
}

func newBuilder(st *store.Store, images ImageBaseURLSource, now func() time.Time) *builder {
	return &builder{st: st, images: images, now: now, warned: map[int64]string{}}
}

// subject is a built message and what the filters need.
type subject struct {
	msg  *Message
	tags []string // movie tags (lower case); nil when unknown
}

// queueData is the JSON shape of the acquisition events' data (QueueItem).
type queueData struct {
	ID          int64    `json:"id"`
	MovieID     int64    `json:"movieId"`
	Title       string   `json:"title"`
	Size        int64    `json:"size"`
	Quality     string   `json:"quality"`
	QualityName string   `json:"qualityName"`
	Status      string   `json:"status"`
	Client      string   `json:"client"`
	IndexerID   *int64   `json:"indexerId"`
	Messages    []string `json:"messages"`
	Manual      bool     `json:"manual"`
}

func decodeData[T any](v any) (T, bool) {
	var out T
	raw, err := json.Marshal(v)
	if err != nil {
		return out, false
	}
	return out, json.Unmarshal(raw, &out) == nil
}

// build returns the message of an event, or nil when the event is not
// notified.
func (b *builder) build(ctx context.Context, e *events.Event) *subject {
	switch e.Type {
	case EventDownloadGrabbed:
		return b.grabbed(ctx, e)
	case EventDownloadFailed:
		return b.failed(ctx, e)
	case EventDownloadUpdated:
		return b.importFailed(ctx, e)
	case EventFileImported:
		return b.imported(ctx, e)
	case events.HealthIssueRaised:
		return b.healthIssue(ctx, e, true)
	case events.HealthIssueResolved:
		return b.healthIssue(ctx, e, false)
	case events.MovieDeleted:
		return b.movieDeleted(ctx, e)
	case events.MovieFileRenamed:
		return b.fileEvent(ctx, e, EventRename, "Renamed", "The files were renamed to match the naming settings.")
	case events.MovieFileDeleted:
		return b.fileEvent(ctx, e, EventDelete, "File removed", "The file went to the recycle bin; restore it from there if needed.")
	}
	return nil
}

// movie loads the movie of a message: reference, poster, link and tags.
func (b *builder) movie(ctx context.Context, id int64, m *Message) []string {
	if id == 0 {
		return nil
	}
	row, err := b.st.Read().GetMovie(ctx, id)
	if err != nil {
		return nil
	}
	m.Movie = &MovieRef{ID: row.ID, TmdbID: row.TmdbID, Title: row.Title, Year: row.Year}
	if row.PosterPath != nil && *row.PosterPath != "" && b.images != nil {
		if base, err := b.images.ImageBaseURL(ctx); err == nil && base != "" {
			m.PosterURL = strings.TrimRight(base, "/") + "/" + posterSize + "/" + strings.TrimLeft(*row.PosterPath, "/")
		}
	}
	if app := b.appURL(ctx); app != "" {
		m.Link = app + "/movies/" + strconv.FormatInt(row.ID, 10)
	}
	tags := []string{}
	if rows, err := b.st.Read().ListMovieTags(ctx, id); err == nil {
		for _, t := range rows {
			tags = append(tags, strings.ToLower(t.Label))
		}
	}
	return tags
}

func (b *builder) appURL(ctx context.Context) string {
	v, err := store.GetSetting[string](ctx, b.st, SettingAppURL)
	if err != nil {
		return ""
	}
	return strings.TrimRight(v, "/")
}

func (b *builder) indexerName(ctx context.Context, id *int64) string {
	if id == nil {
		return ""
	}
	ix, err := b.st.Read().GetIndexer(ctx, *id)
	if err != nil {
		return ""
	}
	return ix.Name
}

func movieTitle(m *Message, fallback string) string {
	if m.Movie != nil {
		return m.Movie.Display()
	}
	return fallback
}

// sentence makes s end with a period.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	s = string(r)
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "!") && !strings.HasSuffix(s, "?") {
		s += "."
	}
	return s
}

// FormatSize formats a size like the rest of Gonema (binary units, "GB").
func FormatSize(n int64) string {
	switch {
	case n <= 0:
		return ""
	case n >= quality.GB:
		return fmt.Sprintf("%.1f GB", float64(n)/quality.GB)
	case n >= quality.MB:
		return fmt.Sprintf("%.0f MB", float64(n)/quality.MB)
	}
	return fmt.Sprintf("%d B", n)
}

func qualityName(id string) string {
	if id == "" {
		return ""
	}
	return quality.ID(id).Name()
}

// details joins the non-empty parts with " · ".
func details(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

func field(fields []Field, key, label, value string) []Field {
	if value == "" {
		return fields
	}
	return append(fields, Field{Key: key, Label: label, Value: value})
}

// ------------------------------------------------------------------ grab

func (b *builder) grabbed(ctx context.Context, e *events.Event) *subject {
	q, ok := decodeData[queueData](e.Data)
	if !ok {
		return nil
	}
	m := &Message{Event: EventGrab, Time: e.Time}
	tags := b.movie(ctx, e.MovieID, m)
	title := movieTitle(m, q.Title)
	qn := q.QualityName
	if qn == "" {
		qn = qualityName(q.Quality)
	}
	indexer := b.indexerName(ctx, q.IndexerID)
	line := details(qn, FormatSize(q.Size))
	if indexer != "" {
		line += " from " + indexer
	}
	m.Title = "Grabbed — " + title
	m.Body = sentence(line) + " Sent to " + orDefault(q.Client, "the download client") + "."
	m.Summary = details(m.Title, line)
	m.Fields = field(m.Fields, "quality", "Quality", qn)
	m.Fields = field(m.Fields, "size", "Size", FormatSize(q.Size))
	m.Fields = field(m.Fields, "indexer", "Indexer", indexer)
	m.Fields = field(m.Fields, "client", "Download client", q.Client)
	m.Fields = field(m.Fields, "release", "Release", q.Title)
	return &subject{msg: m, tags: tags}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ------------------------------------------------------------------ failures

func lastMessage(q *queueData) string {
	if len(q.Messages) == 0 {
		return ""
	}
	return q.Messages[len(q.Messages)-1]
}

// nextStep says what the automatic retry does after a failed download, with
// the same budget as the acquisition service (grabs per movie and per day).
func (b *builder) nextStep(ctx context.Context, q *queueData, m *Message) string {
	if m.Movie != nil {
		if row, err := b.st.Read().GetMovie(ctx, m.Movie.ID); err == nil && !row.Monitored {
			return "Blocklisted it. The movie is not monitored, so no other release is tried."
		}
	}
	maxAttempts := DefaultMaxAttempts
	if v, err := store.GetSetting[int](ctx, b.st, SettingMaxAttempts); err == nil && v > 0 {
		maxAttempts = v
	}
	dl, err := b.st.Read().GetDownload(ctx, q.ID)
	if err != nil {
		return "Blocklisted it, trying the next release."
	}
	n, err := b.st.Read().CountGrabsBetween(ctx, db.CountGrabsBetweenParams{
		MovieID: dl.MovieID, Since: store.Millis(b.now().Add(-24 * time.Hour)), Until: dl.GrabbedAt,
	})
	if err != nil {
		return "Blocklisted it, trying the next release."
	}
	if int(n) >= maxAttempts {
		return fmt.Sprintf("Blocklisted it. That was release %d of %d today: automatic retries stop until tomorrow; RSS and the daily search keep looking.", n, maxAttempts)
	}
	return fmt.Sprintf("Blocklisted it, trying release #%d of %d.", n+1, maxAttempts)
}

func (b *builder) failed(ctx context.Context, e *events.Event) *subject {
	q, ok := decodeData[queueData](e.Data)
	if !ok {
		return nil
	}
	m := &Message{Event: EventFailure, Time: e.Time, Problem: true}
	tags := b.movie(ctx, e.MovieID, m)
	title := movieTitle(m, q.Title)
	reason := lastMessage(&q)
	next := b.nextStep(ctx, &q, m)
	m.Title = "Download failed — " + title
	m.Body = strings.TrimSpace(sentence(reason) + " " + next)
	m.Summary = m.Title + ". " + next
	m.Fields = field(m.Fields, "quality", "Quality", orDefault(q.QualityName, qualityName(q.Quality)))
	m.Fields = field(m.Fields, "client", "Download client", q.Client)
	m.Fields = field(m.Fields, "release", "Release", q.Title)
	m.Fields = field(m.Fields, "reason", "Reason", reason)
	return &subject{msg: m, tags: tags}
}

// importFailed notifies a download whose import could not run (status
// warning, message "Import failed: ..."), once per download and message.
func (b *builder) importFailed(ctx context.Context, e *events.Event) *subject {
	q, ok := decodeData[queueData](e.Data)
	if !ok || q.Status != "warning" {
		return nil
	}
	reason := ""
	for _, msg := range q.Messages {
		if strings.HasPrefix(msg, "Import failed") {
			reason = msg
		}
	}
	if reason == "" {
		return nil
	}
	b.mu.Lock()
	seen := b.warned[q.ID] == reason
	if !seen {
		if len(b.warned) > 1000 {
			clear(b.warned)
		}
		b.warned[q.ID] = reason
	}
	b.mu.Unlock()
	if seen {
		return nil
	}
	m := &Message{Event: EventFailure, Time: e.Time, Problem: true}
	tags := b.movie(ctx, e.MovieID, m)
	m.Title = "Import failed — " + movieTitle(m, q.Title)
	m.Body = sentence(reason) + " The download stays in the queue until it is imported or removed."
	m.Summary = m.Title + ". The download stays in the queue."
	m.Fields = field(m.Fields, "release", "Release", q.Title)
	m.Fields = field(m.Fields, "reason", "Reason", strings.TrimPrefix(strings.TrimPrefix(reason, "Import failed"), ": "))
	return &subject{msg: m, tags: tags}
}

// ------------------------------------------------------------------ imports

func (b *builder) imported(ctx context.Context, e *events.Event) *subject {
	data, _ := decodeData[map[string]any](e.Data)
	upgraded, _ := data["upgraded"].(bool)
	newQ := qualityName(str(data["quality"]))
	m := &Message{Event: EventImport, Time: e.Time}
	tags := b.movie(ctx, e.MovieID, m)
	if m.Movie == nil {
		return nil // the movie is gone already
	}
	var size int64
	if f, err := b.st.Read().GetCurrentMovieFile(ctx, e.MovieID); err == nil {
		size = f.Size
	}
	var indexer, release string
	if id, ok := data["downloadId"].(float64); ok && id > 0 {
		if dl, err := b.st.Read().GetDownload(ctx, int64(id)); err == nil {
			indexer, release = b.indexerName(ctx, dl.IndexerID), dl.ReleaseTitle
		}
	}
	if upgraded {
		m.Event = EventUpgrade
		prev := b.previousQuality(ctx, e.MovieID)
		change := newQ
		if prev != "" {
			change = prev + " → " + newQ
		}
		m.Title = "Upgraded — " + m.Movie.Display()
		m.Body = sentence(change) + " The previous file is in the recycle bin."
		m.Summary = m.Title + ": " + change
		m.Fields = field(m.Fields, "previousQuality", "Previous quality", prev)
	} else {
		line := details(newQ, FormatSize(size))
		if indexer != "" {
			line += " from " + indexer
		}
		m.Title = "Imported — " + m.Movie.Display()
		m.Body = sentence(line)
		m.Summary = details(m.Title, line)
	}
	m.Fields = field(m.Fields, "quality", "Quality", newQ)
	m.Fields = field(m.Fields, "size", "Size", FormatSize(size))
	m.Fields = field(m.Fields, "indexer", "Indexer", indexer)
	m.Fields = field(m.Fields, "release", "Release", release)
	return &subject{msg: m, tags: tags}
}

// previousQuality reads the replaced file's quality from the upgrade's
// history row (written before the event is published).
func (b *builder) previousQuality(ctx context.Context, movieID int64) string {
	h, err := b.st.Read().LatestMovieHistory(ctx, db.LatestMovieHistoryParams{MovieID: &movieID, Event: "upgraded"})
	if err != nil {
		return ""
	}
	var data struct {
		Previous struct {
			Quality string `json:"quality"`
		} `json:"previous"`
	}
	if json.Unmarshal([]byte(h.Data), &data) != nil {
		return ""
	}
	return qualityName(data.Previous.Quality)
}

// str returns the string form of a scalar from event data.
func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	}
	return fmt.Sprint(v)
}

// ------------------------------------------------------------------ library

func (b *builder) movieDeleted(_ context.Context, e *events.Event) *subject {
	data, _ := decodeData[struct {
		ID     int64  `json:"id"`
		TmdbID int64  `json:"tmdbId"`
		Title  string `json:"title"`
		Year   int64  `json:"year"`
		Note   string `json:"note"`
	}](e.Data)
	if data.Title == "" {
		return nil
	}
	ref := &MovieRef{ID: orID(data.ID, e.MovieID), TmdbID: data.TmdbID, Title: data.Title, Year: data.Year}
	m := &Message{Event: EventDelete, Time: e.Time, Movie: ref}
	m.Title = "Removed — " + ref.Display()
	m.Body = sentence(orDefault(data.Note, "removed from the library"))
	m.Summary = m.Title + ". " + m.Body
	return &subject{msg: m} // the movie row and its tags are gone
}

func orID(a, b int64) int64 {
	if a != 0 {
		return a
	}
	return b
}

func (b *builder) fileEvent(ctx context.Context, e *events.Event, kind EventKind, verb, body string) *subject {
	m := &Message{Event: kind, Time: e.Time}
	tags := b.movie(ctx, e.MovieID, m)
	if m.Movie == nil {
		return nil
	}
	m.Title = verb + " — " + m.Movie.Display()
	m.Body = body
	m.Summary = m.Title
	return &subject{msg: m, tags: tags}
}

// ------------------------------------------------------------------ health

// healthIssue notifies the issues of the health checks (internal/health):
// the scheduler publishes an issue once when it appears (or changes severity)
// and once when it goes away.
func (b *builder) healthIssue(ctx context.Context, e *events.Event, raised bool) *subject {
	ev, ok := decodeData[events.HealthIssueEvent](e.Data)
	if !ok || ev.Issue.Title == "" {
		return nil
	}
	is := ev.Issue
	m := &Message{Event: EventHealth, Time: e.Time, HealthSource: is.Source, HealthID: issueSubjectID(is.ID)}
	if is.Link != "" {
		if app := b.appURL(ctx); app != "" {
			m.Link = app + "/" + strings.TrimLeft(is.Link, "/")
		}
	}
	if raised {
		m.Problem = is.Severity != events.SeverityInfo
		m.Title = "Health issue — " + is.Title
		m.Body = is.Message
		m.Summary = m.Title
		m.Fields = field(m.Fields, "severity", "Severity", string(is.Severity))
		for i, step := range is.Fix {
			m.Fields = field(m.Fields, "fix"+strconv.Itoa(i+1), "To fix", step)
		}
		return &subject{msg: m}
	}
	m.Title = "Resolved — " + is.Title
	m.Body = "This health issue is gone; nothing to do."
	m.Summary = m.Title
	return &subject{msg: m}
}

// issueSubjectID returns the number after the last ":" of an issue id
// ("notification.failing:3" -> 3), 0 if none.
func issueSubjectID(id string) int64 {
	i := strings.LastIndexByte(id, ':')
	if i < 0 {
		return 0
	}
	n, err := strconv.ParseInt(id[i+1:], 10, 64)
	if err != nil {
		return 0
	}
	return n
}
