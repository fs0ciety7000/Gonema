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

// EventTagsChanged is published when a tag is created, renamed or deleted.
const EventTagsChanged events.Type = "tags.changed"

// TagUsage is a tag and the number of movies carrying it.
type TagUsage struct {
	ID     int64
	Label  string
	Movies int64
}

// ListTags returns every tag with its usage, by label.
func (s *Service) ListTags(ctx context.Context) ([]TagUsage, error) {
	rows, err := s.st.Read().ListTagsWithUsage(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("library: list tags: %w", err))
	}
	out := make([]TagUsage, 0, len(rows))
	for _, r := range rows {
		out = append(out, TagUsage{ID: r.ID, Label: r.Label, Movies: r.Movies})
	}
	return out, nil
}

func tagLabel(label string) (string, error) {
	l, err := normalizeTags([]string{label})
	if err != nil {
		return "", err
	}
	if len(l) == 0 {
		return "", invalid("label", "a tag needs a label")
	}
	return l[0], nil
}

// tagConflict is a label already used by another tag.
func tagConflict(label string) error {
	return &conflictError{InputError{Field: "label", Msg: fmt.Sprintf("a tag named %q already exists", label)}}
}

// conflictError is an InputError that also matches store.ErrConflict (409).
type conflictError struct{ InputError }

func (e *conflictError) Is(target error) bool {
	return target == ErrInvalid || target == store.ErrConflict
}

// CreateTag creates a tag. Labels are unique (exact match).
func (s *Service) CreateTag(ctx context.Context, label string) (TagUsage, error) {
	l, err := tagLabel(label)
	if err != nil {
		return TagUsage{}, err
	}
	t, err := s.st.Write().CreateTag(ctx, l)
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrConflict) {
			return TagUsage{}, tagConflict(l)
		}
		return TagUsage{}, store.MapError(fmt.Errorf("library: create tag: %w", err))
	}
	s.publish(events.Event{Type: EventTagsChanged, Data: map[string]any{"id": t.ID, "label": t.Label, "change": "created"}})
	return TagUsage{ID: t.ID, Label: t.Label}, nil
}

// RenameTag changes the label of a tag; the movies keep it.
func (s *Service) RenameTag(ctx context.Context, id int64, label string) (TagUsage, error) {
	l, err := tagLabel(label)
	if err != nil {
		return TagUsage{}, err
	}
	var out TagUsage
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		t, err := q.RenameTag(ctx, db.RenameTagParams{ID: id, Label: l})
		if err != nil {
			switch err = store.MapError(err); {
			case errors.Is(err, store.ErrNotFound):
				return notFound("tag %d does not exist", id)
			case errors.Is(err, store.ErrConflict):
				return tagConflict(l)
			}
			return err
		}
		n, err := q.CountTagMovies(ctx, id)
		if err != nil {
			return err
		}
		out = TagUsage{ID: t.ID, Label: t.Label, Movies: n}
		return nil
	})
	if err != nil {
		return TagUsage{}, fmt.Errorf("library: rename tag %d: %w", id, err)
	}
	s.publish(events.Event{Type: EventTagsChanged, Data: map[string]any{"id": id, "label": out.Label, "change": "renamed"}})
	return out, nil
}

// DeleteTag deletes a tag and removes it from its movies. It returns the ids of
// the movies that carried it (to undo, re-create the tag and add it back).
func (s *Service) DeleteTag(ctx context.Context, id int64) (TagUsage, []int64, error) {
	var t db.Tag
	var movies []int64
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		var err error
		if t, err = q.GetTag(ctx, id); err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return notFound("tag %d does not exist", id)
			}
			return err
		}
		if movies, err = q.ListMovieIDsByTag(ctx, id); err != nil {
			return err
		}
		return store.RequireRow(q.DeleteTag(ctx, id))
	})
	if err != nil {
		return TagUsage{}, nil, fmt.Errorf("library: delete tag %d: %w", id, err)
	}
	s.publish(events.Event{Type: EventTagsChanged, Data: map[string]any{"id": id, "label": t.Label, "change": "deleted"}})
	for _, m := range movies {
		s.publish(events.Event{Type: events.MovieUpdated, MovieID: m, Data: MovieEvent{ID: m, Changes: []string{"tags"}}})
	}
	return TagUsage{ID: t.ID, Label: t.Label, Movies: int64(len(movies))}, movies, nil
}

// tagIDsFor returns the ids of labels, creating the missing tags (in q's
// transaction).
func tagIDsFor(ctx context.Context, q *db.Queries, labels []string) ([]int64, error) {
	ids := make([]int64, 0, len(labels))
	for _, l := range labels {
		t, err := q.GetTagByLabel(ctx, l)
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			t, err = q.CreateTag(ctx, l)
		}
		if err != nil {
			return nil, fmt.Errorf("tag %q: %w", l, err)
		}
		ids = append(ids, t.ID)
	}
	return ids, nil
}

func movieTagLabels(ctx context.Context, q *db.Queries, movieID int64) ([]string, error) {
	tags, err := q.ListMovieTags(ctx, movieID)
	if err != nil {
		return nil, fmt.Errorf("tags of movie %d: %w", movieID, err)
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, t.Label)
	}
	return out, nil
}

// sameLabels reports whether two label lists hold the same labels, in any order.
func sameLabels(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
