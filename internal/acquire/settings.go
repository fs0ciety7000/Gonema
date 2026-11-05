package acquire

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/download/deluge"
	"github.com/fs0ciety7000/gonema/internal/download/nzbget"
	"github.com/fs0ciety7000/gonema/internal/download/qbittorrent"
	"github.com/fs0ciety7000/gonema/internal/download/sabnzbd"
	"github.com/fs0ciety7000/gonema/internal/download/transmission"
	"github.com/fs0ciety7000/gonema/internal/indexer"
	"github.com/fs0ciety7000/gonema/internal/indexer/newznab"
	"github.com/fs0ciety7000/gonema/internal/indexer/torznab"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// testTimeout bounds a connection test.
const testTimeout = 30 * time.Second

// IndexerConfig is what an IndexerFactory needs, secrets decrypted.
type IndexerConfig struct {
	Info       indexer.Info
	Kind       string // newznab or torznab
	BaseURL    string
	APIPath    string
	APIKey     string
	Categories []int
}

// IndexerFactory builds an indexer client.
type IndexerFactory func(cfg IndexerConfig) (indexer.Indexer, error)

// ClientFactory builds a download client.
type ClientFactory func(kind download.Kind, cfg *download.Config) (download.Client, error)

func defaultIndexerFactory(version string, log *slog.Logger) IndexerFactory {
	return func(c IndexerConfig) (indexer.Indexer, error) {
		o := newznab.Options{
			Info: c.Info, BaseURL: c.BaseURL, APIPath: c.APIPath, APIKey: c.APIKey,
			Categories: c.Categories, Version: version, Logger: log,
		}
		var (
			cl  *newznab.Client
			err error
		)
		if c.Kind == "torznab" {
			cl, err = torznab.New(o)
		} else {
			cl, err = newznab.New(o)
		}
		if err != nil {
			return nil, err
		}
		return cl, nil
	}
}

func defaultClientFactory(kind download.Kind, cfg *download.Config) (download.Client, error) {
	var (
		c   download.Client
		err error
	)
	switch kind {
	case download.KindSABnzbd:
		var x *sabnzbd.Client
		if x, err = sabnzbd.New(cfg); err == nil {
			c = x
		}
	case download.KindNZBGet:
		var x *nzbget.Client
		if x, err = nzbget.New(cfg); err == nil {
			c = x
		}
	case download.KindQBittorrent:
		var x *qbittorrent.Client
		if x, err = qbittorrent.New(cfg); err == nil {
			c = x
		}
	case download.KindDeluge:
		var x *deluge.Client
		if x, err = deluge.New(cfg); err == nil {
			c = x
		}
	case download.KindTransmission:
		var x *transmission.Client
		if x, err = transmission.New(cfg); err == nil {
			c = x
		}
	default:
		return nil, fmt.Errorf("unknown download client kind %q", kind)
	}
	return c, err
}

// defaultPort returns the default port of a client kind.
func defaultPort(kind download.Kind) int64 {
	switch kind {
	case download.KindSABnzbd:
		return sabnzbd.DefaultPort
	case download.KindNZBGet:
		return nzbget.DefaultPort
	case download.KindQBittorrent:
		return qbittorrent.DefaultPort
	case download.KindDeluge:
		return deluge.DefaultPort
	case download.KindTransmission:
		return transmission.DefaultPort
	}
	return 0
}

// TestResult is the outcome of a connection test.
type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message" doc:"Plain-language result, safe to display"`
}

func testResult(err error) TestResult {
	if err == nil {
		return TestResult{OK: true, Message: "Connection successful."}
	}
	return TestResult{Message: err.Error()}
}

func indexerAD(id int64) []byte { return []byte("indexers/" + strconv.FormatInt(id, 10)) }
func clientAD(id int64) []byte  { return []byte("download-clients/" + strconv.FormatInt(id, 10)) }

// indexerSecrets is the encrypted part of an indexer.
type indexerSecrets struct {
	APIKey string `json:"apiKey,omitempty"`
}

// clientSecrets is the encrypted part of a download client.
type clientSecrets struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	APIKey   string `json:"apiKey,omitempty"`
}

func (s *Service) seal(v any, ad []byte) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if string(raw) == "{}" {
		return nil, nil
	}
	return s.box.Seal(raw, ad)
}

func (s *Service) open(sealed, ad []byte, dst any) error {
	if len(sealed) == 0 {
		return nil
	}
	raw, err := s.box.Open(sealed, ad)
	if err != nil {
		return fmt.Errorf("decrypt secrets (master key changed?): %w", err)
	}
	return json.Unmarshal(raw, dst)
}

// ----------------------------------------------------------------- indexers

// IndexerHealth is the health of an indexer, from its circuit breaker.
type IndexerHealth struct {
	State         string     `json:"state" enum:"ok,failing,disabled" doc:"disabled: paused after repeated failures until disabledUntil"`
	FailureCount  int64      `json:"failureCount"`
	DisabledUntil *time.Time `json:"disabledUntil"`
	LastError     string     `json:"lastError,omitempty"`
	LastOKAt      *time.Time `json:"lastOkAt"`
}

