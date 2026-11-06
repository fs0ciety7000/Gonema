package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/api"
	"github.com/fs0ciety7000/gonema/internal/backup"
	"github.com/fs0ciety7000/gonema/internal/config"
	"github.com/fs0ciety7000/gonema/internal/health"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/logbuf"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// logOutputs are the log outputs besides the logger: the in-memory ring, the
// runtime level and the optional rotating file.
type logOutputs struct {
	level *slog.LevelVar
	buf   *logbuf.Buffer
	file  *logbuf.RotatingFile // nil without GONEMA_LOG_FILE
}

func (o *logOutputs) close() {
	if o != nil && o.file != nil {
		_ = o.file.Close()
	}
}

// newLogging builds the server logger: text or JSON on stderr (and the log
// file when configured), teed to the in-memory ring of the Logs screen, every
// output redacted, at a level that can change at runtime.
func newLogging(cfg *config.Config, stderr io.Writer) (*slog.Logger, *logOutputs, error) {
	outs := &logOutputs{level: new(slog.LevelVar), buf: logbuf.NewBuffer(cfg.Log.Buffer)}
	outs.level.Set(cfg.LogLevel)
	w := stderr
	if cfg.Log.File != "" {
		f, err := logbuf.OpenRotatingFile(cfg.Log.File, int64(cfg.Log.MaxSizeMB)<<20, cfg.Log.Keep)
		if err != nil {
			return nil, nil, fmt.Errorf("log file: %w", err)
		}
		outs.file = f
		w = logbuf.TeeWriter{stderr, f}
	}
	opts := &slog.HandlerOptions{Level: outs.level}
	var base slog.Handler = slog.NewTextHandler(w, opts)
	if cfg.LogFormat == config.FormatJSON {
		base = slog.NewJSONHandler(w, opts)
	}
	return slog.New(logbuf.NewHandler(base, outs.buf, outs.level)), outs, nil
}

// newOps builds the backup service, registers the scheduled backup and
// returns the dependencies of the operations endpoints.
func newOps(ctx context.Context, cfg *config.Config, st *store.Store, key secretbox.Key, src secretbox.KeySource,
	runner *jobs.Runner, imp *importer.Service, outs *logOutputs, log *slog.Logger) (api.OpsDeps, error) {
	bk, err := backup.New(backup.Options{
		DataDir: cfg.DataDir, Store: st, Version: version, Key: key, KeyFromEnv: src == secretbox.SourceEnv, Logger: log,
	})
	if err != nil {
		return api.OpsDeps{}, err
	}
	bk.CleanTemp()
	set := &backup.SettingsStore{St: st}
	set.Get(ctx) // loads the interval for the scheduler
	bk.RegisterJobs(runner, set)
	return api.OpsDeps{
		Backups: bk, BackupSettings: set, BackupJobs: runner,
		Logs: outs.buf, LogLevel: outs.level, LogFile: outs.file, Imports: imp,
	}, nil
}

// registerOpsChecks adds the health checks of interrupted imports and
// scheduled backups.
func registerOpsChecks(h *health.Scheduler, imp *importer.Service, bk *backup.Service) {
	h.Register(health.ImportJournalCheck(imp))
	h.Register(health.BackupCheck(bk))
}

// ----------------------------------------------------------------- CLI

func opsCommand(name string) bool {
	return name == "backup" || name == "restore" || name == "healthcheck"
}

// runOps runs `gonema backup`, `gonema restore` and `gonema healthcheck`.
func runOps(ctx context.Context, cmd string, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	switch cmd {
	case "backup":
		return runBackup(ctx, args, stdout, stderr, getenv)
	case "restore":
		return runRestore(ctx, args, os.Stdin, stdout, stderr, getenv)
	}
	return runHealthcheck(ctx, args, stdout, stderr, getenv)
}

// defaultDataDir is the data directory of the environment (as serve).
func defaultDataDir(getenv func(string) string) string {
	if d := strings.TrimSpace(getenv(config.EnvDataDir)); d != "" {
		return d
	}
	return config.Defaults(inContainer()).DataDir
}

