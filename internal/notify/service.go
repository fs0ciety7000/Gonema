package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	ErrInvalid = errors.New("notify: invalid settings")
	// ErrNotFound: no such notification. Also matches store.ErrNotFound.
	ErrNotFound = errors.New("notify: not found")
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

// HealthSource is the source of the health.changed events of notifications.
const HealthSource = "notification"

// HealthEvent is the data of the health.changed events this package publishes
// (same shape as the other health events).
type HealthEvent struct {
	Source  string `json:"source"`
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// FailuresBeforeHealth is the number of consecutive failed deliveries (each
// retried already) that raises a health issue.
const FailuresBeforeHealth = 3

// Defaults of the dispatcher.
const (
	eventBuffer = 1024
	jobQueue    = 256
	sendTimeout = 10 * time.Minute // one delivery, retries and Retry-After waits included
	testTimeout = 30 * time.Second
)

// ServiceOptions configure a Service.
type ServiceOptions struct {
	Store   *store.Store
	Box     *secretbox.Box
	Bus     *events.Bus // optional: no dispatch without it
	Logger  *slog.Logger
	Now     func() time.Time
	HTTP    *http.Client
	Version string
	Images  ImageBaseURLSource // optional: poster URLs

	Workers int // default 4
	Retry   outbound.Retry
	// NewNotifier builds a notifier; default New. Tests replace it.
	NewNotifier func(Config, Options) (Notifier, error)
}

// Service manages the notifications (settings with encrypted secrets, tests)
// and dispatches bus events to them.
type Service struct {
	st      *store.Store
	box     *secretbox.Box
	bus     *events.Bus
	log     *slog.Logger
	now     func() time.Time
	nopts   Options
	newN    func(Config, Options) (Notifier, error)
	retry   outbound.Retry
	workers int
	b       *builder

	jobs    chan sendJob
	sub     *events.Subscription
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	once    sync.Once
	dropped atomic.Uint64
}

type sendJob struct {
	id  int64
	msg *Message
}

// NewService returns a service. Call Start to dispatch bus events.
func NewService(o ServiceOptions) (*Service, error) {
	if o.Store == nil || o.Box == nil {
		return nil, errors.New("notify: store and secret box are required")
	}
	s := &Service{
		st: o.Store, box: o.Box, bus: o.Bus, log: o.Logger, now: o.Now, newN: o.NewNotifier, retry: o.Retry, workers: o.Workers,
		nopts: Options{HTTP: o.HTTP, UserAgent: "Gonema/" + o.Version},
		jobs:  make(chan sendJob, jobQueue),
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.nopts.Now = s.now
	s.nopts.defaultClients()
	if s.newN == nil {
		s.newN = New
	}
	if s.workers <= 0 {
		s.workers = 4
	}
	if s.retry.Attempts == 0 {
		s.retry = outbound.Retry{Attempts: 4, Backoff: []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}}
	}
	s.b = newBuilder(o.Store, o.Images, s.now)
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s, nil
}

// ----------------------------------------------------------------- views

// DeliveryHealth is the outcome of the last deliveries of a notification.
type DeliveryHealth struct {
	FailureCount int64      `json:"failureCount" doc:"Consecutive failed deliveries"`
	LastError    string     `json:"lastError,omitempty"`
	LastOKAt     *time.Time `json:"lastOkAt"`
	LastFailedAt *time.Time `json:"lastFailedAt"`
}

// EventFlag is one row of the event matrix.
type EventFlag struct {
	Event   EventKind `json:"event" enum:"grab,import,upgrade,failure,health,rename,delete"`
	Label   string    `json:"label"`
	Enabled bool      `json:"enabled"`
}

// NotificationSecretsSet tells which secrets are stored; their values are never returned.
type NotificationSecretsSet struct {
	WebhookURL    bool     `json:"webhookUrl"`
	Token         bool     `json:"token"`
	UserKey       bool     `json:"userKey"`
	Password      bool     `json:"password"`
	SigningSecret bool     `json:"signingSecret"`
	HeaderNames   []string `json:"headerNames" doc:"Names of the stored custom headers (values are secret)"`
}

// Notification is a notification as the API shows it.
type Notification struct {
	ID          int64                  `json:"id"`
	Name        string                 `json:"name"`
	Kind        Kind                   `json:"kind" enum:"webhook,discord,telegram,pushover,ntfy,gotify,slack,email"`
	Settings    ServiceSettings        `json:"settings"`
	SecretsSet  NotificationSecretsSet `json:"secretsSet"`
	Events      []EventKind            `json:"events"`
	EventMatrix []EventFlag            `json:"eventMatrix" doc:"Every event with its state"`
	Tags        []string               `json:"tags" doc:"Only movies with one of these tags; empty for all movies"`
	Enabled     bool                   `json:"enabled"`
	Health      DeliveryHealth         `json:"health"`
}

// NotificationSecretsInput are write-only secrets: nil keeps the stored value, "" removes
// it. Headers: nil keeps the stored headers, an empty object removes them.
type NotificationSecretsInput struct {
	WebhookURL    *string           `json:"webhookUrl,omitempty" maxLength:"2048" doc:"Discord, Slack: webhook URL"`
	Token         *string           `json:"token,omitempty" maxLength:"512" doc:"Telegram bot token, Pushover application token, ntfy access token, Gotify application token"`
	UserKey       *string           `json:"userKey,omitempty" maxLength:"64" doc:"Pushover user or group key"`
	Password      *string           `json:"password,omitempty" maxLength:"512" doc:"ntfy basic auth or SMTP password"`
	SigningSecret *string           `json:"signingSecret,omitempty" maxLength:"512" doc:"webhook: HMAC-SHA256 key of the X-Gonema-Signature header"`
	Headers       map[string]string `json:"headers,omitempty" doc:"webhook: custom headers (values are secret)"`
}

// NotificationInput creates or replaces a notification.
type NotificationInput struct {
	Name     string                    `json:"name" minLength:"1" maxLength:"100"`
	Kind     Kind                      `json:"kind" enum:"webhook,discord,telegram,pushover,ntfy,gotify,slack,email"`
	Settings ServiceSettings           `json:"settings,omitempty" doc:"Omitted: none (kinds whose settings are all secrets)"`
	Secrets  *NotificationSecretsInput `json:"secrets,omitempty"`
	Events   []EventKind               `json:"events,omitempty" maxItems:"7" doc:"Omitted: grab, import, upgrade, failure, health"`
	Tags     []string                  `json:"tags,omitempty" maxItems:"20"`
	Enabled  *bool                     `json:"enabled,omitempty" doc:"Default true"`
}

// NotificationTest is the outcome of a test message.
type NotificationTest struct {
	OK      bool   `json:"ok"`
	Message string `json:"message" doc:"Plain-language result, safe to display"`
}

func ad(id int64) []byte { return []byte("notifications/" + strconv.FormatInt(id, 10)) }

func (s *Service) seal(v *Secrets, id int64) ([]byte, error) {
	if v.isZero() {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return s.box.Seal(raw, ad(id))
}

func (s *Service) open(sealed []byte, id int64) (Secrets, error) {
	var v Secrets
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

func millisTime(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := store.FromMillis(*ms)
	return &t
}

func (s *Service) view(r *db.Notification) Notification {
	evs := decodeJSON[[]EventKind](r.Events)
	if evs == nil {
		evs = []EventKind{}
	}
	tags := decodeJSON[[]string](r.Tags)
	if tags == nil {
		tags = []string{}
	}
	v := Notification{
		ID: r.ID, Name: r.Name, Kind: Kind(r.Kind), Settings: decodeJSON[ServiceSettings](r.Settings), Events: evs, Tags: tags,
		Enabled: r.Enabled,
		Health:  DeliveryHealth{FailureCount: r.FailureCount, LastOKAt: millisTime(r.LastOkAt), LastFailedAt: millisTime(r.LastFailedAt)},
	}
	if r.LastError != nil {
		v.Health.LastError = *r.LastError
	}
	for _, k := range EventKinds {
		v.EventMatrix = append(v.EventMatrix, EventFlag{Event: k, Label: k.Label(), Enabled: slices.Contains(evs, k)})
	}
	sec, err := s.open(r.Secrets, r.ID)
	v.SecretsSet.HeaderNames = []string{}
	if err != nil {
		s.log.Warn("notification secrets unreadable", slog.Int64("notification_id", r.ID), slog.Any("error", err))
		return v
	}
	v.SecretsSet = NotificationSecretsSet{
		WebhookURL: sec.WebhookURL != "", Token: sec.Token != "", UserKey: sec.UserKey != "", Password: sec.Password != "",
		SigningSecret: sec.SigningSecret != "", HeaderNames: slices.Sorted(maps.Keys(sec.Headers)),
	}
	if v.SecretsSet.HeaderNames == nil {
		v.SecretsSet.HeaderNames = []string{}
	}
	return v
}

// normalizeTags trims, lower-cases, deduplicates and sorts tag labels.
func normalizeTags(tags []string) ([]string, error) {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if len(t) > 64 {
			return nil, invalid("The tag %q is longer than 64 characters.", t[:64])
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return out, nil
}

func mergeSecrets(cur Secrets, in *NotificationSecretsInput) Secrets {
	if in == nil {
		return cur
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&cur.WebhookURL, in.WebhookURL)
	set(&cur.Token, in.Token)
	set(&cur.UserKey, in.UserKey)
	set(&cur.SigningSecret, in.SigningSecret)
	if in.Password != nil {
		cur.Password = *in.Password // passwords keep their spaces
	}
	if in.Headers != nil {
		cur.Headers = nil
		if len(in.Headers) > 0 {
			cur.Headers = maps.Clone(in.Headers)
		}
	}
	return cur
}

// validated checks in with the merged secrets by building the notifier, and
// returns the row values.
func (s *Service) validated(in *NotificationInput, sec *Secrets) (db.CreateNotificationParams, error) {
	var p db.CreateNotificationParams
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return p, invalid("The name must be 1 to 100 characters long.")
	}
	if !in.Kind.Valid() {
		return p, invalid("Unknown notification kind %q.", in.Kind)
	}
	evs := in.Events
	if evs == nil {
		evs = slices.Clone(DefaultEvents)
	}
	for _, e := range evs {
		if !e.Valid() {
			return p, invalid("Unknown event %q: use grab, import, upgrade, failure, health, rename or delete.", e)
		}
	}
	canon := make([]EventKind, 0, len(evs))
	for _, k := range EventKinds {
		if slices.Contains(evs, k) {
			canon = append(canon, k)
		}
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return p, err
	}
	if _, err := s.newN(Config{Kind: in.Kind, Settings: in.Settings, Secrets: *sec}, s.nopts); err != nil {
		if errors.Is(err, ErrConfig) {
			return p, invalid("%s", sentence(ConfigMessage(err)))
		}
		return p, invalid("The settings are not valid: %s", err.Error())
	}
	return db.CreateNotificationParams{
		Name: in.Name, Kind: string(in.Kind), Settings: encodeJSON(in.Settings), Events: encodeJSON(canon),
		Tags: encodeJSON(tags), Enabled: in.Enabled == nil || *in.Enabled,
	}, nil
}

func notFound(err error, id int64) error {
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return &Error{Kind: ErrNotFound, Msg: fmt.Sprintf("Notification %d does not exist.", id)}
	}
	return store.MapError(fmt.Errorf("notify: read %d: %w", id, err))
}

func nameConflict(err error, name string) error {
	err = store.MapError(err)
	if errors.Is(err, store.ErrConflict) {
		return &Error{Kind: ErrInvalid, Msg: fmt.Sprintf("A notification named %q already exists.", name), Err: err}
	}
	return err
}

// List returns every notification.
func (s *Service) List(ctx context.Context) ([]Notification, error) {
	rows, err := s.st.Read().ListNotifications(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("notify: list: %w", err))
	}
	out := make([]Notification, 0, len(rows))
	for i := range rows {
		out = append(out, s.view(&rows[i]))
	}
	return out, nil
}

// Get returns one notification.
func (s *Service) Get(ctx context.Context, id int64) (Notification, error) {
	r, err := s.st.Read().GetNotification(ctx, id)
	if err != nil {
		return Notification{}, notFound(err, id)
	}
	return s.view(&r), nil
}

// Create validates and stores a notification; secrets are encrypted.
func (s *Service) Create(ctx context.Context, in NotificationInput) (Notification, error) {
	sec := mergeSecrets(Secrets{}, in.Secrets)
	p, err := s.validated(&in, &sec)
	if err != nil {
		return Notification{}, err
	}
	var row db.Notification
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		if row, err = q.CreateNotification(ctx, p); err != nil {
			return nameConflict(err, p.Name)
		}
		sealed, err := s.seal(&sec, row.ID)
		if err != nil {
			return fmt.Errorf("encrypt secrets: %w", err)
		}
		row.Secrets = sealed
		return store.RequireRow(q.SetNotificationSecrets(ctx, db.SetNotificationSecretsParams{ID: row.ID, Secrets: sealed}))
	})
	if err != nil {
		return Notification{}, fmt.Errorf("notify: create: %w", err)
	}
	s.log.Info("notification added", slog.Int64("notification_id", row.ID), slog.String("name", row.Name), slog.String("kind", row.Kind))
	return s.view(&row), nil
}

