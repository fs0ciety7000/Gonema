package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// server is a gonema process started by a test.
type server struct {
	t    *testing.T
	cmd  *exec.Cmd
	base string
	logs *bytes.Buffer
	done chan error
}

func buildBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the binary")
	}
	if runtime.GOOS == "windows" {
		t.Skip("uses SIGTERM")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go command not found")
	}
	bin := filepath.Join(t.TempDir(), "gonema")
	build := exec.Command(goBin, "build", "-trimpath", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// smokeEnv is the environment of the smoke test: loopback only, and the API
// opened to direct local requests (GONEMA_AUTH=disabled_for_local) so the
// test needs no account.
func smokeEnv(dataDir string) []string {
	return append(os.Environ(), "GONEMA_DATA_DIR="+dataDir, "GONEMA_BIND=127.0.0.1:0", "GONEMA_LOG_LEVEL=info",
		"GONEMA_MASTER_KEY=", "GONEMA_AUTH=disabled_for_local", "GONEMA_LOG_FILE=logs/gonema.log")
}

func startServer(t *testing.T, bin, dataDir string) *server {
	t.Helper()
	s := &server{t: t, cmd: exec.Command(bin, "serve"), logs: new(bytes.Buffer), done: make(chan error, 1)}
	s.cmd.Env = smokeEnv(dataDir)
	stderr, err := s.cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	addr := make(chan string, 1)
	re := regexp.MustCompile(`address=(http://[0-9.:]+)/`)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			s.logs.WriteString(sc.Text() + "\n") // read by stop, after done
			if m := re.FindStringSubmatch(sc.Text()); m != nil {
				addr <- m[1]
			}
		}
		s.done <- s.cmd.Wait()
	}()
	t.Cleanup(func() {
		if s.cmd.ProcessState == nil {
			_ = s.cmd.Process.Kill()
			if err := <-s.done; err != nil {
				t.Logf("killed server: %v", err)
			}
		}
	})
	select {
	case s.base = <-addr:
	case <-time.After(15 * time.Second):
		t.Fatal("the server did not start")
	}
	return s
}

func (s *server) stop() string {
	s.t.Helper()
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		s.t.Fatal(err)
	}
	select {
	case err := <-s.done:
		if err != nil {
			s.t.Errorf("exit: %v", err)
		}
	case <-time.After(20 * time.Second):
		s.t.Fatal("no exit within 20 s of SIGTERM")
	}
	return s.logs.String()
}

func (s *server) call(method, path, body string, want int) []byte {
	s.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, s.base+path, r)
	if err != nil {
		s.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		s.t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, want, b)
	}
	return b
}

