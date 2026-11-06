package main

import (
	"log/slog"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/health"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// newHealth registers the health checks. Run the scheduler with Run.
func newHealth(st *store.Store, bus *events.Bus, log *slog.Logger, lib *library.Service, acq *acquire.Service,
	imp *importer.Service) (*health.Scheduler, health.SettingsStore) {
	set := health.SettingsStore{St: st}
	h := health.New(health.Options{Bus: bus, Logger: log})
	h.Register(health.MetadataCheck(lib, time.Now))
	h.Register(health.RootFoldersCheck(st.Read(), library.OSFS{}, set.Get))
	h.Register(health.IndexersCheck(acq, time.Now, time.Local))
	h.Register(health.ClientsCheck(acq))
	h.Register(health.DownloadsCheck(st, library.OSFS{}, time.Now))
	h.Register(health.RecycleBinCheck(imp, set.Get, time.Now))
	h.Register(health.DatabaseCheck(st, st, time.Now))
	return h, set
}
