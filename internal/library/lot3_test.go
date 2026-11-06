package library

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// addFile gives a movie a file named scene.
func (e *env) addFile(t *testing.T, movieID int64, scene string, size int64) {
	t.Helper()
	p := parser.Parse(scene)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Write().UpsertMovieFile(context.Background(), db.UpsertMovieFileParams{
		MovieID: movieID, RelativePath: scene + ".mkv", Size: size, Quality: string(quality.FromParsed(&p)), Revision: 1,
		Languages: "[]", SceneName: scene, Parsed: string(raw), MediaInfo: "{}", ImportedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
}

func ruleID(t *testing.T, e *env, name string) int64 {
	t.Helper()
	rules, err := e.svc.ListScoringRules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if r.Name == name {
			return r.ID
		}
	}
	t.Fatalf("no rule %q", name)
	return 0
}

func validProfile(name string) ProfileInput {
	return ProfileInput{
		Name: name, UpgradeAllowed: true, Cutoff: "HD", MinUpgradeStep: 1,
		Items: []quality.Item{
			{Name: "HD", Qualities: []quality.ID{quality.Bluray1080p, quality.WebDL1080p}, Allowed: true},
			{Qualities: []quality.ID{quality.WebDL720p}, Allowed: true},
		},
	}
}

func TestQualityProfileValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tests := []struct {
		name    string
		edit    func(p *ProfileInput)
		wantErr string
	}{
		{"empty name", func(p *ProfileInput) { p.Name = "  " }, "name is empty"},
		{"cutoff not in profile", func(p *ProfileInput) { p.Cutoff = "remux-2160p" }, `cutoff "remux-2160p" is not a quality or group`},
		{"duplicate quality and bad language", func(p *ProfileInput) {
			p.Items = append(p.Items, quality.Item{Qualities: []quality.ID{quality.WebDL720p}, Allowed: true})
			p.Languages = []string{"elvish"}
		}, `appears twice`},
		{"protocol", func(p *ProfileInput) { p.PreferredProtocol = "carrier-pigeon" }, "preferred protocol"},
		{"sizes", func(p *ProfileInput) {
			p.Sizes = quality.Sizes{quality.WebDL720p: {MinMBPerMin: 10, PreferredMBPerMin: 5}}
		}, "below the minimum"},
		{"unknown rule score", func(p *ProfileInput) { p.Scores = map[int64]int{99999: 10} }, "scoring rule 99999 does not exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validProfile("P " + tt.name)
			tt.edit(&in)
			_, _, err := e.svc.CreateQualityProfile(ctx, in)
			var ie *InputError
			if !errors.Is(err, ErrInvalid) || !errors.As(err, &ie) || !strings.Contains(ie.Msg, tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if strings.Contains(ie.Msg, "quality: invalid") {
				t.Errorf("message keeps the package prefix: %q", ie.Msg)
			}
		})
	}
	// Every problem at once.
	in := validProfile("Many")
	in.Cutoff, in.Languages = "dvd", []string{"en", "en"}
	_, _, err := e.svc.CreateQualityProfile(ctx, in)
	if err == nil || !strings.Contains(err.Error(), "cutoff") || !strings.Contains(err.Error(), "listed twice") {
		t.Errorf("joined errors = %v", err)
	}
	if _, _, err := e.svc.CreateQualityProfile(ctx, validProfile("HD")); !errors.Is(err, store.ErrConflict) || !errors.Is(err, ErrInvalid) {
		t.Errorf("duplicate name = %v", err)
	}
}

