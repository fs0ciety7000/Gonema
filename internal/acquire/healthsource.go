package acquire

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/search"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// probeTimeout bounds the connection test of one client by the health check.
const probeTimeout = 15 * time.Second

// IndexerState is an indexer and its circuit breaker, for the health checks.
type IndexerState struct {
	ID   int64
	Name string
	// Enabled is true when the indexer serves RSS, automatic or interactive
	// searches.
	Enabled bool
	Health  search.Health
}

// IndexerStates returns every indexer with its breaker state: the live one
// when the indexer was used since the start, else the persisted one.
func (s *Service) IndexerStates(ctx context.Context) ([]IndexerState, error) {
	rows, err := s.st.Read().ListIndexers(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("acquire: list indexers: %w", err))
	}
	out := make([]IndexerState, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		h, ok := s.orch.Health(r.ID)
		if !ok {
			h = healthOf(r)
		}
		out = append(out, IndexerState{
			ID: r.ID, Name: r.Name, Enabled: r.EnableRss || r.EnableAutoSearch || r.EnableInteractive, Health: h,
		})
	}
	return out, nil
}

// ClientState is a download client and the outcome of a connection test.
type ClientState struct {
	ID       int64
	Name     string
	Kind     string
	Protocol string
	Host     string
	Port     int64
	Enabled  bool
	// Err is the test failure (matches the download.Err* kinds); nil when the
	// client answered or is disabled (not tested).
	Err error
}

// ProbeClients tests every enabled download client concurrently and records
// the outcome in the client health, like the queue tracker does.
func (s *Service) ProbeClients(ctx context.Context) ([]ClientState, error) {
	rows, err := s.st.Read().ListDownloadClients(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("acquire: list download clients: %w", err))
	}
	out := make([]ClientState, len(rows))
	var wg sync.WaitGroup
	for i := range rows {
		r := &rows[i]
		out[i] = ClientState{ID: r.ID, Name: r.Name, Kind: r.Kind, Protocol: r.Protocol, Host: r.Host, Port: r.Port, Enabled: r.Enabled}
		if !r.Enabled {
			continue
		}
		wg.Go(func() { out[i].Err = s.probeClient(ctx, r) })
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) probeClient(ctx context.Context, r *db.DownloadClient) error {
	cl, err := s.downloadClient(r)
	if err != nil {
		return err
	}
	tctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	err = cl.Test(tctx)
	if ctx.Err() == nil {
		s.recordClientHealth(ctx, r, err)
	}
	return err
}