// Indexer is an indexer as shown to the user. The API key is never included.
type Indexer struct {
	ID                int64         `json:"id"`
	Name              string        `json:"name"`
	Kind              string        `json:"kind" enum:"newznab,torznab"`
	Protocol          string        `json:"protocol" enum:"usenet,torrent"`
	BaseURL           string        `json:"baseUrl"`
	APIPath           string        `json:"apiPath"`
	Categories        []int         `json:"categories"`
	APIKeySet         bool          `json:"apiKeySet" doc:"Whether an API key is stored; the key itself is never returned"`
	Priority          int64         `json:"priority" doc:"1 (preferred) to 50"`
	EnableRSS         bool          `json:"enableRss"`
	EnableAutoSearch  bool          `json:"enableAutoSearch"`
	EnableInteractive bool          `json:"enableInteractive"`
	MinSeeders        *int64        `json:"minSeeders"`
	SeedRatio         *float64      `json:"seedRatio"`
	SeedTimeMin       *int64        `json:"seedTimeMin"`
	Tags              []string      `json:"tags"`
	Health            IndexerHealth `json:"health"`
}

// IndexerInput creates or replaces an indexer.
type IndexerInput struct {
	Name       string `json:"name" minLength:"1" maxLength:"100"`
	Kind       string `json:"kind" enum:"newznab,torznab"`
	BaseURL    string `json:"baseUrl" minLength:"1" maxLength:"2048" doc:"Indexer root URL, e.g. https://indexer.example"`
	APIPath    string `json:"apiPath,omitempty" maxLength:"256" doc:"Default /api"`
	Categories []int  `json:"categories,omitempty" maxItems:"100" doc:"Empty: the movie categories announced by the indexer"`
	// APIKey is write-only: nil keeps the stored key, "" removes it.
	APIKey            *string  `json:"apiKey,omitempty" maxLength:"512" doc:"Write-only. Omit to keep the stored key; an empty string removes it"`
	Priority          int64    `json:"priority,omitempty" minimum:"0" maximum:"50" doc:"1 (preferred) to 50; 0 selects 25"`
	EnableRSS         *bool    `json:"enableRss,omitempty" doc:"Default true"`
	EnableAutoSearch  *bool    `json:"enableAutoSearch,omitempty" doc:"Default true"`
	EnableInteractive *bool    `json:"enableInteractive,omitempty" doc:"Default true"`
	MinSeeders        *int64   `json:"minSeeders,omitempty" minimum:"0" maximum:"100000"`
	SeedRatio         *float64 `json:"seedRatio,omitempty" minimum:"0" maximum:"1000"`
	SeedTimeMin       *int64   `json:"seedTimeMin,omitempty" minimum:"0" maximum:"5256000"`
	Tags              []string `json:"tags,omitempty" maxItems:"20"`
}

// orTrue returns *p, true when p is nil (settings that default to on).
func orTrue(p *bool) bool { return p == nil || *p }

// indexerParams validates in and returns the row values.
func (s *Service) indexerParams(in *IndexerInput) (db.CreateIndexerParams, error) {
	var p db.CreateIndexerParams
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return p, invalid("The indexer name must be 1 to 100 characters long.")
	}
	var proto decision.Protocol
	switch in.Kind {
	case "newznab":
		proto = decision.Usenet
	case "torznab":
		proto = decision.Torrent
	default:
		return p, invalid("The indexer kind must be newznab (Usenet) or torznab (BitTorrent).")
	}
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	u, err := url.Parse(in.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return p, invalid("The indexer URL must start with http:// or https://, name a host, and carry no credentials nor query string (the API key has its own field).")
	}
	in.APIPath = strings.TrimSpace(in.APIPath)
	if in.APIPath == "" {
		in.APIPath = newznab.DefaultAPIPath
	}
	if !strings.HasPrefix(in.APIPath, "/") {
		in.APIPath = "/" + in.APIPath
	}
	if strings.ContainsAny(in.APIPath, "?#") {
		return p, invalid("The API path must not contain ? or #.")
	}
	for _, c := range in.Categories {
		if c < 1 || c > 999999 {
			return p, invalid("Category %d is not a valid Newznab category number.", c)
		}
	}
	if in.Priority == 0 {
		in.Priority = indexer.DefaultPriority
	}
	if in.Priority < 1 || in.Priority > 50 {
		return p, invalid("The priority must be between 1 (preferred) and 50.")
	}
	if (in.MinSeeders != nil && *in.MinSeeders < 0) || (in.SeedRatio != nil && *in.SeedRatio < 0) || (in.SeedTimeMin != nil && *in.SeedTimeMin < 0) {
		return p, invalid("Seeding settings must not be negative.")
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return p, err
	}
	return db.CreateIndexerParams{
		Name: in.Name, Kind: in.Kind, Protocol: string(proto), BaseUrl: in.BaseURL, ApiPath: in.APIPath,
		Categories: jsonArray(in.Categories), Priority: in.Priority,
		EnableRss: orTrue(in.EnableRSS), EnableAutoSearch: orTrue(in.EnableAutoSearch),
		EnableInteractive: orTrue(in.EnableInteractive),
		MinSeeders:        in.MinSeeders, SeedRatio: in.SeedRatio, SeedTimeMin: in.SeedTimeMin, Tags: jsonArray(tags),
	}, nil
}