func TestQualityProfileCRUDAndRescore(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	hdr := ruleID(t, e, quality.RuleDolbyVision)
	x265 := ruleID(t, e, quality.RuleX265HD)

	in := validProfile("Cinema")
	in.PreferredProtocol = ProtocolTorrent
	in.Sizes = quality.Sizes{quality.WebDL720p: {MinMBPerMin: 1, PreferredMBPerMin: 2, MaxMBPerMin: 3}}
	in.Scores = map[int64]int{hdr: 40, x265: 0}
	in.Languages = []string{"original"}
	p, _, err := e.svc.CreateQualityProfile(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if p.PreferredProtocol != "torrent" || len(p.Sizes) != 1 || p.Scores[hdr] != 40 || len(p.Scores) != 1 ||
		len(p.Profile.Items) != 2 || p.Profile.Items[0].Name != "HD" || p.Usage.Movies != 0 {
		t.Fatalf("created = %+v", p)
	}
	sizes, proto, err := LoadProfileOverrides(ctx, e.st.Read(), p.ID)
	if err != nil || proto != "torrent" || sizes[quality.WebDL720p].MaxMBPerMin != 3 {
		t.Errorf("overrides = %v %q %v", sizes, proto, err)
	}
	eff := EffectiveSizes(quality.DefaultSizes(), sizes)
	if eff[quality.WebDL720p].MaxMBPerMin != 3 || eff[quality.Bluray1080p] != quality.DefaultSizes()[quality.Bluray1080p] {
		t.Errorf("effective sizes = %v", eff[quality.WebDL720p])
	}

	// A movie of the profile with a DV file stored with a stale score.
	e.prov.set(released(1, "Alien", 1979))
	m := e.add(t, 1)
	cin := p.ID
	if _, err := e.svc.UpdateMovie(ctx, m.ID, UpdateOptions{QualityProfileID: &cin}); err != nil {
		t.Fatal(err)
	}
	e.addFile(t, m.ID, "Alien.1979.1080p.WEB-DL.DV.H265-GRP", 4<<30)

	// Update: scores kept when absent; rescoring brings the file to 40.
	upd := validProfile("Cinema UHD")
	upd.MinScore = -10
	got, res, err := e.svc.UpdateQualityProfile(ctx, p.ID, upd)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Cinema UHD" || got.Scores[hdr] != 40 || len(got.Sizes) != 1 || got.PreferredProtocol != "" || res.RescoredFiles != 1 {
		t.Fatalf("updated = %+v %+v", got, res)
	}
	f, _ := e.st.Read().GetCurrentMovieFile(ctx, m.ID)
	if f.CustomScore != 40 {
		t.Errorf("file score = %d, want 40", f.CustomScore)
	}
	// Scores replaced: rules left out score 0; the file follows.
	upd.Scores = map[int64]int{x265: -5}
	upd.Sizes = quality.Sizes{}
	got, res, err = e.svc.UpdateQualityProfile(ctx, p.ID, upd)
	if err != nil || got.Scores[hdr] != 0 || got.Scores[x265] != -5 || len(got.Sizes) != 0 || res.RescoredFiles != 1 {
		t.Fatalf("replaced scores = %+v %+v %v", got.Scores, res, err)
	}
	if f, _ := e.st.Read().GetCurrentMovieFile(ctx, m.ID); f.CustomScore != -5 {
		t.Errorf("file score = %d, want -5", f.CustomScore)
	}

	// Delete: in use, then free.
	if err := e.svc.DeleteQualityProfile(ctx, p.ID); !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "used by 1 movies") {
		t.Errorf("delete in use = %v", err)
	}
	hd := e.lib.ProfileID
	if _, err := e.svc.UpdateMovie(ctx, m.ID, UpdateOptions{QualityProfileID: &hd}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteQualityProfile(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetQualityProfile(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get deleted = %v", err)
	}
	if _, _, err := e.svc.UpdateQualityProfile(ctx, p.ID, upd); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update deleted = %v", err)
	}
	list, err := e.svc.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range list {
		if q.ID == hd && q.Usage.Movies != 1 {
			t.Errorf("HD usage = %+v", q.Usage)
		}
		if q.ID != hd && q.Usage.Movies == 0 {
			if err := e.svc.DeleteQualityProfile(ctx, q.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	// HD is used; even unused, the last profile cannot go.
	if _, err := e.svc.DeleteMovie(ctx, m.ID, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteQualityProfile(ctx, hd); !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "last quality profile") {
		t.Errorf("delete last = %v", err)
	}
}

func TestScoringRuleCRUD(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.prov.set(released(1, "Alien", 1979))
	e.prov.set(released(2, "Heat", 1995))
	a, h := e.add(t, 1), e.add(t, 2)
	e.addFile(t, a.ID, "Alien.1979.2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.Atmos.7.1-BOBiNE", 60<<30)
	e.addFile(t, h.ID, "Heat.1995.1080p.BluRay.x264-MiNiPiX", 10<<30)
	prof := e.lib.ProfileID

	bad := []struct {
		name    string
		in      ScoringRuleInput
		wantErr string
	}{
		{"regex", ScoringRuleInput{Name: "R", Conditions: []quality.Condition{{Kind: quality.KindTitleRegex, Value: "(x"}}}, "condition 1 (title_regex): invalid regular expression"},
		{"no condition", ScoringRuleInput{Name: "R"}, "has no condition"},
		{"empty name", ScoringRuleInput{Name: " ", Conditions: []quality.Condition{{Kind: quality.KindCodec, Value: "h265"}}}, "name is empty"},
		{"unknown profile", ScoringRuleInput{Name: "R", Conditions: []quality.Condition{{Kind: quality.KindCodec, Value: "h265"}}, Scores: map[int64]int{9999: 1}}, "quality profile 9999 does not exist"},
		{"duplicate", ScoringRuleInput{Name: quality.RuleDolbyVision, Conditions: []quality.Condition{{Kind: quality.KindCodec, Value: "h265"}}}, "already exists"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := e.svc.CreateScoringRule(ctx, tt.in)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}

	in := ScoringRuleInput{
		Name: "Lossless Atmos", Description: "TrueHD Atmos", IncludeInRename: true,
		Conditions: []quality.Condition{
			{Kind: quality.KindAudio, Value: "atmos", Required: true},
			{Kind: quality.KindReleaseGroup, Value: "MiNiPiX", Negate: true, Required: true},
			{Kind: quality.KindIndexer, Value: "Nebula", Negate: true},
		},
		Scores: map[int64]int{prof: 30},
	}
	r, res, err := e.svc.CreateScoringRule(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "Lossless Atmos" || !r.IncludeInRename || len(r.Conditions) != 3 || r.Scores[prof] != 30 || res.RescoredFiles != 1 {
		t.Fatalf("created = %+v %+v", r, res)
	}
	if f, _ := e.st.Read().GetCurrentMovieFile(ctx, a.ID); f.CustomScore != 30 {
		t.Errorf("Alien score = %d", f.CustomScore)
	}
	n, ids, err := e.svc.RuleMatches(ctx, in.Conditions, 10)
	if err != nil || n != 1 || !slices.Equal(ids, []int64{a.ID}) {
		t.Errorf("matches = %d %v %v", n, ids, err)
	}
	n, ids, err = e.svc.RuleMatches(ctx, []quality.Condition{{Kind: quality.KindSource, Value: "bluray"}}, 1)
	if err != nil || n != 2 || len(ids) != 1 {
		t.Errorf("limited matches = %d %v %v", n, ids, err)
	}
	if _, _, err := e.svc.RuleMatches(ctx, []quality.Condition{{Kind: "nope", Value: "x"}}, 1); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad draft = %v", err)
	}

	// Update: scores absent keep; rename; conditions replaced.
	in.Name, in.Scores = "Atmos", nil
	in.Conditions = in.Conditions[:1]
	r, _, err = e.svc.UpdateScoringRule(ctx, r.ID, in)
	if err != nil || r.Name != "Atmos" || len(r.Conditions) != 1 || r.Scores[prof] != 30 {
		t.Fatalf("updated = %+v %v", r, err)
	}
	got, err := e.svc.GetScoringRule(ctx, r.ID)
	if err != nil || got.Name != "Atmos" {
		t.Errorf("get = %+v %v", got, err)
	}
	if _, _, err := e.svc.UpdateScoringRule(ctx, 99999, in); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update missing = %v", err)
	}
	res, err = e.svc.DeleteScoringRule(ctx, r.ID)
	if err != nil || res.RescoredFiles != 1 {
		t.Errorf("delete = %+v %v", res, err)
	}
	if f, _ := e.st.Read().GetCurrentMovieFile(ctx, a.ID); f.CustomScore != 0 {
		t.Errorf("Alien score after delete = %d", f.CustomScore)
	}
	if _, err := e.svc.DeleteScoringRule(ctx, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete twice = %v", err)
	}
	if _, err := e.svc.GetScoringRule(ctx, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get deleted = %v", err)
	}
}

func TestCalendar(t *testing.T) {
	e := newEnv(t) // today is 2026-11-05
	ctx := context.Background()
	mk := func(id int, title, status string, cinema, digital, physical string) *metadata.Movie {
		m := released(id, title, 2026)
		m.Status = metadata.Status(status)
		m.Releases = metadata.ReleaseSelection{}
		for _, d := range []struct {
			s   string
			dst *metadata.DatedRelease
		}{{cinema, &m.Releases.Cinema}, {digital, &m.Releases.Digital}, {physical, &m.Releases.Physical}} {
			if d.s != "" {
				dt, err := metadata.ParseDate(d.s)
				if err != nil {
					t.Fatal(err)
				}
				*d.dst = metadata.DatedRelease{Date: dt, Region: "US"}
			}
		}
		return m
	}
	e.prov.set(mk(1, "Quiet Hours", "in_cinemas", "2026-10-30", "2026-11-07", "2026-12-01"))
	e.prov.set(mk(2, "Iron Meadow", "announced", "2026-11-20", "", ""))
	e.prov.set(mk(3, "Orbit", "released", "2026-06-01", "2026-09-01", "2026-11-03"))
	quiet, iron, orbit := e.add(t, 1), e.add(t, 2), e.add(t, 3)
	off := false
	if _, err := e.svc.UpdateMovie(ctx, iron.ID, UpdateOptions{Monitored: &off}); err != nil {
		t.Fatal(err)
	}
	e.addFile(t, orbit.ID, "Orbit.2026.1080p.WEB-DL.H264-GRP", 4<<30)

	evs, err := e.svc.Calendar(ctx, CalendarQuery{Start: "2026-10-26", End: "2026-12-06"})
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ date, typ, title, note string }
	var got []row
	for _, ev := range evs {
		got = append(got, row{ev.Date, ev.Type, ev.Movie.Title, ev.Note})
	}
	want := []row{
		{"2026-10-30", "cinema", "Quiet Hours", "Gonema waits for the digital or physical release"},
		{"2026-11-03", "physical", "Orbit", "Already have WEBDL-1080p"},
		{"2026-11-07", "digital", "Quiet Hours", "Will search on release day"},
		{"2026-12-01", "physical", "Quiet Hours", "Will search on release day"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("monitored events:\n got %v\nwant %v", got, want)
	}
	if evs[0].Searchable || !evs[2].Searchable || evs[2].Movie.ID != quiet.ID {
		t.Errorf("searchable = %v %v", evs[0].Searchable, evs[2].Searchable)
	}

	all, err := e.svc.Calendar(ctx, CalendarQuery{Start: "2026-11-01", End: "2026-11-30", Unmonitored: true, Types: []string{ReleaseCinema}})
	if err != nil || len(all) != 1 || all[0].Movie.ID != iron.ID || all[0].Note != "Announced; not monitored" {
		t.Errorf("unmonitored cinema = %+v %v", all, err)
	}
	up, err := e.svc.Upcoming(ctx, 60, 1)
	if err != nil || len(up) != 1 || up[0].Date != "2026-11-07" {
		t.Errorf("upcoming = %+v %v", up, err)
	}

	bad := []CalendarQuery{
		{Start: "2026-13-01", End: "2026-12-01"},
		{Start: "2026-11-01", End: "x"},
		{Start: "2026-11-02", End: "2026-11-01"},
		{Start: "2026-01-01", End: "2027-06-01"},
		{Start: "2026-11-01", End: "2026-11-02", Types: []string{"vhs"}},
	}
	for _, q := range bad {
		if _, err := e.svc.Calendar(ctx, q); !errors.Is(err, ErrInvalid) {
			t.Errorf("Calendar(%+v) = %v, want invalid", q, err)
		}
	}
}

func TestSearchLibrary(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	titles := map[int]string{1: "Halcyon Drift", 2: "Drift: Signal", 3: "The Halcyon Days", 4: "Amélie", 5: "1917", 6: "Hal and the Cyon"}
	for id, title := range titles {
		m := released(id, title, 2000+id)
		if id == 4 {
			m.OriginalTitle = "Le Fabuleux Destin d'Amélie Poulain"
		}
		e.prov.set(m)
		e.add(t, id)
	}
	tests := []struct {
		q          string
		want       []string
		firstKind  string
		firstRange [][2]int
	}{
		{"hal", []string{"Hal and the Cyon", "Halcyon Drift", "The Halcyon Days"}, MatchPrefix, [][2]int{{0, 3}}},
		{"drift", []string{"Drift: Signal", "Halcyon Drift"}, MatchPrefix, [][2]int{{0, 5}}},
		{"cyon hal", []string{"Hal and the Cyon", "Halcyon Drift", "The Halcyon Days"}, MatchWords, [][2]int{{0, 3}, {12, 16}}},
		{"amelie", []string{"Amélie"}, MatchPrefix, [][2]int{{0, 6}}},
		{"fabuleux", []string{"Amélie"}, MatchWord, [][2]int{{3, 11}}},
		{"hcd", []string{"Halcyon Drift", "The Halcyon Days"}, MatchFuzzy, [][2]int{{0, 1}, {3, 4}, {8, 9}}},
		{"1917", []string{"1917"}, MatchPrefix, [][2]int{{0, 4}}},
		{"drift 2001", []string{"Halcyon Drift"}, MatchWord, [][2]int{{8, 13}}},
		{"2002", []string{"Drift: Signal"}, MatchSubstring, [][2]int{}},
		{"zebra crossing", nil, "", nil},
		{"  ", nil, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.q, func(t *testing.T) {
			hits, total, err := e.svc.SearchLibrary(ctx, tt.q, 10)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, h := range hits {
				got = append(got, h.Movie.Title)
			}
			if !slices.Equal(got, tt.want) || total != len(tt.want) {
				t.Fatalf("hits = %v (%d), want %v", got, total, tt.want)
			}
			if len(hits) > 0 && (hits[0].Kind != tt.firstKind || !slices.Equal(hits[0].Ranges, tt.firstRange)) {
				t.Errorf("first = %s %v (%s), want %s %v", hits[0].Kind, hits[0].Ranges, hits[0].Field, tt.firstKind, tt.firstRange)
			}
		})
	}
	hits, total, err := e.svc.SearchLibrary(ctx, "hal", 1)
	if err != nil || len(hits) != 1 || total != 3 {
		t.Errorf("limit = %d of %d, %v", len(hits), total, err)
	}
	if _, _, err := e.svc.SearchLibrary(ctx, "x", 0); !errors.Is(err, ErrInvalid) {
		t.Errorf("limit 0 = %v", err)
	}
	if _, _, err := e.svc.SearchLibrary(ctx, strings.Repeat("x", 201), 5); !errors.Is(err, ErrInvalid) {
		t.Errorf("long query = %v", err)
	}
}

func BenchmarkSearchLibrary(b *testing.B) {
	e := newEnv(b)
	ctx := context.Background()
	for i := 1; i <= 2000; i++ {
		e.prov.set(released(i, "Movie Number "+itoa7(i)+" Of The Great Drift", 1990+i%30))
		if _, _, err := e.svc.AddMovie(ctx, i, AddOptions{RootFolderID: e.lib.RootFolderID, QualityProfileID: e.lib.ProfileID, Monitored: true}); err != nil {
			b.Fatal(err)
		}
	}
	if _, _, err := e.svc.SearchLibrary(ctx, "drift", 8); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, _, err := e.svc.SearchLibrary(ctx, "grt drft", 8); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSetupStatusAndPreferences(t *testing.T) {
	e := newEnv(t) // TMDB key and root folder set; starter profiles seeded
	ctx := context.Background()
	st, err := e.svc.SetupStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	done := map[string]bool{}
	for _, s := range st.Steps {
		done[s.ID] = s.Done
	}
	if st.Complete || st.Next != StepDownloadClient || !done[StepTMDB] || !done[StepRootFolder] || done[StepQualityProfile] ||
		done[StepFirstMovie] || st.Steps[0].ID != StepAccount || st.Steps[0].Available {
		t.Fatalf("status = %+v", st)
	}
	if err := e.svc.SetSetupDismissed(ctx, true); err != nil {
		t.Fatal(err)
	}

	p, err := e.svc.UIPreferences(ctx)
	if err != nil || p != DefaultUIPreferences() {
		t.Fatalf("defaults = %+v %v", p, err)
	}
	prof := e.lib.ProfileID
	p.DefaultQualityProfileID, p.Theme, p.WeekStartsOn = &prof, "dark", 0
	if _, err := e.svc.SetUIPreferences(ctx, p); err != nil {
		t.Fatal(err)
	}
	st, _ = e.svc.SetupStatus(ctx)
	if !st.Dismissed || !slices.ContainsFunc(st.Steps, func(s SetupStep) bool { return s.ID == StepQualityProfile && s.Done }) {
		t.Errorf("after preferences = %+v", st)
	}
	got, _ := e.svc.UIPreferences(ctx)
	if got.Theme != "dark" || got.DefaultQualityProfileID == nil || *got.DefaultQualityProfileID != prof || got.WeekStartsOn != 0 {
		t.Errorf("stored = %+v", got)
	}
	bad := 999999
	for _, edit := range []func(p *UIPreferences){
		func(p *UIPreferences) { p.Theme = "sepia" },
		func(p *UIPreferences) { p.Density = "huge" },
		func(p *UIPreferences) { p.DefaultMinimumAvailability = "soon" },
		func(p *UIPreferences) { p.WeekStartsOn = 3 },
		func(p *UIPreferences) { id := int64(bad); p.DefaultQualityProfileID = &id },
		func(p *UIPreferences) { id := int64(bad); p.DefaultRootFolderID = &id },
	} {
		q := DefaultUIPreferences()
		edit(&q)
		if _, err := e.svc.SetUIPreferences(ctx, q); !errors.Is(err, ErrInvalid) {
			t.Errorf("SetUIPreferences(%+v) = %v", q, err)
		}
	}
}

func TestSearchPeopleAndOverview(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.prov.set(released(1, "Alien", 1979))
	a := e.add(t, 1)
	e.prov.persons[10] = &metadata.PersonDetails{TMDBID: 10, Name: "Sigourney Weaver", KnownForDepartment: "Acting"}
	e.prov.persons[11] = &metadata.PersonDetails{TMDBID: 11, Name: "Weaver Adult", Adult: true}
	e.prov.knownFor[10] = []metadata.MovieSummary{{TMDBID: 1, Title: "Alien"}, {TMDBID: 2, Title: "Aliens"}, {TMDBID: 3, Title: "X", Adult: true}}
	p, err := e.svc.SearchPeople(ctx, "weaver", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Results) != 1 || p.Results[0].Name != "Sigourney Weaver" || len(p.Results[0].KnownFor) != 2 ||
		p.Results[0].KnownFor[0].MovieID != a.ID || p.Results[0].KnownFor[1].MovieID != 0 {
		t.Fatalf("people = %+v", p)
	}
	for _, q := range []string{"", strings.Repeat("x", 201)} {
		if _, err := e.svc.SearchPeople(ctx, q, 1); !errors.Is(err, ErrInvalid) {
			t.Errorf("SearchPeople(%q) = %v", q, err)
		}
	}
	d, err := e.svc.Person(ctx, 10)
	if err != nil || d.Name != "Sigourney Weaver" {
		t.Errorf("person = %+v %v", d, err)
	}
	if _, err := e.svc.Person(ctx, 404); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown person = %v", err)
	}

	e.addFile(t, a.ID, "Alien.1979.1080p.BluRay.x264-GRP", 8<<30)
	ov, err := e.svc.Overview(ctx, testNow.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	u := ov.Folders[e.lib.RootFolderID]
	if ov.AddedSince != 1 || u.Movies != 1 || u.Files != 1 || u.SizeOnDisk != 8<<30 {
		t.Errorf("overview = %+v", ov)
	}
	for _, ev := range []string{"imported", "imported", "upgraded", "grabbed"} {
		if _, err := e.st.Write().AppendHistory(ctx, db.AppendHistoryParams{Event: ev, Data: "{}", CreatedAt: store.Millis(testNow)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.st.Write().AppendHistory(ctx, db.AppendHistoryParams{Event: "upgraded", Data: "{}", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	counts, err := e.svc.HistoryCounts(ctx, testNow.Add(-time.Hour), []string{"imported", "upgraded"})
	if err != nil || len(counts) != 2 || counts["imported"] != 2 || counts["upgraded"] != 1 {
		t.Errorf("history counts = %v %v", counts, err)
	}
}
