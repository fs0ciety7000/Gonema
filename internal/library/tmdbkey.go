package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// SettingTMDBKey is the settings key of the encrypted TMDB credential. The value
// is the secretbox ciphertext (JSON base64 string), bound to this key name.
const SettingTMDBKey = "metadata.tmdb_key"

var tmdbKeyAD = []byte("settings/" + SettingTMDBKey)

// TMDBConfigured reports whether a TMDB key is in use.
func (s *Service) TMDBConfigured() bool {
	_, err := s.Provider()
	return err == nil
}

// SetTMDBKey checks the credential against TMDB, then stores it encrypted and
// switches the provider to it. Nothing is saved when TMDB rejects the key or
// cannot be reached. The key is never logged nor returned.
func (s *Service) SetTMDBKey(ctx context.Context, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return invalid("key", "the TMDB key is empty")
	}
	p, err := s.factory(key)
	if err != nil {
		return &InputError{Field: "key", Msg: "this does not look like a TMDB API key or read access token; copy it from https://www.themoviedb.org/settings/api"}
	}
	if err := p.ValidateKey(ctx); err != nil {
		return fmt.Errorf("library: validate TMDB key: %w", err)
	}
	sealed, err := s.box.Seal([]byte(key), tmdbKeyAD)
	if err != nil {
		return fmt.Errorf("library: encrypt TMDB key: %w", err)
	}
	if err := store.SetSetting(ctx, s.st, SettingTMDBKey, sealed); err != nil {
		return fmt.Errorf("library: save TMDB key: %w", err)
	}
	s.provMu.Lock()
	s.provider = p
	s.provMu.Unlock()
	s.log.Info("TMDB key updated")
	s.publish(events.Event{Type: events.SettingsChanged, Data: map[string]any{"setting": "tmdbKey", "configured": true}})
	return nil
}

// loadTMDBKey builds the provider from the stored key, if any.
func (s *Service) loadTMDBKey(ctx context.Context) error {
	sealed, err := store.GetSetting[[]byte](ctx, s.st, SettingTMDBKey)
	if errors.Is(err, store.ErrNotFound) {
		s.log.Info("no TMDB key configured yet: adding movies needs one (Settings > Metadata)")
		return nil
	}
	if err != nil {
		return fmt.Errorf("library: read TMDB key: %w", err)
	}
	key, err := s.box.Open(sealed, tmdbKeyAD)
	if err != nil {
		return fmt.Errorf("library: decrypt TMDB key: %w", err)
	}
	p, err := s.factory(string(key))
	if err != nil {
		return fmt.Errorf("library: stored TMDB key: %w", err)
	}
	s.provMu.Lock()
	s.provider = p
	s.provMu.Unlock()
	s.log.Debug("TMDB key loaded", slog.Bool("configured", true))
	return nil
}
