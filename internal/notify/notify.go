package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/outbound"
)

// EventKind is what a message is about (and a notifications.events value).
type EventKind string

// Event kinds.
const (
	EventGrab    EventKind = "grab"    // a release was sent to a download client
	EventImport  EventKind = "import"  // a movie file was imported
	EventUpgrade EventKind = "upgrade" // a better file replaced the previous one
	EventFailure EventKind = "failure" // download failed, retries exhausted, import failed
	EventHealth  EventKind = "health"  // a health issue was raised or resolved
	EventRename  EventKind = "rename"  // movie files were renamed
	EventDelete  EventKind = "delete"  // a movie or a file was removed
	EventTest    EventKind = "test"    // test message from the settings page
)

// EventKinds lists the kinds a notification can subscribe to, in display order.
var EventKinds = []EventKind{EventGrab, EventImport, EventUpgrade, EventFailure, EventHealth, EventRename, EventDelete}

// DefaultEvents are the kinds of a notification created without a list.
var DefaultEvents = []EventKind{EventGrab, EventImport, EventUpgrade, EventFailure, EventHealth}

// Valid reports whether k can be subscribed to.
func (k EventKind) Valid() bool {
	for _, v := range EventKinds {
		if v == k {
			return true
		}
	}
	return false
}

// Label is the name shown in the event matrix.
func (k EventKind) Label() string {
	switch k {
	case EventGrab:
		return "Release grabbed"
	case EventImport:
		return "Movie imported"
	case EventUpgrade:
		return "Movie upgraded"
	case EventFailure:
		return "Download or import failed"
	case EventHealth:
		return "Health issue raised or resolved"
	case EventRename:
		return "Files renamed"
	case EventDelete:
		return "Movie or file removed"
	case EventTest:
		return "Test"
	}
	return string(k)
}

// Field is one detail of a message.
type Field struct {
	Key   string `json:"key"`   // stable: quality, size, indexer, release, reason...
	Label string `json:"label"` // shown to people
	Value string `json:"value"`
}

// MovieRef identifies the movie of a message.
type MovieRef struct {
	ID     int64  `json:"id"`
	TmdbID int64  `json:"tmdbId,omitempty"`
	Title  string `json:"title"`
	Year   int64  `json:"year,omitempty"`
}

// Display returns "Title (Year)".
func (m *MovieRef) Display() string {
	if m == nil {
		return ""
	}
	if m.Year > 0 {
		return fmt.Sprintf("%s (%d)", m.Title, m.Year)
	}
	return m.Title
}

// Message is a notification, independent of the service that delivers it.
type Message struct {
	Event EventKind
	Time  time.Time
	// Title is the headline ("Grabbed — Halcyon Drift (2024)").
	Title string
	// Body is one or two plain sentences, without markup.
	Body string
	// Summary is the one-line form (title and the gist of the body).
	Summary string
	// Problem marks failures and raised health issues (higher priority).
	Problem bool
	// Movie, PosterURL and Link are empty when the message is not about a
	// movie (health, test). PosterURL is a public TMDB image URL; Link opens
	// the movie in Gonema when the application URL is configured.
	Movie     *MovieRef
	PosterURL string
	Link      string
	Fields    []Field
	// Source of a health message ("indexer", "notification"...) and its id:
	// a notification never receives the health messages about itself.
	HealthSource string
	HealthID     int64
}

// Text returns the title, the body and the fields as plain text.
func (m *Message) Text() string {
	var b strings.Builder
	b.WriteString(m.Title)
	if m.Body != "" {
		b.WriteString("\n")
		b.WriteString(m.Body)
	}
	if len(m.Fields) > 0 {
		b.WriteString("\n")
		for _, f := range m.Fields {
			b.WriteString("\n")
			b.WriteString(f.Label)
			b.WriteString(": ")
			b.WriteString(f.Value)
		}
	}
	if m.Link != "" {
		b.WriteString("\n\n")
		b.WriteString(m.Link)
	}
	return b.String()
}

