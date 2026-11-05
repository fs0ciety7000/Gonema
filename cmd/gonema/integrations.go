package main

import (
	"log/slog"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/health"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/mediaserver"
	"github.com/fs0ciety7000/gonema/internal/notify"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// startIntegrations starts the notification dispatcher and the media server
// refresher, both following the bus. Close them before closing the bus.
func startIntegrations(st *store.Store, box *secretbox.Box, bus *events.Bus, lib *library.Service, log *slog.Logger) (notes *notify.Service, media *mediaserver.Service, err error) {
	notes, err = notify.NewService(notify.ServiceOptions{Store: st, Box: box, Bus: bus, Logger: log, Version: version, Images: lib})
	if err != nil {
		return nil, nil, err
	}
	media, err = mediaserver.NewService(mediaserver.ServiceOptions{Store: st, Box: box, Bus: bus, Logger: log, Version: version})
	if err != nil {
		return nil, nil, err
	}
	notes.Start()
	media.Start()
	return notes, media, nil
}

// registerIntegrationChecks adds the health checks of the notifications and
// media servers; they run again after their health.changed events.
func registerIntegrationChecks(h *health.Scheduler, notes *notify.Service, media *mediaserver.Service) {
	h.Register(health.Check{ID: notify.CheckID, Triggers: []events.Type{events.HealthChanged}, Run: notes.HealthIssues})
	h.Register(health.Check{ID: mediaserver.CheckID, Triggers: []events.Type{events.HealthChanged}, Run: media.HealthIssues})
}
