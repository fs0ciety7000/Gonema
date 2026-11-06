package api

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/backup"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/logbuf"
)

type opsEnv struct {
	*testEnv
	bk  *backup.Service
	lv  *slog.LevelVar
	buf *logbuf.Buffer
	log *slog.Logger
}

func newOpsEnv(t *testing.T) *opsEnv {
	t.Helper()
	return newOpsEnvWith(t, nil)
}

func newOpsEnvWith(t *testing.T, mod func(*backup.Options)) *opsEnv {
	t.Helper()
	e := newTestEnv(t, "")
	opts := backup.Options{DataDir: t.TempDir(), Store: e.st, Version: "test"}
	if mod != nil {
		mod(&opts)
	}
	bk, err := backup.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	imp, err := importer.New(importer.Options{Store: e.st, Bus: e.bus})
	if err != nil {
		t.Fatal(err)
	}
	lv := new(slog.LevelVar)
	buf := logbuf.NewBuffer(100)
	log := slog.New(logbuf.NewHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: lv}), buf, lv))
	e.srv.RegisterOpsRoutes(OpsDeps{
		Backups: bk, BackupSettings: &backup.SettingsStore{St: e.st}, Logs: buf, LogLevel: lv, Imports: imp,
	})
	e.srv.log = log
	return &opsEnv{testEnv: e, bk: bk, lv: lv, buf: buf, log: log}
}

