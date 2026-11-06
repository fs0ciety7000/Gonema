package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/fs0ciety7000/gonema/internal/backup"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/logbuf"
)

// OpsDeps are the services of the operations endpoints (backups, logs, import
// journal).
type OpsDeps struct {
	Backups        *backup.Service
	BackupSettings *backup.SettingsStore
	BackupJobs     BackupScheduler // jobs.Runner; optional
	Logs           *logbuf.Buffer
	LogLevel       *slog.LevelVar
	LogFile        *logbuf.RotatingFile // optional
	Imports        *importer.Service
}

// BackupScheduler reschedules the scheduled backup and tells its next run
// (jobs.Runner).
type BackupScheduler interface {
	Reschedule(kind string) bool
	NextRun(ctx context.Context, kind string) (time.Time, bool)
}

// RegisterOpsRoutes adds the backup, log and import journal endpoints. Call it
// once, after New and before serving. They are under /api like every other
// route, so they need an authenticated caller.
func (s *Server) RegisterOpsRoutes(d OpsDeps) {
	r := &opsRoutes{s: s, d: d}
	r.backups()
	r.logs()
	r.journal()
}

type opsRoutes struct {
	s *Server
	d OpsDeps
}

// problem maps backup errors; the others go through Server.problem.
func (r *opsRoutes) problem(ctx context.Context, err error) error {
	msg := errText(err)
	switch {
	case msg != "" && (errors.Is(err, backup.ErrInvalid) || errors.Is(err, backup.ErrConfirmRequired)):
		return huma.Error422UnprocessableEntity(msg)
	case msg != "" && errors.Is(err, backup.ErrNotFound):
		return huma.Error404NotFound(msg)
	case msg != "" && errors.Is(err, backup.ErrBusy):
		return huma.Error409Conflict(msg)
	case msg != "" && (errors.Is(err, importer.ErrInvalid) || errors.Is(err, importer.ErrConfirmRequired)):
		return huma.Error422UnprocessableEntity(msg)
	case msg != "" && errors.Is(err, importer.ErrNotFound):
		return huma.Error404NotFound(msg)
	}
	return r.s.problem(ctx, err, false)
}

// ----------------------------------------------------------------- backups

// Named after the API (OpenAPI schema names): same fields as the service types.
type (
	// BackupInfo describes a backup archive.
	BackupInfo backup.Info
	// PendingRestore is a restore staged for the next start.
	PendingRestore backup.Pending
	// RestoreStaged is the outcome of a restore request.
	RestoreStaged struct {
		RestartRequired  bool       `json:"restartRequired"`
		PreRestoreBackup BackupInfo `json:"preRestoreBackup" doc:"The backup of the state before the restore"`
		Warnings         []string   `json:"warnings,omitempty"`
		Message          string     `json:"message"`
	}
	// BackupSettings configure the scheduled backups.
	BackupSettings backup.Settings
	// BackupSettingsView is BackupSettings with the schedule.
	BackupSettingsView struct {
		BackupSettings
		NextRunAt *time.Time `json:"nextRunAt" doc:"Next scheduled backup; null when scheduled backups are off or not scheduled yet"`
	}
	// LogEntry is one log record, redacted.
	LogEntry logbuf.Entry
	// ImportJournalEntry is an interrupted import.
	ImportJournalEntry importer.JournalEntry
)

// CreateBackupRequest asks for a backup.
type CreateBackupRequest struct {
	IncludeKey bool `json:"includeKey,omitempty" doc:"Also store gonema.key in the archive. Warning: whoever gets the archive can then decrypt every stored secret"`
}

// RestoreBackupRequest asks to restore a backup of the list.
type RestoreBackupRequest struct {
	Name       string `json:"name" maxLength:"80" doc:"Archive name from the list"`
	Confirm    bool   `json:"confirm" doc:"Must be true"`
	RestoreKey bool   `json:"restoreKey,omitempty" doc:"Also restore gonema.key from the archive (when it includes one); the current key is moved aside"`
}

// SetLogLevelRequest changes the log level.
type SetLogLevelRequest struct {
	Level string `json:"level" enum:"debug,info,warn,error"`
}

func backupInfos(in []backup.Info) []BackupInfo {
	out := make([]BackupInfo, len(in))
	for i := range in {
		out[i] = BackupInfo(in[i])
	}
	return out
}

