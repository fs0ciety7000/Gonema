package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/acquire/acquiretest"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

func TestQualityProfileRoutes(t *testing.T) {
	e := newTestEnv(t, "")
	list := decode[[]QualityProfileDetail](t, e.do(t, "GET", p1+"/quality-profiles", ""))
	if len(list) < 2 {
		t.Fatalf("profiles = %d", len(list))
	}
	var hd QualityProfileDetail
	for _, p := range list {
		if p.ID == e.profID {
			hd = p
		}
	}
	if hd.Name != "HD" || len(hd.Entries) != 3 || hd.Entries[0].Group != "HD-1080p" || len(hd.Entries[0].Qualities) != 2 ||
		len(hd.Sizes) != 4 || hd.Sizes[0].Custom || hd.CutoffLabel != "HD-1080p" || len(hd.Scores) == 0 || len(hd.Items) != 4 {
		t.Fatalf("HD = %+v", hd)
	}
	dv := hd.Scores[1].RuleID // starter rules: HDR10+, Dolby Vision...

	body := fmt.Sprintf(`{"name":"Cinema","entries":[{"group":"UHD","qualities":["remux-2160p","bluray-2160p"],"allowed":true},`+
		`{"qualities":["webdl-1080p"],"allowed":true}],"cutoff":"UHD","upgradeAllowed":true,"minScore":0,"cutoffScore":120,`+
		`"minUpgradeStep":10,"preferredProtocol":"usenet","sizes":[{"quality":"webdl-1080p","minMbPerMin":5,"preferredMbPerMin":20,"maxMbPerMin":60}],`+
		`"scores":[{"ruleId":%d,"score":40}]}`, dv)
	r := e.do(t, "POST", p1+"/quality-profiles", body)
	saved := decode[QualityProfileSaved](t, r)
	p := saved.Profile
	if r.status != 201 || p.Name != "Cinema" || p.CutoffScore != 120 || p.PreferredProtocol != "usenet" || len(p.Entries) != 2 {
		t.Fatalf("create = %d %s", r.status, r.body)
	}
	if i := slices.IndexFunc(p.Sizes, func(s ProfileSize) bool { return s.Quality == "webdl-1080p" }); i < 0 || !p.Sizes[i].Custom || p.Sizes[i].MaxMbPerMin != 60 {
		t.Errorf("sizes = %+v", p.Sizes)
	}
	if i := slices.IndexFunc(p.Scores, func(s ProfileScore) bool { return s.RuleID == dv }); i < 0 || p.Scores[i].Score != 40 {
		t.Errorf("scores = %+v", p.Scores)
	}
	problemOK(t, e.do(t, "POST", p1+"/quality-profiles", body), 409)
	detail := problemOK(t, e.do(t, "POST", p1+"/quality-profiles", strings.Replace(body, `"cutoff":"UHD"`, `"cutoff":"dvd"`, 1)), 422)
	if !strings.Contains(detail, `cutoff "dvd"`) {
		t.Errorf("validation detail = %q", detail)
	}
	problemOK(t, e.do(t, "POST", p1+"/quality-profiles", strings.Replace(body, `"usenet"`, `"ftp"`, 1)), 422)

	// Replace without sizes nor scores: both kept.
	upd := `{"name":"Cinema UHD","entries":[{"qualities":["webdl-1080p"],"allowed":true}],"cutoff":"webdl-1080p",` +
		`"upgradeAllowed":false,"minScore":-5,"cutoffScore":0,"minUpgradeStep":1}`
	r = e.do(t, "PUT", fmt.Sprintf("%s/quality-profiles/%d", p1, p.ID), upd)
	saved = decode[QualityProfileSaved](t, r)
	if r.status != 200 || saved.Profile.Name != "Cinema UHD" || saved.Profile.UpgradeAllowed || !saved.Profile.Sizes[0].Custom || saved.Profile.PreferredProtocol != "" {
		t.Fatalf("update = %d %s", r.status, r.body)
	}
	if got := decode[QualityProfileDetail](t, e.do(t, "GET", fmt.Sprintf("%s/quality-profiles/%d", p1, p.ID), "")); got.MinScore != -5 {
		t.Errorf("get = %+v", got)
	}
	problemOK(t, e.do(t, "GET", p1+"/quality-profiles/99999", ""), 404)
	problemOK(t, e.do(t, "PUT", p1+"/quality-profiles/99999", upd), 404)

	// In use: 409; then deleted.
	e.setKey(t)
	m := e.addMovie(t, 7, "Alien", 1979, e.rootID)
	if r := e.do(t, "PATCH", fmt.Sprintf("%s/movies/%d", p1, m.ID), fmt.Sprintf(`{"qualityProfileId":%d}`, p.ID)); r.status != 200 {
		t.Fatalf("patch = %d %s", r.status, r.body)
	}
	if d := problemOK(t, e.do(t, "DELETE", fmt.Sprintf("%s/quality-profiles/%d", p1, p.ID), ""), 409); !strings.Contains(d, "used by 1 movies") {
		t.Errorf("in use detail = %q", d)
	}
	if r := e.do(t, "PATCH", fmt.Sprintf("%s/movies/%d", p1, m.ID), fmt.Sprintf(`{"qualityProfileId":%d}`, e.profID)); r.status != 200 {
		t.Fatal(r.body)
	}
	if r := e.do(t, "DELETE", fmt.Sprintf("%s/quality-profiles/%d", p1, p.ID), ""); r.status != 204 {
		t.Errorf("delete = %d %s", r.status, r.body)
	}

	q := decode[[]QualityDefinition](t, e.do(t, "GET", p1+"/qualities", ""))
	if len(q) != len(quality.All()) || q[0].ID != "unknown" || q[len(q)-1].ID != "brdisk-2160p" || q[len(q)-1].Sizes.MaxMbPerMin == 0 {
		t.Errorf("qualities = %+v", q[len(q)-1])
	}
}

