package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/naming"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// NamingSettings are the folder and file name templates.
type NamingSettings struct {
	FolderTemplate string `json:"folderTemplate"`
	FileTemplate   string `json:"fileTemplate"`
	// RenameFiles names imported files with FileTemplate; false keeps the
	// original file name (sanitized).
	RenameFiles bool `json:"renameFiles"`
}

// DefaultNaming returns the default templates.
func DefaultNaming() NamingSettings {
	return NamingSettings{FolderTemplate: library.DefaultFolderTemplate, FileTemplate: naming.DefaultFileTemplate, RenameFiles: true}
}

// Naming returns the naming settings (defaults for unset or invalid ones).
func (s *Service) Naming(ctx context.Context) (NamingSettings, error) {
	n := DefaultNaming()
	if v, err := store.GetSetting[string](ctx, s.st, library.SettingFolderTemplate); err == nil && library.ValidateFolderTemplate(v) == nil {
		n.FolderTemplate = v
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return n, err
	}
	if v, err := store.GetSetting[string](ctx, s.st, naming.SettingFileTemplate); err == nil && naming.Validate(v) == nil {
		n.FileTemplate = v
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return n, err
	}
	if v, err := store.GetSetting[bool](ctx, s.st, naming.SettingRenameFiles); err == nil {
		n.RenameFiles = v
	} else if !errors.Is(err, store.ErrNotFound) {
		return n, err
	}
	return n, nil
}

// validate checks both templates; the error lists every problem.
func (n *NamingSettings) validate() error {
	var msgs []string
	if err := library.ValidateFolderTemplate(n.FolderTemplate); err != nil {
		msgs = append(msgs, err.Error())
	}
	if err := naming.Validate(n.FileTemplate); err != nil {
		msgs = append(msgs, err.Error())
	}
	if len(msgs) == 0 {
		return nil
	}
	msg := msgs[0]
	if len(msgs) > 1 {
		msg += "; " + msgs[1]
	}
	return userErr(ErrInvalid, nil, "%s", msg)
}

// SetNaming validates and stores the naming settings. They apply to the movies
// added and files imported from now on; existing files are not renamed.
func (s *Service) SetNaming(ctx context.Context, n NamingSettings) (NamingSettings, error) {
	if err := n.validate(); err != nil {
		return NamingSettings{}, err
	}
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		for key, v := range map[string]any{
			library.SettingFolderTemplate: n.FolderTemplate,
			naming.SettingFileTemplate:    n.FileTemplate,
			naming.SettingRenameFiles:     n.RenameFiles,
		} {
			raw, err := json.Marshal(v)
			if err != nil {
				return fmt.Errorf("encode %s: %w", key, err)
			}
			if err := q.SetSetting(ctx, db.SetSettingParams{Key: key, Value: string(raw)}); err != nil {
				return fmt.Errorf("save %s: %w", key, err)
			}
		}
		return nil
	})
	if err != nil {
		return NamingSettings{}, fmt.Errorf("importer: save naming settings: %w", err)
	}
	s.publish(events.Event{Type: events.SettingsChanged, Data: map[string]any{"key": "naming"}})
	return n, nil
}

// NamingPreview is the rendering of templates for one movie.
type NamingPreview struct {
	Folder string `json:"folder"`
	File   string `json:"file"`
	// Example is true when no movie was given: the preview uses a well-known
	// movie and release, not library data.
	Example bool  `json:"example"`
	MovieID int64 `json:"movieId,omitempty"`
}

// PreviewNaming renders n for a library movie (its current file when it has
// one), or for a built-in example when movieID is 0. Invalid templates return
// ErrInvalid with every problem.
func (s *Service) PreviewNaming(ctx context.Context, n NamingSettings, movieID int64) (NamingPreview, error) {
	if err := n.validate(); err != nil {
		return NamingPreview{}, err
	}
	tmpl, err := naming.Parse(n.FileTemplate)
	if err != nil {
		return NamingPreview{}, userErr(ErrInvalid, err, "%s", err.Error())
	}
	if movieID == 0 {
		v := naming.Example()
		return NamingPreview{
			Folder: library.FolderName(n.FolderTemplate, library.FolderVars{Title: v.Title, OriginalTitle: v.OriginalTitle, Year: v.Year, TmdbID: v.TmdbID, ImdbID: v.ImdbID}),
			File:   tmpl.FileName(&v, "mkv"), Example: true,
		}, nil
	}
	t, err := s.loadTarget(ctx, movieID)
	if err != nil {
		return NamingPreview{}, err
	}
	var parsed parser.ParsedRelease
	q, ext := quality.Unknown, "mkv"
	var score quality.Breakdown
	if t.current != nil {
		_ = json.Unmarshal([]byte(t.current.Parsed), &parsed) // a broken record previews without release details
		q = qualityID(t.current.Quality)
		ext = filepath.Ext(t.current.RelativePath)
		rel := quality.Release{Title: t.current.SceneName, Parsed: &parsed, Size: t.current.Size}
		score = quality.Score(&t.profile, t.rules, &rel)
	}
	v := varsOf(t, &parsed, q, score)
	file := tmpl.FileName(&v, ext)
	if !n.RenameFiles && t.current != nil {
		file = filepath.Base(t.current.RelativePath)
	}
	return NamingPreview{
		Folder:  library.FolderName(n.FolderTemplate, library.FolderVars{Title: v.Title, OriginalTitle: v.OriginalTitle, Year: v.Year, TmdbID: v.TmdbID, ImdbID: v.ImdbID}),
		File:    file,
		MovieID: movieID,
	}, nil
}