// BackupList is the backups page.
type BackupList struct {
	Backups        []BackupInfo    `json:"backups" doc:"Newest first"`
	PendingRestore *PendingRestore `json:"pendingRestore,omitempty" doc:"A restore staged for the next start"`
	Directory      string          `json:"directory" doc:"Folder of the archives, inside the data directory"`
	KeyNotice      string          `json:"keyNotice" doc:"What a backup holds about the master key, to show next to the list"`
	NextScheduled  *time.Time      `json:"nextScheduledAt" doc:"Next scheduled backup; null when scheduled backups are off or not scheduled yet"`
}

// nextBackup is the next scheduled backup time, nil when off or unknown.
func (r *opsRoutes) nextBackup(ctx context.Context, set *backup.Settings) *time.Time {
	if !set.Enabled || r.d.BackupJobs == nil {
		return nil
	}
	t, ok := r.d.BackupJobs.NextRun(ctx, backup.JobScheduled)
	if !ok {
		return nil
	}
	t = t.UTC()
	return &t
}

func (r *opsRoutes) settingsView(ctx context.Context, set backup.Settings) BackupSettingsView {
	return BackupSettingsView{BackupSettings: BackupSettings(set), NextRunAt: r.nextBackup(ctx, &set)}
}

// KeyNotice explains the master key and backups.
const KeyNotice = "Backups do not include the master key (gonema.key) unless you ask for it: the database stores your indexer, " +
	"download client and TMDB secrets encrypted with that key. Keep a copy of gonema.key somewhere safe, apart from the backups. " +
	"An archive that includes the key unlocks every stored secret: keep such archives private."

// BackupNameParam is the {name} path parameter of an archive.
type BackupNameParam struct {
	Name string `path:"name" maxLength:"80" pattern:"^gonema-backup-[0-9]{8}-[0-9]{6}(-[0-9]{1,3})?-(manual|scheduled|pre-restore|uploaded)\\.zip$" doc:"Archive name"`
}

type uploadForm struct {
	File huma.FormFile `form:"file" contentType:"application/zip,application/x-zip-compressed,application/octet-stream" required:"true" doc:"A backup archive made by Gonema"`
}

// maxUpload is the static bound of an uploaded archive; the effective limit is
// backup.Service.UploadLimit (twice the database, at least 1 GiB), checked by
// limitUpload before the body is read.
const maxUpload = 16 << 30

// limitUpload refuses an archive above the upload limit or without room on
// disk before its body is read, and caps the body at the limit.
func (r *opsRoutes) limitUpload(ctx huma.Context, next func(huma.Context)) {
	req, w := humago.Unwrap(ctx)
	svc := r.d.Backups
	err := svc.CheckUpload(req.ContentLength)
	switch {
	case errors.Is(err, backup.ErrTooLarge):
		_ = huma.WriteErr(r.s.api, ctx, http.StatusRequestEntityTooLarge, errText(err))
		return
	case err != nil:
		_ = huma.WriteErr(r.s.api, ctx, http.StatusInsufficientStorage, errText(err))
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, svc.UploadLimit())
	next(ctx)
}

