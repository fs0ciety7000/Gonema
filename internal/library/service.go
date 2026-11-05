// Package library is the movie library service: adding movies from the
// metadata provider, refreshing them, settings, root folders, quality profiles,
// collections, and the in-memory release matcher.
//
// The service never deletes nor moves user files. Deleting a movie removes its
// database rows only; the folder and its files stay on disk.
package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Errors, matched with errors.Is. Not-found conditions wrap store.ErrNotFound
// and conflicts store.ErrConflict.
var (
	// ErrNoProvider: no TMDB key is configured yet.
	ErrNoProvider = errors.New("library: no TMDB key configured; add one in Settings > Metadata")
	// ErrInvalid: an input was rejected; errors.As with *InputError gives the field.
	ErrInvalid = errors.New("library: invalid input")
)

// InputError is a rejected input. Msg is a plain-language sentence shown to the
// user as is.
type InputError struct {
	Field string
	Msg   string
}

func (e *InputError) Error() string { return e.Msg }

// Is makes InputError match ErrInvalid.
func (e *InputError) Is(target error) bool { return target == ErrInvalid }

func invalid(field, format string, args ...any) error {
	return &InputError{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// notFound wraps store.ErrNotFound with a plain-language message.
type notFoundError struct{ msg string }

func (e *notFoundError) Error() string        { return e.msg }
func (e *notFoundError) Is(target error) bool { return target == store.ErrNotFound }

func notFound(format string, args ...any) error {
	return &notFoundError{msg: fmt.Sprintf(format, args...)}
}

// KeyedProvider is a metadata provider built from a user credential.
type KeyedProvider interface {
	metadata.Provider
	// ValidateKey checks the credential against the provider.
	ValidateKey(ctx context.Context) error
}

// ProviderFactory builds a provider from a credential (tmdb.New in production).
type ProviderFactory func(key string) (KeyedProvider, error)

// JobQueue is the part of the job runner the service uses.
type JobQueue interface {
	Enqueue(ctx context.Context, kind string, payload any, opts jobs.EnqueueOptions) (db.Job, bool, error)
	Has(kind string) bool
}

// Images is the part of the image cache the service uses.
type Images interface {
	Fetch(ctx context.Context, kind imagecache.Kind, size, tmdbPath string) (string, error)
	PosterColor(ctx context.Context, tmdbPath string) (string, error)
}

// Options configures a Service. Store, Box and NewProvider are required.
type Options struct {
	Store       *store.Store
	Box         *secretbox.Box
	NewProvider ProviderFactory
	Jobs        JobQueue     // optional: without it, no image/search job is enqueued
	Images      Images       // optional: set later with SetImages (it depends on the provider)
	Bus         *events.Bus  // optional
	FS          FS           // default OSFS
	Logger      *slog.Logger // default discards
	Now         func() time.Time
}

// Service is the library service. It is safe for concurrent use.
type Service struct {
	st      *store.Store
	box     *secretbox.Box
	factory ProviderFactory
	jobs    JobQueue
	bus     *events.Bus
	fs      FS
	log     *slog.Logger
	now     func() time.Time

	provMu   sync.RWMutex
	provider KeyedProvider
	images   Images

	matcher matcher

	healthMu sync.Mutex
	health   map[int64]string // root folder id -> last error ("" = ok)
}

// New returns a service. Call Init before use.
func New(opts Options) (*Service, error) {
	if opts.Store == nil || opts.Box == nil || opts.NewProvider == nil {
		return nil, errors.New("library: store, secret box and provider factory are required")
	}
	s := &Service{
		st: opts.Store, box: opts.Box, factory: opts.NewProvider, jobs: opts.Jobs,
		bus: opts.Bus, fs: opts.FS, log: opts.Logger, now: opts.Now, images: opts.Images,
		health: make(map[int64]string),
	}
	if s.fs == nil {
		s.fs = OSFS{}
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// Init prepares the service at startup: seeds the starter quality profiles on
// an empty database, loads the stored TMDB key (without network access) and
// builds the release matcher.
func (s *Service) Init(ctx context.Context) error {
	n, err := s.SeedQualityProfiles(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		s.log.Info("seeded starter quality profiles", slog.Int("count", n))
	}
	if err := s.loadTMDBKey(ctx); err != nil {
		// A key that cannot be decrypted (master key changed) must not prevent the
		// server from starting: the user can enter it again.
		s.log.Error("stored TMDB key unusable; enter it again in Settings > Metadata", slog.Any("error", err))
	}
	return s.RebuildMatcher(ctx)
}

// SetImages sets the image cache (built after the service, as it uses the
// service's provider for the image base URL).
func (s *Service) SetImages(images Images) {
	s.provMu.Lock()
	defer s.provMu.Unlock()
	s.images = images
}

func (s *Service) imageCache() Images {
	s.provMu.RLock()
	defer s.provMu.RUnlock()
	return s.images
}

// Provider returns the current metadata provider, or ErrNoProvider.
func (s *Service) Provider() (KeyedProvider, error) {
	s.provMu.RLock()
	defer s.provMu.RUnlock()
	if s.provider == nil {
		return nil, ErrNoProvider
	}
	return s.provider, nil
}

// ImageBaseURL implements imagecache.BaseURLSource with the current provider.
func (s *Service) ImageBaseURL(ctx context.Context) (string, error) {
	p, err := s.Provider()
	if err != nil {
		return "", err
	}
	src, ok := p.(imagecache.BaseURLSource)
	if !ok {
		return "", errors.New("library: the metadata provider serves no images")
	}
	return src.ImageBaseURL(ctx)
}

func (s *Service) publish(e events.Event) {
	if s.bus != nil {
		s.bus.Publish(e)
	}
}

// enqueue adds a job if a queue is configured and a handler exists for kind.
func (s *Service) enqueue(ctx context.Context, kind string, payload any, opts jobs.EnqueueOptions) {
	if s.jobs == nil || !s.jobs.Has(kind) {
		s.log.Debug("job kind not available, not enqueued", slog.String("kind", kind))
		return
	}
	if _, _, err := s.jobs.Enqueue(ctx, kind, payload, opts); err != nil {
		s.log.Error("enqueue job", slog.String("kind", kind), slog.Any("error", err))
	}
}

// SchemaVersion returns the database schema version (for the status endpoint).
func (s *Service) SchemaVersion(ctx context.Context) (int64, error) {
	return s.st.SchemaVersion(ctx)
}