// Update replaces the settings of a notification and resets its health.
func (s *Service) Update(ctx context.Context, id int64, in NotificationInput) (Notification, error) {
	var row db.Notification
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetNotification(ctx, id)
		if err != nil {
			return notFound(err, id)
		}
		sec, err := s.open(cur.Secrets, id)
		if err != nil {
			s.log.Warn("stored notification secrets unreadable; replaced by the submitted ones",
				slog.Int64("notification_id", id), slog.Any("error", err))
			sec = Secrets{}
		}
		if Kind(cur.Kind) != in.Kind {
			sec = Secrets{} // secrets of another service never carry over
		}
		sec = mergeSecrets(sec, in.Secrets)
		p, err := s.validated(&in, &sec)
		if err != nil {
			return err
		}
		row, err = q.UpdateNotification(ctx, db.UpdateNotificationParams{
			ID: id, Name: p.Name, Kind: p.Kind, Settings: p.Settings, Events: p.Events, Tags: p.Tags, Enabled: p.Enabled,
		})
		if err != nil {
			return nameConflict(err, p.Name)
		}
		sealed, err := s.seal(&sec, id)
		if err != nil {
			return fmt.Errorf("encrypt secrets: %w", err)
		}
		row.Secrets = sealed
		if err := store.RequireRow(q.SetNotificationSecrets(ctx, db.SetNotificationSecretsParams{ID: id, Secrets: sealed})); err != nil {
			return err
		}
		row.FailureCount, row.LastError = 0, nil
		return store.RequireRow(q.ResetNotificationHealth(ctx, id))
	})
	if err != nil {
		var ue *Error
		if errors.As(err, &ue) {
			return Notification{}, err
		}
		return Notification{}, fmt.Errorf("notify: update %d: %w", id, err)
	}
	s.log.Info("notification updated", slog.Int64("notification_id", id), slog.String("name", row.Name))
	return s.view(&row), nil
}

