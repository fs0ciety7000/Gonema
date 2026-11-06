// Package acquiretest provides HTTP fakes for acquisition tests: a Torznab
// tracker serving searches, RSS and .torrent files behind an API key, and a
// qBittorrent WebUI API. Both run on httptest servers and are safe for
// concurrent use.
package acquiretest

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/download"
)

// MakeTorrent returns a minimal single-file .torrent named name and its info hash.
func MakeTorrent(name string) (torrent []byte, infoHash string) {
	info := fmt.Sprintf("d6:lengthi1000e4:name%d:%s12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaae", len(name), name)
	b := []byte("d8:announce23:http://tracker/announce4:info" + info + "e")
	h, err := download.InfoHash(b)
	if err != nil {
		panic(err)
	}
	return b, h
}

// Item is one release of a fake tracker.
type Item struct {
	Title   string
	Size    int64
	Seeders int
	Pub     time.Time
	// GUIDIsLink makes the GUID the download link (some indexers do), to check
	// that GUIDs never leak.
	GUIDIsLink bool
	// MagnetOnly publishes a magnet link instead of a .torrent.
	MagnetOnly bool

	torrent []byte
	hash    string
}

// Hash returns the info hash of the item's torrent.
func (it *Item) Hash() string { return it.hash }

// Tracker is a fake Torznab indexer.
type Tracker struct {
	Srv    *httptest.Server
	APIKey string

	mu        sync.Mutex
	items     []*Item
	delay     time.Duration
	fail      int // HTTP status to answer with, 0 = normal
	searches  int
	downloads int
}

// NewTracker starts a tracker requiring apiKey.
func NewTracker(t testing.TB, apiKey string, items ...Item) *Tracker {
	t.Helper()
	tr := &Tracker{APIKey: apiKey}
	for i := range items {
		tr.Add(items[i])
	}
	tr.Srv = httptest.NewServer(tr)
	t.Cleanup(tr.Srv.Close)
	return tr
}

// Add publishes an item (newest first in the feed).
func (tr *Tracker) Add(it Item) *Item {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	c := it
	c.torrent, c.hash = MakeTorrent(c.Title)
	tr.items = append([]*Item{&c}, tr.items...)
	return &c
}

// Find returns the item with that title.
func (tr *Tracker) Find(title string) *Item {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for _, it := range tr.items {
		if it.Title == title {
			return it
		}
	}
	return nil
}

// SetDelay delays search answers (a slow indexer).
func (tr *Tracker) SetDelay(d time.Duration) {
	tr.mu.Lock()
	tr.delay = d
	tr.mu.Unlock()
}

// SetFail makes every answer an HTTP error with status (0 restores).
func (tr *Tracker) SetFail(status int) {
	tr.mu.Lock()
	tr.fail = status
	tr.mu.Unlock()
}

// Counts returns the number of searches and file downloads served.
func (tr *Tracker) Counts() (searches, downloads int) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.searches, tr.downloads
}

// DownloadURL returns the authenticated download link of an item.
func (tr *Tracker) DownloadURL(n int) string {
	return fmt.Sprintf("%s/dl/%d.torrent?apikey=%s", tr.Srv.URL, n, tr.APIKey)
}

const capsXML = `<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <server version="1.3" title="Fake Tracker"/>
  <limits default="100" max="100"/>
  <searching>
    <search available="yes" supportedParams="q"/>
    <movie-search available="yes" supportedParams="q,imdbid,tmdbid"/>
  </searching>
  <categories><category id="2000" name="Movies"><subcat id="2040" name="Movies/HD"/></category></categories>
</caps>`

