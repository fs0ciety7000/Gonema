package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fs0ciety7000/gonema/internal/outbound"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const (
	testBotToken = "123456789:AAHsecretBotTokenValue_xyz-0123456789"
	testWebhook  = "/api/webhooks/1234/s3cr3t-webhook-token"
)

// sampleMessage is the grab of the copy examples, with every optional part.
func sampleMessage() *Message {
	return &Message{
		Event: EventGrab, Time: time.Date(2026, 11, 5, 20, 15, 0, 0, time.UTC),
		Title:     "Grabbed — Halcyon Drift (2024)",
		Body:      "Remux-2160p · 58.4 GB from Nebula NZB. Sent to SABnzbd.",
		Summary:   "Grabbed — Halcyon Drift (2024) · Remux-2160p · 58.4 GB from Nebula NZB",
		Movie:     &MovieRef{ID: 7, TmdbID: 900001, Title: "Halcyon Drift", Year: 2024},
		PosterURL: "https://image.tmdb.org/t/p/w342/poster.jpg",
		Link:      "https://gonema.example.lan/movies/7",
		Fields: []Field{
			{Key: "quality", Label: "Quality", Value: "Remux-2160p"},
			{Key: "size", Label: "Size", Value: "58.4 GB"},
			{Key: "indexer", Label: "Indexer", Value: "Nebula NZB"},
			{Key: "release", Label: "Release", Value: "Halcyon.Drift.2024.2160p.UHD.BluRay.REMUX.HDR.HEVC.Atmos-GRP <@everyone> & co"},
		},
	}
}

func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/notify -run Golden -update)", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("%s mismatch (-want +got):\n%s", name, diff)
	}
}

func TestPayloadGolden(t *testing.T) {
	m := sampleMessage()
	golden(t, "webhook.json", NewPayload(m))
	golden(t, "discord.json", discordPayload(m, "Gonema"))
	golden(t, "slack.json", slackPayload(m))
	golden(t, "telegram.json", telegramPayload(m, "-1001234567890", 0, false))
	golden(t, "ntfy.json", ntfyPayload(m, "gonema-movies", 3, []string{"movies"}))
	golden(t, "gotify.json", gotifyPayload(m, 5))

	h := &Message{Event: EventHealth, Time: m.Time, Title: "Health issue — Nebula NZB is not answering", Body: "No answer for 10 minutes. Others keep searching.",
		Summary: "Health issue — Nebula NZB is not answering", Problem: true, HealthSource: "indexers", HealthID: 3}
	golden(t, "webhook_health.json", NewPayload(h))
}

type capture struct {
	mu   sync.Mutex
	reqs []*http.Request
	body [][]byte
}

func (c *capture) last(t *testing.T) (*http.Request, []byte) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reqs) == 0 {
		t.Fatal("no request")
	}
	return c.reqs[len(c.reqs)-1], c.body[len(c.body)-1]
}

