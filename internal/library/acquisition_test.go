package library

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

func TestSeedScoringRulesRoundTrip(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	rules, err := LoadScoringRules(ctx, e.st.Read())
	if err != nil {
		t.Fatal(err)
	}
	starter := quality.StarterRules()
	if len(rules) != len(starter) {
		t.Fatalf("%d rules loaded, want %d", len(rules), len(starter))
	}
	for i, r := range rules {
		want := starter[i]
		if r.Name() != want.Name() || r.Description() != want.Description() {
			t.Errorf("rule %d = %q %q, want %q %q", i, r.Name(), r.Description(), want.Name(), want.Description())
		}
		if d := cmp.Diff(want.Conditions(), r.Conditions()); d != "" {
			t.Errorf("rule %q conditions (-starter +db):\n%s", r.Name(), d)
		}
	}

	profiles, err := e.svc.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, row := range profiles {
		want, ok := quality.StarterProfile(row.Name)
		if !ok {
			continue // the "HD" fixture of storetest
		}
		seen++
		got, err := LoadQualityProfile(ctx, e.st.Read(), row.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d := cmp.Diff(want, got); d != "" {
			t.Errorf("profile %q (-starter +db):\n%s", row.Name, d)
		}
		if err := got.Validate(rules); err != nil {
			t.Errorf("profile %q: %v", row.Name, err)
		}
		// Same rules and profile: same score.
		p := parser.Parse("Movie.2020.2160p.UHD.BluRay.x265.DV.HDR10Plus-GRP")
		rel := &quality.Release{Title: "Movie.2020.2160p.UHD.BluRay.x265.DV.HDR10Plus-GRP", Parsed: &p, IndexerFlags: []string{"freeleech"}}
		if a, b := quality.Score(&want, starter, rel), quality.Score(&got, rules, rel); !cmp.Equal(a, b) {
			t.Errorf("profile %q: score %+v from starter, %+v from db", row.Name, a, b)
		}
	}
	if seen != len(quality.StarterProfiles()) {
		t.Errorf("%d starter profiles in db, want %d", seen, len(quality.StarterProfiles()))
	}

	// Seeding is done once: deleting every rule does not bring them back.
	if n, err := e.svc.SeedScoringRules(ctx); err != nil || n != 0 {
		t.Errorf("second seed = %d, %v", n, err)
	}
	if _, err := e.st.Write().DeleteSetting(ctx, settingScoringSeeded); err != nil {
		t.Fatal(err)
	}
	// Flag lost but rules present (database seeded by hand): nothing duplicated.
	if n, err := e.svc.SeedScoringRules(ctx); err != nil || n != 0 {
		t.Errorf("seed with existing rules = %d, %v", n, err)
	}
	if c, _ := e.st.Read().CountScoringRules(ctx); c != int64(len(starter)) {
		t.Errorf("%d rules after reseed, want %d", c, len(starter))
	}

	sizes, err := LoadQualitySizes(ctx, e.st.Read())
	if err != nil || !cmp.Equal(sizes, quality.DefaultSizes()) {
		t.Errorf("sizes = %v, %v", sizes, err)
	}
}

func TestMovieHistoryAddedDeleted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	w := events.NewHistoryWriter(e.bus, e.st, slog.New(slog.DiscardHandler))
	e.prov.set(released(550, "Fight Club", 1999))
	m := e.add(t, 550)
	if _, err := e.svc.DeleteMovie(ctx, m.ID, DeleteOptions{AddExclusion: true}); err != nil {
		t.Fatal(err)
	}
	e.bus.Close()
	if err := w.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := e.st.Read().ListMovieHistory(ctx, db.ListMovieHistoryParams{MovieID: m.ID, BeforeID: 1 << 62, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var evs []string
	for _, r := range rows {
		evs = append(evs, r.Event)
		if r.SourceTitle != "Fight Club" {
			t.Errorf("%s: source title %q", r.Event, r.SourceTitle)
		}
	}
	if !slices.Equal(evs, []string{"movie_deleted", "movie_added"}) {
		t.Errorf("history = %v", evs)
	}
}

// TestMatcherCoalescesBulkAdds adds 1,000 movies: the matcher index is not
// rebuilt per movie but once, at the first read, and that read sees them all.
func TestMatcherCoalescesBulkAdds(t *testing.T) {
	e := newEnv(t)
	e.svc.matcher.mu.Lock()
	e.svc.matcher.debounce = time.Hour // only the read rebuilds in this test
	e.svc.matcher.mu.Unlock()
	const n = 1000
	title := func(i int) string { // 1,280 distinct pronounceable words
		const c, v = "BCDFGHKLMNPRSTVZ", "aeiou"
		return fmt.Sprintf("Zor %c%c%cn", c[i%16], v[(i/16)%5], c[(i/80)%16]+'a'-'A')
	}
	for i := range n {
		e.prov.set(released(1000+i, title(i), 1990+i%30))
	}
	before := e.svc.matcher.rebuilds.Load()
	for i := range n {
		e.add(t, 1000+i)
	}
	if got := e.svc.matcher.rebuilds.Load() - before; got != 0 {
		t.Errorf("%d rebuilds during %d adds, want 0 (coalesced)", got, n)
	}
	last := n - 1
	name := fmt.Sprintf("%s.%d.1080p.BluRay.x264-GRP", title(last), 1990+last%30)
	cands := e.svc.MatchRelease(parser.Parse(name))
	if len(cands) == 0 || !cands[0].Confident() || cands[0].Title != title(last) {
		t.Fatalf("match %q = %+v", name, cands)
	}
	if got := e.svc.MatcherSize(); got != n {
		t.Errorf("matcher size %d, want %d", got, n)
	}
	if got := e.svc.matcher.rebuilds.Load() - before; got != 1 {
		t.Errorf("%d rebuilds after the first read, want 1", got)
	}

	// The debounced rebuild runs without any read.
	e.svc.matcher.mu.Lock()
	e.svc.matcher.debounce = time.Millisecond
	e.svc.matcher.mu.Unlock()
	e.prov.set(released(5000, "Quiet Harbor", 2001))
	e.add(t, 5000)
	deadline := time.Now().Add(5 * time.Second)
	for e.svc.matcher.stale.Load() {
		if time.Now().After(deadline) {
			t.Fatal("debounced rebuild did not run")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestLoadQualityProfileCutoffInGroup: a cutoff stored as a member of a group
// (accepted by movie_state) is loaded as stored; the engine resolves it to the
// group, with the same rank and label as before the stored value was kept.
func TestLoadQualityProfileCutoffInGroup(t *testing.T) {
	e := newEnv(t)
	p, err := LoadQualityProfile(context.Background(), e.st.Read(), e.lib.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Cutoff != "webdl-1080p" || len(p.Items) != 3 || !slices.Equal(p.Items[0].Qualities, []quality.ID{quality.Bluray1080p, quality.WebDL1080p}) {
		t.Errorf("profile = %+v", p)
	}
	if err := p.Validate(nil); err != nil {
		t.Errorf("validate: %v", err)
	}
	if p.CutoffRank() != 3 || p.CutoffLabel() != "HD-1080p" {
		t.Errorf("cutoff rank %d label %q, want the group's (3, HD-1080p)", p.CutoffRank(), p.CutoffLabel())
	}
	byGroup := p
	byGroup.Cutoff = "HD-1080p"
	if byGroup.CutoffRank() != p.CutoffRank() {
		t.Errorf("member cutoff rank %d differs from the group's %d", p.CutoffRank(), byGroup.CutoffRank())
	}
}
