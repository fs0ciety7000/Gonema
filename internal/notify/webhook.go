package notify

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/outbound"
)

// SchemaVersion is the version of the generic webhook payload.
const SchemaVersion = "gonema.v1"

// Headers of the generic webhook.
const (
	HeaderEvent     = "X-Gonema-Event"
	HeaderDelivery  = "X-Gonema-Delivery"
	HeaderTimestamp = "X-Gonema-Timestamp"
	HeaderSignature = "X-Gonema-Signature"
)

// Payload is the JSON body of the generic webhook, schema "gonema.v1". Fields
// are only ever added within a version; a breaking change gets "gonema.v2".
type Payload struct {
	Schema  string            `json:"schema"`           // "gonema.v1"
	Event   EventKind         `json:"event"`            // grab, import, upgrade, failure, health, rename, delete, test
	Time    time.Time         `json:"time"`             // RFC 3339, UTC
	Title   string            `json:"title"`            // headline
	Body    string            `json:"body"`             // plain sentences
	Summary string            `json:"summary"`          // one line
	Problem bool              `json:"problem"`          // failure or raised health issue
	Movie   *PayloadMovie     `json:"movie"`            // null when not about a movie
	Fields  []Field           `json:"fields"`           // ordered details
	Data    map[string]string `json:"data"`             // the fields by key
	Health  *PayloadHealth    `json:"health,omitempty"` // health messages only
}

// PayloadMovie is the movie of a webhook payload.
type PayloadMovie struct {
	ID        int64  `json:"id"`
	TmdbID    int64  `json:"tmdbId,omitempty"`
	Title     string `json:"title"`
	Year      int64  `json:"year,omitempty"`
	PosterURL string `json:"posterUrl,omitempty"`
	Link      string `json:"link,omitempty"`
}

// PayloadHealth identifies the subject of a health message.
type PayloadHealth struct {
	Source string `json:"source"`
	ID     int64  `json:"id,omitempty"`
}

// NewPayload builds the gonema.v1 payload of m.
func NewPayload(m *Message) Payload {
	p := Payload{
		Schema: SchemaVersion, Event: m.Event, Time: m.Time.UTC(), Title: m.Title, Body: m.Body, Summary: m.Summary,
		Problem: m.Problem, Fields: m.Fields, Data: make(map[string]string, len(m.Fields)),
	}
	if p.Fields == nil {
		p.Fields = []Field{}
	}
	for _, f := range m.Fields {
		p.Data[f.Key] = f.Value
	}
	if m.Movie != nil {
		p.Movie = &PayloadMovie{
			ID: m.Movie.ID, TmdbID: m.Movie.TmdbID, Title: m.Movie.Title, Year: m.Movie.Year, PosterURL: m.PosterURL, Link: m.Link,
		}
	}
	if m.HealthSource != "" {
		p.Health = &PayloadHealth{Source: m.HealthSource, ID: m.HealthID}
	}
	return p
}

// Sign returns the signature header value of a delivery:
// "sha256=" + hex(HMAC-SHA256(secret, timestamp + "." + body)).
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

type webhook struct {
	c       *outbound.Client
	url     string
	headers map[string]string
	secret  string
	now     func() time.Time
}

// reservedHeaders cannot be set by custom headers.
var reservedHeaders = map[string]bool{
	"Content-Type": true, "Content-Length": true, "Host": true, "User-Agent": true, "Transfer-Encoding": true, "Connection": true,
}

func newWebhook(c *outbound.Client, s *ServiceSettings, sec *Secrets, now func() time.Time) (*webhook, error) {
	u, err := requireURL(s.URL, "webhook URL")
	if err != nil {
		return nil, err
	}
	if len(sec.Headers) > 20 {
		return nil, configErr("a webhook takes at most 20 custom headers")
	}
	for k, v := range sec.Headers {
		ck := http.CanonicalHeaderKey(k)
		if !validHeaderName(k) {
			return nil, configErr("the header name %q is not valid", truncate(k, 40))
		}
		if reservedHeaders[ck] || strings.HasPrefix(ck, "X-Gonema-") {
			return nil, configErr("the header %s is set by Gonema and cannot be replaced", ck)
		}
		if strings.ContainsAny(v, "\r\n\x00") || len(v) > 4096 {
			return nil, configErr("the value of header %s is not valid (one line, at most 4096 characters)", ck)
		}
	}
	return &webhook{c: c, url: u, headers: sec.Headers, secret: sec.SigningSecret, now: now}, nil
}

func validHeaderName(k string) bool {
	if k == "" || len(k) > 100 {
		return false
	}
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}

func (w *webhook) Test(ctx context.Context) error { return w.Send(ctx, TestMessage(w.now())) }

func (w *webhook) Send(ctx context.Context, m *Message) error {
	body, err := json.Marshal(NewPayload(m))
	if err != nil {
		return w.c.Fail(outbound.ErrResponse, 0, "cannot encode the payload")
	}
	ts := strconv.FormatInt(w.now().Unix(), 10)
	h := http.Header{}
	for k, v := range w.headers {
		h.Set(k, v)
	}
	h.Set("Content-Type", "application/json")
	h.Set(HeaderEvent, string(m.Event))
	h.Set(HeaderDelivery, deliveryID())
	h.Set(HeaderTimestamp, ts)
	if w.secret != "" {
		h.Set(HeaderSignature, Sign(w.secret, ts, body))
	}
	resp, err := w.c.Do(ctx, http.MethodPost, w.url, h, body)
	if err != nil {
		return err
	}
	return w.c.Check(resp, snippet(resp.Body))
}

func deliveryID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails (crypto/rand)
	return hex.EncodeToString(b[:])
}

// snippet returns the start of a text body, for error details.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if strings.HasPrefix(s, "<") {
		return "" // an HTML error page says nothing useful
	}
	return truncate(strings.Join(strings.Fields(s), " "), 200)
}
