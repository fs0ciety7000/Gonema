package mediaserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/outbound"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Errors, matched with errors.Is.
var (
	// ErrInvalid: the settings were refused; the message says why.
	ErrInvalid = errors.New("mediaserver: invalid settings")
	// ErrNotFound: no such media server. Also matches store.ErrNotFound.
	ErrNotFound = errors.New("mediaserver: not found")
)

// Error carries a plain-language message for the user.
type Error struct {
	Kind error
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}

// UserMessage returns the plain-language message.
func (e *Error) UserMessage() string { return e.Msg }

// Is matches the kind (and store.ErrNotFound for ErrNotFound).
func (e *Error) Is(target error) bool {
	return target == e.Kind || (e.Kind == ErrNotFound && target == store.ErrNotFound)
}

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.Err }

func invalid(format string, args ...any) error {
	return &Error{Kind: ErrInvalid, Msg: fmt.Sprintf(format, args...)}
}

// HealthSource is the source of the health.changed events of media servers.
const HealthSource = "mediaServer"

// HealthEvent is the data of the health.changed events this package publishes
// (same shape as the other health events: source, id, name, ok, message).
type HealthEvent struct {
	Source  string `json:"source"`
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// Defaults.
const (
	DefaultDebounce = 5 * time.Second
	DefaultMaxWait  = 30 * time.Second
	testTimeout     = 20 * time.Second
	refreshTimeout  = 2 * time.Minute // one refresh, retries included
	eventBuffer     = 256
	workQueue       = 256
)

// ServiceOptions configure a Service.
type ServiceOptions struct {
	Store   *store.Store
	Box     *secretbox.Box
	Bus     *events.Bus // optional: no refresh on events without it
	Logger  *slog.Logger
	Now     func() time.Time
	HTTP    *http.Client // default outbound.NewHTTPClient()
	Version string

	Debounce time.Duration // default DefaultDebounce
	MaxWait  time.Duration // default DefaultMaxWait
	Workers  int           // default 2
	Retry    outbound.Retry
	// NewServer builds a server from its configuration; default New. Tests
	// replace it.
	NewServer func(Config, Options) (Server, error)
}

// Service manages the media servers: settings with encrypted secrets, tests,
// and the refresher that rescans movie folders after imports, upgrades,
// renames and deletions.
type Service struct {
	st        *store.Store
	box       *secretbox.Box
	bus       *events.Bus
	log       *slog.Logger
	now       func() time.Time
	sopts     Options
	newServer func(Config, Options) (Server, error)
	retry     outbound.Retry
	workers   int
	deb       *debouncer

	work   chan refreshJob
	sub    *events.Subscription
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

type refreshJob struct {
	t      target
	reason Reason
}

// NewService returns a service. Call Start to follow the bus.
func NewService(o ServiceOptions) (*Service, error) {
	if o.Store == nil || o.Box == nil {
		return nil, errors.New("mediaserver: store and secret box are required")
	}
	s := &Service{
		st: o.Store, box: o.Box, bus: o.Bus, log: o.Logger, now: o.Now, newServer: o.NewServer,
		retry: o.Retry, workers: o.Workers,
		sopts: Options{HTTP: o.HTTP, UserAgent: "Gonema/" + o.Version, Version: o.Version},
		work:  make(chan refreshJob, workQueue),
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.newServer == nil {
		s.newServer = New
	}
	if s.sopts.HTTP == nil {
		s.sopts.HTTP = outbound.NewHTTPClient()
	}
	if s.workers <= 0 {
		s.workers = 2
	}
	if s.retry.Attempts == 0 {
		s.retry = outbound.Retry{Attempts: 3, Backoff: []time.Duration{2 * time.Second, 10 * time.Second}}
	}
	window, maxWait := o.Debounce, o.MaxWait
	if window <= 0 {
		window = DefaultDebounce
	}
	if maxWait <= 0 {
		maxWait = DefaultMaxWait
	}
	s.deb = newDebouncer(window, maxWait, s.now, s.enqueue)
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s, nil
}

// ----------------------------------------------------------------- views

// RefreshHealth is the outcome of the last refreshes of a server.
type RefreshHealth struct {
	FailureCount int64      `json:"failureCount" doc:"Consecutive failed refreshes"`
	LastError    string     `json:"lastError,omitempty"`
	LastOKAt     *time.Time `json:"lastOkAt"`
	LastFailedAt *time.Time `json:"lastFailedAt"`
}

// ReasonFlag is one row of the refresh matrix.
type ReasonFlag struct {
	Reason  Reason `json:"reason" enum:"add,upgrade,rename,delete"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
}

// MediaServer is a media server as the API shows it. Secrets are never
// returned: tokenSet and passwordSet say whether one is stored.
type MediaServer struct {
	ID            int64               `json:"id"`
	Name          string              `json:"name"`
	Kind          Kind                `json:"kind" enum:"plex,jellyfin,kodi"`
	BaseURL       string              `json:"baseUrl"`
	TokenSet      bool                `json:"tokenSet" doc:"Whether a Plex token or Jellyfin API key is stored; it is never returned"`
	Username      string              `json:"username" doc:"Kodi web server user"`
	PasswordSet   bool                `json:"passwordSet" doc:"Whether a Kodi password is stored; it is never returned"`
	PathMappings  []ServerPathMapping `json:"pathMappings"`
	RefreshOn     []Reason            `json:"refreshOn"`
	RefreshMatrix []ReasonFlag        `json:"refreshMatrix" doc:"Every refresh reason with its state"`
	CleanOnDelete bool                `json:"cleanOnDelete" doc:"Kodi: clean the library after a file is removed"`
	Enabled       bool                `json:"enabled"`
	Health        RefreshHealth       `json:"health"`
}

// MediaServerInput creates or replaces a media server. Nil secrets keep the stored
// values; "" removes them.
type MediaServerInput struct {
	Name          string              `json:"name" minLength:"1" maxLength:"100"`
	Kind          Kind                `json:"kind" enum:"plex,jellyfin,kodi"`
	BaseURL       string              `json:"baseUrl" minLength:"1" maxLength:"2048" doc:"e.g. http://192.168.1.10:32400 (Plex), http://jellyfin:8096, http://kodi:8080"`
	Token         *string             `json:"token,omitempty" maxLength:"512" doc:"Write-only. Plex token or Jellyfin API key"`
	Username      *string             `json:"username,omitempty" maxLength:"256" doc:"Kodi web server user"`
	Password      *string             `json:"password,omitempty" maxLength:"512" doc:"Write-only. Kodi web server password"`
	PathMappings  []ServerPathMapping `json:"pathMappings,omitempty" maxItems:"20"`
	RefreshOn     []Reason            `json:"refreshOn,omitempty" maxItems:"4" doc:"Omitted: every reason"`
	CleanOnDelete bool                `json:"cleanOnDelete,omitempty"`
	Enabled       *bool               `json:"enabled,omitempty" doc:"Default true"`
}

// MediaServerTest is the outcome of a connection test.
type MediaServerTest struct {
	OK      bool   `json:"ok"`
	Message string `json:"message" doc:"Plain-language result, safe to display"`
}

type secrets struct {
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type options struct {
	CleanOnDelete bool `json:"cleanOnDelete,omitempty"`
}

func ad(id int64) []byte { return []byte("media-servers/" + strconv.FormatInt(id, 10)) }

func (s *Service) seal(v secrets, id int64) ([]byte, error) {
	if v == (secrets{}) {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return s.box.Seal(raw, ad(id))
}

func (s *Service) open(sealed []byte, id int64) (secrets, error) {
	var v secrets
	if len(sealed) == 0 {
		return v, nil
	}
	raw, err := s.box.Open(sealed, ad(id))
	if err != nil {
		return v, fmt.Errorf("decrypt secrets (master key changed?): %w", err)
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("decode secrets: %w", err)
	}
	return v, nil
}

func decodeJSON[T any](raw string) T {
	var v T
	_ = json.Unmarshal([]byte(raw), &v) // CHECK constraints guarantee valid JSON
	return v
}

func encodeJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(raw)
}

func millisPtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := store.FromMillis(*ms)
	return &t
}

func (s *Service) view(r *db.MediaServer) MediaServer {
	refresh := decodeJSON[[]Reason](r.RefreshOn)
	if refresh == nil {
		refresh = []Reason{}
	}
	maps := decodeJSON[[]ServerPathMapping](r.PathMappings)
	if maps == nil {
		maps = []ServerPathMapping{}
	}
	v := MediaServer{
		ID: r.ID, Name: r.Name, Kind: Kind(r.Kind), BaseURL: r.BaseUrl, PathMappings: maps, RefreshOn: refresh,
		CleanOnDelete: decodeJSON[options](r.Options).CleanOnDelete, Enabled: r.Enabled,
		Health: RefreshHealth{
			FailureCount: r.FailureCount, LastOKAt: millisPtr(r.LastOkAt), LastFailedAt: millisPtr(r.LastFailedAt),
		},
	}
	if r.LastError != nil {
		v.Health.LastError = *r.LastError
	}
	for _, reason := range Reasons {
		v.RefreshMatrix = append(v.RefreshMatrix, ReasonFlag{Reason: reason, Label: reasonLabel(reason), Enabled: slices.Contains(refresh, reason)})
	}
	sec, err := s.open(r.Secrets, r.ID)
	if err != nil {
		s.log.Warn("media server secrets unreadable", slog.Int64("media_server_id", r.ID), slog.Any("error", err))
		v.TokenSet, v.PasswordSet = len(r.Secrets) > 0, len(r.Secrets) > 0
		return v
	}
	v.TokenSet, v.Username, v.PasswordSet = sec.Token != "", sec.Username, sec.Password != ""
	return v
}

func reasonLabel(r Reason) string {
	switch r {
	case ReasonAdd:
		return "Movie imported"
	case ReasonUpgrade:
		return "Movie upgraded"
	case ReasonRename:
		return "Files renamed"
	case ReasonDelete:
		return "File removed"
	}
	return string(r)
}

// params validates in and returns the row values.
func params(in *MediaServerInput) (db.CreateMediaServerParams, error) {
	var p db.CreateMediaServerParams
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return p, invalid("The name must be 1 to 100 characters long.")
	}
	if !in.Kind.Valid() {
		return p, invalid("Unknown media server kind %q: use plex, jellyfin or kodi.", in.Kind)
	}
	u, err := outbound.ValidateURL(in.BaseURL)
	if err != nil {
		return p, invalid("The server URL is not valid: %s.", strings.TrimPrefix(err.Error(), outbound.ErrInvalidURL.Error()+": "))
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.RawQuery != "" {
		return p, invalid("The server URL must not have a query string.")
	}
	maps := make([]ServerPathMapping, 0, len(in.PathMappings))
	for _, m := range in.PathMappings {
		m.From, m.To = strings.TrimSpace(m.From), strings.TrimSpace(m.To)
		if m.From == "" || m.To == "" {
			return p, invalid("Each path mapping needs both folders: Gonema's and the server's.")
		}
		if len(m.From) > 4096 || len(m.To) > 4096 {
			return p, invalid("A path mapping folder is longer than 4096 characters.")
		}
		maps = append(maps, m)
	}
	refresh := in.RefreshOn
	if refresh == nil {
		refresh = slices.Clone(Reasons)
	}
	out := make([]Reason, 0, len(refresh))
	for _, r := range Reasons { // canonical order, no duplicates
		if slices.Contains(refresh, r) {
			out = append(out, r)
		}
	}
	for _, r := range refresh {
		if !r.Valid() {
			return p, invalid("Unknown refresh reason %q: use add, upgrade, rename or delete.", r)
		}
	}
	enabled := in.Enabled == nil || *in.Enabled
	return db.CreateMediaServerParams{
		Name: in.Name, Kind: string(in.Kind), BaseUrl: u.String(), PathMappings: encodeJSON(maps),
		RefreshOn: encodeJSON(out), Options: encodeJSON(options{CleanOnDelete: in.CleanOnDelete}), Enabled: enabled,
	}, nil
}

func mergeSecrets(cur secrets, in *MediaServerInput) secrets {
	if in.Token != nil {
		cur.Token = strings.TrimSpace(*in.Token)
	}
	if in.Username != nil {
		cur.Username = strings.TrimSpace(*in.Username)
	}
	if in.Password != nil {
		cur.Password = *in.Password
	}
	return cur
}

func notFound(err error, id int64) error {
	if errors.Is(err, store.ErrNotFound) || errors.Is(store.MapError(err), store.ErrNotFound) {
		return &Error{Kind: ErrNotFound, Msg: fmt.Sprintf("Media server %d does not exist.", id)}
	}
	return store.MapError(fmt.Errorf("mediaserver: read %d: %w", id, err))
}

func nameConflict(err error, name string) error {
	err = store.MapError(err)
	if errors.Is(err, store.ErrConflict) {
		return &Error{Kind: ErrInvalid, Msg: fmt.Sprintf("A media server named %q already exists.", name), Err: err}
	}
	return err
}

// List returns every media server.
func (s *Service) List(ctx context.Context) ([]MediaServer, error) {
	rows, err := s.st.Read().ListMediaServers(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("mediaserver: list: %w", err))
	}
	out := make([]MediaServer, 0, len(rows))
	for i := range rows {
		out = append(out, s.view(&rows[i]))
	}
	return out, nil
}

// Get returns one media server.
func (s *Service) Get(ctx context.Context, id int64) (MediaServer, error) {
	r, err := s.st.Read().GetMediaServer(ctx, id)
	if err != nil {
		return MediaServer{}, notFound(err, id)
	}
	return s.view(&r), nil
}

// Create validates and stores a media server; secrets are encrypted.
func (s *Service) Create(ctx context.Context, in MediaServerInput) (MediaServer, error) {
	p, err := params(&in)
	if err != nil {
		return MediaServer{}, err
	}
	var row db.MediaServer
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		if row, err = q.CreateMediaServer(ctx, p); err != nil {
			return nameConflict(err, p.Name)
		}
		sealed, err := s.seal(mergeSecrets(secrets{}, &in), row.ID)
		if err != nil {
			return fmt.Errorf("encrypt secrets: %w", err)
		}
		row.Secrets = sealed
		return store.RequireRow(q.SetMediaServerSecrets(ctx, db.SetMediaServerSecretsParams{ID: row.ID, Secrets: sealed}))
	})
	if err != nil {
		return MediaServer{}, fmt.Errorf("mediaserver: create: %w", err)
	}
	s.log.Info("media server added", slog.Int64("media_server_id", row.ID), slog.String("name", row.Name), slog.String("kind", row.Kind))
	return s.view(&row), nil
}

// Update replaces the settings of a media server and resets its health.
func (s *Service) Update(ctx context.Context, id int64, in MediaServerInput) (MediaServer, error) {
	p, err := params(&in)
	if err != nil {
		return MediaServer{}, err
	}
	var row db.MediaServer
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetMediaServer(ctx, id)
		if err != nil {
			return notFound(err, id)
		}
		sec, err := s.open(cur.Secrets, id)
		if err != nil {
			s.log.Warn("stored media server secrets unreadable; replaced by the submitted ones",
				slog.Int64("media_server_id", id), slog.Any("error", err))
			sec = secrets{}
		}
		row, err = q.UpdateMediaServer(ctx, db.UpdateMediaServerParams{
			ID: id, Name: p.Name, Kind: p.Kind, BaseUrl: p.BaseUrl, PathMappings: p.PathMappings,
			RefreshOn: p.RefreshOn, Options: p.Options, Enabled: p.Enabled,
		})
		if err != nil {
			return nameConflict(err, p.Name)
		}
		sealed, err := s.seal(mergeSecrets(sec, &in), id)
		if err != nil {
			return fmt.Errorf("encrypt secrets: %w", err)
		}
		row.Secrets = sealed
		if err := store.RequireRow(q.SetMediaServerSecrets(ctx, db.SetMediaServerSecretsParams{ID: id, Secrets: sealed})); err != nil {
			return err
		}
		row.FailureCount, row.LastError = 0, nil
		return store.RequireRow(q.ResetMediaServerHealth(ctx, id))
	})
	if err != nil {
		return MediaServer{}, fmt.Errorf("mediaserver: update %d: %w", id, err)
	}
	s.log.Info("media server updated", slog.Int64("media_server_id", id), slog.String("name", row.Name))
	return s.view(&row), nil
}

// Delete removes a media server.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := store.RequireRow(s.st.Write().DeleteMediaServer(ctx, id)); err != nil {
		return notFound(err, id)
	}
	s.log.Info("media server removed", slog.Int64("media_server_id", id))
	return nil
}

// config builds the configuration of a stored server.
func (s *Service) config(r *db.MediaServer) (Config, error) {
	sec, err := s.open(r.Secrets, r.ID)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Kind: Kind(r.Kind), BaseURL: r.BaseUrl, Token: sec.Token, Username: sec.Username, Password: sec.Password,
		Mappings: decodeJSON[[]ServerPathMapping](r.PathMappings), CleanOnDelete: decodeJSON[options](r.Options).CleanOnDelete,
	}, nil
}

func testResult(err error) MediaServerTest {
	if err == nil {
		return MediaServerTest{OK: true, Message: "Connection successful."}
	}
	return MediaServerTest{Message: err.Error()}
}

// Test checks a stored server. A success resets its health.
func (s *Service) Test(ctx context.Context, id int64) (MediaServerTest, error) {
	r, err := s.st.Read().GetMediaServer(ctx, id)
	if err != nil {
		return MediaServerTest{}, notFound(err, id)
	}
	cfg, err := s.config(&r)
	if err != nil {
		return testResult(err), nil
	}
	err = s.test(ctx, cfg)
	if err == nil {
		s.recordOK(ctx, &r)
	}
	return testResult(err), nil
}

// TestConfig tests settings before they are saved. With id > 0, the stored
// secrets of that server fill the omitted ones.
func (s *Service) TestConfig(ctx context.Context, id int64, in MediaServerInput) (MediaServerTest, error) {
	p, err := params(&in)
	if err != nil {
		return MediaServerTest{}, err
	}
	var sec secrets
	if id > 0 {
		r, err := s.st.Read().GetMediaServer(ctx, id)
		if err != nil {
			return MediaServerTest{}, notFound(err, id)
		}
		if sec, err = s.open(r.Secrets, id); err != nil {
			return testResult(err), nil
		}
	}
	sec = mergeSecrets(sec, &in)
	cfg := Config{
		Kind: in.Kind, BaseURL: p.BaseUrl, Token: sec.Token, Username: sec.Username, Password: sec.Password,
		Mappings: decodeJSON[[]ServerPathMapping](p.PathMappings), CleanOnDelete: in.CleanOnDelete,
	}
	return testResult(s.test(ctx, cfg)), nil
}

func (s *Service) test(ctx context.Context, cfg Config) error {
	srv, err := s.newServer(cfg, s.sopts)
	if err != nil {
		return err
	}
	tctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	return srv.Test(tctx)
}
