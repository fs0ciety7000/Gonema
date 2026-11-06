package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/metadata"
)

func dateOf(tm time.Time) metadata.Date { return metadata.DateOf(tm) }

// TestCollectionsRoutes: GET /collections from the index and the parts
// snapshot, GET /collections/{id} with every film and its state.
func TestCollectionsRoutes(t *testing.T) {
	e := newTestEnv(t, "")
	if r := e.do(t, "PUT", p1+"/settings/tmdb", `{"key":"`+testKey+`"}`); r.status != 200 {
		t.Fatalf("tmdb key = %d", r.status)
	}
	ref := &metadata.CollectionRef{TMDBID: 10, Name: "Fight Saga", PosterPath: "/saga.jpg", BackdropPath: "/back.jpg"}
	e.prov.movies[550].Collection = ref
	soon := dateOf(time.Now().AddDate(0, 1, 0))
	e.prov.collections = map[int]*metadata.Collection{10: {TMDBID: 10, Name: "Fight Saga", Parts: []metadata.MovieSummary{
		{TMDBID: 550, Title: "Fight Club", Year: 1999, ReleaseDate: metadata.Date{Year: 1999, Month: 10, Day: 15}},
		{TMDBID: 551, Title: "Fight Club 2", Year: 2001, ReleaseDate: metadata.Date{Year: 2001, Month: 1, Day: 1}, PosterPath: "/two.jpg"},
		{TMDBID: 552, Title: "Fight Club 3", Year: soon.Year, ReleaseDate: soon},
	}}}
	if r := e.do(t, "POST", p1+"/movies", fmt.Sprintf(`{"tmdbId":550,"rootFolderId":%d,"qualityProfileId":%d}`, e.rootID, e.profID)); r.status != 201 {
		t.Fatalf("add = %d %s", r.status, r.body)
	}
	list := decode[CollectionList](t, e.do(t, "GET", p1+"/collections", ""))
	if list.Total != 1 || list.Totals.Collections != 1 || list.Collections[0].PartsKnown || len(list.Collections[0].Films) != 1 {
		t.Fatalf("list before the TMDB read = %+v", list)
	}
	id := list.Collections[0].ID
	d := decode[CollectionDetail](t, e.do(t, "GET", fmt.Sprintf("%s/collections/%d", p1, id), ""))
	if d.Name != "Fight Saga" || !d.PartsKnown || len(d.Films) != 3 || d.Counts.Missing != 1 || d.Counts.Upcoming != 1 ||
		d.Counts.InLibrary != 1 || d.BackdropURL == nil || !strings.HasSuffix(*d.BackdropURL, "/images/backdrop/w1280/back.jpg") {
		t.Fatalf("detail = %+v", d)
	}
	f := d.Films
	if f[0].State != "wanted" || f[0].MovieID == nil || f[0].Monitored == nil || f[1].State != "missing" || f[1].InLibrary ||
		f[1].PosterURL == nil || f[2].State != "not_released" || f[2].NextRelease == nil || f[2].NextRelease.Date != soon.String() {
		t.Errorf("films = %+v", f)
	}
	list = decode[CollectionList](t, e.do(t, "GET", p1+"/collections?missing=true&sort=missing&q=fight", ""))
	if list.Total != 1 || !list.Collections[0].HasMissing || list.Totals.NotInLibrary != 2 || list.Totals.Upcoming != 1 || list.Totals.WithMissing != 1 {
		t.Errorf("filtered list = %+v", list)
	}
	if l := decode[CollectionList](t, e.do(t, "GET", p1+"/collections?monitored=true", "")); l.Total != 0 || l.Totals.Collections != 1 {
		t.Errorf("monitored list = %+v", l)
	}
	problemOK(t, e.do(t, "GET", p1+"/collections?sort=nope", ""), 422)
	problemOK(t, e.do(t, "GET", p1+"/collections/999", ""), 404)
	// Monitoring adds the released film now, the upcoming one later.
	r := e.do(t, "PUT", fmt.Sprintf("%s/collections/%d/monitor", p1, id), fmt.Sprintf(`{"monitored":true,"qualityProfileId":%d,"rootFolderId":%d,"searchOnAdd":false}`, e.profID, e.rootID))
	if r.status != 200 || !strings.Contains(r.body, `"upcoming":[{"tmdbId":552`) {
		t.Errorf("monitor = %d %s", r.status, r.body)
	}
}