func (s *Service) indexerView(r *db.Indexer) Indexer {
	v := Indexer{
		ID: r.ID, Name: r.Name, Kind: r.Kind, Protocol: r.Protocol, BaseURL: r.BaseUrl, APIPath: r.ApiPath,
		Categories: decodeArray[int](r.Categories), APIKeySet: len(r.Secrets) > 0, Priority: r.Priority,
		EnableRSS: r.EnableRss, EnableAutoSearch: r.EnableAutoSearch, EnableInteractive: r.EnableInteractive,
		MinSeeders: r.MinSeeders, SeedRatio: r.SeedRatio, SeedTimeMin: r.SeedTimeMin, Tags: decodeArray[string](r.Tags),
		Health: IndexerHealth{
			State: "ok", FailureCount: r.FailureCount, DisabledUntil: timePtr(r.DisabledUntil),
			LastError: deref(r.LastError), LastOKAt: timePtr(r.LastOkAt),
		},
	}
	switch {
	case r.DisabledUntil != nil && *r.DisabledUntil > store.Millis(s.now()):
		v.Health.State = "disabled"
	case r.FailureCount > 0:
		v.Health.State = "failing"
	}
	return v
}

// ListIndexers returns every indexer, preferred first.
func (s *Service) ListIndexers(ctx context.Context) ([]Indexer, error) {
	rows, err := s.st.Read().ListIndexers(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("acquire: list indexers: %w", err))
	}
	out := make([]Indexer, 0, len(rows))
	for i := range rows {
		out = append(out, s.indexerView(&rows[i]))
	}
	return out, nil
}

// GetIndexer returns one indexer.
func (s *Service) GetIndexer(ctx context.Context, id int64) (Indexer, error) {
	r, err := s.st.Read().GetIndexer(ctx, id)
	if err != nil {
		return Indexer{}, mapNotFound(err, "Indexer %d does not exist.", id)
	}
	return s.indexerView(&r), nil
}

func nameConflict(err error, what, name string) error {
	if errors.Is(store.MapError(err), store.ErrConflict) {
		return conflict("A %s named %q already exists.", what, name)
	}
	return store.MapError(err)
}

// CreateIndexer validates and stores a new indexer; its API key is encrypted.
func (s *Service) CreateIndexer(ctx context.Context, in IndexerInput) (Indexer, error) {
	p, err := s.indexerParams(&in)
	if err != nil {
		return Indexer{}, err
	}
	var row db.Indexer
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		if row, err = q.CreateIndexer(ctx, p); err != nil {
			return nameConflict(err, "indexer", p.Name)
		}
		if in.APIKey == nil || strings.TrimSpace(*in.APIKey) == "" {
			return nil
		}
		sealed, err := s.seal(indexerSecrets{APIKey: strings.TrimSpace(*in.APIKey)}, indexerAD(row.ID))
		if err != nil {
			return fmt.Errorf("encrypt API key: %w", err)
		}
		row.Secrets = sealed
		return store.RequireRow(q.SetIndexerSecrets(ctx, db.SetIndexerSecretsParams{ID: row.ID, Secrets: sealed}))
	})
	if err != nil {
		return Indexer{}, fmt.Errorf("acquire: create indexer: %w", err)
	}
	s.log.Info("indexer added", slog.Int64("indexer_id", row.ID), slog.String("name", row.Name), slog.String("kind", row.Kind))
	return s.indexerView(&row), nil
}

// UpdateIndexer replaces the settings of an indexer. A nil APIKey keeps the
// stored key. The health is reset: the new settings get a fresh start.
func (s *Service) UpdateIndexer(ctx context.Context, id int64, in IndexerInput) (Indexer, error) {
	p, err := s.indexerParams(&in)
	if err != nil {
		return Indexer{}, err
	}
	var row db.Indexer
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		row, err = q.UpdateIndexer(ctx, db.UpdateIndexerParams{
			ID: id, Name: p.Name, Kind: p.Kind, Protocol: p.Protocol, BaseUrl: p.BaseUrl, ApiPath: p.ApiPath,
			Categories: p.Categories, Priority: p.Priority, EnableRss: p.EnableRss, EnableAutoSearch: p.EnableAutoSearch,
			EnableInteractive: p.EnableInteractive, MinSeeders: p.MinSeeders, SeedRatio: p.SeedRatio,
			SeedTimeMin: p.SeedTimeMin, Tags: p.Tags,
		})
		if err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return notFound("Indexer %d does not exist.", id)
			}
			return nameConflict(err, "indexer", p.Name)
		}
		if in.APIKey != nil {
			sealed, err := s.seal(indexerSecrets{APIKey: strings.TrimSpace(*in.APIKey)}, indexerAD(id))
			if err != nil {
				return fmt.Errorf("encrypt API key: %w", err)
			}
			row.Secrets = sealed
			if err := store.RequireRow(q.SetIndexerSecrets(ctx, db.SetIndexerSecretsParams{ID: id, Secrets: sealed})); err != nil {
				return err
			}
		}
		row.FailureCount, row.DisabledUntil, row.LastError = 0, nil, nil
		return store.RequireRow(q.UpdateIndexerHealth(ctx, db.UpdateIndexerHealthParams{ID: id, LastOkAt: row.LastOkAt}))
	})
	if err != nil {
		return Indexer{}, fmt.Errorf("acquire: update indexer %d: %w", id, err)
	}
	s.forgetIndexer(id)
	s.log.Info("indexer updated", slog.Int64("indexer_id", id), slog.String("name", row.Name))
	return s.indexerView(&row), nil
}