// Delete removes a notification.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := store.RequireRow(s.st.Write().DeleteNotification(ctx, id)); err != nil {
		return notFound(err, id)
	}
	s.log.Info("notification removed", slog.Int64("notification_id", id))
	return nil
}

func testResult(err error) NotificationTest {
	if err == nil {
		return NotificationTest{OK: true, Message: "Test message sent."}
	}
	if errors.Is(err, ErrConfig) {
		return NotificationTest{Message: sentence(ConfigMessage(err))}
	}
	return NotificationTest{Message: err.Error()}
}

// notifier builds the notifier of a stored row.
func (s *Service) notifier(r *db.Notification) (Notifier, error) {
	sec, err := s.open(r.Secrets, r.ID)
	if err != nil {
		return nil, err
	}
	return s.newN(Config{Kind: Kind(r.Kind), Settings: decodeJSON[ServiceSettings](r.Settings), Secrets: sec}, s.nopts)
}

// Test sends a test message with a stored notification. A success resets its
// health.
func (s *Service) Test(ctx context.Context, id int64) (NotificationTest, error) {
	r, err := s.st.Read().GetNotification(ctx, id)
	if err != nil {
		return NotificationTest{}, notFound(err, id)
	}
	n, err := s.notifier(&r)
	if err != nil {
		return testResult(err), nil
	}
	tctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	if err := n.Test(tctx); err != nil {
		return testResult(err), nil
	}
	s.recordOK(ctx, &r)
	return testResult(nil), nil
}

