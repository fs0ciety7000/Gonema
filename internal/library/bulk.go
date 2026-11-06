package library

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// MaxBulk is the largest number of movies of one bulk request.
const MaxBulk = 10000

// BulkSet are the settings a bulk edit changes; nil and empty fields are left
// unchanged. Tags in both AddTags and RemoveTags are added.
type BulkSet struct {
	Monitored           *bool
	QualityProfileID    *int64
	MinimumAvailability *string
	AddTags             []string
	RemoveTags          []string
}

// MovieSettings are the user settings of a movie: what a bulk edit returns as
// the previous values, and what RestoreSettings puts back (Undo).
type MovieSettings struct {
	ID                  int64    `json:"id"`
	Monitored           bool     `json:"monitored"`
	QualityProfileID    int64    `json:"qualityProfileId"`
	MinimumAvailability string   `json:"minimumAvailability" enum:"announced,in_cinemas,released"`
	RootFolderID        int64    `json:"rootFolderId" doc:"Informative: a restore does not move movies"`
	Tags                []string `json:"tags"`
}

// checkIDs validates the movie ids of a bulk request (deduplicated, in order).
func checkIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, invalid("ids", "select at least one movie")
	}
	if len(ids) > MaxBulk {
		return nil, invalid("ids", "at most %d movies can be changed at once", MaxBulk)
	}
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, invalid("ids", "movie ids are positive numbers")
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// BulkUpdate changes the settings of several movies in one transaction: all of
// them or none. It returns the previous settings of every movie (for Undo) and
// the ids of the movies that changed.
func (s *Service) BulkUpdate(ctx context.Context, ids []int64, set BulkSet) (previous []MovieSettings, changed []int64, err error) {
	if ids, err = checkIDs(ids); err != nil {
		return nil, nil, err
	}
	if set.MinimumAvailability != nil && !validAvailability(*set.MinimumAvailability) {
		return nil, nil, invalid("minimumAvailability", "minimum availability must be announced, in_cinemas or released")
	}
	add, err := normalizeTags(set.AddTags)
	if err != nil {
		return nil, nil, err
	}
	remove, err := normalizeTags(set.RemoveTags)
	if err != nil {
		return nil, nil, err
	}
	remove = slices.DeleteFunc(remove, func(l string) bool { return slices.Contains(add, l) })
	if set.QualityProfileID != nil {
		if _, err := s.st.Read().GetQualityProfile(ctx, *set.QualityProfileID); err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return nil, nil, invalid("qualityProfileId", "quality profile %d does not exist", *set.QualityProfileID)
			}
			return nil, nil, store.MapError(fmt.Errorf("library: quality profile: %w", err))
		}
	}
	changes := map[int64][]string{}
	var updated []db.Movie
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		addIDs, err := tagIDsFor(ctx, q, add)
		if err != nil {
			return err
		}
		removeIDs := make([]int64, 0, len(remove))
		for _, l := range remove {
			t, err := q.GetTagByLabel(ctx, l)
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				continue // nobody has it
			}
			if err != nil {
				return fmt.Errorf("tag %q: %w", l, err)
			}
			removeIDs = append(removeIDs, t.ID)
		}
		previous = make([]MovieSettings, 0, len(ids))
		for _, id := range ids {
			cur, err := q.GetMovie(ctx, id)
			if err != nil {
				if errors.Is(store.MapError(err), store.ErrNotFound) {
					return notFound("movie %d is not in the library", id)
				}
				return err
			}
			labels, err := movieTagLabels(ctx, q, id)
			if err != nil {
				return err
			}
			previous = append(previous, settingsOf(&cur, labels))
			p := db.UpdateMovieSettingsParams{
				ID: id, Monitored: cur.Monitored, QualityProfileID: cur.QualityProfileID,
				MinimumAvailability: cur.MinimumAvailability, RootFolderID: cur.RootFolderID, Path: cur.Path,
			}
			var c []string
			if set.Monitored != nil && *set.Monitored != cur.Monitored {
				p.Monitored = *set.Monitored
				c = append(c, "monitored")
			}
			if set.QualityProfileID != nil && *set.QualityProfileID != cur.QualityProfileID {
				p.QualityProfileID = *set.QualityProfileID
				c = append(c, "qualityProfileId")
			}
			if set.MinimumAvailability != nil && *set.MinimumAvailability != cur.MinimumAvailability {
				p.MinimumAvailability = *set.MinimumAvailability
				c = append(c, "minimumAvailability")
			}
			if len(c) > 0 {
				m, err := q.UpdateMovieSettings(ctx, p)
				if err != nil {
					return fmt.Errorf("update movie %d: %w", id, err)
				}
				updated = append(updated, m)
			}
			tagsChanged := false
			for i, tid := range addIDs {
				if !slices.Contains(labels, add[i]) {
					if err := q.AddMovieTag(ctx, db.AddMovieTagParams{MovieID: id, TagID: tid}); err != nil {
						return fmt.Errorf("tag movie %d: %w", id, err)
					}
					tagsChanged = true
				}
			}
			for _, tid := range removeIDs {
				n, err := q.RemoveMovieTag(ctx, db.RemoveMovieTagParams{MovieID: id, TagID: tid})
				if err != nil {
					return fmt.Errorf("untag movie %d: %w", id, err)
				}
				tagsChanged = tagsChanged || n > 0
			}
			if tagsChanged {
				c = append(c, "tags")
			}
			if len(c) > 0 {
				changes[id] = c
				changed = append(changed, id)
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("library: bulk update: %w", err)
	}
	s.publishSettingsChanged(changes, updated)
	return previous, changed, nil
}