func server(t *testing.T, fn func(w http.ResponseWriter, r *http.Request, body []byte)) (*httptest.Server, *capture) {
	t.Helper()
	c := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.reqs = append(c.reqs, r.Clone(context.Background()))
		c.body = append(c.body, b)
		c.mu.Unlock()
		fn(w, r, b)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func newNotifier(t *testing.T, cfg Config) Notifier {
	t.Helper()
	n, err := New(cfg, Options{UserAgent: "Gonema/test", Now: func() time.Time { return time.Unix(1_800_000_000, 0) }})
	if err != nil {
		t.Fatalf("New(%s): %v", cfg.Kind, err)
	}
	return n
}

func TestWebhook(t *testing.T) {
	srv, c := server(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(http.StatusNoContent) })
	n := newNotifier(t, Config{Kind: Webhook, Settings: ServiceSettings{URL: srv.URL + "/hook"},
		Secrets: Secrets{SigningSecret: "hmac-key-0123", Headers: map[string]string{"Authorization": "Bearer custom-token-xyz"}}})
	if err := n.Send(context.Background(), sampleMessage()); err != nil {
		t.Fatal(err)
	}
	r, body := c.last(t)
	ts := r.Header.Get(HeaderTimestamp)
	if r.Method != http.MethodPost || r.URL.Path != "/hook" || ts != "1800000000" || r.Header.Get(HeaderEvent) != "grab" ||
		len(r.Header.Get(HeaderDelivery)) != 32 || r.Header.Get("Authorization") != "Bearer custom-token-xyz" ||
		r.Header.Get("Content-Type") != "application/json" || r.Header.Get("User-Agent") != "Gonema/test" {
		t.Errorf("headers = %v", r.Header)
	}
	if want := Sign("hmac-key-0123", ts, body); !hmac.Equal([]byte(r.Header.Get(HeaderSignature)), []byte(want)) {
		t.Errorf("signature = %q, want %q", r.Header.Get(HeaderSignature), want)
	}
	var p Payload
	if err := json.Unmarshal(body, &p); err != nil || p.Schema != "gonema.v1" || p.Data["quality"] != "Remux-2160p" || p.Movie.Link == "" {
		t.Errorf("payload = %+v, %v", p, err)
	}
	// Sign follows the documented formula: HMAC-SHA256(secret, timestamp + "." + body).
	mac := hmac.New(sha256.New, []byte("k"))
	mac.Write([]byte("1.{}"))
	if got, want := Sign("k", "1", []byte("{}")), "sha256="+hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Errorf("Sign = %q, want %q", got, want)
	}

	for _, h := range []map[string]string{
		{"Content-Type": "text/plain"}, {"X-Gonema-Event": "x"}, {"Bad Name": "x"}, {"X-Ok": "a\r\nInjected: 1"},
	} {
		if _, err := New(Config{Kind: Webhook, Settings: ServiceSettings{URL: srv.URL}, Secrets: Secrets{Headers: h}}, Options{}); !errors.Is(err, ErrConfig) {
			t.Errorf("headers %v accepted: %v", h, err)
		}
	}
}

func TestDiscordRateLimit(t *testing.T) {
	calls := 0
	srv, c := server(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message": "You are being rate limited.", "retry_after": 2.5, "global": false}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	n := newNotifier(t, Config{Kind: Discord, Settings: ServiceSettings{Username: "Gonema"}, Secrets: Secrets{WebhookURL: srv.URL + testWebhook}})
	var slept []time.Duration
	r := outbound.Retry{Attempts: 3, Backoff: []time.Duration{time.Second}, Sleep: func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}}
	err := r.Do(context.Background(), func(ctx context.Context) error { return n.Send(ctx, sampleMessage()) })
	if err != nil || calls != 2 || len(slept) != 1 || slept[0] != 2500*time.Millisecond {
		t.Fatalf("err %v, calls %d, slept %v", err, calls, slept)
	}
	_, body := c.last(t)
	var b discordBody
	if err := json.Unmarshal(body, &b); err != nil || len(b.Embeds) != 1 || b.Embeds[0].Color != colorDownloading || b.AllowedMentions.Parse == nil {
		t.Errorf("body = %s", body)
	}

	// The webhook token (URL path) never appears in errors.
	srv.Close()
	err = n.Send(context.Background(), sampleMessage())
	if err == nil || strings.Contains(err.Error(), "s3cr3t-webhook-token") || strings.Contains(err.Error(), "1234") {
		t.Errorf("error leaks the webhook URL: %v", err)
	}
}

