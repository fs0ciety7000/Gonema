// Command gonema is a self-hosted movie collection manager.
//
// Usage:
//
//	gonema [serve] [flags]   run the server (default)
//	gonema version           print the version
//
// Configuration comes from flags and GONEMA_* environment variables; see
// package config or `gonema serve -h`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/api"
	"github.com/fs0ciety7000/gonema/internal/config"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/metadata/tmdb"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// shutdownTimeout bounds the graceful shutdown.
const shutdownTimeout = 15 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv)
	stop()
	os.Exit(code)
}

// run executes a subcommand and returns the process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	cmd := "serve"
	if len(args) > 0 && (args[0] == "serve" || args[0] == "version" || args[0] == "help") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "version":
		_, _ = fmt.Fprintln(stdout, "gonema", version)
		return 0
	case "help":
		_, _ = fmt.Fprintln(stdout, "usage: gonema [serve] [flags] | gonema version\nrun `gonema serve -h` for the flags")
		return 0
	}
	cfg, err := config.Load(args, config.Env{Getenv: getenv, InContainer: inContainer()}, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "gonema:", err)
		return 2
	}
	log := cfg.NewLogger(stderr)
	if err := serve(ctx, &cfg, log, getenv); err != nil {
		log.Error("gonema stopped with an error", slog.Any("error", err))
		return 1
	}
	return 0
}

// inContainer reports whether the process runs in a Docker or Podman container.
func inContainer() bool {
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return false
}

// serve wires the services, serves HTTP until ctx is canceled, then shuts down
// in order: stop accepting and end event streams, stop the queue tracker, the
// jobs and the background searches and imports, drain the history writer,
// close the database.
func serve(ctx context.Context, cfg *config.Config, log *slog.Logger, getenv func(string) string) (err error) {
	start := time.Now()
	log.Info("starting gonema", append([]any{slog.String("version", version)}, cfg.LogAttrs()...)...)
	if !cfg.IsLoopback() {
		log.Warn("listening on a non-local address without authentication (added in a later version): restrict access with a firewall or reverse proxy",
			slog.String("bind", cfg.Bind))
	}

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	key, src, err := secretbox.LoadKey(filepath.Join(cfg.DataDir, "gonema.key"), getenv)
	if err != nil {
		return fmt.Errorf("master key: %w", err)
	}
	if src == secretbox.SourceCreated {
		log.Warn("created a new master key: back up gonema.key separately from the database", slog.String("path", filepath.Join(cfg.DataDir, "gonema.key")))
	}
	box, err := secretbox.New(key)
	if err != nil {
		return fmt.Errorf("master key: %w", err)
	}

	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "gonema.db"))
	if err != nil {
		return err
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			err = errors.Join(err, cerr)
		}
	}()

	bus := events.New(events.Options{})
	history := events.NewHistoryWriter(bus, st, log)
	runner := jobs.New(st, jobs.Options{Workers: cfg.Workers, Logger: log, Bus: bus})

	lib, err := library.New(library.Options{
		Store: st, Box: box, Jobs: runner, Bus: bus, Logger: log,
		NewProvider: func(key string) (library.KeyedProvider, error) {
			c, err := tmdb.New(key, tmdb.Options{Version: version, Logger: log})
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	})
	if err != nil {
		return err
	}
	images, err := imagecache.New(filepath.Join(cfg.DataDir, "cache"), lib, imagecache.Options{
		UserAgent: "Gonema/" + version + " (+https://github.com/fs0ciety7000/gonema)",
	})
	if err != nil {
		return err
	}
	lib.SetImages(images)
	if err := lib.Init(ctx); err != nil {
		return err
	}
	lib.RegisterJobs(runner)

	imp, err := importer.New(importer.Options{Store: st, Matcher: lib, Library: lib, Bus: bus, Logger: log})
	if err != nil {
		return err
	}
	acq, err := acquire.New(acquire.Options{
		Store: st, Box: box, Matcher: lib, Jobs: runner, Bus: bus, Logger: log, Version: version,
		Importer: importAdapter{imp},
	})
	if err != nil {
		return err
	}
	if err := acq.Init(ctx); err != nil {
		return err
	}
	acq.RegisterJobs(ctx, runner)

	srv := api.New(api.Deps{
		Library: lib, Acquire: acq, Jobs: runner, Bus: bus, Store: st, Logger: log, Version: version,
		StartedAt: start, DataDir: cfg.DataDir, BaseURL: cfg.BaseURL,
	})
	srv.RegisterImportRoutes(imp)
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	httpSrv.RegisterOnShutdown(srv.Shutdown)

	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", cfg.Bind)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Bind, err)
	}
	if err := runner.Start(context.WithoutCancel(ctx)); err != nil {
		_ = ln.Close()
		return err
	}
	trackCtx, stopTracker := context.WithCancel(context.WithoutCancel(ctx))
	trackerDone := make(chan struct{})
	go func() {
		defer close(trackerDone)
		acq.RunTracker(trackCtx)
	}()
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()
	log.Info("gonema is ready", slog.String("address", "http://"+ln.Addr().String()+cfg.BaseURL+"/"),
		slog.Duration("startup", time.Since(start)))

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err = <-serveErr:
		log.Error("HTTP server failed", slog.Any("error", err))
	}

	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	var errs []error
	if serr := httpSrv.Shutdown(sctx); serr != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", serr))
	}
	stopTracker()
	<-trackerDone
	if jerr := runner.Stop(sctx); jerr != nil {
		errs = append(errs, jerr)
	}
	acq.Close()
	bus.Close()
	if herr := history.Wait(sctx); herr != nil {
		errs = append(errs, fmt.Errorf("history writer: %w", herr))
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		errs = append(errs, err)
	}
	log.Info("gonema stopped")
	return errors.Join(errs...)
}