// Notifier delivers messages to one service.
type Notifier interface {
	// Test checks the settings by sending a test message.
	Test(ctx context.Context) error
	// Send delivers a message.
	Send(ctx context.Context, m *Message) error
}

// Kind names a notification service (notifications.kind).
type Kind string

// Kinds.
const (
	Webhook  Kind = "webhook"
	Discord  Kind = "discord"
	Telegram Kind = "telegram"
	Pushover Kind = "pushover"
	Ntfy     Kind = "ntfy"
	Gotify   Kind = "gotify"
	Slack    Kind = "slack"
	Email    Kind = "email"
)

// Kinds lists the supported kinds.
var Kinds = []Kind{Webhook, Discord, Telegram, Pushover, Ntfy, Gotify, Slack, Email}

// Valid reports whether k is supported.
func (k Kind) Valid() bool {
	for _, v := range Kinds {
		if v == k {
			return true
		}
	}
	return false
}

// Label is the product name used in messages.
func (k Kind) Label() string {
	switch k {
	case Webhook:
		return "Webhook"
	case Discord:
		return "Discord"
	case Telegram:
		return "Telegram"
	case Pushover:
		return "Pushover"
	case Ntfy:
		return "ntfy"
	case Gotify:
		return "Gotify"
	case Slack:
		return "Slack"
	case Email:
		return "Email"
	}
	return string(k)
}

// ServiceSettings are the non-secret settings of a notification. Each kind uses a
// subset (see the field docs); the others must stay empty.
type ServiceSettings struct {
	URL       string   `json:"url,omitempty" maxLength:"2048" doc:"webhook: address the JSON is POSTed to"`
	ServerURL string   `json:"serverUrl,omitempty" maxLength:"2048" doc:"ntfy, Gotify: server address (ntfy default https://ntfy.sh); Telegram, Pushover: API address override"`
	Topic     string   `json:"topic,omitempty" maxLength:"64" doc:"ntfy: topic name"`
	ChatID    string   `json:"chatId,omitempty" maxLength:"64" doc:"Telegram: chat id or @channel"`
	ThreadID  int64    `json:"threadId,omitempty" minimum:"0" doc:"Telegram: forum topic id"`
	Priority  *int     `json:"priority,omitempty" minimum:"-2" maximum:"10" doc:"Pushover -2..1, ntfy 1..5, Gotify 0..10; problems get one step more"`
	Tags      []string `json:"tags,omitempty" maxItems:"10" doc:"ntfy: tags shown with the message"`
	Device    string   `json:"device,omitempty" maxLength:"100" doc:"Pushover: device name, empty for all"`
	Sound     string   `json:"sound,omitempty" maxLength:"50" doc:"Pushover: sound name"`
	Silent    bool     `json:"silent,omitempty" doc:"Telegram: deliver without sound"`
	Username  string   `json:"username,omitempty" maxLength:"256" doc:"Discord: display name; ntfy: basic auth user; email: SMTP user"`
	Host      string   `json:"host,omitempty" maxLength:"253" doc:"email: SMTP host"`
	Port      int      `json:"port,omitempty" minimum:"0" maximum:"65535" doc:"email: SMTP port, 0 for the default of the encryption"`
	// Encryption: "starttls" (default, port 587), "tls" (implicit TLS, port 465)
	// or "none" (port 25; no password is sent over it, except to localhost).
	Encryption string   `json:"encryption,omitempty" enum:"starttls,tls,none," doc:"email: starttls (587), tls (465) or none (25)"`
	From       string   `json:"from,omitempty" maxLength:"320" doc:"email: sender address"`
	To         []string `json:"to,omitempty" maxItems:"20" doc:"email: recipients"`
}