func (r *opsRoutes) backups() {
	tags := []string{"Backups"}
	svc := r.d.Backups
	huma.Register(r.s.api, huma.Operation{
		OperationID: "list-backups", Method: http.MethodGet, Path: "/backups", Summary: "Backups in the data directory",
		Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body BackupList }, error) {
		list, err := svc.List(ctx)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		p, err := svc.PendingRestore()
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		set := r.d.BackupSettings.Get(ctx)
		return &struct{ Body BackupList }{BackupList{
			Backups: backupInfos(list), PendingRestore: (*PendingRestore)(p), Directory: svc.Dir(), KeyNotice: KeyNotice,
			NextScheduled: r.nextBackup(ctx, &set),
		}}, nil
	})

	huma.Register(r.s.api, huma.Operation{
		OperationID: "create-backup", Method: http.MethodPost, Path: "/backups", Summary: "Back up the database now",
		Description: "A consistent online copy (VACUUM INTO, writers are not blocked) zipped with a manifest (Gonema version, " +
			"schema version, date). The master key file gonema.key is NOT included unless includeKey is true: such an archive " +
			"unlocks every stored secret. 409 while another backup or restore runs.",
		Tags: tags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct{ Body CreateBackupRequest }) (*struct{ Body BackupInfo }, error) {
		info, err := svc.Create(ctx, backup.KindManual, in.Body.IncludeKey)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{ Body BackupInfo }{BackupInfo(info)}, nil
	})

	huma.Register(r.s.api, huma.Operation{
		OperationID: "download-backup", Method: http.MethodGet, Path: "/backups/{name}/download", Summary: "Download a backup archive",
		Description: "Streams the zip. The name must be an archive name Gonema creates: no path can be given.",
		Tags:        tags,
		Responses: map[string]*huma.Response{
			"200": {Description: "The archive", Content: map[string]*huma.MediaType{
				"application/zip": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}},
			}},
		},
	}, func(ctx context.Context, in *BackupNameParam) (*huma.StreamResponse, error) {
		f, size, err := svc.Open(in.Name)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		name := in.Name
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			defer func() { _ = f.Close() }()
			req, w := humago.Unwrap(hctx)
			h := w.Header()
			h.Set("Content-Type", "application/zip")
			h.Set("Content-Length", strconv.FormatInt(size, 10))
			h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
			h.Set("Cache-Control", "no-store")
			h.Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusOK)
			if _, err := io.Copy(deadlineWriter{w: w, rc: http.NewResponseController(w)}, f); err != nil {
				r.s.log.Debug("backup download interrupted", slog.String("request_id", RequestID(req.Context())), slog.Any("error", err))
			}
		}}, nil
	})

	huma.Register(r.s.api, huma.Operation{
		OperationID: "delete-backup", Method: http.MethodDelete, Path: "/backups/{name}", Summary: "Delete a backup archive",
		Description: "Requires confirm=true (422 otherwise). Only archives of the backups folder can be deleted.",
		Tags:        tags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		BackupNameParam
		Confirm bool `query:"confirm" doc:"Must be true"`
	}) (*struct{}, error) {
		if err := svc.Delete(in.Name, in.Confirm); err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{}{}, nil
	})

	huma.Register(r.s.api, huma.Operation{
		OperationID: "upload-backup", Method: http.MethodPost, Path: "/backups/upload", Summary: "Add a backup archive from this computer",
		Description: "multipart/form-data with a file field. The archive is validated (manifest, checksum, database integrity, " +
			"schema version not newer than this Gonema) and stored as kind uploaded; restore it by name. " +
			"Size limit: twice the current database, at least 1 GiB (413 above); 507 when the disk has no room for it.",
		Tags: tags, DefaultStatus: http.StatusCreated, MaxBodyBytes: maxUpload, BodyReadTimeout: 30 * time.Minute,
		Errors:      []int{http.StatusRequestEntityTooLarge, http.StatusInsufficientStorage, http.StatusUnprocessableEntity},
		Middlewares: huma.Middlewares{r.limitUpload},
	}, func(ctx context.Context, in *struct {
		RawBody huma.MultipartFormFiles[uploadForm]
	}) (*struct{ Body BackupInfo }, error) {
		f := in.RawBody.Data().File
		defer func() { _ = f.Close() }()
		info, err := svc.Import(ctx, f)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{ Body BackupInfo }{BackupInfo(info)}, nil
	})

	huma.Register(r.s.api, huma.Operation{
		OperationID: "restore-backup", Method: http.MethodPost, Path: "/backups/restore", Summary: "Restore a backup at the next start",
		Description: "Requires confirm=true. Validates the archive (manifest, checksum, integrity, schema version), backs up the " +
			"current state first (kind pre-restore), then stages the database: it replaces the current one when Gonema restarts " +
			"(202; restart Gonema to apply it). To restore a file from this computer, upload it first (POST /backups/upload).",
		Tags: tags, DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *struct{ Body RestoreBackupRequest }) (*struct{ Body RestoreStaged }, error) {
		st, err := svc.StageRestore(ctx, in.Body.Name, in.Body.RestoreKey, in.Body.Confirm)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		if r.s.d.Bus != nil {
			r.s.d.Bus.Publish(events.Event{Type: events.SettingsChanged, Data: map[string]any{"setting": "backups.restore"}})
		}
		return &struct{ Body RestoreStaged }{RestoreStaged{
			RestartRequired: st.RestartRequired, PreRestoreBackup: BackupInfo(st.PreRestoreBackup), Warnings: st.Warnings, Message: st.Message,
		}}, nil
	})

	huma.Register(r.s.api, huma.Operation{
		OperationID: "cancel-restore", Method: http.MethodDelete, Path: "/backups/restore", Summary: "Cancel the staged restore",
		Description: "The pre-restore backup made when it was staged is kept. 404 when nothing is staged.",
		Tags:        tags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		if err := svc.CancelRestore(); err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{}{}, nil
	})

	huma.Register(r.s.api, huma.Operation{
		OperationID: "get-backup-settings", Method: http.MethodGet, Path: "/settings/backups", Summary: "Scheduled backups",
		Tags: []string{"Settings"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body BackupSettingsView }, error) {
		return &struct{ Body BackupSettingsView }{r.settingsView(ctx, r.d.BackupSettings.Get(ctx))}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "set-backup-settings", Method: http.MethodPut, Path: "/settings/backups", Summary: "Change scheduled backups",
		Description: "Interval (hours), scheduled backups kept, and whether they include gonema.key (off by default: see the warning). " +
			"A new interval applies at once (nextRunAt may show the previous due time for a moment).",
		Tags: []string{"Settings"},
	}, func(ctx context.Context, in *struct{ Body BackupSettings }) (*struct{ Body BackupSettingsView }, error) {
		out, err := r.d.BackupSettings.Set(ctx, backup.Settings(in.Body))
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		if r.d.BackupJobs != nil {
			r.d.BackupJobs.Reschedule(backup.JobScheduled)
		}
		if r.s.d.Bus != nil {
			r.s.d.Bus.Publish(events.Event{Type: events.SettingsChanged, Data: map[string]any{"setting": "backups"}})
		}
		return &struct{ Body BackupSettingsView }{r.settingsView(ctx, out)}, nil
	})
}