func TestTelegram(t *testing.T) {
	srv, c := server(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if r.URL.Path != "/bot"+testBotToken+"/sendMessage" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":404,"description":"Not Found"}`))
			return
		}
		if strings.Contains(string(body), `"chat_id":"-100404"`) {
			w.WriteHeader(http.StatusBadRequest)
			// A hostile or buggy server echoing the token must not leak it.
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found for bot` + testBotToken + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	})
	cfg := Config{Kind: Telegram, Settings: ServiceSettings{ServerURL: srv.URL, ChatID: "-1001234567890", ThreadID: 9, Silent: true}, Secrets: Secrets{Token: testBotToken}}
	n := newNotifier(t, cfg)
	if err := n.Send(context.Background(), sampleMessage()); err != nil {
		t.Fatal(err)
	}
	_, body := c.last(t)
	var b telegramBody
	if err := json.Unmarshal(body, &b); err != nil || b.ParseMode != "HTML" || b.MessageThreadID != 9 || !b.DisableNotification ||
		!strings.Contains(b.Text, "&lt;@everyone&gt; &amp; co") || !strings.HasPrefix(b.Text, "<b>Grabbed — Halcyon Drift (2024)</b>") {
		t.Errorf("body = %s", body)
	}
	cfg.Settings.ChatID = "-100404"
	err := newNotifier(t, cfg).Send(context.Background(), sampleMessage())
	if err == nil || !strings.Contains(err.Error(), "chat not found") || strings.Contains(err.Error(), "AAHsecret") || outbound.Temporary(err) {
		t.Errorf("chat not found: %v", err)
	}
	srv.Close()
	err = n.Send(context.Background(), sampleMessage())
	if err == nil || strings.Contains(err.Error(), "AAHsecret") || strings.Contains(err.Error(), "/bot") {
		t.Errorf("transport error leaks the token: %v", err)
	}
	if _, err := New(Config{Kind: Telegram, Settings: ServiceSettings{ChatID: "1"}, Secrets: Secrets{Token: "nope"}}, Options{}); !errors.Is(err, ErrConfig) ||
		strings.Contains(err.Error(), "nope") {
		t.Errorf("malformed token: %v", err)
	}
}

func TestPushover(t *testing.T) {
	token, user := strings.Repeat("a", 30), strings.Repeat("u", 30)
	srv, c := server(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		f, _ := url.ParseQuery(string(body))
		if r.URL.Path != "/1/messages.json" || f.Get("token") != token || f.Get("user") != user {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"user":"invalid","errors":["user identifier is invalid"],"status":0}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":1,"request":"647d2300-702c-4b38-8b2f-d56326ae460b"}`))
	})
	pr := -1
	n := newNotifier(t, Config{Kind: Pushover, Settings: ServiceSettings{ServerURL: srv.URL, Priority: &pr, Sound: "none"}, Secrets: Secrets{Token: token, UserKey: user}})
	m := sampleMessage()
	m.Problem = true
	if err := n.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	_, body := c.last(t)
	f, _ := url.ParseQuery(string(body))
	if f.Get("title") != m.Title || f.Get("priority") != "0" || f.Get("url") != m.Link || f.Get("url_title") != "Open in Gonema" ||
		f.Get("sound") != "none" || !strings.Contains(f.Get("message"), "Quality: Remux-2160p") {
		t.Errorf("form = %v", f)
	}
	bad := newNotifier(t, Config{Kind: Pushover, Settings: ServiceSettings{ServerURL: srv.URL}, Secrets: Secrets{Token: token, UserKey: strings.Repeat("x", 30)}})
	if err := bad.Send(context.Background(), m); err == nil || !strings.Contains(err.Error(), "user identifier is invalid") || strings.Contains(err.Error(), token) {
		t.Errorf("bad user: %v", err)
	}
	if _, err := New(Config{Kind: Pushover, Secrets: Secrets{Token: "short", UserKey: user}}, Options{}); !errors.Is(err, ErrConfig) {
		t.Errorf("short token: %v", err)
	}
}