func TestBackupRoutes(t *testing.T) {
	e := newOpsEnv(t)
	r := e.do(t, "GET", "/api/v1/backups", "")
	list := decode[BackupList](t, r)
	if r.status != 200 || len(list.Backups) != 0 || list.PendingRestore != nil || !strings.Contains(list.KeyNotice, "gonema") {
		t.Fatalf("empty list = %d %s", r.status, r.body)
	}
	r = e.do(t, "POST", "/api/v1/backups", "{}")
	created := decode[BackupInfo](t, r)
	if r.status != 201 || created.IncludesKey || created.Kind != backup.KindManual || !created.Restorable {
		t.Fatalf("create = %d %s", r.status, r.body)
	}
	if list := decode[BackupList](t, e.do(t, "GET", "/api/v1/backups", "")); len(list.Backups) != 1 || list.Backups[0].Name != created.Name {
		t.Errorf("list = %+v", list)
	}

	// Download streams the archive, uncompressed by the gzip middleware.
	r = e.do(t, "GET", "/api/v1/backups/"+created.Name+"/download", "", "Accept-Encoding", "gzip")
	if r.status != 200 || r.header.Get("Content-Type") != "application/zip" || r.header.Get("Content-Encoding") != "" ||
		!strings.Contains(r.header.Get("Content-Disposition"), created.Name) || int64(len(r.body)) != created.Size || !strings.HasPrefix(r.body, "PK") {
		t.Fatalf("download = %d %v (%d bytes)", r.status, r.header, len(r.body))
	}
	archive := r.body

	// Path traversal is impossible: the name must match the archive pattern.
	for _, bad := range []string{"..%2F..%2Fgonema.db", "%2Fetc%2Fpasswd", "gonema-backup-20261106-100000-manual.zip%2F..", "..", "gonema.db"} {
		r := e.do(t, "GET", "/api/v1/backups/"+bad+"/download", "")
		if r.status != 404 && r.status != 422 {
			t.Errorf("download %q = %d %s", bad, r.status, r.body)
		}
	}
	problemOK(t, e.do(t, "GET", "/api/v1/backups/gonema-backup-20991106-100000-manual.zip/download", ""), 404)

	// Upload (multipart) adds a validated archive.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "my-backup.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(fw, archive); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, "POST", "/api/v1/backups/upload", "", "Content-Type", mw.FormDataContentType())
	if r.status != 422 {
		t.Errorf("upload without a body = %d", r.status)
	}
	r = e.uploadBackup(t, mw.FormDataContentType(), body.Bytes())
	uploaded := decode[BackupInfo](t, r)
	if r.status != 201 || !strings.HasSuffix(uploaded.Name, "-uploaded.zip") {
		t.Fatalf("upload = %d %s", r.status, r.body)
	}
	body.Reset()
	mw = multipart.NewWriter(&body)
	fw, _ = mw.CreateFormFile("file", "junk.zip")
	_, _ = io.WriteString(fw, "not a zip")
	_ = mw.Close()
	problemOK(t, e.uploadBackup(t, mw.FormDataContentType(), body.Bytes()), 422)

	// Restore: confirm required, then staged (202), visible in the list, cancelable.
	problemOK(t, e.do(t, "POST", "/api/v1/backups/restore", `{"name":"`+uploaded.Name+`","confirm":false}`), 422)
	problemOK(t, e.do(t, "POST", "/api/v1/backups/restore", `{"name":"../x.zip","confirm":true}`), 404)
	r = e.do(t, "POST", "/api/v1/backups/restore", `{"name":"`+uploaded.Name+`","confirm":true}`)
	staged := decode[RestoreStaged](t, r)
	if r.status != 202 || !staged.RestartRequired || staged.PreRestoreBackup.Kind != backup.KindPreRestore {
		t.Fatalf("restore = %d %s", r.status, r.body)
	}
	if list := decode[BackupList](t, e.do(t, "GET", "/api/v1/backups", "")); list.PendingRestore == nil || list.PendingRestore.Name != uploaded.Name {
		t.Errorf("pending = %+v", list.PendingRestore)
	}
	if r := e.do(t, "DELETE", "/api/v1/backups/restore", ""); r.status != 204 {
		t.Errorf("cancel = %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "DELETE", "/api/v1/backups/restore", ""), 404)

	// Delete needs confirm=true.
	problemOK(t, e.do(t, "DELETE", "/api/v1/backups/"+created.Name, ""), 422)
	if r := e.do(t, "DELETE", "/api/v1/backups/"+created.Name+"?confirm=true", ""); r.status != 204 {
		t.Errorf("delete = %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "DELETE", "/api/v1/backups/"+created.Name+"?confirm=true", ""), 404)

	// Settings.
	if s := decode[BackupSettings](t, e.do(t, "GET", "/api/v1/settings/backups", "")); s != BackupSettings(backup.DefaultSettings()) {
		t.Errorf("default settings = %+v", s)
	}
	r = e.do(t, "PUT", "/api/v1/settings/backups", `{"enabled":true,"intervalHours":24,"keep":7,"includeKey":false}`)
	if s := decode[BackupSettings](t, r); r.status != 200 || s.IntervalHours != 24 || s.Keep != 7 {
		t.Errorf("set = %d %s", r.status, r.body)
	}
	if r := e.do(t, "PUT", "/api/v1/settings/backups", `{"enabled":true,"intervalHours":0,"keep":7,"includeKey":false}`); r.status != 422 {
		t.Errorf("invalid settings = %d", r.status)
	}
}

// uploadBackup posts a raw body with a content type to the backup upload.
func (e *testEnv) uploadBackup(t *testing.T, contentType string, body []byte) response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, e.ts.URL+"/api/v1/backups/upload", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set(APIKeyHeader, e.apiKey)
	resp, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: string(b)}
}

func TestLogRoutes(t *testing.T) {
	e := newOpsEnv(t)
	e.lv.Set(slog.LevelDebug)
	e.log.Debug("debug line")
	e.log.Info("calling indexer", slog.String("url", "https://idx.example/api?apikey=SECRET123&t=search"))
	e.log.Warn("disk almost full", slog.String("path", "/movies"))

	r := e.do(t, "GET", "/api/v1/logs", "")
	all := decode[LogList](t, r)
	if r.status != 200 || len(all.Entries) != 3 || all.Capacity != 100 || all.Entries[0].Message != "debug line" {
		t.Fatalf("logs = %d %s", r.status, r.body)
	}
	if strings.Contains(r.body, "SECRET123") {
		t.Error("secret in the log API")
	}
	if l := decode[LogList](t, e.do(t, "GET", "/api/v1/logs?level=warn", "")); len(l.Entries) != 1 || l.Entries[0].Level != "warn" {
		t.Errorf("warn = %+v", l)
	}
	if l := decode[LogList](t, e.do(t, "GET", "/api/v1/logs?q=INDEXER&limit=5", "")); len(l.Entries) != 1 {
		t.Errorf("q = %+v", l)
	}
	since := url.QueryEscape(time.Now().Add(time.Hour).Format(time.RFC3339))
	if l := decode[LogList](t, e.do(t, "GET", "/api/v1/logs?since="+since, "")); len(l.Entries) != 0 {
		t.Errorf("since = %+v", l)
	}
	if r := e.do(t, "GET", "/api/v1/logs?level=loud", ""); r.status != 422 {
		t.Errorf("bad level = %d", r.status)
	}
	r = e.do(t, "GET", "/api/v1/logs/download", "")
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "text/plain") || strings.Count(r.body, "\n") != 3 || strings.Contains(r.body, "SECRET123") {
		t.Errorf("download = %d %q", r.status, r.body)
	}

	r = e.do(t, "GET", "/api/v1/settings/logs", "")
	if s := decode[LogSettings](t, r); s.Level != "debug" || s.BufferSize != 100 || s.File.Enabled {
		t.Errorf("settings = %s", r.body)
	}
	r = e.do(t, "PUT", "/api/v1/settings/logs", `{"level":"error"}`)
	if s := decode[LogSettings](t, r); r.status != 200 || s.Level != "error" || e.lv.Level() != slog.LevelError {
		t.Errorf("set level = %d %s", r.status, r.body)
	}
	if r := e.do(t, "PUT", "/api/v1/settings/logs", `{"level":"loud"}`); r.status != 422 {
		t.Errorf("bad level = %d", r.status)
	}
	e.lv.Set(slog.LevelInfo)

	// Live stream: replay after Last-Event-ID, then new entries, filtered by level.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.ts.URL+"/api/v1/logs/stream?level=info", http.NoBody)
	req.Header.Set(APIKeyHeader, e.apiKey)
	req.Header.Set("Last-Event-ID", "1")
	resp, err := e.ts.Client().Do(req) //nolint:bodyclose // closed by the defer below
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream = %d %v", resp.StatusCode, resp.Header)
	}
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var got []string
	deadline := time.After(5 * time.Second)
	logged := false
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed; read %q", got)
			}
			got = append(got, l)
			if l == ": connected" && !logged {
				logged = true
				e.log.Debug("hidden by level")
				e.log.Error("live entry")
			}
		case <-deadline:
			t.Fatalf("no live entry; read %q", got)
		}
		if len(got) > 0 && strings.Contains(got[len(got)-1], "live entry") {
			break
		}
	}
	text := strings.Join(got, "\n")
	if !strings.Contains(text, "event: log.entry") || !strings.Contains(text, "calling indexer") || strings.Contains(text, "debug line") ||
		strings.Contains(text, "hidden by level") || strings.Contains(text, "SECRET123") {
		t.Errorf("stream = %s", text)
	}
}