// deadlineWriter extends the write deadline before each write: a long
// download goes on as long as the client reads, a stalled one is dropped.
type deadlineWriter struct {
	w  io.Writer
	rc *http.ResponseController
}

func (d deadlineWriter) Write(p []byte) (int, error) {
	_ = d.rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
	return d.w.Write(p)
}

// ----------------------------------------------------------------- logs

// LogList is a page of log entries.
type LogList struct {
	Entries  []LogEntry `json:"entries" doc:"Oldest first: the newest limit matches"`
	Capacity int        `json:"capacity" doc:"Entries kept in memory"`
}

// LogFileSettings describes the optional log file.
type LogFileSettings struct {
	Enabled   bool   `json:"enabled"`
	Path      string `json:"path,omitempty"`
	MaxSizeMB int    `json:"maxSizeMB,omitempty"`
	Keep      int    `json:"keep,omitempty"`
}

// LogSettings are the log level and outputs.
type LogSettings struct {
	Level      string          `json:"level" enum:"debug,info,warn,error"`
	BufferSize int             `json:"bufferSize" doc:"Entries kept in memory (GONEMA_LOG_BUFFER)"`
	File       LogFileSettings `json:"file" doc:"Log file (GONEMA_LOG_FILE), read-only here"`
}

func (r *opsRoutes) logSettings() LogSettings {
	out := LogSettings{Level: logbuf.LevelName(r.d.LogLevel.Level()), BufferSize: r.d.Logs.Capacity()}
	if f := r.d.LogFile; f != nil {
		out.File = LogFileSettings{Enabled: true, Path: f.Path(), MaxSizeMB: int(f.MaxBytes() >> 20), Keep: f.Keep()}
	}
	return out
}

type logStreamInput struct {
	Level       string   `query:"level" enum:"debug,info,warn,error" default:"debug" doc:"Minimum level"`
	Component   []string `query:"component" maxItems:"32" doc:"Components, comma-separated (indexer,download...); all when absent"`
	LastEventID string   `header:"Last-Event-ID" maxLength:"20" doc:"Id of the last entry received; newer entries still in memory are replayed"`
}