// openForOps opens the backup service of an existing data directory. It
// never creates a database nor a master key.
func openForOps(ctx context.Context, dataDir string, getenv func(string) string) (*backup.Service, *store.Store, error) {
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, nil, err
	}
	dbPath := filepath.Join(dataDir, backup.DBFile)
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil, fmt.Errorf("no Gonema database in %s (%w): check -data-dir", dataDir, err)
	}
	opts := backup.Options{DataDir: dataDir, Version: version}
	if getenv(secretbox.EnvMasterKey) != "" {
		opts.KeyFromEnv = true
	}
	keyPath := filepath.Join(dataDir, backup.KeyFile)
	if _, err := os.Stat(keyPath); err == nil || opts.KeyFromEnv {
		k, _, err := secretbox.LoadKey(keyPath, getenv) // reads only: the file exists or the variable is set
		if err != nil {
			return nil, nil, fmt.Errorf("master key: %w", err)
		}
		opts.Key = k
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("master key: %w", err)
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return nil, nil, err
	}
	opts.Store = st
	bk, err := backup.New(opts)
	if err != nil {
		_ = st.Close()
		return nil, nil, err
	}
	return bk, st, nil
}

func runBackup(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("gonema backup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", defaultDataDir(getenv), "data directory ("+config.EnvDataDir+")")
	includeKey := fs.Bool("include-key", false, "also store the master key ("+backup.KeyFile+") in the archive: whoever has the archive can then decrypt every stored secret")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	bk, st, err := openForOps(ctx, *dataDir, getenv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "gonema:", err)
		return 1
	}
	defer func() { _ = st.Close() }()
	info, err := bk.Create(ctx, backup.KindManual, *includeKey)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "gonema:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "backup created: %s (%d bytes, schema %d)\n", filepath.Join(bk.Dir(), info.Name), info.Size, info.SchemaVersion)
	if info.IncludesKey {
		_, _ = fmt.Fprintln(stdout, "WARNING: this archive includes the master key: anyone who gets it can decrypt your stored secrets. Keep it private.")
	} else {
		_, _ = fmt.Fprintf(stdout, "note: the master key (%s) is not in the archive: keep a copy of it apart from your backups.\n", backup.KeyFile)
	}
	return 0
}

func runRestore(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("gonema restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", defaultDataDir(getenv), "data directory ("+config.EnvDataDir+")")
	restoreKey := fs.Bool("restore-key", false, "also restore the master key from the archive (when it includes one)")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "usage: gonema restore [-data-dir DIR] [-restore-key] [-yes] <archive.zip>\n"+
			"Validates the archive, backs up the current database, and stages the restore: it is applied when Gonema next starts.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	archive := fs.Arg(0)
	if !*yes {
		_, _ = fmt.Fprintf(stdout, "Restore %s? It replaces the whole database (library, settings, history) when Gonema next starts.\nType yes to continue: ", archive)
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if strings.TrimSpace(strings.ToLower(line)) != "yes" {
			_, _ = fmt.Fprintln(stderr, "gonema: restore canceled")
			return 1
		}
	}
	bk, st, err := openForOps(ctx, *dataDir, getenv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "gonema:", err)
		return 1
	}
	defer func() { _ = st.Close() }()
	name := filepath.Base(archive)
	if !backup.ValidName(name) || filepath.Dir(mustAbs(archive)) != bk.Dir() {
		f, err := os.Open(archive) //nolint:gosec // a path given by the operator on the command line
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "gonema:", err)
			return 1
		}
		info, err := bk.Import(ctx, f)
		_ = f.Close()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "gonema:", err)
			return 1
		}
		name = info.Name
	}
	staged, err := bk.StageRestore(ctx, name, *restoreKey, true)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "gonema:", err)
		return 1
	}
	for _, w := range staged.Warnings {
		_, _ = fmt.Fprintln(stdout, "warning:", w)
	}
	_, _ = fmt.Fprintln(stdout, staged.Message)
	return 0
}

func mustAbs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// runHealthcheck asks /health of the local server: exit 0 when it answers 200.
// It is the HEALTHCHECK of the Docker image, which has no shell nor curl.
func runHealthcheck(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("gonema healthcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bind := strings.TrimSpace(getenv(config.EnvBind))
	if bind == "" {
		bind = config.Defaults(inContainer()).Bind
	}
	url := fs.String("url", healthURL(bind), "URL of /health")
	timeout := fs.Duration("timeout", 3*time.Second, "time limit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *url, nil)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "gonema: healthcheck:", err)
		return 2
	}
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "gonema: healthcheck:", err)
		return 1
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintln(stderr, "gonema: healthcheck: HTTP", resp.StatusCode)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "ok")
	return 0
}

// healthURL turns a listening address into the URL of /health on this host
// (an unspecified address is reached on the loopback).
func healthURL(bind string) string {
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		host, port = "127.0.0.1", strconv.Itoa(config.DefaultPort)
	}
	switch host {
	case "", "0.0.0.0", "localhost":
		host = "127.0.0.1"
	case "::", "[::]":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/health"
}