func TestNtfy(t *testing.T) {
	srv, c := server(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if r.Header.Get("Authorization") != "Bearer tk_ntfytoken0123" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":40101,"http":401,"error":"unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"x","event":"message"}`))
	})
	five := 5
	n := newNotifier(t, Config{Kind: Ntfy, Settings: ServiceSettings{ServerURL: srv.URL + "/", Topic: "gonema-movies", Priority: &five, Tags: []string{" movies ", ""}},
		Secrets: Secrets{Token: "tk_ntfytoken0123"}})
	if err := n.Send(context.Background(), sampleMessage()); err != nil {
		t.Fatal(err)
	}
	r, body := c.last(t)
	var b ntfyBody
	if err := json.Unmarshal(body, &b); err != nil || r.URL.Path != "/" || b.Topic != "gonema-movies" || b.Priority != 5 ||
		b.Click != sampleMessage().Link || b.Icon == "" || len(b.Tags) != 1 || b.Tags[0] != "movies" {
		t.Errorf("request %s %s", r.URL.Path, body)
	}
	basic := newNotifier(t, Config{Kind: Ntfy, Settings: ServiceSettings{ServerURL: srv.URL, Topic: "t", Username: "phil"}, Secrets: Secrets{Password: "pw-123456"}})
	if err := basic.Send(context.Background(), sampleMessage()); !errors.Is(err, outbound.ErrAuth) || strings.Contains(err.Error(), "pw-123456") {
		t.Errorf("basic: %v", err)
	}
	if r, _ := c.last(t); r.Header.Get("Authorization") == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
		t.Errorf("basic auth header = %q", r.Header.Get("Authorization"))
	}
	if _, err := New(Config{Kind: Ntfy, Settings: ServiceSettings{Topic: "a/b"}}, Options{}); !errors.Is(err, ErrConfig) {
		t.Errorf("bad topic: %v", err)
	}
}

func TestGotifyAndSlack(t *testing.T) {
	srv, c := server(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		switch r.URL.Path {
		case "/gotify/message":
			if r.Header.Get("X-Gotify-Key") != "AppToken.123" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"Unauthorized","errorCode":401,"errorDescription":"you need to provide a valid access token or user credentials to access this api"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":1}`))
		case "/services/T000/B000/XXXX":
			_, _ = w.Write([]byte("ok"))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("no_service"))
		}
	})
	g := newNotifier(t, Config{Kind: Gotify, Settings: ServiceSettings{ServerURL: srv.URL + "/gotify"}, Secrets: Secrets{Token: "AppToken.123"}})
	if err := g.Send(context.Background(), sampleMessage()); err != nil {
		t.Fatal(err)
	}
	if _, body := c.last(t); !strings.Contains(string(body), `"client::notification"`) {
		t.Errorf("gotify body = %s", body)
	}
	bad := newNotifier(t, Config{Kind: Gotify, Settings: ServiceSettings{ServerURL: srv.URL + "/gotify"}, Secrets: Secrets{Token: "Wrong.Token"}})
	if err := bad.Send(context.Background(), sampleMessage()); !errors.Is(err, outbound.ErrAuth) || !strings.Contains(err.Error(), "valid access token") {
		t.Errorf("gotify auth: %v", err)
	}

	s := newNotifier(t, Config{Kind: Slack, Secrets: Secrets{WebhookURL: srv.URL + "/services/T000/B000/XXXX"}})
	if err := s.Send(context.Background(), sampleMessage()); err != nil {
		t.Fatal(err)
	}
	gone := newNotifier(t, Config{Kind: Slack, Secrets: Secrets{WebhookURL: srv.URL + "/services/T000/B000/YYYY"}})
	err := gone.Send(context.Background(), sampleMessage())
	if err == nil || !strings.Contains(err.Error(), "no_service") || strings.Contains(err.Error(), "YYYY") {
		t.Errorf("slack: %v", err)
	}
}

func TestNewValidation(t *testing.T) {
	for _, cfg := range []Config{
		{Kind: "sms"},
		{Kind: Webhook, Settings: ServiceSettings{URL: "file:///etc/passwd"}},
		{Kind: Webhook, Settings: ServiceSettings{URL: "http://user:pw@host/x"}},
		{Kind: Discord},
		{Kind: Slack, Secrets: Secrets{WebhookURL: "gopher://x"}},
		{Kind: Telegram, Secrets: Secrets{Token: testBotToken}},
		{Kind: Gotify, Secrets: Secrets{Token: "x"}},
		{Kind: Email, Settings: ServiceSettings{Host: "smtp", From: "a@b.c"}},
		{Kind: Email, Settings: ServiceSettings{Host: "smtp", From: "nope", To: []string{"a@b.c"}}},
		{Kind: Email, Settings: ServiceSettings{Host: "smtp", From: "a@b.c", To: []string{"a@b.c\r\nBcc: x@y.z"}}},
		{Kind: Email, Settings: ServiceSettings{Host: "smtp", From: "a@b.c", To: []string{"a@b.c"}, Encryption: "ssl3"}},
	} {
		if _, err := New(cfg, Options{}); !errors.Is(err, ErrConfig) {
			t.Errorf("New(%+v) = %v, want ErrConfig", cfg, err)
		}
	}
}

func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{{"abc", 3, "abc"}, {"abcd", 3, "ab…"}, {"éèàù", 2, "é…"}, {"x", 0, ""}, {"xy", 1, "…"}} {
		if got := truncate(tc.in, tc.n); got != tc.want {
			t.Errorf("truncate(%q, %d) = %q", tc.in, tc.n, got)
		}
	}
}
