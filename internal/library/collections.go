package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// CollectionDetails is a collection, the library movies that belong to it and,
// when the provider answered, all of its parts.
type CollectionDetails struct {
	Collection db.Collection
	Movies     []db.Movie
	Parts      []metadata.MovieSummary // nil when the provider is unavailable
	PartsError string                  // why Parts is nil, plain language
}

// GetCollection returns a collection with its movies.
func (s *Service) GetCollection(ctx context.Context, id int64) (CollectionDetails, error) {
	var d CollectionDetails
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		var err error
		if d.Collection, err = q.GetCollection(ctx, id); err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return notFound("collection %d does not exist", id)
			}
			return err
		}
		d.Movies, err = q.ListCollectionMovies(ctx, id)
		return err
	})
	if err != nil {
		return CollectionDetails{}, fmt.Errorf("library: get collection %d: %w", id, err)
	}
	p, err := s.Provider()
	if err != nil {
		d.PartsError = ErrNoProvider.Error()
		return d, nil
	}
	c, err := p.Collection(ctx, int(d.Collection.TmdbID))
	if err != nil {
		s.log.Warn("collection parts unavailable", slog.Int64("collection_id", id), slog.Any("error", err))
		d.PartsError = "TMDB did not return the movies of this collection; try again later"
		return d, nil
	}
	d.Parts = c.Parts
	return d, nil
}

// MonitorOptions are the settings of a monitored collection.
type MonitorOptions struct {
	Monitored           bool
	QualityProfileID    int64
	RootFolderID        int64
	MinimumAvailability string
	SearchOnAdd         bool
}

// MonitorResult reports what MonitorCollection did.
type MonitorResult struct {
	Collection db.Collection
	Added      []db.Movie
	Skipped    []SkippedPart
}

// SkippedPart is a collection movie that was not added, with the reason.
type SkippedPart struct {
	TmdbID int    `json:"tmdbId"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// MonitorCollection saves the collection settings and, when monitored, adds
// every movie of the collection that is not in the library yet (excluded
// movies are skipped). Already present movies are left as they are.
func (s *Service) MonitorCollection(ctx context.Context, id int64, opts MonitorOptions) (MonitorResult, error) {
	if opts.MinimumAvailability == "" {
		opts.MinimumAvailability = AvailReleased
	}
	if !validAvailability(opts.MinimumAvailability) {
		return MonitorResult{}, invalid("minimumAvailability", "minimum availability must be announced, in_cinemas or released")
	}
	p := db.UpdateCollectionSettingsParams{ID: id, Monitored: opts.Monitored, MinimumAvailability: opts.MinimumAvailability}
	if opts.Monitored {
		if err := s.checkRefs(ctx, opts.RootFolderID, opts.QualityProfileID); err != nil {
			return MonitorResult{}, err
		}
		p.QualityProfileID, p.RootFolderID = &opts.QualityProfileID, &opts.RootFolderID
	}
	var res MonitorResult
	var err error
	if res.Collection, err = s.st.Write().UpdateCollectionSettings(ctx, p); err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return MonitorResult{}, notFound("collection %d does not exist", id)
		}
		return MonitorResult{}, store.MapError(fmt.Errorf("library: update collection %d: %w", id, err))
	}
	s.publish(events.Event{Type: events.CollectionUpdated, Data: map[string]any{"id": id, "monitored": opts.Monitored}})
	if !opts.Monitored {
		return res, nil
	}
	prov, err := s.Provider()
	if err != nil {
		return res, err
	}
	c, err := prov.Collection(ctx, int(res.Collection.TmdbID))
	if err != nil {
		return res, fmt.Errorf("library: collection %d parts: %w", id, err)
	}
	for i := range c.Parts {
		part := &c.Parts[i]
		excluded, err := s.st.Read().IsImportListExcluded(ctx, int64(part.TMDBID))
		if err != nil {
			return res, store.MapError(fmt.Errorf("library: exclusion of %d: %w", part.TMDBID, err))
		}
		if excluded {
			res.Skipped = append(res.Skipped, SkippedPart{TmdbID: part.TMDBID, Title: part.Title, Reason: "on the exclusion list"})
			continue
		}
		m, created, err := s.AddMovie(ctx, part.TMDBID, AddOptions{
			RootFolderID: opts.RootFolderID, QualityProfileID: opts.QualityProfileID,
			MinimumAvailability: opts.MinimumAvailability, Monitored: true, SearchOnAdd: opts.SearchOnAdd,
		})
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			s.log.Warn("collection part not added", slog.Int("tmdb_id", part.TMDBID), slog.Any("error", err))
			res.Skipped = append(res.Skipped, SkippedPart{TmdbID: part.TMDBID, Title: part.Title, Reason: err.Error()})
		case created:
			res.Added = append(res.Added, m)
		}
	}
	return res, nil
}