func (r *opsRoutes) logs() {
	tags := []string{"Logs"}
	huma.Register(r.s.api, huma.Operation{
		OperationID: "list-logs", Method: http.MethodGet, Path: "/logs", Summary: "Recent log entries",
		Description: "From the in-memory ring (the last GONEMA_LOG_BUFFER entries). Secrets are redacted.",
		Tags:        tags,
	}, func(_ context.Context, in *struct {
		Level     string    `query:"level" enum:"debug,info,warn,error" default:"debug" doc:"Minimum level"`
		Since     time.Time `query:"since" doc:"Entries at or after this time (RFC 3339)"`
		Q         string    `query:"q" maxLength:"200" doc:"Case-insensitive text in the message or the attributes"`
		Component []string  `query:"component" maxItems:"32" doc:"Components, comma-separated (indexer,download...); all when absent"`
		Limit     int       `query:"limit" minimum:"1" maximum:"5000" default:"500"`
	}) (*struct{ Body LogList }, error) {
		entries := r.d.Logs.Entries(logbuf.Query{MinLevel: in.Level, Since: in.Since, Text: in.Q,
			Components: logComponents(in.Component), Limit: in.Limit})
		out := make([]LogEntry, len(entries))
		for i := range entries {
			out[i] = LogEntry(entries[i])
		}
		return &struct{ Body LogList }{LogList{Entries: out, Capacity: r.d.Logs.Capacity()}}, nil
	})

	huma.Register(r.s.api, huma.Operation{
		OperationID: "stream-logs", Method: http.MethodGet, Path: "/logs/stream", Summary: "Live log entries (Server-Sent Events)",
		Description: "A dedicated stream, apart from /events: each message has an id, the event name log.entry and a log entry " +
			"as JSON data. A comment is sent every 15 s; \"resync\" means entries were dropped (slow connection): fetch /logs again.",
		Tags: tags,
		Responses: map[string]*huma.Response{
			"200": {Description: "An endless text/event-stream", Content: map[string]*huma.MediaType{
				"text/event-stream": {Schema: &huma.Schema{Type: huma.TypeString}},
			}},
		},
		Errors: []int{http.StatusTooManyRequests},
	}, func(ctx context.Context, in *logStreamInput) (*huma.StreamResponse, error) {
		release, err := r.s.openStream(ctx)
		if err != nil {
			return nil, err
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			defer release()
			req, w := humago.Unwrap(hctx)
			r.streamLogs(req, w, in)
		}}, nil
	})

	huma.Register(r.s.api, huma.Operation{
		OperationID: "download-logs", Method: http.MethodGet, Path: "/logs/download", Summary: "Download the logs",
		Description: "The log file and its rotated files (oldest first) when a log file is configured, else the in-memory " +
			"entries, as text. Secrets are redacted.",
		Tags: tags,
		Responses: map[string]*huma.Response{
			"200": {Description: "Log lines", Content: map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: huma.TypeString}}}},
		},
	}, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			req, w := humago.Unwrap(hctx)
			h := w.Header()
			h.Set("Content-Type", "text/plain; charset=utf-8")
			h.Set("Content-Disposition", `attachment; filename="gonema-`+time.Now().UTC().Format("20060102-150405")+`.log"`)
			h.Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			dw := deadlineWriter{w: w, rc: http.NewResponseController(w)}
			var err error
			if r.d.LogFile != nil {
				err = r.d.LogFile.WriteAll(dw)
			} else {
				err = r.d.Logs.WriteText(dw)
			}
			if err != nil {
				r.s.log.Debug("log download interrupted", slog.String("request_id", RequestID(req.Context())), slog.Any("error", err))
			}
		}}, nil
	})

	huma.Register(r.s.api, huma.Operation{
		OperationID: "get-log-settings", Method: http.MethodGet, Path: "/settings/logs", Summary: "Log level and outputs",
		Tags: []string{"Settings"},
	}, func(context.Context, *struct{}) (*struct{ Body LogSettings }, error) {
		return &struct{ Body LogSettings }{r.logSettings()}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "set-log-settings", Method: http.MethodPut, Path: "/settings/logs", Summary: "Change the log level now",
		Description: "Applies at once to every output. It is not saved: at the next start the level of GONEMA_LOG_LEVEL " +
			"(or -log-level) applies again.",
		Tags: []string{"Settings"},
	}, func(ctx context.Context, in *struct{ Body SetLogLevelRequest }) (*struct{ Body LogSettings }, error) {
		l, err := logbuf.ParseLevel(in.Body.Level)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("The level must be debug, info, warn or error.")
		}
		old := r.d.LogLevel.Level()
		r.d.LogLevel.Set(l)
		if old != l {
			r.s.log.Warn("log level changed", slog.String("from", logbuf.LevelName(old)), slog.String("to", logbuf.LevelName(l)),
				slog.String("request_id", RequestID(ctx)))
		}
		if r.s.d.Bus != nil {
			r.s.d.Bus.Publish(events.Event{Type: events.SettingsChanged, Data: map[string]any{"setting": "logs"}})
		}
		return &struct{ Body LogSettings }{r.logSettings()}, nil
	})
}

