package logbuf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRedact(t *testing.T) {
	cases := []struct{ in, want string }{
		{"GET https://indexer.example/api?t=search&apikey=abcdef123&q=dune", "GET https://indexer.example/api?t=search&apikey=" + Redacted + "&q=dune"},
		{"https://x.example/rss?passkey=0123456789abcdef", "https://x.example/rss?passkey=" + Redacted},
		{"POST /api?token=s3cr3t", "POST /api?token=" + Redacted},
		{"login failed: password=hunter2 user=bob", "login failed: password=" + Redacted + " user=bob"},
		{"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig", "Authorization: " + Redacted},
		{"authorization=Basic dXNlcjpwYXNz", "authorization=" + Redacted},
		{"header X-Api-Key: 0123456789", "header X-Api-Key: " + Redacted},
		{"X-Plex-Token=abcdef", "X-Plex-Token=" + Redacted},
		{"Cookie: SID=31d4d96e407aad42; lang=en", "Cookie: " + Redacted},
		{"token bearer abcdefghijklmnop", "token bearer " + Redacted},
		{`{"username":"bob","password":"hunter2","apiKey":"k-123"}`, `{"username":"bob","password":"` + Redacted + `","apiKey":"` + Redacted + `"}`},
		{"dial http://admin:adminadmin@192.168.1.5:8080/transmission/rpc", "dial http://admin:" + Redacted + "@192.168.1.5:8080/transmission/rpc"},
		{"nothing secret in here", "nothing secret in here"},
		{"elapsed=3s status=200", "elapsed=3s status=200"},
	}
	for _, c := range cases {
		if got := Redact(c.in); got != c.want {
			t.Errorf("Redact(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestSensitiveKey(t *testing.T) {
	for _, k := range []string{"password", "apiKey", "api_key", "API-KEY", "tmdb_api_key", "Authorization", "client.token", "session", "user_password", "master_key"} {
		if !SensitiveKey(k) {
			t.Errorf("%q not sensitive", k)
		}
	}
	for _, k := range []string{"key", "movie_id", "path", "status", "request_id", "keys_count"} {
		if SensitiveKey(k) {
			t.Errorf("%q sensitive", k)
		}
	}
}

type secretErr struct{}

func (secretErr) Error() string { return "request https://u:p4ss@host/x?apikey=zzz failed" }

// TestHandlerRedactsEveryOutput checks that a secret logged as a message, an
// attribute, an error, a group or a WithAttrs attribute reaches neither the
// text output nor the buffer.
func TestHandlerRedactsEveryOutput(t *testing.T) {
	var out bytes.Buffer
	lv := new(slog.LevelVar)
	buf := NewBuffer(10)
	log := slog.New(NewHandler(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: lv}), buf, lv))
	log = log.With(slog.String("api_key", "TOPSECRET1"))
	log.WithGroup("client").Info("calling https://x/api?apikey=TOPSECRET2",
		slog.String("password", "TOPSECRET3"),
		slog.Any("error", secretErr{}),
		slog.Group("req", slog.String("Authorization", "Bearer TOPSECRET4"), slog.String("url", "http://a/?token=TOPSECRET5")),
		slog.String("movie", "Dune"),
	)
	text := out.String()
	entries := buf.Entries(Query{})
	if len(entries) != 1 {
		t.Fatalf("%d entries", len(entries))
	}
	all := text + fmt.Sprint(entries[0])
	for _, s := range []string{"TOPSECRET", "p4ss", "zzz"} {
		if strings.Contains(all, s) {
			t.Errorf("secret %q leaked:\n%s", s, all)
		}
	}
	e := entries[0]
	if e.Attrs["client.movie"] != "Dune" || e.Attrs["api_key"] != Redacted || e.Attrs["client.req.Authorization"] != Redacted || e.Level != "info" {
		t.Errorf("entry = %+v", e)
	}
	if !strings.Contains(text, "movie=Dune") {
		t.Errorf("text output lost attributes: %s", text)
	}
}

func TestBufferRingQueryAndLevel(t *testing.T) {
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelInfo)
	buf := NewBuffer(3)
	log := slog.New(NewHandler(slog.NewTextHandler(new(bytes.Buffer), &slog.HandlerOptions{Level: lv}), buf, lv))
	log.Debug("hidden")
	log.Info("one")
	log.Warn("two", slog.String("movie", "Heat"))
	log.Error("three")
	log.Info("four")
	got := buf.Entries(Query{})
	if len(got) != 3 || got[0].Message != "two" || got[2].Message != "four" || got[0].ID != 2 {
		t.Fatalf("ring = %+v", got)
	}
	if got := buf.Entries(Query{MinLevel: "warn"}); len(got) != 2 || got[1].Message != "three" {
		t.Errorf("min warn = %+v", got)
	}
	if got := buf.Entries(Query{Text: "HEAT"}); len(got) != 1 || got[0].Message != "two" {
		t.Errorf("text = %+v", got)
	}
	if got := buf.Entries(Query{Limit: 1}); len(got) != 1 || got[0].Message != "four" {
		t.Errorf("limit = %+v", got)
	}
	if got := buf.Entries(Query{AfterID: 2}); len(got) != 2 || got[0].Message != "three" {
		t.Errorf("after = %+v", got)
	}
	if got := buf.Entries(Query{Since: time.Now().Add(time.Hour)}); len(got) != 0 {
		t.Errorf("since future = %+v", got)
	}
	lv.Set(slog.LevelDebug) // runtime change
	log.Debug("visible")
	if got := buf.Entries(Query{Limit: 1}); got[0].Message != "visible" {
		t.Errorf("debug after level change = %+v", got)
	}
	var text bytes.Buffer
	if err := buf.WriteText(&text); err != nil || strings.Count(text.String(), "\n") != 3 || !strings.Contains(text.String(), "ERROR three") {
		t.Errorf("text = %q %v", text.String(), err)
	}
	if l, err := ParseLevel("WARN"); err != nil || l != slog.LevelWarn {
		t.Errorf("parse = %v %v", l, err)
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("bad level accepted")
	}
}

