// Command gonema is a self-hosted movie collection manager.
//
// Usage:
//
//	gonema [serve] [flags]   run the server (default)
//	gonema version           print the version
//	gonema openapi           print the OpenAPI document of the API
//	gonema demo seed [flags] create a demo database of fictional movies
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
	"strings"
	"syscall"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/api"
	"github.com/fs0ciety7000/gonema/internal/auth"
	"github.com/fs0ciety7000/gonema/internal/backup"
	"github.com/fs0ciety7000/gonema/internal/config"
	"github.com/fs0ciety7000/gonema/internal/demo"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/logbuf"
	"github.com/fs0ciety7000/gonema/internal/metadata/tmdb"
	"github.com/fs0ciety7000/gonema/internal/netx"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/webui"
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
	if len(args) > 0 && (args[0] == "serve" || args[0] == "version" || args[0] == "help" || args[0] == "openapi" || args[0] == "demo" ||
		opsCommand(args[0])) {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "version":
		_, _ = fmt.Fprintln(stdout, "gonema", version)
		return 0
	case "help":
		_, _ = fmt.Fprintln(stdout, "usage: gonema [serve] [flags] | gonema version | gonema openapi | gonema demo seed [flags]\n"+
			"       gonema backup [flags] | gonema restore [flags] <archive.zip> | gonema healthcheck [flags]\n"+
			"run `gonema <command> -h` for the flags")
		return 0
	case "openapi":
		return runOpenAPI(stdout, stderr)
	case "demo":
		return runDemo(ctx, args, stdout, stderr, getenv)
	case "backup", "restore", "healthcheck":
		return runOps(ctx, cmd, args, stdout, stderr, getenv)
	}
	cfg, err := config.Load(args, config.Env{Getenv: getenv, InContainer: inContainer()}, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "gonema:", err)
		return 2
	}
	log, outs, err := newLogging(&cfg, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "gonema:", err)
		return 2
	}
	defer outs.close()
	if err := serve(ctx, &cfg, log, outs, getenv); err != nil {
		log.Error("gonema stopped with an error", slog.String(logbuf.ComponentKey, "server"), slog.Any("error", err))
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
// in order: stop accepting and end event streams, stop the queue tracker and
// the health checks, the jobs and the background searches and imports, drain
// the history writer, close the database.
func serve(ctx context.Context, cfg *config.Config, base *slog.Logger, outs *logOutputs, getenv func(string) string) (err error) {
	start := time.Now()
	logFor := func(name string) *slog.Logger { return base.With(slog.String(logbuf.ComponentKey, name)) }
	log := logFor("server")
	log.Info("starting gonema", append([]any{slog.String("version", version)}, cfg.LogAttrs()...)...)
	netx.Configure(cfg.Security.Outbound)
	if cfg.Security.AuthMode == config.AuthDisabledForLocal {
		log.Warn("GONEMA_AUTH=disabled_for_local: requests from loopback and private addresses skip the login; anyone on the LAN can use Gonema")
		if inContainer() {
			log.Warn("GONEMA_AUTH=disabled_for_local in a container: with Docker or Podman port publishing (docker-proxy, rootless " +
				"networking), every client can appear to come from the bridge gateway, a private address, and skip the login. " +
				"Prefer GONEMA_AUTH=enabled, or use host networking on a trusted LAN only (see docs/security.md)")
		}
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

	if _, err := backup.ApplyPending(cfg.DataDir, time.Now(), logFor("backup")); err != nil {
		return fmt.Errorf("apply the staged restore: %w", err)
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
	history := events.NewHistoryWriter(bus, st, logFor("history"))
	runner := jobs.New(st, jobs.Options{Workers: cfg.Workers, Logger: logFor("jobs"), Bus: bus})

	lib, err := library.New(library.Options{
		Store: st, Box: box, Jobs: runner, Bus: bus, Logger: logFor("library"), DataDir: cfg.DataDir,
		NewProvider: func(key string) (library.KeyedProvider, error) {
			c, err := tmdb.New(key, tmdb.Options{Version: version, Logger: logFor("metadata")})
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
	var imageStore api.ImageStore = images
	if isDemo, err := lib.IsDemo(ctx); err != nil {
		return err
	} else if isDemo {
		log.Warn("this data directory holds DEMO data: fictional movies created by `gonema demo seed`")
		imageStore = demo.NewImages(images, st)
	}
	ui, err := webui.New(webui.Options{BaseURL: cfg.BaseURL})
	if err != nil {
		return err
	}
	if !ui.Built() {
		log.Warn("this binary has no web interface (built without `make web`): a placeholder page is served")
	}

	imp, err := importer.New(importer.Options{Store: st, Matcher: lib, Library: lib, Bus: bus, Logger: logFor("import"), DataDir: cfg.DataDir, Jobs: runner})
	if err != nil {
		return err
	}
	imp.RegisterJobs(runner)
	lib.SetReservedFolders(func(ctx context.Context) []string { // a root folder cannot be (in) the recycle bin
		if set, err := imp.Settings(ctx); err == nil && set.RecycleBin != "" {
			return []string{set.RecycleBin}
		}
		return nil
	})
	if _, err := imp.Recover(ctx); err != nil { // before the queue tracker resumes imports
		return err
	}
	ops, err := newOps(ctx, cfg, st, key, src, runner, imp, outs, logFor("backup"))
	if err != nil {
		return err
	}
	acq, err := acquire.New(acquire.Options{
		Store: st, Box: box, Matcher: lib, Jobs: runner, Bus: bus, Logger: logFor("acquire"), Version: version,
		Importer: importAdapter{imp}, ImporterWritesHistory: true,
	})
	if err != nil {
		return err
	}
	if err := acq.Init(ctx); err != nil {
		return err
	}
	acq.RegisterJobs(ctx, runner)
	notes, media, err := startIntegrations(st, box, bus, lib, logFor)
	if err != nil {
		return err
	}

	checks, healthSettings := newHealth(st, bus, logFor("health"), lib, acq, imp)
	registerOpsChecks(checks, imp, ops.Backups)
	registerIntegrationChecks(checks, notes, media)
	authSvc, err := auth.New(auth.Options{Store: st, Logger: logFor("auth")})
	if err != nil {
		return err
	}
	if need, err := authSvc.NeedsSetup(ctx); err != nil {
		return err
	} else if need {
		tok, err := authSvc.SetupToken(ctx)
		if err != nil {
			return err
		}
		// The token is logged under a key the redaction does not hide: reading
		// the logs is what proves the right to create the account remotely.
		log.Warn("no account exists yet: open the web interface to create the admin account. From another machine, "+
			"enter this one-time setup token; from the server itself (http://localhost) it is not needed",
			slog.String("setup", tok), slog.String("bind", cfg.Bind))
	}
	srv := newAPI(api.Deps{
		Library: lib, Acquire: acq, Jobs: runner, Bus: bus, Store: st, Logger: logFor("api"), Version: version,
		StartedAt: start, DataDir: cfg.DataDir, BaseURL: cfg.BaseURL, Bind: cfg.Bind, LogLevel: strings.ToLower(cfg.LogLevel.String()),
		Images: imageStore, UI: ui, Auth: authSvc, Security: cfg.Security,
	}, imp, notes, media, checks, healthSettings, ops)
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(logFor("http").Handler(), slog.LevelWarn),
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
	healthCtx, stopHealth := context.WithCancel(context.WithoutCancel(ctx))
	healthDone := make(chan struct{})
	go func() {
		defer close(healthDone)
		checks.Run(healthCtx)
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
	stopHealth()
	<-healthDone
	if jerr := runner.Stop(sctx); jerr != nil {
		errs = append(errs, jerr)
	}
	acq.Close()
	notes.Close()
	media.Close()
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