// LogEntryEvent is the SSE event name of /logs/stream.
const LogEntryEvent = "log.entry"

// streamLogs serves one /logs/stream client until it leaves or the server
// shuts down. Entries are offered without blocking the logger: a slow client
// misses some and gets "resync".
func (r *opsRoutes) streamLogs(req *http.Request, w http.ResponseWriter, in *logStreamInput) {
	ctx := req.Context()
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	sub := r.d.Logs.Subscribe(sseBuffer)
	defer sub.Close()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var buf bytes.Buffer
	write := func() bool {
		_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
		_, err := w.Write(buf.Bytes())
		buf.Reset()
		if err == nil {
			err = rc.Flush()
		}
		return err == nil
	}
	filter := logbuf.Query{MinLevel: in.Level, Components: logComponents(in.Component)}
	var last uint64
	send := func(e *logbuf.Entry) bool {
		if e.ID <= last {
			return true
		}
		last = e.ID
		if !filter.Match(e) {
			return true
		}
		data, err := json.Marshal(e)
		if err != nil {
			return true
		}
		buf.WriteString("id: " + strconv.FormatUint(e.ID, 10) + "\nevent: " + LogEntryEvent + "\ndata: ")
		buf.Write(data)
		buf.WriteString("\n\n")
		return write()
	}
	buf.WriteString("retry: " + strconv.Itoa(sseRetryMS) + "\n: connected\n\n")
	if !write() {
		return
	}
	if in.LastEventID != "" {
		if id, err := strconv.ParseUint(strings.TrimSpace(in.LastEventID), 10, 64); err == nil {
			for _, e := range r.d.Logs.Entries(logbuf.Query{AfterID: id}) {
				if !send(&e) {
					return
				}
			}
		}
	}
	heartbeat := time.NewTicker(r.s.d.SSEHeartbeat)
	defer heartbeat.Stop()
	var dropped uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.s.streams.Done():
			return
		case <-heartbeat.C:
			buf.WriteString(": ping\n\n")
			if !write() {
				return
			}
		case e, ok := <-sub.C():
			if !ok {
				return
			}
			if d := sub.Dropped(); d > dropped {
				dropped = d
				buf.WriteString("event: " + ResyncEvent + "\ndata: {\"reason\":\"log entries were dropped\"}\n\n")
				if !write() {
					return
				}
			}
			if !send(&e) {
				return
			}
		}
	}
}

// logComponents normalizes the component filter: trimmed, lower case, no
// empty or duplicate name.
func logComponents(in []string) []string {
	var out []string
	for _, v := range in {
		for c := range strings.SplitSeq(v, ",") {
			c = strings.ToLower(strings.TrimSpace(c))
			if c != "" && len(c) <= 64 && !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	return out
}

// ----------------------------------------------------------------- import journal

func (r *opsRoutes) journal() {
	tags := []string{"Import"}
	huma.Register(r.s.api, huma.Operation{
		OperationID: "list-import-journal", Method: http.MethodGet, Path: "/imports/journal",
		Summary: "Interrupted imports that need attention",
		Description: "At startup Gonema reconciles the imports a crash interrupted: it completes the database when the file " +
			"arrived and runs the import again when nothing moved. The entries it could not settle without guessing are " +
			"listed here (and as health issues); all=true lists the latest 500 entries of every status.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		All bool `query:"all" doc:"Every recent entry, not only those needing attention"`
	}) (*struct{ Body []ImportJournalEntry }, error) {
		list, err := r.d.Imports.ListJournal(ctx, in.All)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		out := make([]ImportJournalEntry, len(list))
		for i := range list {
			out[i] = ImportJournalEntry(list[i])
		}
		return &struct{ Body []ImportJournalEntry }{out}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "dismiss-import-journal", Method: http.MethodPost, Path: "/imports/journal/{id}/dismiss",
		Summary:     "Mark an interrupted import as handled",
		Description: "Requires confirm=true. Changes no file: you checked the files by hand. The health issue goes away.",
		Tags:        tags,
	}, func(ctx context.Context, in *struct {
		IDParam
		Confirm bool `query:"confirm" doc:"Must be true"`
	}) (*struct{ Body ImportJournalEntry }, error) {
		out, err := r.d.Imports.DismissJournal(ctx, in.ID, in.Confirm)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{ Body ImportJournalEntry }{ImportJournalEntry(out)}, nil
	})
}
