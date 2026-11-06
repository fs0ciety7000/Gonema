// Package demo creates a demo database of fictional movies, to try Gonema or
// measure the interface with a large library without real data.
//
// A demo database is flagged with the "demo" setting: the API reports it
// (system/status demo: true) and the UI shows a permanent banner. Seeding
// refuses any data directory whose database is not a demo database, and never
// writes outside the data directory. Images (gradients with the title) are
// rendered locally; nothing goes to the network.
package demo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/match"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Errors, matched with errors.Is.
var (
	// ErrNotDemo: the data directory holds a database that is not a demo
	// database. Seeding it would mix fictional movies with a real library.
	ErrNotDemo = errors.New("demo: the data directory holds a real (non-demo) database")
	// ErrAlreadySeeded: the demo database already has movies.
	ErrAlreadySeeded = errors.New("demo: the demo database already has movies")
)

// MaxFilms bounds the size of a demo library.
const MaxFilms = 100_000

// DBFile is the database file name in a data directory (as the server uses).
const DBFile = "gonema.db"

// Options configures Seed.
type Options struct {
	DataDir   string // required; created if needed
	Films     int    // 1 to MaxFilms
	Seed      uint64 // random seed: the same seed gives the same library
	Now       time.Time
	Prerender bool // render the w342 posters now (other sizes render on first request)
	Logger    *slog.Logger
}

// Result reports what Seed created.
type Result struct {
	Films       int
	WithFile    int
	Downloading int
	Failed      int
	Posters     int
	DBPath      string
	Elapsed     time.Duration
}