// DeleteIndexer removes an indexer. Downloads and blocklist entries keep their
// record without the indexer.
func (s *Service) DeleteIndexer(ctx context.Context, id int64) error {
	if err := store.RequireRow(s.st.Write().DeleteIndexer(ctx, id)); err != nil {
		return mapNotFound(err, "Indexer %d does not exist.", id)
	}
	s.forgetIndexer(id)
	s.log.Info("indexer removed", slog.Int64("indexer_id", id))
	return nil
}

func (s *Service) forgetIndexer(id int64) {
	s.cacheMu.Lock()
	delete(s.ixCache, id)
	s.cacheMu.Unlock()
	s.orch.Reset(id)
	s.healthMu.Lock()
	delete(s.health, id)
	s.healthMu.Unlock()
}

type cachedIndexer struct {
	fp string
	ix indexer.Indexer
}

func fingerprint(parts ...any) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = fmt.Fprintf(h, "%v\x00", p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// indexerClient returns the client of a stored indexer, cached while its
// settings do not change (the capabilities cache lives in the client).
func (s *Service) indexerClient(r *db.Indexer) (indexer.Indexer, error) {
	fp := fingerprint(r.Name, r.Kind, r.BaseUrl, r.ApiPath, r.Categories, r.Priority, string(r.Secrets))
	s.cacheMu.Lock()
	c, ok := s.ixCache[r.ID]
	s.cacheMu.Unlock()
	if ok && c.fp == fp {
		return c.ix, nil
	}
	var sec indexerSecrets
	if err := s.open(r.Secrets, indexerAD(r.ID), &sec); err != nil {
		return nil, fmt.Errorf("indexer %q: %w", r.Name, err)
	}
	ix, err := s.newIx(IndexerConfig{
		Info: indexer.Info{ID: r.ID, Name: r.Name, Priority: int(r.Priority), Protocol: decision.Protocol(r.Protocol)},
		Kind: r.Kind, BaseURL: r.BaseUrl, APIPath: r.ApiPath, APIKey: sec.APIKey, Categories: decodeArray[int](r.Categories),
	})
	if err != nil {
		return nil, fmt.Errorf("indexer %q: %w", r.Name, err)
	}
	s.cacheMu.Lock()
	s.ixCache[r.ID] = cachedIndexer{fp: fp, ix: ix}
	s.cacheMu.Unlock()
	return ix, nil
}

// TestIndexer tests a stored indexer. A success resets its health.
func (s *Service) TestIndexer(ctx context.Context, id int64) (TestResult, error) {
	r, err := s.st.Read().GetIndexer(ctx, id)
	if err != nil {
		return TestResult{}, mapNotFound(err, "Indexer %d does not exist.", id)
	}
	ix, err := s.indexerClient(&r)
	if err != nil {
		return testResult(err), nil
	}
	tctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	if err := ix.Test(tctx); err != nil {
		return testResult(err), nil
	}
	s.orch.Reset(id)
	if err := store.RequireRow(s.st.Write().UpdateIndexerHealth(ctx, db.UpdateIndexerHealthParams{
		ID: id, LastOkAt: ptr(store.Millis(s.now())),
	})); err != nil {
		return TestResult{}, fmt.Errorf("acquire: reset health of indexer %d: %w", id, err)
	}
	s.healthMu.Lock()
	delete(s.health, id)
	s.healthMu.Unlock()
	return testResult(nil), nil
}

// TestIndexerConfig tests settings before they are saved. When in.APIKey is nil
// and id names a stored indexer, its stored key is used (editing a form without
// typing the key again).
func (s *Service) TestIndexerConfig(ctx context.Context, id int64, in IndexerInput) (TestResult, error) {
	p, err := s.indexerParams(&in)
	if err != nil {
		return TestResult{}, err
	}
	key := ""
	if in.APIKey != nil {
		key = strings.TrimSpace(*in.APIKey)
	} else if id > 0 {
		r, err := s.st.Read().GetIndexer(ctx, id)
		if err != nil {
			return TestResult{}, mapNotFound(err, "Indexer %d does not exist.", id)
		}
		var sec indexerSecrets
		if err := s.open(r.Secrets, indexerAD(id), &sec); err != nil {
			return testResult(err), nil
		}
		key = sec.APIKey
	}
	ix, err := s.newIx(IndexerConfig{
		Info: indexer.Info{ID: id, Name: p.Name, Priority: int(p.Priority), Protocol: decision.Protocol(p.Protocol)},
		Kind: p.Kind, BaseURL: p.BaseUrl, APIPath: p.ApiPath, APIKey: key, Categories: in.Categories,
	})
	if err != nil {
		return testResult(err), nil
	}
	tctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	return testResult(ix.Test(tctx)), nil
}

// ----------------------------------------------------------------- download clients

// ClientHealth is the health of a download client, from the queue tracker.
type ClientHealth struct {
	FailureCount int64  `json:"failureCount" doc:"Consecutive failed calls"`
	LastError    string `json:"lastError,omitempty"`
}

// DownloadClient is a download client as shown to the user. The password and
// API key are never included.
type DownloadClient struct {
	ID              int64        `json:"id"`
	Name            string       `json:"name"`
	Kind            string       `json:"kind" enum:"sabnzbd,nzbget,qbittorrent,deluge,transmission"`
	Protocol        string       `json:"protocol" enum:"usenet,torrent"`
	Host            string       `json:"host"`
	Port            int64        `json:"port"`
	UseTLS          bool         `json:"useTls"`
	URLBase         string       `json:"urlBase"`
	Username        string       `json:"username"`
	PasswordSet     bool         `json:"passwordSet" doc:"Whether a password is stored; it is never returned"`
	APIKeySet       bool         `json:"apiKeySet" doc:"Whether an API key is stored; it is never returned"`
	Category        string       `json:"category"`
	Priority        int64        `json:"priority" doc:"Lower is preferred among enabled clients of the same protocol"`
	Enabled         bool         `json:"enabled"`
	RemoveCompleted bool         `json:"removeCompleted"`
	RemoveFailed    bool         `json:"removeFailed"`
	Tags            []string     `json:"tags"`
	Health          ClientHealth `json:"health"`
}

// DownloadClientInput creates or replaces a download client. Nil secrets keep
// the stored values; "" removes them.
type DownloadClientInput struct {
	Name            string   `json:"name" minLength:"1" maxLength:"100"`
	Kind            string   `json:"kind" enum:"sabnzbd,nzbget,qbittorrent,deluge,transmission"`
	Host            string   `json:"host" minLength:"1" maxLength:"253" doc:"Host name or IP address, without scheme"`
	Port            int64    `json:"port,omitempty" minimum:"0" maximum:"65535" doc:"0 selects the default port of the kind"`
	UseTLS          bool     `json:"useTls,omitempty"`
	URLBase         string   `json:"urlBase,omitempty" maxLength:"256"`
	Username        *string  `json:"username,omitempty" maxLength:"256"`
	Password        *string  `json:"password,omitempty" maxLength:"512" doc:"Write-only"`
	APIKey          *string  `json:"apiKey,omitempty" maxLength:"512" doc:"Write-only (SABnzbd)"`
	Category        string   `json:"category,omitempty" maxLength:"100"`
	Priority        int64    `json:"priority,omitempty" minimum:"0" maximum:"50" doc:"0 selects 1"`
	Enabled         *bool    `json:"enabled,omitempty" doc:"Default true"`
	RemoveCompleted bool     `json:"removeCompleted,omitempty"`
	RemoveFailed    *bool    `json:"removeFailed,omitempty" doc:"Default true"`
	Tags            []string `json:"tags,omitempty" maxItems:"20"`
}

func (s *Service) clientParams(in *DownloadClientInput) (db.CreateDownloadClientParams, error) {
	var p db.CreateDownloadClientParams
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return p, invalid("The download client name must be 1 to 100 characters long.")
	}
	kind := download.Kind(in.Kind)
	proto := kind.Protocol()
	if proto == "" {
		return p, invalid("Unknown download client kind %q.", in.Kind)
	}
	if in.Port == 0 {
		in.Port = defaultPort(kind)
	}
	cfg := download.Config{Host: in.Host, Port: int(in.Port), UseTLS: in.UseTLS, URLBase: in.URLBase}
	if _, err := cfg.BaseURL(int(in.Port)); err != nil {
		return p, invalid("The download client address is not valid: give a host name or IP address without scheme, a port between 1 and 65535 and a plain URL base path.")
	}
	if in.Priority == 0 {
		in.Priority = 1
	}
	if in.Priority < 1 || in.Priority > 50 {
		return p, invalid("The priority must be between 1 (preferred) and 50.")
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return p, err
	}
	return db.CreateDownloadClientParams{
		Name: in.Name, Kind: in.Kind, Protocol: string(proto), Host: strings.TrimSpace(in.Host), Port: in.Port,
		UseTls: in.UseTLS, UrlBase: strings.Trim(strings.TrimSpace(in.URLBase), "/"), Category: strings.TrimSpace(in.Category),
		Priority: in.Priority, Enabled: orTrue(in.Enabled), RemoveCompleted: in.RemoveCompleted,
		RemoveFailed: orTrue(in.RemoveFailed), Tags: jsonArray(tags),
	}, nil
}