func TestScoringRuleRoutes(t *testing.T) {
	e := newTestEnv(t, "")
	e.setKey(t)
	m := e.addMovie(t, 7, "Halcyon Drift", 2024, e.rootID)
	scene := "Halcyon.Drift.2024.2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.Atmos.7.1-BOBiNE"
	pr := parser.Parse(scene)
	if _, err := e.st.Write().UpsertMovieFile(context.Background(), db.UpsertMovieFileParams{
		MovieID: m.ID, RelativePath: "h.mkv", Size: 60 << 30, Quality: "remux-2160p", Revision: 1, Languages: "[]",
		SceneName: scene, Parsed: mustJSON(t, pr), MediaInfo: "{}", ImportedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}

	kinds := decode[[]ConditionKindInfo](t, e.do(t, "GET", p1+"/scoring-rules/kinds", ""))
	if len(kinds) != 13 || kinds[0].Kind != "release_group" || kinds[0].Values == nil {
		t.Fatalf("kinds = %+v", kinds)
	}
	rules := decode[[]ScoringRule](t, e.do(t, "GET", p1+"/scoring-rules", ""))
	if len(rules) != 8 || rules[0].ReadsAs == "" || len(rules[0].Scores) < 2 {
		t.Fatalf("rules = %+v", rules)
	}

	body := fmt.Sprintf(`{"name":"Lossless Atmos","conditions":[{"kind":"audio","value":"atmos","required":true},`+
		`{"kind":"release_group","value":"MiNiPiX","negate":true,"required":true}],"scores":[{"profileId":%d,"score":30}]}`, e.profID)
	r := e.do(t, "POST", p1+"/scoring-rules", body)
	saved := decode[ScoringRuleSaved](t, r)
	if r.status != 201 || saved.RescoredFiles != 1 || saved.Rule.ReadsAs != "the release has atmos audio and is not from MiNiPiX" {
		t.Fatalf("create = %d %s", r.status, r.body)
	}
	if i := slices.IndexFunc(saved.Rule.Scores, func(s ProfileRuleScore) bool { return s.ProfileID == e.profID }); i < 0 || saved.Rule.Scores[i].Score != 30 {
		t.Errorf("scores = %+v", saved.Rule.Scores)
	}
	problemOK(t, e.do(t, "POST", p1+"/scoring-rules", body), 409)
	d := problemOK(t, e.do(t, "POST", p1+"/scoring-rules", `{"name":"Bad","conditions":[{"kind":"title_regex","value":"(x"}]}`), 422)
	if !strings.Contains(d, "condition 1 (title_regex)") {
		t.Errorf("regex detail = %q", d)
	}
	problemOK(t, e.do(t, "POST", p1+"/scoring-rules", `{"name":"Bad","conditions":[{"kind":"shade","value":"red"}]}`), 422)

	id := saved.Rule.ID
	r = e.do(t, "PUT", fmt.Sprintf("%s/scoring-rules/%d", p1, id), `{"name":"Atmos","conditions":[{"kind":"audio","value":"atmos"}]}`)
	if got := decode[ScoringRuleSaved](t, r); r.status != 200 || got.Rule.Name != "Atmos" || len(got.Rule.Conditions) != 1 {
		t.Fatalf("update = %d %s", r.status, r.body)
	}
	if got := decode[ScoringRule](t, e.do(t, "GET", fmt.Sprintf("%s/scoring-rules/%d", p1, id), "")); got.Name != "Atmos" {
		t.Errorf("get = %+v", got)
	}
	r = e.do(t, "POST", p1+"/scoring-rules/matches", `{"conditions":[{"kind":"hdr","value":"dv"}]}`)
	if got := decode[RuleMatches](t, r); r.status != 200 || got.Count != 1 || len(got.Movies) != 1 || got.Movies[0].ID != m.ID {
		t.Errorf("matches = %d %s", r.status, r.body)
	}
	r = e.do(t, "DELETE", fmt.Sprintf("%s/scoring-rules/%d", p1, id), "")
	if r.status != 200 || !strings.Contains(r.body, `"rescoredFiles":1`) {
		t.Errorf("delete = %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "DELETE", fmt.Sprintf("%s/scoring-rules/%d", p1, id), ""), 404)
}

func TestCalendarDashboardAndSetupRoutes(t *testing.T) {
	e := newTestEnv(t, "")
	r := e.do(t, "GET", p1+"/onboarding", "")
	st := decode[SetupStatus](t, r)
	if r.status != 200 || st.Complete || st.Next != "tmdb" || len(st.Steps) != 7 {
		t.Fatalf("onboarding = %d %s", r.status, r.body)
	}
	e.setKey(t)
	now := time.Now()
	soon := metadata.Date{Year: now.Year(), Month: now.Month(), Day: now.Day()}.String()
	d, _ := metadata.ParseDate(soon)
	e.prov.movies[9] = &metadata.Movie{TMDBID: 9, Title: "Quiet Hours", Year: d.Year, ReleaseDate: d,
		Releases: metadata.ReleaseSelection{Digital: metadata.DatedRelease{Date: d, Region: "US"}}}
	if r := e.do(t, "POST", p1+"/movies", fmt.Sprintf(`{"tmdbId":9,"rootFolderId":%d,"qualityProfileId":%d}`, e.rootID, e.profID)); r.status != 201 {
		t.Fatalf("add = %d %s", r.status, r.body)
	}
	r = e.do(t, "GET", p1+"/calendar?start="+soon+"&end="+soon, "")
	cal := decode[CalendarPage](t, r)
	if r.status != 200 || len(cal.Events) != 1 || cal.Events[0].Type != "digital" || cal.Events[0].Movie.Title != "Quiet Hours" ||
		!cal.Events[0].Searchable || cal.Events[0].Note == "" {
		t.Fatalf("calendar = %d %s", r.status, r.body)
	}
	if r := e.do(t, "GET", p1+"/calendar", ""); r.status != 200 {
		t.Errorf("default range = %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "GET", p1+"/calendar?start=2026-11-10&end=2026-11-01", ""), 422)
	problemOK(t, e.do(t, "GET", p1+"/calendar?start=2026-11-10&end=2026-11-11&types=vhs", ""), 422)

	r = e.do(t, "GET", p1+"/dashboard", "")
	dash := decode[Dashboard](t, r)
	if r.status != 200 || dash.Library.Total != 1 || len(dash.RecentlyAdded) != 1 || len(dash.Disks) != 1 || dash.Disks[0].Movies != 1 ||
		dash.ThisMonth.Added != 1 || dash.LastRSSSync != nil || len(dash.Upcoming) != 1 || dash.Indexers == nil || dash.Health.Top == nil {
		t.Fatalf("dashboard = %d %s", r.status, r.body)
	}

	r = e.do(t, "GET", p1+"/settings/general", "")
	gs := decode[GeneralSettings](t, r)
	if r.status != 200 || gs.UI.Theme != "system" || gs.Server.Version != "test" {
		t.Fatalf("general = %d %s", r.status, r.body)
	}
	body := fmt.Sprintf(`{"theme":"dark","density":"compact","defaultQualityProfileId":%d,"defaultRootFolderId":%d,`+
		`"defaultMinimumAvailability":"released","calendarShowUnmonitored":true,"weekStartsOn":1}`, e.profID, e.rootID)
	if r := e.do(t, "PUT", p1+"/settings/ui", body); r.status != 200 || !strings.Contains(r.body, `"theme":"dark"`) {
		t.Fatalf("set ui = %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "PUT", p1+"/settings/ui", strings.Replace(body, `"dark"`, `"sepia"`, 1)), 422)
	problemOK(t, e.do(t, "PUT", p1+"/settings/ui", strings.Replace(body, fmt.Sprintf(`"defaultQualityProfileId":%d`, e.profID), `"defaultQualityProfileId":99999`, 1)), 422)
	r = e.do(t, "PUT", p1+"/onboarding", `{"dismissed":true}`)
	st = decode[SetupStatus](t, r)
	if r.status != 200 || !st.Dismissed || st.Next != "download_client" {
		t.Errorf("onboarding after = %d %s", r.status, r.body)
	}
}

func TestPeopleAndPaletteRoutes(t *testing.T) {
	e := newTestEnv(t, "")
	r := e.do(t, "GET", p1+"/search?q=fight&remote=true", "")
	pal := decode[PaletteResult](t, r)
	if r.status != 200 || len(pal.Films) != 0 || pal.Remote == nil || pal.Remote.Error != "No TMDB key is configured yet." {
		t.Fatalf("palette without key = %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "GET", p1+"/discover/people?query=tom", ""), 503)
	e.setKey(t)
	m := e.addMovie(t, 1, "Halcyon Drift", 2024, e.rootID)
	e.addMovie(t, 2, "The Halcyon Days", 2020, e.rootID)
	e.prov.persons = map[int]*metadata.PersonDetails{31: {TMDBID: 31, Name: "Tom Hanks", ProfilePath: "/tom.jpg", KnownForDepartment: "Acting",
		Birthday: metadata.Date{Year: 1956, Month: 7, Day: 9}}}
	e.prov.people = map[int]*metadata.Filmography{31: {PersonID: 31, Cast: []metadata.FilmCredit{{Movie: metadata.MovieSummary{TMDBID: 1, Title: "Halcyon Drift"}, Character: "Rook"}}}}

	r = e.do(t, "GET", p1+"/search?q=hal&limit=1", "")
	pal = decode[PaletteResult](t, r)
	if r.status != 200 || len(pal.Films) != 1 || pal.FilmsTotal != 2 || pal.Films[0].ID != m.ID || pal.Films[0].Match != "prefix" ||
		!slices.Equal(pal.Films[0].Ranges[0], []int{0, 3}) || pal.Remote != nil {
		t.Fatalf("palette = %d %s", r.status, r.body)
	}
	r = e.do(t, "GET", p1+"/search?q=hanks&remote=true", "")
	pal = decode[PaletteResult](t, r)
	if r.status != 200 || pal.Remote == nil || pal.Remote.Error != "" || len(pal.Remote.People) != 1 || pal.Remote.People[0].Name != "Tom Hanks" {
		t.Fatalf("palette remote = %d %s", r.status, r.body)
	}
	if r := e.do(t, "GET", p1+"/search?q=ha&remote=true", ""); decode[PaletteResult](t, r).Remote != nil {
		t.Errorf("remote below 3 characters: %s", r.body)
	}

	r = e.do(t, "GET", p1+"/discover/people?query=tom", "")
	pp := decode[PeoplePage](t, r)
	if r.status != 200 || len(pp.Results) != 1 || pp.Results[0].ProfileURL == nil || !strings.HasSuffix(*pp.Results[0].ProfileURL, "/images/profile/w185/tom.jpg") ||
		len(pp.Results[0].KnownFor) != 3 || !pp.Results[0].KnownFor[0].InLibrary || pp.Results[0].KnownFor[2].InLibrary {
		t.Fatalf("people = %d %s", r.status, r.body)
	}
	r = e.do(t, "GET", p1+"/discover/person/31", "")
	f := decode[Filmography](t, r)
	if r.status != 200 || f.Person == nil || f.Person.Name != "Tom Hanks" || f.Person.Birthday == nil || *f.Person.Birthday != "1956-07-09" || len(f.Movies) != 1 {
		t.Fatalf("person = %d %s", r.status, r.body)
	}
	e.prov.personsErr = metadata.ErrUnauthorized
	r = e.do(t, "GET", p1+"/discover/person/31", "")
	if f := decode[Filmography](t, r); r.status != 200 || f.Person != nil || len(f.Movies) != 1 {
		t.Errorf("person without profile = %d %s", r.status, r.body)
	}
}

func TestHistoryExport(t *testing.T) {
	e := newTestEnv(t, "")
	e.setKey(t)
	m := e.addMovie(t, 1, "Alien", 1979, e.rootID)
	ctx := context.Background()
	for i, ev := range []string{"grabbed", "imported", "grabbed"} {
		title := fmt.Sprintf("Alien.1979.Release.%d", i)
		if i == 2 {
			title = "=HYPERLINK(\"x\")"
		}
		if _, err := e.st.Write().AppendHistory(ctx, db.AppendHistoryParams{
			MovieID: &m.ID, Event: ev, SourceTitle: title, Data: `{"reason":"because"}`, CreatedAt: int64(1000 + i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	r := e.do(t, "GET", p1+"/history/export?event=grabbed", "")
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "text/csv") || !strings.Contains(r.header.Get("Content-Disposition"), ".csv") {
		t.Fatalf("csv = %d %v", r.status, r.header)
	}
	rows, err := csv.NewReader(strings.NewReader(r.body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0][0] != "id" || rows[1][2] != "grabbed" || rows[1][4] != "Alien" || rows[1][6] != `'=HYPERLINK("x")` || rows[2][11] != "because" {
		t.Fatalf("csv rows = %q", rows)
	}
	r = e.do(t, "GET", fmt.Sprintf("%s/history/export?format=json&movieId=%d", p1, m.ID), "")
	list := decode[[]HistoryRow](t, r)
	if r.status != 200 || r.header.Get("Content-Type") != "application/json" || len(list) < 3 || list[0].SourceTitle != `=HYPERLINK("x")` {
		t.Fatalf("json = %d %s", r.status, r.body)
	}
	r = e.do(t, "GET", p1+"/history/export?format=json&event=upgraded", "")
	if r.status != 200 || strings.TrimSpace(r.body) != "[]" {
		t.Errorf("empty json = %d %q", r.status, r.body)
	}
	problemOK(t, e.do(t, "GET", p1+"/history/export?since=2026-01-02T00:00:00Z&until=2026-01-01T00:00:00Z", ""), 422)
	problemOK(t, e.do(t, "GET", p1+"/history/export?format=xml", ""), 422)
}

func TestPreviewPauseAllAndRetryRoutes(t *testing.T) {
	e := newAcquireEnv(t)
	prof := e.profID

	// Preview without a movie, then against Fight Club (no file).
	title := "Fight.Club.1999.1080p.BluRay.x264-GRP"
	r := e.do(t, "POST", p1+"/quality/preview", fmt.Sprintf(`{"title":%q,"profileId":%d,"size":10737418240}`, title, prof))
	pv := decode[acquire.Preview](t, r)
	if r.status != 200 || pv.Quality != "bluray-1080p" || !pv.QualityAllowed || pv.Parsed.ReleaseGroup != "GRP" || pv.Decision != nil || pv.Profile != "HD" {
		t.Fatalf("preview = %d %s", r.status, r.body)
	}
	r = e.do(t, "POST", p1+"/quality/preview", fmt.Sprintf(`{"title":%q,"movieId":%d}`, "Fight.Club.1999.DVDRip.XviD-OLD", e.movieID))
	pv = decode[acquire.Preview](t, r)
	if r.status != 200 || pv.Decision == nil || pv.Decision.Accepted || len(pv.Decision.Rejections) == 0 || pv.ProfileID != prof {
		t.Fatalf("preview against movie = %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "POST", p1+"/quality/preview", `{"title":"x"}`), 422)
	problemOK(t, e.do(t, "POST", p1+"/quality/preview", `{"title":"x","profileId":99999}`), 404)

	// Nothing to pause yet.
	r = e.do(t, "POST", p1+"/queue/pause", "")
	if res := decode[acquire.PauseAllResult](t, r); r.status != 200 || !res.Paused || res.Changed != 0 || len(res.Clients) != 0 {
		t.Fatalf("pause empty = %d %s", r.status, r.body)
	}

	tr := acquiretest.NewTracker(t, acqTrackerKey, acquiretest.Item{Title: title, Size: 10 << 30, Seeders: 50})
	qb := acquiretest.NewQBittorrent(t, "admin", acqQBPass)
	ix := decode[acquire.Indexer](t, e.do(t, "POST", p1+"/indexers", fmt.Sprintf(`{"name":"Tracker","kind":"torznab","baseUrl":%q,"apiKey":%q}`, tr.Srv.URL, acqTrackerKey)))
	if r := e.do(t, "POST", p1+"/download-clients", fmt.Sprintf(`{"name":"qB","kind":"qbittorrent","host":"127.0.0.1","port":%d,"username":"admin","password":%q}`, qb.Port(), acqQBPass)); r.status != 201 {
		t.Fatalf("client = %d %s", r.status, r.body)
	}

	// The tracker fails: retry the indexer once it answers again.
	tr.SetFail(http.StatusInternalServerError)
	started := decode[SearchStarted](t, e.do(t, "POST", fmt.Sprintf("%s/movies/%d/search/interactive", p1, e.movieID), ""))
	var snap acquire.SearchSnapshot
	acquiretest.Wait(t, "search finished", func() bool {
		snap = decode[acquire.SearchSnapshot](t, e.do(t, "GET", p1+"/searches/"+started.SearchID, ""))
		return snap.Status == "finished"
	})
	if len(snap.Results) != 0 || snap.Indexers[0].Status != "failed" {
		t.Fatalf("failed search = %+v", snap)
	}
	tr.SetFail(0)
	retryURL := fmt.Sprintf("%s/searches/%s/indexers/%d/retry", p1, started.SearchID, ix.ID)
	r = e.do(t, "POST", retryURL, "")
	if got := decode[acquire.SearchSnapshot](t, r); r.status != 202 || got.Indexers[0].Status != "pending" {
		t.Fatalf("retry = %d %s", r.status, r.body)
	}
	acquiretest.Wait(t, "retry finished", func() bool {
		snap = decode[acquire.SearchSnapshot](t, e.do(t, "GET", p1+"/searches/"+started.SearchID, ""))
		return snap.Status == "finished" && snap.Indexers[0].Status != "pending"
	})
	if len(snap.Results) != 1 || snap.Indexers[0].Status != "done" || !snap.Results[0].Accepted {
		t.Fatalf("after retry = %+v", snap)
	}
	problemOK(t, e.do(t, "POST", fmt.Sprintf("%s/searches/%s/indexers/999/retry", p1, started.SearchID), ""), 404)
	problemOK(t, e.do(t, "POST", fmt.Sprintf("%s/searches/%s/indexers/%d/retry", p1, strings.Repeat("a", 32), ix.ID), ""), 404)

	// Grab it, then pause and resume everything.
	if r := e.do(t, "POST", p1+"/searches/"+started.SearchID+"/grab", fmt.Sprintf(`{"key":%q}`, snap.Results[0].Key)); r.status != 201 {
		t.Fatalf("grab = %d %s", r.status, r.body)
	}
	r = e.do(t, "POST", p1+"/queue/pause", "")
	res := decode[acquire.PauseAllResult](t, r)
	if r.status != 200 || res.Changed != 1 || len(res.Clients) != 1 || !res.Clients[0].Supported || res.Clients[0].Kind != "qbittorrent" {
		t.Fatalf("pause all = %d %s", r.status, r.body)
	}
	if !qb.Paused(qb.Torrents()[0].Hash) {
		t.Error("qBittorrent torrent not paused")
	}
	if res := decode[acquire.PauseAllResult](t, e.do(t, "POST", p1+"/queue/pause", "")); res.Changed != 0 || res.Skipped != 1 {
		t.Errorf("pause twice = %+v", res)
	}
	r = e.do(t, "POST", p1+"/queue/resume", "")
	if res := decode[acquire.PauseAllResult](t, r); r.status != 200 || res.Paused || res.Changed != 1 {
		t.Fatalf("resume all = %d %s", r.status, r.body)
	}
	if qb.Paused(qb.Torrents()[0].Hash) {
		t.Error("qBittorrent torrent still paused")
	}

	// The dashboard sees the indexer, the client and the download.
	dash := decode[Dashboard](t, e.do(t, "GET", p1+"/dashboard", ""))
	if len(dash.Indexers) != 1 || len(dash.Clients) != 1 || dash.Queue.Count != 1 || len(dash.Queue.Items) != 1 ||
		dash.Queue.Items[0].Title != "Fight Club" || len(dash.RecentActivity) == 0 {
		t.Errorf("dashboard = %+v", dash)
	}
	noLeak(t, "responses", fmt.Sprint(dash), tr)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCSVSafe(t *testing.T) {
	for in, want := range map[string]string{
		"":               "",
		"Fight Club":     "Fight Club",
		"-12":            "-12",
		"+3.5":           "+3.5",
		"=1+1":           "'=1+1",
		"+cmd":           "'+cmd",
		"-2+3":           "'-2+3",
		"@SUM(A1)":       "'@SUM(A1)",
		"\t=1":           "'\t=1",
		"\r=1":           "'\r=1",
		"\tplain":        "'\tplain",
		" =HYPERLINK(1)": "' =HYPERLINK(1)",
		"\x00\x1f=1":     "'\x00\x1f=1",
		" =1":            "' =1",
		"＝1+1":           "'＝1+1",
		"＋1":             "'＋1",
		"－1":             "'－1",
		"＠A1":            "'＠A1",
		"  Fight Club":   "  Fight Club",
		"a=b":            "a=b",
	} {
		if got := csvSafe([]string{in})[0]; got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}
