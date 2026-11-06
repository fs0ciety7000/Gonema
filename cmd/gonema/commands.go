package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/api"
	"github.com/fs0ciety7000/gonema/internal/config"
	"github.com/fs0ciety7000/gonema/internal/demo"
	"github.com/fs0ciety7000/gonema/internal/health"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/mediaserver"
	"github.com/fs0ciety7000/gonema/internal/notify"
)

// newAPI builds the API with every route group. serve and openapi share it,
// so the printed document is exactly what the server serves.
func newAPI(d api.Deps, imp *importer.Service, notes *notify.Service, media *mediaserver.Service,
	checks *health.Scheduler, healthSettings health.SettingsStore) *api.Server {
	srv := api.New(d)
	srv.RegisterImportRoutes(imp)
	srv.RegisterIntegrationRoutes(notes, media)
	srv.RegisterHealthRoutes(checks, healthSettings)
	return srv
}

// runOpenAPI prints the OpenAPI document. Route registration only records the
// handlers, so the services are not started (no database, no network).
func runOpenAPI(stdout, stderr io.Writer) int {
	srv := newAPI(api.Deps{Acquire: &acquire.Service{}, Version: version},
		&importer.Service{}, &notify.Service{}, &mediaserver.Service{}, &health.Scheduler{}, health.SettingsStore{})
	out, err := json.MarshalIndent(srv.OpenAPI(), "", "  ")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "gonema: encode the OpenAPI document:", err)
		return 1
	}
	if _, err := stdout.Write(append(out, '\n')); err != nil {
		_, _ = fmt.Fprintln(stderr, "gonema:", err)
		return 1
	}
	return 0
}

// runDemo runs `gonema demo seed`.
func runDemo(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 || args[0] != "seed" {
		_, _ = fmt.Fprintln(stderr, "usage: gonema demo seed [-films N] [-data-dir DIR] [-seed N] [-prerender=false]")
		return 2
	}
	fs := flag.NewFlagSet("gonema demo seed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	films := fs.Int("films", 1000, fmt.Sprintf("number of fictional movies, 1 to %d", demo.MaxFilms))
	dataDir := fs.String("data-dir", strings.TrimSpace(getenv(config.EnvDataDir)),
		"data directory of the DEMO database; must be new or hold a demo database ("+config.EnvDataDir+")")
	seed := fs.Uint64("seed", 1, "random seed: the same seed gives the same library")
	prerender := fs.Bool("prerender", true, "render the grid posters now (other sizes are rendered on first view)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *dataDir == "" {
		_, _ = fmt.Fprintln(stderr, "gonema: -data-dir is required: use a separate directory for the demo, never your real one")
		return 2
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	res, err := demo.Seed(ctx, demo.Options{DataDir: *dataDir, Films: *films, Seed: *seed, Now: time.Now(), Prerender: *prerender, Logger: log})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "gonema:", err)
		if errors.Is(err, demo.ErrNotDemo) {
			_, _ = fmt.Fprintln(stderr, "gonema: nothing was changed; choose an empty directory for the demo")
		}
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "demo library created in %s: %d films (%d on disk, %d downloading, %d failed), %d posters, in %s\n",
		res.DBPath, res.Films, res.WithFile, res.Downloading, res.Failed, res.Posters, res.Elapsed.Round(time.Millisecond))
	_, _ = fmt.Fprintf(stdout, "start it with: gonema serve -data-dir %s\n", *dataDir)
	return 0
}