func TestSubscriptionNeverBlocks(t *testing.T) {
	buf := NewBuffer(10)
	lv := new(slog.LevelVar)
	log := slog.New(NewHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: lv}), buf, lv))
	sub := buf.Subscribe(2)
	done := make(chan struct{})
	go func() {
		for range 100 {
			log.Info("x")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("logging blocked on a slow subscriber")
	}
	if sub.Dropped() != 98 || len(sub.C()) != 2 {
		t.Errorf("dropped %d, queued %d", sub.Dropped(), len(sub.C()))
	}
	sub.Close()
	sub.Close()
	log.Info("after close") // must not panic
}

func TestConcurrentLogging(t *testing.T) {
	buf := NewBuffer(50)
	lv := new(slog.LevelVar)
	log := slog.New(NewHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: lv}), buf, lv))
	sub := buf.Subscribe(1000)
	defer sub.Close()
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 100 {
				log.With("g", g).Info("msg", "i", i)
				_ = buf.Entries(Query{Text: "msg", Limit: 5})
			}
		})
	}
	wg.Wait()
	if got := buf.Entries(Query{}); len(got) != 50 || got[49].ID != 800 {
		t.Errorf("ring after concurrent logging: %d entries, last %d", len(got), got[len(got)-1].ID)
	}
}

func TestRotatingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "gonema.log")
	r, err := OpenRotatingFile(path, 1, 2) // rounded up to 1 MiB
	if err != nil {
		t.Fatal(err)
	}
	line := bytes.Repeat([]byte("x"), 400<<10)
	line[len(line)-1] = '\n'
	for range 10 { // 4 MB: several rotations
		if _, err := r.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{path, path + ".1", path + ".2"} {
		fi, err := os.Stat(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if fi.Size() > 1<<20 {
			t.Errorf("%s is %d bytes, over the limit", name, fi.Size())
		}
	}
	if _, err := os.Stat(path + ".3"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("more rotated files than kept: %v", err)
	}
	var all bytes.Buffer
	if err := r.WriteAll(&all); err != nil {
		t.Fatal(err)
	}
	if all.Len()%len(line) != 0 || all.Len() < 2*len(line) {
		t.Errorf("WriteAll gave %d bytes", all.Len())
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write(line); err == nil {
		t.Error("write after close")
	}
	if _, err := OpenRotatingFile("", 1, 1); err == nil {
		t.Error("empty path accepted")
	}
}

func TestTeeWriterKeepsGoing(t *testing.T) {
	var a, b bytes.Buffer
	n, err := TeeWriter{&a, failWriter{}, &b}.Write([]byte("hi"))
	if n != 2 || err == nil || a.String() != "hi" || b.String() != "hi" {
		t.Errorf("tee = %d %v %q %q", n, err, a.String(), b.String())
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func BenchmarkHandler(b *testing.B) {
	lv := new(slog.LevelVar)
	log := slog.New(NewHandler(slog.NewTextHandler(new(bytes.Buffer), &slog.HandlerOptions{Level: lv}), NewBuffer(0), lv))
	ctx := context.Background()
	for b.Loop() {
		log.LogAttrs(ctx, slog.LevelInfo, "job done", slog.Int64("job_id", 42), slog.String("kind", "rss.sync"), slog.Duration("elapsed", time.Second))
	}
}