func settingsOf(m *db.Movie, tags []string) MovieSettings {
	if tags == nil {
		tags = []string{}
	}
	return MovieSettings{
		ID: m.ID, Monitored: m.Monitored, QualityProfileID: m.QualityProfileID,
		MinimumAvailability: m.MinimumAvailability, RootFolderID: m.RootFolderID, Tags: tags,
	}
}

// publishSettingsChanged publishes movie.updated for each changed movie.
func (s *Service) publishSettingsChanged(changes map[int64][]string, updated []db.Movie) {
	byID := make(map[int64]*db.Movie, len(updated))
	for i := range updated {
		byID[updated[i].ID] = &updated[i]
	}
	ids := make([]int64, 0, len(changes))
	for id := range changes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		ev := MovieEvent{ID: id}
		if m := byID[id]; m != nil {
			ev = movieEvent(m)
		}
		ev.Changes = changes[id]
		s.publish(events.Event{Type: events.MovieUpdated, MovieID: id, Data: ev})
	}
}

// RestoreSettings puts movies back to the given settings (the Undo of a bulk
// edit): monitored, quality profile, minimum availability and the exact tag
// list. Root folders are not changed (moving files back is a move). All or
// nothing; it returns the ids of the movies that changed.
func (s *Service) RestoreSettings(ctx context.Context, prev []MovieSettings) ([]int64, error) {
	ids := make([]int64, 0, len(prev))
	for i := range prev {
		ids = append(ids, prev[i].ID)
	}
	if _, err := checkIDs(ids); err != nil {
		return nil, err
	}
	profiles := map[int64]bool{}
	for i := range prev {
		p := &prev[i]
		if !validAvailability(p.MinimumAvailability) {
			return nil, invalid("minimumAvailability", "minimum availability must be announced, in_cinemas or released")
		}
		if profiles[p.QualityProfileID] {
			continue
		}
		if _, err := s.st.Read().GetQualityProfile(ctx, p.QualityProfileID); err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return nil, invalid("qualityProfileId", "quality profile %d does not exist", p.QualityProfileID)
			}
			return nil, store.MapError(fmt.Errorf("library: quality profile: %w", err))
		}
		profiles[p.QualityProfileID] = true
	}
	changes := map[int64][]string{}
	var updated []db.Movie
	var changed []int64
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		for i := range prev {
			p := &prev[i]
			tags, err := normalizeTags(p.Tags)
			if err != nil {
				return err
			}
			cur, err := q.GetMovie(ctx, p.ID)
			if err != nil {
				if errors.Is(store.MapError(err), store.ErrNotFound) {
					return notFound("movie %d is not in the library", p.ID)
				}
				return err
			}
			var c []string
			if cur.Monitored != p.Monitored {
				c = append(c, "monitored")
			}
			if cur.QualityProfileID != p.QualityProfileID {
				c = append(c, "qualityProfileId")
			}
			if cur.MinimumAvailability != p.MinimumAvailability {
				c = append(c, "minimumAvailability")
			}
			if len(c) > 0 {
				m, err := q.UpdateMovieSettings(ctx, db.UpdateMovieSettingsParams{
					ID: p.ID, Monitored: p.Monitored, QualityProfileID: p.QualityProfileID,
					MinimumAvailability: p.MinimumAvailability, RootFolderID: cur.RootFolderID, Path: cur.Path,
				})
				if err != nil {
					return fmt.Errorf("restore movie %d: %w", p.ID, err)
				}
				updated = append(updated, m)
			}
			labels, err := movieTagLabels(ctx, q, p.ID)
			if err != nil {
				return err
			}
			if !sameLabels(labels, tags) {
				if err := setTags(ctx, q, p.ID, tags); err != nil {
					return err
				}
				c = append(c, "tags")
			}
			if len(c) > 0 {
				changes[p.ID] = c
				changed = append(changed, p.ID)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("library: restore settings: %w", err)
	}
	s.publishSettingsChanged(changes, updated)
	return changed, nil
}

// MovieSettingsOf returns the current settings of a movie.
func (s *Service) MovieSettingsOf(ctx context.Context, id int64) (MovieSettings, error) {
	var out MovieSettings
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		m, err := q.GetMovie(ctx, id)
		if err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return notFound("movie %d is not in the library", id)
			}
			return err
		}
		labels, err := movieTagLabels(ctx, q, id)
		if err != nil {
			return err
		}
		out = settingsOf(&m, labels)
		return nil
	})
	if err != nil {
		return MovieSettings{}, fmt.Errorf("library: movie settings %d: %w", id, err)
	}
	return out, nil
}
