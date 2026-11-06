package library

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// insertMovies adds n movies in one transaction (fast setup for large indexes).
func insertMovies(tb testing.TB, e *env, from, n int) []int64 {
	tb.Helper()
	ctx := context.Background()
	ids := make([]int64, 0, n)
	err := e.st.WithTx(ctx, func(q *db.Queries) error {
		for i := from; i < from+n; i++ {
			title := "Index Movie " + strconv.Itoa(i)
			m, err := q.InsertMovie(ctx, db.InsertMovieParams{
				TmdbID: int64(100000 + i), Title: title, SortTitle: title, CleanTitle: parser.CleanTitle(title),
				Year: int64(1950 + i%70), Status: "released", Genres: `["Drama"]`, Ratings: "{}", Extra: "{}",
				Monitored: i%3 != 0, QualityProfileID: e.lib.ProfileID, MinimumAvailability: "released",
				RootFolderID: e.lib.RootFolderID, Path: title, AddedAt: store.Millis(testNow) + int64(i),
			})
			if err != nil {
				return err
			}
			ids = append(ids, m.ID)
		}
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	return ids
}

// checkIndexFresh compares the installed index with a full reload.
func checkIndexFresh(t *testing.T, e *env, step string) {
	t.Helper()
	ctx := context.Background()
	got, err := e.svc.index(ctx)
	if err != nil {
		t.Fatalf("%s: index: %v", step, err)
	}
	want, err := e.svc.loadIndex(ctx)
	if err != nil {
		t.Fatalf("%s: load: %v", step, err)
	}
	if got.version != want.version {
		t.Errorf("%s: version %d, want %d", step, got.version, want.version)
	}
	if d := cmp.Diff(want.rows, got.rows); d != "" {
		t.Errorf("%s: rows differ from a full reload (-want +got):\n%s", step, d)
	}
	norm := func(m map[int64][]int64) map[int64][]int64 {
		out := map[int64][]int64{}
		for k, v := range m {
			if len(v) > 0 {
				out[k] = v
			}
		}
		return out
	}
	if d := cmp.Diff(norm(want.tags), norm(got.tags)); d != "" {
		t.Errorf("%s: tags differ (-want +got):\n%s", step, d)
	}
}

func TestIndexDeltaMatchesFullReload(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ids := insertMovies(t, e, 0, 50)
	checkIndexFresh(t, e, "initial")
	first := e.svc.idx.Load()

	w := e.st.Write()
	steps := []struct {
		name string
		do   func() error
	}{
		{"monitor", func() error {
			_, err := w.SetMovieMonitored(ctx, db.SetMovieMonitoredParams{Monitored: false, ID: ids[1]})
			return err
		}},
		{"file", func() error {
			_, err := w.UpsertMovieFile(ctx, db.UpsertMovieFileParams{
				MovieID: ids[2], RelativePath: "a.mkv", Size: 1 << 30, Quality: "webdl-1080p", Revision: 1,
				Languages: "[]", Parsed: "{}", MediaInfo: `{"hdr":["dv"]}`, ImportedAt: store.Millis(testNow),
			})
			return err
		}},
		{"tag", func() error {
			tag, err := w.CreateTag(ctx, "kids")
			if err != nil {
				return err
			}
			if err := w.AddMovieTag(ctx, db.AddMovieTagParams{MovieID: ids[3], TagID: tag.ID}); err != nil {
				return err
			}
			return w.AddMovieTag(ctx, db.AddMovieTagParams{MovieID: ids[4], TagID: tag.ID})
		}},
		{"download failed", func() error {
			d, err := w.InsertDownload(ctx, db.InsertDownloadParams{
				MovieID: ids[5], ReleaseTitle: "Index.Movie.5.1080p.WEB-DL-GRP", Protocol: "usenet", Quality: "webdl-1080p",
				Revision: 1, Decision: "{}", GrabbedAt: store.Millis(testNow),
			})
			if err != nil {
				return err
			}
			_, err = w.UpdateDownloadState(ctx, db.UpdateDownloadStateParams{ID: d.ID, Status: "failed", StatusMessages: "[]"})
			return err
		}},
		{"delete", func() error { _, err := w.DeleteMovie(ctx, ids[6]); return err }},
		{"delete first and last", func() error {
			if _, err := w.DeleteMovie(ctx, ids[0]); err != nil {
				return err
			}
			_, err := w.DeleteMovie(ctx, ids[len(ids)-1])
			return err
		}},
		{"insert", func() error { insertMovies(t, e, 1000, 3); return nil }},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.name, store.MapError(err))
		}
		before := e.svc.idx.Load()
		next, err := e.svc.deltaIndex(ctx, before)
		if err != nil || next == nil {
			t.Fatalf("%s: no delta (%v): a per-movie change must not reload everything", s.name, err)
		}
		checkIndexFresh(t, e, s.name)
	}
	if first.rows[1].Monitored != (1%3 != 0) {
		t.Error("an earlier index was modified in place: pages already served would change")
	}
}