func (s *Service) clientView(r *db.DownloadClient) DownloadClient {
	v := DownloadClient{
		ID: r.ID, Name: r.Name, Kind: r.Kind, Protocol: r.Protocol, Host: r.Host, Port: r.Port, UseTLS: r.UseTls,
		URLBase: r.UrlBase, Category: r.Category, Priority: r.Priority, Enabled: r.Enabled,
		RemoveCompleted: r.RemoveCompleted, RemoveFailed: r.RemoveFailed, Tags: decodeArray[string](r.Tags),
		Health: ClientHealth{FailureCount: r.FailureCount, LastError: deref(r.LastError)},
	}
	var sec clientSecrets
	if err := s.open(r.Secrets, clientAD(r.ID), &sec); err != nil {
		s.log.Warn("download client secrets unreadable", slog.Int64("client_id", r.ID), slog.Any("error", err))
		v.PasswordSet, v.APIKeySet = len(r.Secrets) > 0, len(r.Secrets) > 0
		return v
	}
	v.Username, v.PasswordSet, v.APIKeySet = sec.Username, sec.Password != "", sec.APIKey != ""
	return v
}

// ListDownloadClients returns every download client, preferred first.
func (s *Service) ListDownloadClients(ctx context.Context) ([]DownloadClient, error) {
	rows, err := s.st.Read().ListDownloadClients(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("acquire: list download clients: %w", err))
	}
	out := make([]DownloadClient, 0, len(rows))
	for i := range rows {
		out = append(out, s.clientView(&rows[i]))
	}
	return out, nil
}