// TestConfig sends a test message with unsaved settings. With id > 0, the
// stored secrets of that notification fill the omitted ones (same kind only).
func (s *Service) TestConfig(ctx context.Context, id int64, in NotificationInput) (NotificationTest, error) {
	var sec Secrets
	if id > 0 {
		r, err := s.st.Read().GetNotification(ctx, id)
		if err != nil {
			return NotificationTest{}, notFound(err, id)
		}
		if Kind(r.Kind) == in.Kind {
			if sec, err = s.open(r.Secrets, id); err != nil {
				return testResult(err), nil
			}
		}
	}
	sec = mergeSecrets(sec, in.Secrets)
	if _, err := s.validated(&in, &sec); err != nil {
		return NotificationTest{}, err
	}
	n, err := s.newN(Config{Kind: in.Kind, Settings: in.Settings, Secrets: sec}, s.nopts)
	if err != nil {
		return testResult(err), nil
	}
	tctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	return testResult(n.Test(tctx)), nil
}

// AppURL returns the application URL used in message links ("" if unset).
func (s *Service) AppURL(ctx context.Context) string { return s.b.appURL(ctx) }

// SetAppURL stores the application URL ("" clears it).
func (s *Service) SetAppURL(ctx context.Context, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		u, err := outbound.ValidateURL(raw)
		if err != nil {
			return "", invalid("The application URL is not valid: %s.", strings.TrimPrefix(err.Error(), outbound.ErrInvalidURL.Error()+": "))
		}
		if u.RawQuery != "" {
			return "", invalid("The application URL must not have a query string.")
		}
		raw = strings.TrimRight(u.String(), "/")
	}
	if err := store.SetSetting(ctx, s.st, SettingAppURL, raw); err != nil {
		return "", fmt.Errorf("notify: save the application URL: %w", err)
	}
	return raw, nil
}