// TestLogRoutesComponent: the component is a field of each entry and filters
// /logs and /logs/stream (a comma list, case-insensitive).
func TestLogRoutesComponent(t *testing.T) {
	e := newOpsEnv(t)
	e.log.With("component", "indexer").Warn("indexer unreachable", slog.String("url", "https://idx.example/api?apikey=SECRET123"))
	e.log.With("component", "download").Info("download imported")
	e.log.Info("no component")

	r := e.do(t, "GET", "/api/v1/logs", "")
	all := decode[LogList](t, r)
	if r.status != 200 || len(all.Entries) != 3 || all.Entries[0].Component != "indexer" || all.Entries[2].Component != "" ||
		all.Entries[0].Attrs["component"] != "" || strings.Contains(r.body, "SECRET123") {
		t.Fatalf("logs = %d %s", r.status, r.body)
	}
	if !strings.Contains(r.body, `"component":""`) {
		t.Errorf("an entry without a component must carry an empty component: %s", r.body)
	}
	l := decode[LogList](t, e.do(t, "GET", "/api/v1/logs?component=Indexer,%20download", ""))
	if len(l.Entries) != 2 || l.Entries[0].Component != "indexer" || l.Entries[1].Component != "download" {
		t.Errorf("component filter = %+v", l)
	}
	if l := decode[LogList](t, e.do(t, "GET", "/api/v1/logs?component=import", "")); len(l.Entries) != 0 {
		t.Errorf("unknown component = %+v", l)
	}
	if l := decode[LogList](t, e.do(t, "GET", "/api/v1/logs?component=indexer&level=error", "")); len(l.Entries) != 0 {
		t.Errorf("component and level = %+v", l)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.ts.URL+"/api/v1/logs/stream?component=download", http.NoBody)
	req.Header.Set(APIKeyHeader, e.apiKey)
	req.Header.Set("Last-Event-ID", "0")
	resp, err := e.ts.Client().Do(req) //nolint:bodyclose // closed by the defer below
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stream = %d", resp.StatusCode)
	}
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var got []string
	deadline := time.After(5 * time.Second)
	logged := false
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed; read %q", got)
			}
			got = append(got, l)
			if l == ": connected" && !logged {
				logged = true
				e.log.With("component", "indexer").Error("other component")
				e.log.Error("no component either")
				e.log.With("component", "download").Info("live download")
			}
		case <-deadline:
			t.Fatalf("no live entry; read %q", got)
		}
		if len(got) > 0 && strings.Contains(got[len(got)-1], "live download") {
			break
		}
	}
	text := strings.Join(got, "\n")
	if !strings.Contains(text, "download imported") || !strings.Contains(text, `"component":"download"`) ||
		strings.Contains(text, "other component") || strings.Contains(text, "no component") || strings.Contains(text, "SECRET123") {
		t.Errorf("stream = %s", text)
	}
}