func TestIndexProfileChangeReloads(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	insertMovies(t, e, 0, 10)
	cur, err := e.svc.index(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.Write().InsertQualityProfileItem(ctx, db.InsertQualityProfileItemParams{
		ProfileID: e.lib.ProfileID, Quality: "brdisk-2160p", Position: 99, Allowed: true,
	}); err != nil {
		t.Fatal(store.MapError(err))
	}
	if next, err := e.svc.deltaIndex(ctx, cur); err != nil || next != nil {
		t.Fatalf("profile change applied as a delta (%v): every movie's state may change", err)
	}
	checkIndexFresh(t, e, "profile")
}

// TestIndexReadersDoNotWaitForRebuild holds the reload lock as if another
// reader were reloading: a reader needing a full reload must get the previous
// index at once, and library.reindexed is published after the reload.
func TestIndexReadersDoNotWaitForRebuild(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	insertMovies(t, e, 0, 10)
	cur, err := e.svc.index(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.Write().InsertQualityProfileItem(ctx, db.InsertQualityProfileItemParams{
		ProfileID: e.lib.ProfileID, Quality: "brdisk-2160p", Position: 99, Allowed: true,
	}); err != nil {
		t.Fatal(store.MapError(err))
	}
	sub := e.bus.Subscribe(16, func(ev *events.Event) bool { return ev.Type == EventLibraryReindexed })
	defer sub.Close()

	e.svc.rebuildMu.Lock()
	done := make(chan *libraryIndex, 1)
	go func() {
		x, err := e.svc.index(ctx)
		if err != nil {
			t.Error(err)
		}
		done <- x
	}()
	select {
	case x := <-done:
		if x != cur {
			t.Error("the reader did not get the previous index")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a reader waited for another reader's reload")
	}
	e.svc.rebuildMu.Unlock()

	if _, err := e.svc.index(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sub.C():
	case <-time.After(5 * time.Second):
		t.Error("no library.reindexed event after a reload that served stale reads")
	}
	checkIndexFresh(t, e, "after reload")
}

// TestIndexConcurrentReadersAndWriters runs readers and writers together; run
// with -race. Each reader must see its own writes (read-your-writes).
func TestIndexConcurrentReadersAndWriters(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ids := insertMovies(t, e, 0, 300)
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Go(func() {
			for i := range 25 {
				id := ids[(g*25+i)%len(ids)]
				if _, err := e.st.Write().SetMovieMonitored(ctx, db.SetMovieMonitoredParams{Monitored: i%2 == 0, ID: id}); err != nil {
					t.Error(err)
					return
				}
				page, err := e.svc.ListMovies(ctx, ListQuery{Sort: SortYearDesc, All: true})
				if err != nil {
					t.Error(err)
					return
				}
				for _, m := range page.Movies {
					if m.ID == id && m.Monitored != (i%2 == 0) {
						t.Errorf("movie %d: own write not visible", id)
					}
				}
				if _, err := e.svc.Stats(ctx); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	for range 2 {
		wg.Go(func() {
			for range 50 {
				if _, _, err := e.svc.SearchLibrary(ctx, "index", 5); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	checkIndexFresh(t, e, "after concurrent writes")
}

func TestIdList(t *testing.T) {
	var got []int64
	if err := json.Unmarshal([]byte(idList([]int64{1, 22, 333})), &got); err != nil || !cmp.Equal(got, []int64{1, 22, 333}) {
		t.Errorf("idList = %v %v", got, err)
	}
	if idList(nil) != "[]" {
		t.Error("empty list")
	}
}

// BenchmarkIndexRefresh measures how long a reader takes to see one change in
// a library of 10,000 movies: per-movie delta versus full reload.
func BenchmarkIndexRefresh(b *testing.B) {
	e := newEnv(b)
	ctx := context.Background()
	ids := insertMovies(b, e, 0, 10000)
	if _, err := e.svc.index(ctx); err != nil {
		b.Fatal(err)
	}
	b.Run("delta", func(b *testing.B) {
		i := 0
		for b.Loop() {
			i++
			if _, err := e.st.Write().SetMovieMonitored(ctx, db.SetMovieMonitoredParams{Monitored: i%2 == 0, ID: ids[i%len(ids)]}); err != nil {
				b.Fatal(err)
			}
			if _, err := e.svc.index(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("full", func(b *testing.B) {
		for b.Loop() {
			if _, err := e.svc.loadIndex(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
}