// CheckDataDir returns nil when dir has no database or an empty demo
// database. It opens an existing database read-only, so that a real database
// is never migrated or modified by the demo command.
func CheckDataDir(ctx context.Context, dir string) error {
	p := filepath.Join(dir, DBFile)
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("demo: %w", err)
	}
	if strings.ContainsAny(p, "?#") {
		return fmt.Errorf("demo: unsupported database path %q", p)
	}
	conn, err := sql.Open("sqlite", "file:"+p+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("demo: open %s: %w", p, err)
	}
	defer conn.Close()
	var value string
	err = conn.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, library.SettingDemo).Scan(&value)
	if err != nil || value != "true" {
		// No settings table, no flag or false: not ours to touch.
		return fmt.Errorf("%w: %s", ErrNotDemo, p)
	}
	var n int64
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM movies`).Scan(&n); err != nil {
		return fmt.Errorf("demo: count movies: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("%w (%d movies): use a new data directory", ErrAlreadySeeded, n)
	}
	return nil
}

// noProvider is the metadata provider factory of the demo: none.
func noProvider(string) (library.KeyedProvider, error) {
	return nil, errors.New("demo: no metadata provider")
}

// Seed creates a demo database in opts.DataDir.
func Seed(ctx context.Context, opts Options) (Result, error) {
	start := time.Now()
	if opts.DataDir == "" {
		return Result{}, errors.New("demo: a data directory is required")
	}
	if opts.Films < 1 || opts.Films > MaxFilms {
		return Result{}, fmt.Errorf("demo: films must be between 1 and %d", MaxFilms)
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	dir, err := filepath.Abs(opts.DataDir)
	if err != nil {
		return Result{}, fmt.Errorf("demo: data directory: %w", err)
	}
	if err := CheckDataDir(ctx, dir); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Result{}, fmt.Errorf("demo: create %s: %w", dir, err)
	}
	res := Result{DBPath: filepath.Join(dir, DBFile)}
	st, err := store.Open(ctx, res.DBPath)
	if err != nil {
		return Result{}, err
	}
	defer st.Close()
	// Flag first: even a partial seed is recognizable as demo data.
	if err := store.SetSetting(ctx, st, library.SettingDemo, true); err != nil {
		return Result{}, fmt.Errorf("demo: flag the database: %w", err)
	}

	key, err := secretbox.NewKey()
	if err != nil {
		return Result{}, fmt.Errorf("demo: %w", err)
	}
	box, err := secretbox.New(key) // only used to build the library service: no secret is stored
	if err != nil {
		return Result{}, fmt.Errorf("demo: %w", err)
	}
	lib, err := library.New(library.Options{Store: st, Box: box, NewProvider: noProvider, Logger: opts.Logger})
	if err != nil {
		return Result{}, err
	}
	if _, err := lib.SeedQualityProfiles(ctx); err != nil {
		return Result{}, err
	}
	if _, err := lib.SeedScoringRules(ctx); err != nil {
		return Result{}, err
	}

	g := &generator{r: rand.New(rand.NewPCG(opts.Seed, opts.Seed^0x9e3779b97f4a7c15)), now: opts.Now, paths: map[string]bool{}}
	if err := g.setup(ctx, st, dir); err != nil {
		return Result{}, err
	}
	const batch = 1000
	for done := 0; done < opts.Films; done += batch {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		n := min(batch, opts.Films-done)
		err := st.WithTx(ctx, func(q *db.Queries) error {
			for i := range n {
				if err := g.movie(ctx, q, done+i+1, &res); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return Result{}, fmt.Errorf("demo: insert movies: %w", err)
		}
	}
	res.Films = opts.Films
	if opts.Prerender {
		cache, err := imagecache.New(filepath.Join(dir, "cache"), noBase{}, imagecache.Options{})
		if err != nil {
			return Result{}, err
		}
		if res.Posters, err = prerender(ctx, cache, g.arts); err != nil {
			return Result{}, err
		}
	}
	res.Elapsed = time.Since(start)
	return res, nil
}

// noBase refuses network access: demo images are never downloaded.
type noBase struct{}

func (noBase) ImageBaseURL(context.Context) (string, error) {
	return "", errors.New("demo: images are rendered locally, never downloaded")
}

type posterArt struct {
	path string
	art  Art
}

type generator struct {
	r           *rand.Rand
	now         time.Time
	profiles    []int64
	roots       []int64
	tags        []db.Tag
	collections []int64
	clientID    int64
	paths       map[string]bool
	arts        []posterArt
}

func (g *generator) setup(ctx context.Context, st *store.Store, dir string) error {
	profiles, err := st.Read().ListQualityProfiles(ctx)
	if err != nil {
		return fmt.Errorf("demo: quality profiles: %w", err)
	}
	if len(profiles) == 0 {
		return errors.New("demo: no quality profile was seeded")
	}
	for _, p := range profiles[:min(2, len(profiles))] {
		g.profiles = append(g.profiles, p.ID)
	}
	return st.WithTx(ctx, func(q *db.Queries) error {
		for _, name := range []string{"movies", "movies-uhd"} {
			p := filepath.Join(dir, "demo-media", name)
			if err := os.MkdirAll(p, 0o750); err != nil {
				return fmt.Errorf("create %s: %w", p, err)
			}
			rf, err := q.CreateRootFolder(ctx, p)
			if err != nil {
				return fmt.Errorf("root folder: %w", err)
			}
			g.roots = append(g.roots, rf.ID)
		}
		for _, label := range []string{"demo", "favorites", "4k-hdr", "family-room", "watch-soon"} {
			t, err := q.CreateTag(ctx, label)
			if err != nil {
				return fmt.Errorf("tag: %w", err)
			}
			g.tags = append(g.tags, t)
		}
		// A disabled client: the tracker leaves the demo downloads alone.
		c, err := q.CreateDownloadClient(ctx, db.CreateDownloadClientParams{
			Name: "Demo client (disabled)", Kind: "sabnzbd", Protocol: "usenet", Host: "demo.invalid", Port: 8080,
			Category: "movies", Priority: 1, Enabled: false, RemoveFailed: true, Tags: "[]",
		})
		if err != nil {
			return fmt.Errorf("download client: %w", err)
		}
		g.clientID = c.ID
		refreshed := store.Millis(g.now)
		for i := range 40 {
			c, err := q.UpsertCollection(ctx, db.UpsertCollectionParams{
				TmdbID: int64(800_000_000 + i + 1), Name: "The " + places[i%len(places)] + " " + []string{"Saga", "Cycle", "Trilogy"}[i%3],
				Overview: "A fictional collection of the demo library.", RefreshedAt: &refreshed,
			})
			if err != nil {
				return fmt.Errorf("collection: %w", err)
			}
			g.collections = append(g.collections, c.ID)
		}
		return nil
	})
}

func pick[T any](r *rand.Rand, list []T) T { return list[r.IntN(len(list))] }

func (g *generator) title() string {
	r := g.r
	var t string
	switch r.IntN(7) {
	case 0, 1:
		t = "The " + pick(r, adjectives) + " " + pick(r, nouns)
	case 2:
		t = pick(r, adjectives) + " " + pick(r, nouns)
	case 3:
		t = pick(r, nouns) + " of " + pick(r, places)
	case 4:
		t = pick(r, places)
	case 5:
		t = "The " + pick(r, nouns)
	default:
		t = pick(r, nouns) + ": " + pick(r, adjectives) + " " + pick(r, nouns)
	}
	if r.IntN(20) == 0 {
		t += " " + []string{"II", "III", "Part Two"}[r.IntN(3)]
	}
	return t
}

func dateOf(t time.Time) *string {
	s := t.Format(time.DateOnly)
	return &s
}

// releaseTemplates are release name suffixes with their weight; the parser
// derives the quality, codecs and HDR from them, as for a real import.
var releaseTemplates = []struct {
	suffix string
	weight int
	mbMin  float64
}{
	{"2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.Atmos.7.1.HEVC", 5, 420},
	{"2160p.BluRay.x265.10bit.HDR10.DTS-HD.MA.5.1", 6, 240},
	{"2160p.AMZN.WEB-DL.DDP5.1.Atmos.DV.HDR10.H.265", 10, 120},
	{"2160p.WEB-DL.DDP5.1.HDR10Plus.H.265", 4, 110},
	{"1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1", 5, 200},
	{"1080p.BluRay.x264.DTS.5.1", 24, 80},
	{"1080p.NF.WEB-DL.DDP5.1.H.264", 26, 45},
	{"1080p.WEBRip.x264.AAC", 6, 30},
	{"720p.BluRay.x264.AC3", 8, 25},
	{"720p.HDTV.x264.AAC", 3, 18},
	{"DVDRip.XviD.MP3", 3, 10},
}

var templateTotal = func() int {
	n := 0
	for _, t := range releaseTemplates {
		n += t.weight
	}
	return n
}()

func (g *generator) template() (suffix string, mbPerMin float64) {
	n := g.r.IntN(templateTotal)
	for _, t := range releaseTemplates {
		if n < t.weight {
			return t.suffix, t.mbMin
		}
		n -= t.weight
	}
	return releaseTemplates[0].suffix, releaseTemplates[0].mbMin
}

func dotted(title string) string {
	var b strings.Builder
	for _, w := range strings.Fields(title) {
		w = strings.Trim(w, ":'")
		if w == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(w)
	}
	return b.String()
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil { // plain values: cannot happen
		panic(err)
	}
	return string(b)
}

func (g *generator) person() metadata.Person {
	return metadata.Person{TMDBID: 700_000_000 + g.r.IntN(1_000_000), Name: pick(g.r, firstNames) + " " + pick(g.r, lastNames)}
}

func (g *generator) movie(ctx context.Context, q *db.Queries, i int, res *Result) error {
	r, nowYear := g.r, g.now.Year()
	title := g.title()
	year := nowYear + 1 - int(r.ExpFloat64()*14)
	year = max(1950, min(year, nowYear+2))
	cinema := time.Date(year, time.Month(1+r.IntN(12)), 1+r.IntN(28), 0, 0, 0, 0, time.UTC)
	digital, physical := cinema.AddDate(0, 0, 60+r.IntN(60)), cinema.AddDate(0, 0, 100+r.IntN(90))
	status, reason := string(metadata.StatusReleased), "Released digitally on "+digital.Format("2 January 2006")
	switch {
	case cinema.After(g.now):
		status, reason = string(metadata.StatusAnnounced), "Announced for "+cinema.Format("2 January 2006")
	case digital.After(g.now) && physical.After(g.now):
		status, reason = string(metadata.StatusInCinemas), "In cinemas since "+cinema.Format("2 January 2006")
	}
	genres := []string{pick(r, genresList)}
	for range r.IntN(3) {
		if gnr := pick(r, genresList); !contains(genres, gnr) {
			genres = append(genres, gnr)
		}
	}
	hue := r.Float64() * 360
	color := DominantColor(hue)
	poster, backdrop := fmt.Sprintf("/demo-p-%06d.jpg", i), fmt.Sprintf("/demo-b-%06d.jpg", i)
	cast := make([]metadata.Credit, 0, 8)
	for o := range 8 {
		cast = append(cast, metadata.Credit{Person: g.person(), Character: pick(r, firstNames) + " " + pick(r, lastNames), Order: o})
	}
	crew := []metadata.Credit{
		{Person: g.person(), Job: "Director", Department: "Directing"},
		{Person: g.person(), Job: "Screenplay", Department: "Writing"},
	}
	extra := mustJSON(map[string]any{
		"tagline": "A fictional film of the demo library.", "cast": cast, "crew": crew,
		"keywords": []string{pick(r, keywordsList), pick(r, keywordsList)}, "statusReason": reason,
	})
	ratings := mustJSON(metadata.Ratings{TMDB: &metadata.Rating{Value: math.Round((4.5+r.Float64()*4.3)*10) / 10, Votes: 50 + r.IntN(30000)}})
	added := g.now.Add(-time.Duration(r.Int64N(int64(3 * 365 * 24 * time.Hour))))
	refreshed := store.Millis(g.now)
	monitored := r.IntN(100) >= 8
	root := g.roots[0]
	if r.IntN(100) < 15 {
		root = g.roots[1]
	}
	folder := fmt.Sprintf("%s (%d)", strings.NewReplacer(":", "", "'", "").Replace(title), year)
	for n := 2; g.paths[fmt.Sprint(root, folder)]; n++ {
		folder = fmt.Sprintf("%s (%d) [%d]", strings.NewReplacer(":", "", "'", "").Replace(title), year, n)
	}
	g.paths[fmt.Sprint(root, folder)] = true
	var collection *int64
	if r.IntN(100) < 12 {
		c := pick(r, g.collections)
		collection = &c
	}
	m, err := q.InsertMovie(ctx, db.InsertMovieParams{
		TmdbID: int64(900_000_000 + i), Title: title, OriginalTitle: title, SortTitle: library.SortTitle(title),
		CleanTitle: match.Key(title), OriginalLanguage: "en", Year: int64(year),
		Overview:   "A fictional film generated for the demo library: " + strings.ToLower(genres[0]) + " in " + pick(r, places) + ".",
		RuntimeMin: int64(78 + r.IntN(98)), Status: status, ReleaseCinema: dateOf(cinema), ReleaseDigital: dateOf(digital),
		ReleasePhysical: dateOf(physical), Certification: pick(r, []string{"G", "PG", "PG-13", "R"}), Genres: mustJSON(genres),
		Ratings: ratings, PosterPath: &poster, BackdropPath: &backdrop, DominantColor: &color, Extra: extra,
		CollectionID: collection, Monitored: monitored, QualityProfileID: pick(r, g.profiles),
		MinimumAvailability: library.AvailReleased, RootFolderID: root, Path: folder,
		AddedAt: store.Millis(added), MetadataRefreshedAt: &refreshed,
	})
	if err != nil {
		return fmt.Errorf("movie %d: %w", i, err)
	}
	g.arts = append(g.arts, posterArt{path: poster, art: Art{Title: title, Year: int64(year), Hue: hue}})
	if err := g.history(ctx, q, &m, "movie_added", title, nil, nil, m.AddedAt, map[string]any{"reason": "Added to the demo library"}); err != nil {
		return err
	}
	if r.IntN(100) < 20 {
		if err := q.AddMovieTag(ctx, db.AddMovieTagParams{MovieID: m.ID, TagID: pick(r, g.tags).ID}); err != nil {
			return fmt.Errorf("tag: %w", err)
		}
	}

	released := status == string(metadata.StatusReleased)
	switch roll := r.IntN(100); {
	case released && roll < 80:
		res.WithFile++
		return g.file(ctx, q, &m, title, year)
	case released && monitored && roll < 82:
		res.Downloading++
		return g.download(ctx, q, &m, title, year, false)
	case released && monitored && roll < 84:
		res.Failed++
		return g.download(ctx, q, &m, title, year, true)
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (g *generator) history(ctx context.Context, q *db.Queries, m *db.Movie, event, source string, qual *string, score *int64, at int64, data map[string]any) error {
	data["demo"] = true
	if _, err := q.AppendHistory(ctx, db.AppendHistoryParams{
		MovieID: &m.ID, Event: event, SourceTitle: source, Quality: qual, CustomScore: score,
		Data: mustJSON(data), CreatedAt: at,
	}); err != nil {
		return fmt.Errorf("history: %w", err)
	}
	return nil
}

func (g *generator) release(title string, year int) (name string, p parser.ParsedRelease, mbPerMin float64) {
	suffix, mbPerMin := g.template()
	name = fmt.Sprintf("%s.%d.%s-%s", dotted(title), year, suffix, pick(g.r, groups))
	return name, parser.Parse(name), mbPerMin
}

func (g *generator) file(ctx context.Context, q *db.Queries, m *db.Movie, title string, year int) error {
	r := g.r
	name, p, mbMin := g.release(title, year)
	qid := quality.FromParsed(&p)
	size := int64(float64(m.RuntimeMin) * mbMin * (0.75 + r.Float64()*0.5) * 1e6)
	var score int64
	for _, h := range p.HDR {
		switch h {
		case parser.DolbyVision:
			score += 40
		case parser.HDR10Plus:
			score += 25
		}
	}
	if p.Atmos {
		score += 10
	}
	if p.Modifier == parser.ModifierRemux {
		score += 20
	}
	score += int64(r.IntN(4)*10 - 10)
	media := map[string]any{"source": "release name"}
	if len(p.HDR) > 0 {
		media["hdr"] = p.HDR
	}
	if p.VideoCodec != parser.CodecUnknown {
		media["videoCodec"] = p.VideoCodec
	}
	if len(p.Audio) > 0 {
		media["audio"] = p.Audio
	}
	if p.Atmos {
		media["atmos"] = true
	}
	if p.Channels != "" {
		media["channels"] = p.Channels
	}
	if p.Resolution != parser.ResolutionUnknown {
		media["resolution"] = p.Resolution
	}
	imported := min(m.AddedAt+r.Int64N(int64(10*24*time.Hour/time.Millisecond)), store.Millis(g.now))
	qs := string(qid)
	if _, err := q.UpsertMovieFile(ctx, db.UpsertMovieFileParams{
		MovieID: m.ID, RelativePath: fmt.Sprintf("%s [%s].mkv", m.Path, qid.Name()), Size: size, Quality: qs,
		Revision: 1, Edition: p.Edition.String(), Languages: `["en"]`, ReleaseGroup: p.ReleaseGroup, SceneName: name,
		Parsed: mustJSON(&p), MediaInfo: mustJSON(media), CustomScore: score, ImportedAt: imported,
	}); err != nil {
		return fmt.Errorf("file: %w", err)
	}
	return g.history(ctx, q, m, "imported", name, &qs, &score, imported,
		map[string]any{"reason": "Imported " + qid.Name() + " (demo data: there is no file on disk)", "size": size})
}

func (g *generator) download(ctx context.Context, q *db.Queries, m *db.Movie, title string, year int, failed bool) error {
	r := g.r
	name, p, mbMin := g.release(title, year)
	qs := string(quality.FromParsed(&p))
	grabbed := store.Millis(g.now) - r.Int64N(int64(6*time.Hour/time.Millisecond))
	ref := fmt.Sprintf("demo-%d", m.ID)
	d, err := q.InsertDownload(ctx, db.InsertDownloadParams{
		MovieID: m.ID, ClientID: &g.clientID, ClientRef: &ref, ReleaseTitle: name, Protocol: "usenet",
		Size: int64(float64(m.RuntimeMin) * mbMin * 1e6), Quality: qs, Revision: 1, Decision: "{}", GrabbedAt: grabbed,
	})
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	// A few downloads transfer, the others wait in the client's queue.
	st := db.UpdateDownloadStateParams{ID: d.ID, Status: "queued", StatusMessages: `[]`}
	switch {
	case failed:
		st.Status, st.Progress, st.StatusMessages = "failed", 1, `["Demo data: the download failed (unpack error)"]`
	case r.IntN(4) == 0:
		eta := int64(60 + r.IntN(3600))
		st.Status, st.Progress, st.SpeedBps, st.EtaS = "downloading", math.Round(r.Float64()*100)/100, int64(1+r.IntN(12))<<20, &eta
	}
	if _, err := q.UpdateDownloadState(ctx, st); err != nil {
		return fmt.Errorf("download state: %w", err)
	}
	if err := g.history(ctx, q, m, "grabbed", name, &qs, nil, grabbed, map[string]any{"reason": "Grabbed (demo data)"}); err != nil {
		return err
	}
	if failed {
		return g.history(ctx, q, m, "download_failed", name, &qs, nil, grabbed+60_000,
			map[string]any{"reason": "The download failed (demo data)"})
	}
	return nil
}

// prerender renders the w342 posters in parallel.
func prerender(ctx context.Context, cache *imagecache.Cache, arts []posterArt) (int, error) {
	work := make(chan posterArt)
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
		count int
	)
	for range max(1, runtime.GOMAXPROCS(0)) {
		wg.Go(func() {
			for a := range work {
				err := renderTo(cache, imagecache.Poster, "w342", a.path, a.art)
				mu.Lock()
				if err != nil && first == nil {
					first = err
				} else if err == nil {
					count++
				}
				mu.Unlock()
			}
		})
	}
	for _, a := range arts {
		if ctx.Err() != nil {
			break
		}
		work <- a
	}
	close(work)
	wg.Wait()
	if first != nil {
		return count, first
	}
	return count, ctx.Err()
}