func (tr *Tracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tr.mu.Lock()
	fail, delay := tr.fail, tr.delay
	tr.mu.Unlock()
	if fail != 0 {
		http.Error(w, "internal error", fail)
		return
	}
	if r.URL.Query().Get("apikey") != tr.APIKey {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0"?><error code="100" description="Incorrect user credentials"/>`)
		return
	}
	switch {
	case r.URL.Path == "/api" && r.URL.Query().Get("t") == "caps":
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, capsXML)
	case r.URL.Path == "/api":
		tr.mu.Lock()
		tr.searches++
		tr.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = io.WriteString(w, tr.feed())
	case strings.HasPrefix(r.URL.Path, "/dl/"):
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/dl/"), ".torrent"))
		tr.mu.Lock()
		var data []byte
		if err == nil && n >= 0 && n < len(tr.items) {
			data = tr.items[len(tr.items)-1-n].torrent
			tr.downloads++
		}
		tr.mu.Unlock()
		if data == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

// feed renders the items; item n (in insertion order) downloads at /dl/n.
func (tr *Tracker) feed() string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel><title>Fake</title>`)
	for i, it := range tr.items {
		n := len(tr.items) - 1 - i
		link := tr.DownloadURL(n)
		if it.MagnetOnly {
			link = "magnet:?xt=urn:btih:" + it.hash + "&dn=" + url.QueryEscape(it.Title)
		}
		guid := fmt.Sprintf("%s/torrents/%d", tr.Srv.URL, n)
		if it.GUIDIsLink {
			guid = link
		}
		pub := it.Pub
		if pub.IsZero() {
			pub = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute)
		}
		fmt.Fprintf(&b, `<item><title>%s</title><guid>%s</guid><comments>%s</comments><pubDate>%s</pubDate><size>%d</size>`,
			html.EscapeString(it.Title), html.EscapeString(guid),
			html.EscapeString(fmt.Sprintf("%s/details/%d?apikey=%s", tr.Srv.URL, n, tr.APIKey)),
			pub.Format(time.RFC1123Z), it.Size)
		fmt.Fprintf(&b, `<link>%s</link>`, html.EscapeString(link))
		if !it.MagnetOnly {
			fmt.Fprintf(&b, `<enclosure url="%s" length="%d" type="application/x-bittorrent"/>`, html.EscapeString(link), it.Size)
		}
		fmt.Fprintf(&b, `<torznab:attr name="category" value="2040"/><torznab:attr name="seeders" value="%d"/><torznab:attr name="peers" value="%d"/><torznab:attr name="infohash" value="%s"/></item>`,
			it.Seeders, it.Seeders+1, it.hash)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

// Torrent is a torrent known by the fake qBittorrent.
type Torrent struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	State       string  `json:"state"`
	Progress    float64 `json:"progress"`
	Size        int64   `json:"size"`
	AmountLeft  int64   `json:"amount_left"`
	DLSpeed     int64   `json:"dlspeed"`
	ETA         int64   `json:"eta"`
	SavePath    string  `json:"save_path"`
	ContentPath string  `json:"content_path"`
}

// QBittorrent is a fake qBittorrent WebUI API (v2.11).
type QBittorrent struct {
	Srv      *httptest.Server
	User     string
	Password string

	mu       sync.Mutex
	torrents map[string]*Torrent
	order    []string
	deleted  map[string]bool // hash -> deleteFiles
	paused   map[string]bool
}

// NewQBittorrent starts a fake qBittorrent.
func NewQBittorrent(t testing.TB, user, password string) *QBittorrent {
	t.Helper()
	q := &QBittorrent{User: user, Password: password, torrents: map[string]*Torrent{}, deleted: map[string]bool{}, paused: map[string]bool{}}
	q.Srv = httptest.NewServer(q)
	t.Cleanup(q.Srv.Close)
	return q
}

// Port returns the TCP port of the server.
func (q *QBittorrent) Port() int {
	u, _ := url.Parse(q.Srv.URL)
	p, _ := strconv.Atoi(u.Port())
	return p
}

// Torrents returns the hashes in the order they were added.
func (q *QBittorrent) Torrents() []Torrent {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []Torrent
	for _, h := range q.order {
		if t, ok := q.torrents[h]; ok {
			out = append(out, *t)
		}
	}
	return out
}

// Set changes the state of a torrent.
func (q *QBittorrent) Set(hash, state string, progress float64, contentPath string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if t, ok := q.torrents[hash]; ok {
		t.State, t.Progress, t.ContentPath = state, progress, contentPath
	}
}

// Drop removes a torrent behind Gonema's back.
func (q *QBittorrent) Drop(hash string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.torrents, hash)
}

// Deleted reports whether a torrent was deleted through the API, and with its files.
func (q *QBittorrent) Deleted(hash string) (deleted, withFiles bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	withFiles, deleted = q.deleted[hash]
	return deleted, withFiles
}

// Paused reports the last pause state set through the API.
func (q *QBittorrent) Paused(hash string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.paused[hash]
}

func (q *QBittorrent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, ok := strings.CutPrefix(r.URL.Path, "/api/v2/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if path == "auth/login" {
		_ = r.ParseForm()
		if r.PostForm.Get("username") != q.User || r.PostForm.Get("password") != q.Password {
			_, _ = io.WriteString(w, "Fails.")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session-" + q.User})
		_, _ = io.WriteString(w, "Ok.")
		return
	}
	if ck, err := r.Cookie("SID"); err != nil || ck.Value != "session-"+q.User {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	switch path {
	case "app/webapiVersion":
		_, _ = io.WriteString(w, "2.11.2")
	case "torrents/add":
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var hash, name string
		if f, _, err := r.FormFile("torrents"); err == nil {
			data, _ := io.ReadAll(f)
			_ = f.Close()
			hash, _ = download.InfoHash(data)
			name = "file"
		} else if u := r.FormValue("urls"); u != "" {
			hash, _ = download.MagnetInfoHash(u)
			name = "magnet"
		}
		if hash == "" {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		if _, dup := q.torrents[hash]; dup {
			_, _ = io.WriteString(w, "Fails.")
			return
		}
		q.torrents[hash] = &Torrent{Hash: hash, Name: name, Category: r.FormValue("category"), State: "downloading", Size: 1000, AmountLeft: 1000, DLSpeed: 1 << 20, ETA: 60}
		q.order = append(q.order, hash)
		_, _ = io.WriteString(w, "Ok.")
	case "torrents/info":
		cat, hashes := r.URL.Query().Get("category"), r.URL.Query().Get("hashes")
		out := []Torrent{}
		for _, h := range q.order {
			t, ok := q.torrents[h]
			if !ok || (cat != "" && t.Category != cat) || (hashes != "" && !strings.EqualFold(hashes, h)) {
				continue
			}
			out = append(out, *t)
		}
		_ = json.NewEncoder(w).Encode(out)
	case "torrents/delete":
		_ = r.ParseForm()
		h := r.PostForm.Get("hashes")
		q.deleted[h] = r.PostForm.Get("deleteFiles") == "true"
		delete(q.torrents, h)
	case "torrents/stop", "torrents/start":
		_ = r.ParseForm()
		h := r.PostForm.Get("hashes")
		q.paused[h] = path == "torrents/stop"
		if t, ok := q.torrents[h]; ok {
			t.State = map[bool]string{true: "stoppedDL", false: "downloading"}[q.paused[h]]
		}
	default:
		http.NotFound(w, r)
	}
}

// Wait polls cond every millisecond until it holds or the deadline passes.
func Wait(t testing.TB, what string, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !cond() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-tick.C:
		}
	}
}