// GetDownloadClient returns one download client.
func (s *Service) GetDownloadClient(ctx context.Context, id int64) (DownloadClient, error) {
	r, err := s.st.Read().GetDownloadClient(ctx, id)
	if err != nil {
		return DownloadClient{}, mapNotFound(err, "Download client %d does not exist.", id)
	}
	return s.clientView(&r), nil
}

// mergeSecrets applies the secrets of in over cur.
func mergeSecrets(cur clientSecrets, in *DownloadClientInput) clientSecrets {
	if in.Username != nil {
		cur.Username = strings.TrimSpace(*in.Username)
	}
	if in.Password != nil {
		cur.Password = *in.Password
	}
	if in.APIKey != nil {
		cur.APIKey = strings.TrimSpace(*in.APIKey)
	}
	return cur
}

// CreateDownloadClient validates and stores a client; secrets are encrypted.
func (s *Service) CreateDownloadClient(ctx context.Context, in DownloadClientInput) (DownloadClient, error) {
	p, err := s.clientParams(&in)
	if err != nil {
		return DownloadClient{}, err
	}
	var row db.DownloadClient
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		if row, err = q.CreateDownloadClient(ctx, p); err != nil {
			return nameConflict(err, "download client", p.Name)
		}
		sealed, err := s.seal(mergeSecrets(clientSecrets{}, &in), clientAD(row.ID))
		if err != nil {
			return fmt.Errorf("encrypt secrets: %w", err)
		}
		row.Secrets = sealed
		return store.RequireRow(q.SetDownloadClientSecrets(ctx, db.SetDownloadClientSecretsParams{ID: row.ID, Secrets: sealed}))
	})
	if err != nil {
		return DownloadClient{}, fmt.Errorf("acquire: create download client: %w", err)
	}
	s.log.Info("download client added", slog.Int64("client_id", row.ID), slog.String("name", row.Name), slog.String("kind", row.Kind))
	return s.clientView(&row), nil
}

// UpdateDownloadClient replaces the settings of a client.
func (s *Service) UpdateDownloadClient(ctx context.Context, id int64, in DownloadClientInput) (DownloadClient, error) {
	p, err := s.clientParams(&in)
	if err != nil {
		return DownloadClient{}, err
	}
	var row db.DownloadClient
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetDownloadClient(ctx, id)
		if err != nil {
			return mapNotFound(err, "Download client %d does not exist.", id)
		}
		var sec clientSecrets
		if err := s.open(cur.Secrets, clientAD(id), &sec); err != nil {
			// Master key changed: the stored credentials are lost; the ones in
			// the request (if any) replace them.
			s.log.Warn("stored download client credentials unreadable; replaced by the submitted ones",
				slog.Int64("client_id", id), slog.Any("error", err))
			sec = clientSecrets{}
		}
		row, err = q.UpdateDownloadClient(ctx, db.UpdateDownloadClientParams{
			ID: id, Name: p.Name, Kind: p.Kind, Protocol: p.Protocol, Host: p.Host, Port: p.Port, UseTls: p.UseTls,
			UrlBase: p.UrlBase, Category: p.Category, Priority: p.Priority, Enabled: p.Enabled,
			RemoveCompleted: p.RemoveCompleted, RemoveFailed: p.RemoveFailed, Tags: p.Tags,
		})
		if err != nil {
			return nameConflict(err, "download client", p.Name)
		}
		sealed, err := s.seal(mergeSecrets(sec, &in), clientAD(id))
		if err != nil {
			return fmt.Errorf("encrypt secrets: %w", err)
		}
		row.Secrets = sealed
		if err := store.RequireRow(q.SetDownloadClientSecrets(ctx, db.SetDownloadClientSecretsParams{ID: id, Secrets: sealed})); err != nil {
			return err
		}
		row.FailureCount, row.LastError = 0, nil
		return store.RequireRow(q.UpdateDownloadClientHealth(ctx, db.UpdateDownloadClientHealthParams{ID: id}))
	})
	if err != nil {
		return DownloadClient{}, fmt.Errorf("acquire: update download client %d: %w", id, err)
	}
	s.forgetClient(id)
	s.log.Info("download client updated", slog.Int64("client_id", id), slog.String("name", row.Name))
	return s.clientView(&row), nil
}

