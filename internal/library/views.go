package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Saved views are named filter, sort and column sets of a UI page. They are
// shared by everyone (user_id NULL) until users exist (phase 8).

// View densities.
const (
	DensityAiry    = "airy"
	DensityCompact = "compact"
)

// Limits of a saved view, so that a client cannot store megabytes of settings.
const (
	maxViewName    = 64
	maxViewPage    = 32
	maxViewJSON    = 8 << 10
	maxViewColumns = 40
)

// ViewInput is a saved view to create or replace.
type ViewInput struct {
	Page     string
	Name     string
	Filters  json.RawMessage // JSON object, opaque to the server
	Sort     json.RawMessage // JSON object
	Columns  []string
	Density  string // DensityAiry (default) or DensityCompact
	Position int64
}

func (v *ViewInput) params() (db.CreateSavedViewParams, error) {
	page, name := strings.TrimSpace(v.Page), strings.TrimSpace(v.Name)
	if page == "" || len(page) > maxViewPage {
		return db.CreateSavedViewParams{}, invalid("page", "the page must be 1 to %d characters", maxViewPage)
	}
	for _, r := range page {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return db.CreateSavedViewParams{}, invalid("page", "the page may only contain a-z, 0-9, - and _")
		}
	}
	if name == "" || len(name) > maxViewName {
		return db.CreateSavedViewParams{}, invalid("name", "the name must be 1 to %d characters", maxViewName)
	}
	filters, err := jsonObject(v.Filters, "filters")
	if err != nil {
		return db.CreateSavedViewParams{}, err
	}
	sort, err := jsonObject(v.Sort, "sort")
	if err != nil {
		return db.CreateSavedViewParams{}, err
	}
	if len(v.Columns) > maxViewColumns {
		return db.CreateSavedViewParams{}, invalid("columns", "at most %d columns", maxViewColumns)
	}
	cols := v.Columns
	if cols == nil {
		cols = []string{}
	}
	for _, c := range cols {
		if c == "" || len(c) > 64 {
			return db.CreateSavedViewParams{}, invalid("columns", "column names must be 1 to 64 characters")
		}
	}
	colJSON, err := json.Marshal(cols)
	if err != nil {
		return db.CreateSavedViewParams{}, fmt.Errorf("library: encode columns: %w", err)
	}
	density := v.Density
	if density == "" {
		density = DensityAiry
	}
	if density != DensityAiry && density != DensityCompact {
		return db.CreateSavedViewParams{}, invalid("density", "density must be airy or compact")
	}
	return db.CreateSavedViewParams{
		Page: page, Name: name, Filters: filters, Sort: sort, Columns: string(colJSON),
		Density: density, Position: v.Position,
	}, nil
}

// jsonObject validates a JSON object of at most maxViewJSON bytes; empty is {}.
func jsonObject(raw json.RawMessage, field string) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "{}", nil
	}
	if len(raw) > maxViewJSON {
		return "", invalid(field, "%s is larger than %d bytes", field, maxViewJSON)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return "", invalid(field, "%s must be a JSON object", field)
	}
	return string(raw), nil
}

func viewConflict(err error, name string) error {
	if errors.Is(store.MapError(err), store.ErrConflict) {
		return fmt.Errorf("%w: %w", &InputError{Field: "name", Msg: fmt.Sprintf("a view named %q already exists on this page", name)}, store.ErrConflict)
	}
	return err
}

// ListViews returns the saved views of a page ("" = every page), in position order.
func (s *Service) ListViews(ctx context.Context, page string) ([]db.SavedView, error) {
	var p *string
	if page != "" {
		p = &page
	}
	rows, err := s.st.Read().ListSavedViews(ctx, p)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("library: list views: %w", err))
	}
	return rows, nil
}

// GetView returns one saved view.
func (s *Service) GetView(ctx context.Context, id int64) (db.SavedView, error) {
	v, err := s.st.Read().GetSavedView(ctx, id)
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return db.SavedView{}, notFound("saved view %d does not exist", id)
		}
		return db.SavedView{}, fmt.Errorf("library: get view %d: %w", id, err)
	}
	return v, nil
}

// CreateView stores a new saved view.
func (s *Service) CreateView(ctx context.Context, in ViewInput) (db.SavedView, error) {
	p, err := in.params()
	if err != nil {
		return db.SavedView{}, err
	}
	v, err := s.st.Write().CreateSavedView(ctx, p)
	if err != nil {
		return db.SavedView{}, fmt.Errorf("library: create view: %w", viewConflict(err, p.Name))
	}
	return v, nil
}

// UpdateView replaces a saved view.
func (s *Service) UpdateView(ctx context.Context, id int64, in ViewInput) (db.SavedView, error) {
	p, err := in.params()
	if err != nil {
		return db.SavedView{}, err
	}
	v, err := s.st.Write().UpdateSavedView(ctx, db.UpdateSavedViewParams{
		ID: id, Page: p.Page, Name: p.Name, Filters: p.Filters, Sort: p.Sort, Columns: p.Columns,
		Density: p.Density, Position: p.Position,
	})
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return db.SavedView{}, notFound("saved view %d does not exist", id)
		}
		return db.SavedView{}, fmt.Errorf("library: update view %d: %w", id, viewConflict(err, p.Name))
	}
	return v, nil
}

// DeleteView deletes a saved view (settings only; no file is involved).
func (s *Service) DeleteView(ctx context.Context, id int64) error {
	if err := store.RequireRow(s.st.Write().DeleteSavedView(ctx, id)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return notFound("saved view %d does not exist", id)
		}
		return fmt.Errorf("library: delete view %d: %w", id, err)
	}
	return nil
}

// SettingDemo marks a demo database (fictional movies created by `gonema demo
// seed`): the UI shows a permanent banner.
const SettingDemo = "demo"

// IsDemo reports whether the database holds demo data.
func (s *Service) IsDemo(ctx context.Context) (bool, error) {
	v, err := store.GetSetting[bool](ctx, s.st, SettingDemo)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("library: demo setting: %w", err)
	}
	return v, nil
}