func TestImportJournalRoutes(t *testing.T) {
	e := newOpsEnv(t)
	r := e.do(t, "GET", "/api/v1/imports/journal", "")
	if r.status != 200 || strings.TrimSpace(r.body) != "[]" {
		t.Errorf("journal = %d %s", r.status, r.body)
	}
	if r := e.do(t, "GET", "/api/v1/imports/journal?all=true", ""); r.status != 200 {
		t.Errorf("all = %d", r.status)
	}
	problemOK(t, e.do(t, "POST", "/api/v1/imports/journal/5/dismiss", ""), 422)
	problemOK(t, e.do(t, "POST", "/api/v1/imports/journal/5/dismiss?confirm=true", ""), 404)
}

func TestOpsRoutesNeedAuth(t *testing.T) {
	e := newOpsEnv(t)
	for _, p := range []string{"/api/v1/backups", "/api/v1/logs", "/api/v1/logs/download", "/api/v1/imports/journal", "/api/v1/settings/logs"} {
		req, _ := http.NewRequestWithContext(context.Background(), "GET", e.ts.URL+p, http.NoBody)
		resp, err := e.ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without credentials = %d", p, resp.StatusCode)
		}
	}
}

// TestBackupUploadLimit: the upload is refused before its body is read when
// it is larger than the limit (413) or the disk has no room for it (507).
func TestBackupUploadLimit(t *testing.T) {
	var mw *multipart.Writer
	body := func() []byte {
		var b bytes.Buffer
		mw = multipart.NewWriter(&b)
		fw, _ := mw.CreateFormFile("file", "x.zip")
		_, _ = fw.Write(bytes.Repeat([]byte("z"), 4<<10))
		_ = mw.Close()
		return b.Bytes()
	}
	small := newOpsEnvWith(t, func(o *backup.Options) { o.MaxDBSize = 1 << 10 }) // limit 1 KiB
	data := body()
	if d := problemOK(t, small.uploadBackup(t, mw.FormDataContentType(), data), 413); !strings.Contains(d, "larger than") {
		t.Errorf("413 detail = %q", d)
	}
	full := newOpsEnvWith(t, func(o *backup.Options) { o.FreeSpace = func(string) (uint64, error) { return 1 << 20, nil } })
	data = body()
	if d := problemOK(t, full.uploadBackup(t, mw.FormDataContentType(), data), 507); !strings.Contains(d, "free space") {
		t.Errorf("507 detail = %q", d)
	}
}