// DeleteDownloadClient removes a client. Its downloads keep their record and
// are no longer tracked.
func (s *Service) DeleteDownloadClient(ctx context.Context, id int64) error {
	if err := store.RequireRow(s.st.Write().DeleteDownloadClient(ctx, id)); err != nil {
		return mapNotFound(err, "Download client %d does not exist.", id)
	}
	s.forgetClient(id)
	s.log.Info("download client removed", slog.Int64("client_id", id))
	return nil
}

func (s *Service) forgetClient(id int64) {
	s.cacheMu.Lock()
	delete(s.clCache, id)
	s.cacheMu.Unlock()
}

type cachedClient struct {
	fp string
	cl download.Client
}

func (s *Service) clientConfig(r *db.DownloadClient, sec clientSecrets) *download.Config {
	return &download.Config{
		Host: r.Host, Port: int(r.Port), UseTLS: r.UseTls, URLBase: r.UrlBase,
		Username: sec.Username, Password: sec.Password, APIKey: sec.APIKey, Category: r.Category,
	}
}

// downloadClient returns the client of a stored row, cached while its settings
// do not change (sessions and logins live in the client).
func (s *Service) downloadClient(r *db.DownloadClient) (download.Client, error) {
	fp := fingerprint(r.Kind, r.Host, r.Port, r.UseTls, r.UrlBase, r.Category, string(r.Secrets))
	s.cacheMu.Lock()
	c, ok := s.clCache[r.ID]
	s.cacheMu.Unlock()
	if ok && c.fp == fp {
		return c.cl, nil
	}
	var sec clientSecrets
	if err := s.open(r.Secrets, clientAD(r.ID), &sec); err != nil {
		return nil, fmt.Errorf("download client %q: %w", r.Name, err)
	}
	cl, err := s.newCl(download.Kind(r.Kind), s.clientConfig(r, sec))
	if err != nil {
		return nil, fmt.Errorf("download client %q: %w", r.Name, err)
	}
	s.cacheMu.Lock()
	s.clCache[r.ID] = cachedClient{fp: fp, cl: cl}
	s.cacheMu.Unlock()
	return cl, nil
}

// TestDownloadClient tests a stored client.
func (s *Service) TestDownloadClient(ctx context.Context, id int64) (TestResult, error) {
	r, err := s.st.Read().GetDownloadClient(ctx, id)
	if err != nil {
		return TestResult{}, mapNotFound(err, "Download client %d does not exist.", id)
	}
	cl, err := s.downloadClient(&r)
	if err != nil {
		return testResult(err), nil
	}
	tctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	err = cl.Test(tctx)
	s.recordClientHealth(ctx, &r, err)
	return testResult(err), nil
}

// TestDownloadClientConfig tests settings before they are saved. Secrets left
// nil come from the stored client id when id > 0.
func (s *Service) TestDownloadClientConfig(ctx context.Context, id int64, in DownloadClientInput) (TestResult, error) {
	p, err := s.clientParams(&in)
	if err != nil {
		return TestResult{}, err
	}
	var sec clientSecrets
	if id > 0 {
		r, err := s.st.Read().GetDownloadClient(ctx, id)
		if err != nil {
			return TestResult{}, mapNotFound(err, "Download client %d does not exist.", id)
		}
		if err := s.open(r.Secrets, clientAD(id), &sec); err != nil {
			return testResult(err), nil
		}
	}
	sec = mergeSecrets(sec, &in)
	row := db.DownloadClient{Host: p.Host, Port: p.Port, UseTls: p.UseTls, UrlBase: p.UrlBase, Category: p.Category}
	cl, err := s.newCl(download.Kind(p.Kind), s.clientConfig(&row, sec))
	if err != nil {
		return testResult(err), nil
	}
	tctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	return testResult(cl.Test(tctx)), nil
}

// recordClientHealth persists the outcome of a client call when it changes.
func (s *Service) recordClientHealth(ctx context.Context, r *db.DownloadClient, callErr error) {
	p := db.UpdateDownloadClientHealthParams{ID: r.ID}
	if callErr != nil {
		msg := callErr.Error()
		p.FailureCount, p.LastError = r.FailureCount+1, &msg
	} else if r.FailureCount == 0 && r.LastError == nil {
		return
	}
	if err := store.RequireRow(s.st.Write().UpdateDownloadClientHealth(context.WithoutCancel(ctx), p)); err != nil {
		s.log.Error("record download client health", slog.Int64("client_id", r.ID), slog.Any("error", err))
		return
	}
	if (callErr == nil) != (r.FailureCount == 0) {
		s.publishClientHealth(r, callErr)
	}
	r.FailureCount, r.LastError = p.FailureCount, p.LastError
}