// TestCalendarFeedRoutes: the read-only key in the query string opens the iCal
// feed and nothing else; regenerate and disable.
func TestCalendarFeedRoutes(t *testing.T) {
	e := newTestEnv(t, "")
	if r := e.do(t, "PUT", p1+"/settings/tmdb", `{"key":"`+testKey+`"}`); r.status != 200 {
		t.Fatal(r.status)
	}
	soon := dateOf(time.Now().AddDate(0, 0, 10))
	e.prov.movies[600] = &metadata.Movie{TMDBID: 600, Title: "Soon; Later, Now", Year: soon.Year, ReleaseDate: soon,
		Releases: metadata.ReleaseSelection{Cinema: metadata.DatedRelease{Date: soon, Region: "US"}}}
	if r := e.do(t, "POST", p1+"/movies", fmt.Sprintf(`{"tmdbId":600,"rootFolderId":%d,"qualityProfileId":%d}`, e.rootID, e.profID)); r.status != 201 {
		t.Fatalf("add = %d %s", r.status, r.body)
	}
	v := decode[CalendarFeedView](t, e.do(t, "GET", p1+"/settings/calendar-feed", ""))
	if v.Enabled || v.Key != nil || v.FutureDays != 180 {
		t.Fatalf("initial = %+v", v)
	}
	anon := &browser{t: t, base: e.ts.URL}
	problemOK(t, anon.do("GET", p1+"/calendar.ics", ""), 401)
	problemOK(t, anon.do("GET", p1+"/settings/calendar-feed", ""), 401)

	v = decode[CalendarFeedView](t, e.do(t, "POST", p1+"/settings/calendar-feed/regenerate", ""))
	if !v.Enabled || v.Key == nil || v.URL == nil || !strings.HasPrefix(*v.URL, e.ts.URL+p1+"/calendar.ics?key=gnc_") || v.Last4 == "" {
		t.Fatalf("regenerate = %+v", v)
	}
	key := *v.Key
	if again := decode[CalendarFeedView](t, e.do(t, "GET", p1+"/settings/calendar-feed", "")); again.Key != nil || again.URL != nil || !again.Enabled {
		t.Errorf("the key is shown again: %+v", again)
	}
	r := anon.do("GET", p1+"/calendar.ics?key="+url.QueryEscape(key), "")
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "text/calendar") {
		t.Fatalf("feed = %d %s", r.status, r.body)
	}
	for _, want := range []string{"BEGIN:VCALENDAR\r\n", `SUMMARY:Soon\; Later\, Now (`, "DTSTART;VALUE=DATE:" + strings.ReplaceAll(soon.String(), "-", ""), "UID:gonema-movie-", "END:VCALENDAR\r\n"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("feed lacks %q:\n%s", want, r.body)
		}
	}
	// The calendar key is not an API key, and the API key does not open the feed.
	problemOK(t, anon.do("GET", p1+"/movies", "", APIKeyHeader, key), 401)
	problemOK(t, anon.do("GET", p1+"/calendar.ics?key="+e.apiKey, ""), 401)
	// Options.
	r = e.do(t, "PUT", p1+"/settings/calendar-feed", `{"pastDays":0,"futureDays":5,"unmonitored":false,"releaseTypes":["digital"]}`)
	if v := decode[CalendarFeedView](t, r); r.status != 200 || v.FutureDays != 5 || !v.Enabled {
		t.Fatalf("options = %d %s", r.status, r.body)
	}
	if r := anon.do("GET", p1+"/calendar.ics?key="+url.QueryEscape(key), ""); strings.Contains(r.body, "BEGIN:VEVENT") {
		t.Errorf("event outside the window or type:\n%s", r.body)
	}
	problemOK(t, e.do(t, "PUT", p1+"/settings/calendar-feed", `{"pastDays":0,"futureDays":5,"unmonitored":false,"releaseTypes":["tv"]}`), 422)
	// Disable needs confirm.
	problemOK(t, e.do(t, "DELETE", p1+"/settings/calendar-feed", ""), 422)
	if r := e.do(t, "DELETE", p1+"/settings/calendar-feed?confirm=true", ""); r.status != 204 {
		t.Fatalf("disable = %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "DELETE", p1+"/settings/calendar-feed?confirm=true", ""), 404)
	problemOK(t, anon.do("GET", p1+"/calendar.ics?key="+url.QueryEscape(key), ""), 401)
	// Wrong keys are limited per address.
	limited := false
	for range 12 {
		if r := anon.do("GET", p1+"/calendar.ics?key=gnc_wrong", ""); r.status == 429 {
			limited = r.header.Get("Retry-After") != ""
			break
		}
	}
	if !limited {
		t.Error("wrong keys are not rate limited")
	}
}

func TestWindowLimiter(t *testing.T) {
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	l := newWindowLimiter(time.Minute, 2, 2)
	l.now = func() time.Time { return now }
	first, second, third := l.count("a"), l.count("a"), l.count("a")
	if first != 0 || second != 0 || third == 0 {
		t.Errorf("counts = %v %v %v: the third event in the window must wait", first, second, third)
	}
	if l.over("a") == 0 || l.over("b") != 0 {
		t.Error("over")
	}
	now = now.Add(time.Minute)
	if l.count("a") != 0 {
		t.Error("new window still limited")
	}
	l.count("b")
	l.count("c") // map full: cleared, bounded memory
	if len(l.m) > 2 {
		t.Errorf("map grew to %d", len(l.m))
	}
}

// TestV11Routes: stop retrying, bulk search, backups and sessions.
func TestV11Routes(t *testing.T) {
	e := newAcquireEnv(t)
	problemOK(t, e.do(t, "POST", p1+"/queue/999/stop-retrying", ""), 404)
	problemOK(t, e.do(t, "POST", p1+"/queue/999/resume-retrying", ""), 404)
	problemOK(t, e.do(t, "POST", p1+"/movies/search", `{}`), 422)
	problemOK(t, e.do(t, "POST", p1+"/movies/search", `{"ids":[1],"filter":"missing_released"}`), 422)
	problemOK(t, e.do(t, "POST", p1+"/movies/search", `{"filter":"nope"}`), 422)
	// This server has no job queue for the acquisition service: refused clearly.
	if d := problemOK(t, e.do(t, "POST", p1+"/movies/search", `{"filter":"missing_released"}`), 422); !strings.Contains(d, "not available") {
		t.Errorf("bulk without jobs = %q", d)
	}
	problemOK(t, e.do(t, "GET", p1+"/bulk-searches/999", ""), 404)
	problemOK(t, e.do(t, "DELETE", p1+"/bulk-searches/999", ""), 404)

	r := e.do(t, "GET", p1+"/settings/security", "")
	var sec SecuritySettings
	if err := json.Unmarshal([]byte(r.body), &sec); err != nil || r.status != 200 {
		t.Fatalf("security = %d %s", r.status, r.body)
	}
	for _, s := range sec.Sessions {
		if s.Via != "direct" && s.Via != "local" && s.Via != "proxy" {
			t.Errorf("session via = %q", s.Via)
		}
	}
	problemOK(t, e.do(t, "GET", p1+"/library-import/folders?rootFolderId=999", ""), 404)
	problemOK(t, e.do(t, "GET", p1+"/library-import/scans/999", ""), 404)
}