// Secrets are the encrypted settings of a notification.
type Secrets struct {
	WebhookURL    string            `json:"webhookUrl,omitempty"`    // Discord, Slack
	Token         string            `json:"token,omitempty"`         // Telegram bot, Pushover app, ntfy access token, Gotify app
	UserKey       string            `json:"userKey,omitempty"`       // Pushover
	Password      string            `json:"password,omitempty"`      // ntfy basic auth, SMTP
	SigningSecret string            `json:"signingSecret,omitempty"` // webhook HMAC
	Headers       map[string]string `json:"headers,omitempty"`       // webhook custom headers
}

func (s *Secrets) isZero() bool {
	return s.WebhookURL == "" && s.Token == "" && s.UserKey == "" && s.Password == "" && s.SigningSecret == "" && len(s.Headers) == 0
}

// Config is a notification as the implementations need it.
type Config struct {
	Kind     Kind
	Settings ServiceSettings
	Secrets  Secrets
}

// Options are the shared dependencies of the implementations.
type Options struct {
	HTTP      *http.Client // default outbound.NewHTTPClient()
	UserAgent string       // default "Gonema"
	Timeout   time.Duration
	Now       func() time.Time
	// Dial opens SMTP connections; default net.Dialer. Tests replace it.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// ErrConfig: the settings are incomplete or invalid; the message says why.
var ErrConfig = errors.New("notify: invalid settings")

func configErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrConfig, fmt.Sprintf(format, args...))
}

// ConfigMessage returns the plain sentence of an ErrConfig error.
func ConfigMessage(err error) string {
	return strings.TrimPrefix(err.Error(), ErrConfig.Error()+": ")
}

// New validates cfg and returns the notifier of its kind.
func New(cfg Config, opts Options) (Notifier, error) {
	if !cfg.Kind.Valid() {
		return nil, configErr("unknown notification kind %q", cfg.Kind)
	}
	if opts.HTTP == nil {
		opts.HTTP = outbound.NewHTTPClient()
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "Gonema"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	red := &outbound.Redactor{}
	sec := cfg.Secrets
	red.Add(sec.WebhookURL, sec.Token, sec.UserKey, sec.Password, sec.SigningSecret)
	for _, v := range sec.Headers {
		red.Add(v)
	}
	if u, err := outbound.ValidateURL(sec.WebhookURL); err == nil {
		// The path of a Discord or Slack webhook URL is its secret.
		red.Add(strings.TrimPrefix(u.Path, "/"), u.EscapedPath())
	}
	client := &outbound.Client{
		Service: cfg.Kind.Label(), HTTP: opts.HTTP, Timeout: opts.Timeout, UserAgent: opts.UserAgent, Redactor: red, Now: opts.Now,
	}
	s := &cfg.Settings
	switch cfg.Kind {
	case Webhook:
		return newWebhook(client, s, &sec, opts.Now)
	case Discord:
		return newDiscord(client, s, &sec)
	case Telegram:
		return newTelegram(client, s, &sec)
	case Pushover:
		return newPushover(client, s, &sec)
	case Ntfy:
		return newNtfy(client, s, &sec)
	case Gotify:
		return newGotify(client, s, &sec)
	case Slack:
		return newSlack(client, s, &sec)
	case Email:
		return newEmail(s, &sec, red, opts)
	}
	return nil, configErr("unknown notification kind %q", cfg.Kind)
}

// requireURL validates a service URL setting.
func requireURL(raw, what string) (string, error) {
	u, err := outbound.ValidateURL(raw)
	if err != nil {
		return "", configErr("the %s is not valid: %s", what, strings.TrimPrefix(err.Error(), outbound.ErrInvalidURL.Error()+": "))
	}
	return u.String(), nil
}

// TestMessage is the message sent by Test.
func TestMessage(now time.Time) *Message {
	return &Message{
		Event: EventTest, Time: now.UTC(), Title: "Test — Gonema",
		Body:    "This is a test notification. If you can read it, the settings work.",
		Summary: "Test — Gonema: the notification settings work.",
	}
}

// truncate cuts s to n runes, ending with an ellipsis when cut.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

func asOutbound(err error, target **outbound.Error) bool { return errors.As(err, target) }