// ----------------------------------------------------------------- remote path mappings

// RemotePathMapping translates the paths a download client reports.
type RemotePathMapping struct {
	ID         int64  `json:"id"`
	Host       string `json:"host" doc:"Host of the download client, as configured on the client"`
	RemotePath string `json:"remotePath" doc:"Path prefix as seen by the download client"`
	LocalPath  string `json:"localPath" doc:"Same folder as seen by Gonema"`
}

// RemotePathMappingInput creates or replaces a mapping.
type RemotePathMappingInput struct {
	Host       string `json:"host" minLength:"1" maxLength:"253"`
	RemotePath string `json:"remotePath" minLength:"1" maxLength:"4096"`
	LocalPath  string `json:"localPath" minLength:"1" maxLength:"4096"`
}

func validateMapping(in *RemotePathMappingInput) error {
	in.Host = strings.TrimSpace(in.Host)
	in.RemotePath = strings.TrimSpace(in.RemotePath)
	in.LocalPath = strings.TrimSpace(in.LocalPath)
	if in.Host == "" || strings.ContainsAny(in.Host, "/?#@ ") {
		return invalid("The host must be the host name or IP address of the download client, as configured on it.")
	}
	if in.RemotePath == "" || !isAbsAny(in.RemotePath) {
		return invalid("The remote path must be absolute (/downloads or D:\\Downloads).")
	}
	if !isAbsAny(in.LocalPath) {
		return invalid("The local path must be absolute.")
	}
	if strings.Contains(in.RemotePath, "..") || strings.Contains(in.LocalPath, "..") {
		return invalid("Paths must not contain \"..\".")
	}
	return nil
}

func isAbsAny(p string) bool {
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\\`) {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

func mappingView(r *db.RemotePathMapping) RemotePathMapping {
	return RemotePathMapping{ID: r.ID, Host: r.Host, RemotePath: r.RemotePath, LocalPath: r.LocalPath}
}

// ListRemotePathMappings returns every mapping.
func (s *Service) ListRemotePathMappings(ctx context.Context) ([]RemotePathMapping, error) {
	rows, err := s.st.Read().ListRemotePathMappings(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("acquire: list remote path mappings: %w", err))
	}
	out := make([]RemotePathMapping, 0, len(rows))
	for i := range rows {
		out = append(out, mappingView(&rows[i]))
	}
	return out, nil
}

func mappingConflict(err error) error {
	if errors.Is(store.MapError(err), store.ErrConflict) {
		return conflict("A mapping for this host and remote path already exists.")
	}
	return store.MapError(err)
}

// CreateRemotePathMapping stores a mapping.
func (s *Service) CreateRemotePathMapping(ctx context.Context, in RemotePathMappingInput) (RemotePathMapping, error) {
	if err := validateMapping(&in); err != nil {
		return RemotePathMapping{}, err
	}
	r, err := s.st.Write().CreateRemotePathMapping(ctx, db.CreateRemotePathMappingParams{Host: in.Host, RemotePath: in.RemotePath, LocalPath: in.LocalPath})
	if err != nil {
		return RemotePathMapping{}, mappingConflict(err)
	}
	return mappingView(&r), nil
}

// UpdateRemotePathMapping replaces a mapping.
func (s *Service) UpdateRemotePathMapping(ctx context.Context, id int64, in RemotePathMappingInput) (RemotePathMapping, error) {
	if err := validateMapping(&in); err != nil {
		return RemotePathMapping{}, err
	}
	r, err := s.st.Write().UpdateRemotePathMapping(ctx, db.UpdateRemotePathMappingParams{ID: id, Host: in.Host, RemotePath: in.RemotePath, LocalPath: in.LocalPath})
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return RemotePathMapping{}, notFound("Remote path mapping %d does not exist.", id)
		}
		return RemotePathMapping{}, mappingConflict(err)
	}
	return mappingView(&r), nil
}

// DeleteRemotePathMapping removes a mapping.
func (s *Service) DeleteRemotePathMapping(ctx context.Context, id int64) error {
	if err := store.RequireRow(s.st.Write().DeleteRemotePathMapping(ctx, id)); err != nil {
		return mapNotFound(err, "Remote path mapping %d does not exist.", id)
	}
	return nil
}

// pathMapper builds the mapper from the stored mappings.
func (s *Service) pathMapper(ctx context.Context) (*download.PathMapper, error) {
	rows, err := s.st.Read().ListRemotePathMappings(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("list remote path mappings: %w", err))
	}
	ms := make([]download.PathMapping, 0, len(rows))
	for _, r := range rows {
		ms = append(ms, download.PathMapping{Host: r.Host, RemotePath: r.RemotePath, LocalPath: r.LocalPath})
	}
	return download.NewPathMapper(ms), nil
}