func runCLI(t *testing.T, bin, dataDir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = smokeEnv(dataDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gonema %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// crashImport imports a file in-process with a crash injected after the file
// was moved and before the database commit: the state a power cut leaves.
func crashImport(t *testing.T, dataDir string) (movieID int64, moved string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dataDir, "gonema.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	}()
	movies, downloads := filepath.Join(dataDir, "movies"), filepath.Join(dataDir, "downloads")
	rel := "Fight.Club.1999.1080p.WEB-DL.DD5.1.H264-GRP"
	src := filepath.Join(downloads, rel+".mkv")
	for _, d := range []string{downloads, movies} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(src, []byte("movie"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(src, 30<<20); err != nil {
		t.Fatal(err)
	}
	rf, err := st.Write().CreateRootFolder(ctx, movies)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := st.Read().ListQualityProfiles(ctx)
	if err != nil || len(profiles) == 0 {
		t.Fatalf("profiles: %v", err)
	}
	m, err := st.Write().InsertMovie(ctx, db.InsertMovieParams{
		TmdbID: 550, Title: "Fight Club", SortTitle: "fight club", CleanTitle: parser.CleanTitle("Fight Club"), Year: 1999,
		Status: "released", Genres: "[]", Ratings: "{}", Extra: "{}", Monitored: true, QualityProfileID: profiles[0].ID,
		MinimumAvailability: "released", RootFolderID: rf.ID, Path: "Fight Club (1999)", AddedAt: store.Millis(time.Now()),
	})
	if err != nil {
		t.Fatal(store.MapError(err))
	}
	imp, err := importer.New(importer.Options{Store: st, Fault: func(step string) error {
		if step == importer.FaultBeforeCommit {
			return errors.New("simulated crash")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	set := importer.DefaultSettings()
	set.AllowedRoots = []string{downloads}
	if _, err := imp.SetSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	if _, err := imp.Import(ctx, importer.Request{MovieID: m.ID, LocalPath: src, Protocol: decision.Usenet, ReleaseTitle: rel}); err == nil || !strings.Contains(err.Error(), "simulated crash") {
		t.Fatalf("the import was not interrupted: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(movies, "Fight Club (1999)"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("moved file: %v %v", entries, err)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still there: %v", err)
	}
	return m.ID, filepath.Join(movies, "Fight Club (1999)", entries[0].Name())
}

// TestSmokeOperations runs the operations end to end on the real binary:
// health, backup create and list, restart, import crash recovery at startup,
// the backup and healthcheck commands, and a restore applied at the next start.
func TestSmokeOperations(t *testing.T) {
	bin := buildBinary(t)
	dataDir := t.TempDir()

	s := startServer(t, bin, dataDir)
	s.call("GET", "/health", "", 200)
	if out := runCLI(t, bin, dataDir, "healthcheck", "-url", s.base+"/health"); !strings.Contains(out, "ok") {
		t.Errorf("healthcheck = %q", out)
	}
	var created struct {
		Name        string `json:"name"`
		IncludesKey bool   `json:"includesKey"`
	}
	if err := json.Unmarshal(s.call("POST", "/api/v1/backups", "{}", 201), &created); err != nil || created.Name == "" || created.IncludesKey {
		t.Fatalf("backup = %+v %v", created, err)
	}
	var list struct {
		Backups []struct {
			Name string `json:"name"`
		} `json:"backups"`
	}
	if err := json.Unmarshal(s.call("GET", "/api/v1/backups", "", 200), &list); err != nil || len(list.Backups) != 1 || list.Backups[0].Name != created.Name {
		t.Fatalf("list = %+v %v", list, err)
	}
	if b := s.call("GET", "/api/v1/logs?q=backup+created", "", 200); !strings.Contains(string(b), "backup created") {
		t.Errorf("logs = %s", b)
	}
	// Each service logs under its component (wired in serve).
	if b := s.call("GET", "/api/v1/logs?component=backup&q=backup+created", "", 200); !strings.Contains(string(b), `"component":"backup"`) {
		t.Errorf("backup component = %s", b)
	}
	if b := s.call("GET", "/api/v1/logs?component=server", "", 200); !strings.Contains(string(b), "gonema is ready") ||
		strings.Contains(string(b), "backup created") {
		t.Errorf("server component = %s", b)
	}
	s.stop()

	// Crash between the file move and the database commit, then restart.
	movieID, moved := crashImport(t, dataDir)
	s = startServer(t, bin, dataDir)
	s.call("GET", "/health", "", 200)
	var journal []struct {
		MovieID int64  `json:"movieId"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(s.call("GET", "/api/v1/imports/journal?all=true", "", 200), &journal); err != nil ||
		len(journal) != 1 || journal[0].Status != "done" || journal[0].MovieID != movieID {
		t.Fatalf("journal = %+v %v", journal, err)
	}
	b := s.call("GET", "/api/v1/movies/"+itoa(movieID), "", 200)
	if !strings.Contains(string(b), filepath.Base(moved)) {
		t.Errorf("movie after recovery has no file: %s", b)
	}
	logs := s.stop()
	if !strings.Contains(logs, "interrupted import completed") {
		t.Errorf("no recovery log line:\n%s", logs)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("recovered file: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dataDir, "logs", "gonema.log")); err != nil || !strings.Contains(string(b), "gonema is ready") {
		t.Errorf("log file: %v", err)
	}

	// CLI backup while stopped, then restore the first backup: applied at the
	// next start, the movie added after it is gone.
	if out := runCLI(t, bin, dataDir, "backup"); !strings.Contains(out, "backup created") || !strings.Contains(out, "not in the archive") {
		t.Errorf("backup command = %q", out)
	}
	if out := runCLI(t, bin, dataDir, "restore", "-yes", filepath.Join(dataDir, "backups", created.Name)); !strings.Contains(out, "restart Gonema") {
		t.Errorf("restore command = %q", out)
	}
	s = startServer(t, bin, dataDir)
	s.call("GET", "/api/v1/movies/"+itoa(movieID), "", 404)
	if logs := s.stop(); !strings.Contains(logs, "backup restored") {
		t.Errorf("no restore log line:\n%s", logs)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestHealthURL(t *testing.T) {
	for bind, want := range map[string]string{
		"0.0.0.0:7676": "http://127.0.0.1:7676/health", "127.0.0.1:9000": "http://127.0.0.1:9000/health",
		"[::]:7676": "http://[::1]:7676/health", ":8080": "http://127.0.0.1:8080/health", "nonsense": "http://127.0.0.1:7676/health",
		"192.168.1.5:7676": "http://192.168.1.5:7676/health",
	} {
		if got := healthURL(bind); got != want {
			t.Errorf("healthURL(%q) = %q, want %q", bind, got, want)
		}
	}
}
