package health

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/indexer"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/metadata/tmdb"
	"github.com/fs0ciety7000/gonema/internal/search"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

func fixed(t time.Time) func() time.Time { return func() time.Time { return t } }

// byID returns the issue with that ID.
func byID(t *testing.T, list []Issue, id string) Issue {
	t.Helper()
	for i := range list {
		if list[i].ID == id {
			return list[i]
		}
	}
	t.Fatalf("no issue %s in %v", id, ids(list))
	return Issue{}
}

// wellFormed checks what every issue must have, whatever the check.
func wellFormed(t *testing.T, list []Issue) {
	t.Helper()
	for i := range list {
		is := &list[i]
		switch {
		case is.ID == "", is.Title == "", is.Message == "", len(is.Fix) == 0:
			t.Errorf("incomplete issue %+v", is)
		case is.Severity.Rank() == 0:
			t.Errorf("issue %s has severity %q", is.ID, is.Severity)
		case !strings.HasPrefix(is.Link, "/"):
			t.Errorf("issue %s links to %q", is.ID, is.Link)
		case strings.HasSuffix(is.Title, "."):
			t.Errorf("issue %s: a title is not a sentence: %q", is.ID, is.Title)
		case !strings.HasSuffix(is.Message, "."):
			t.Errorf("issue %s: the message must end with a full stop: %q", is.ID, is.Message)
		}
		for _, f := range is.Fix {
			if !strings.HasSuffix(f, ".") {
				t.Errorf("issue %s: fix step %q must be a sentence", is.ID, f)
			}
		}
		lower := strings.ToLower(is.Message + is.Title)
		for _, word := range []string{"error:", "failed to", "fatal", "!"} {
			if strings.Contains(lower, word) && !strings.Contains(lower, "last error:") {
				t.Errorf("issue %s: alarming wording %q in %q", is.ID, word, is.Message)
			}
		}
	}
}

// ----------------------------------------------------------------- format

func TestFormat(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("CET", 3600)
	now := time.Date(2026, 11, 5, 20, 50, 0, 0, loc)
	for _, tt := range []struct{ got, want string }{
		{duration(30 * time.Second), "less than a minute"},
		{duration(time.Minute), "1 minute"},
		{duration(40 * time.Minute), "40 minutes"},
		{duration(90 * time.Minute), "1 hour"},
		{duration(47 * time.Hour), "47 hours"},
		{duration(72 * time.Hour), "3 days"},
		{clock(now.Add(40*time.Minute), now, loc), "21:30"},
		{clock(now.Add(13*time.Hour), now, loc), "tomorrow at 09:50"},
		{clock(now.Add(50*time.Hour), now, loc), "Sat 7 Nov at 22:50"},
		{clock(time.Date(2026, 11, 5, 20, 30, 0, 0, time.UTC), now, loc), "21:30"},
		{size(512_400_000), "512 MB"},
		{size(9_120_000_000), "9.1 GB"},
		{size(38_400_000_000), "38 GB"},
		{size(1_230_000_000_000), "1.2 TB"},
		{size(4_000), "4 kB"},
		{plural(1, "film", "films"), "1 film"},
		{plural(0, "film", "films"), "0 films"},
		{parentDir("/downloads/complete/Fight.Club"), "/downloads/complete/"},
		{parentDir("/downloads/complete/Fight.Club/"), "/downloads/complete/"},
		{parentDir(`D:\Downloads\Fight.Club`), `D:\Downloads\`},
		{fmt.Sprint(median(nil)), "0"},
		{fmt.Sprint(median([]int64{1, 5, 9})), "5"},
		{fmt.Sprint(median([]int64{2, 4, 6, 8})), "5"},
		{fmt.Sprint(hasPathPrefix("/mnt/dl/x", "/mnt/dl/")), "true"},
		{fmt.Sprint(hasPathPrefix("/mnt/dlx/x", "/mnt/dl")), "false"},
		{fmt.Sprint(hasPathPrefix(`D:\dl\x`, `D:\dl`)), "true"},
	} {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
}

// ----------------------------------------------------------------- metadata

type fakeProvider struct {
	metadata.Provider // unused methods panic
	err               error
	clock             tmdb.ServerClock
}

func (p *fakeProvider) ValidateKey(context.Context) error { return p.err }

func (p *fakeProvider) LastServerClock() (tmdb.ServerClock, bool) {
	return p.clock, !p.clock.Server.IsZero()
}

type metaSource struct {
	p   library.KeyedProvider
	err error
}

func (m metaSource) Provider() (library.KeyedProvider, error) { return m.p, m.err }

func TestMetadataCheck(t *testing.T) {
	t.Parallel()
	now := t0
	tests := []struct {
		name string
		src  metaSource
		want []string
		msg  map[string]string // exact messages
	}{
		{"no key", metaSource{err: library.ErrNoProvider}, []string{"tmdb.key_missing"}, map[string]string{
			"tmdb.key_missing": "Gonema gets films, posters and release dates from TMDB and needs your own free key to ask it. " +
				"Until one is set, films can't be added and the ones in your library aren't refreshed.",
		}},
		{"rejected", metaSource{p: &fakeProvider{err: fmt.Errorf("%w: TMDB rejected this key", metadata.ErrUnauthorized)}},
			[]string{"tmdb.key_rejected"}, map[string]string{
				"tmdb.key_rejected": "TMDB no longer accepts the key Gonema uses, so films can't be added and their details aren't refreshed. " +
					"Your library, downloads and imports carry on as usual.",
			}},
		{"unreachable", metaSource{p: &fakeProvider{err: errors.New("tmdb: GET /authentication: dial tcp: no route to host")}},
			[]string{"tmdb.unreachable"}, nil},
		{"fine", metaSource{p: &fakeProvider{clock: tmdb.ServerClock{Server: now.Add(-time.Minute), Local: now.Add(-time.Minute)}}}, nil, nil},
		{"clock ahead", metaSource{p: &fakeProvider{clock: tmdb.ServerClock{Server: now.Add(-12 * time.Minute), Local: now}}},
			[]string{"clock.skew"}, map[string]string{
				"clock.skew": "This server's clock is 12 minutes ahead of TMDB's. Release dates, the minimum age of Usenet posts and " +
					"retry times are computed from it, so some may come too early or too late.",
			}},
		{"clock behind", metaSource{p: &fakeProvider{clock: tmdb.ServerClock{Server: now.Add(2 * time.Hour), Local: now}}},
			[]string{"clock.skew"}, nil},
		{"within tolerance", metaSource{p: &fakeProvider{clock: tmdb.ServerClock{Server: now.Add(4 * time.Minute), Local: now}}}, nil, nil},
		{"stale observation ignored", metaSource{p: &fakeProvider{clock: tmdb.ServerClock{Server: now.Add(-3 * time.Hour), Local: now.Add(-2 * time.Hour)}}}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := MetadataCheck(tt.src, fixed(now)).Run(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(ids(got), tt.want) {
				t.Fatalf("issues = %v, want %v", ids(got), tt.want)
			}
			wellFormed(t, got)
			for id, msg := range tt.msg {
				if is := byID(t, got, id); is.Message != msg {
					t.Errorf("%s message:\n got %q\nwant %q", id, is.Message, msg)
				}
			}
		})
	}
	t.Run("behind wording", func(t *testing.T) {
		t.Parallel()
		is, bad := clockSkew(-2 * time.Hour)
		if !bad || is.Title != "The server clock is off by 2 hours" || !strings.Contains(is.Message, "2 hours behind TMDB's") {
			t.Errorf("issue = %+v", is)
		}
	})
	t.Run("canceled run keeps findings", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := MetadataCheck(metaSource{p: &fakeProvider{err: context.Canceled}}, fixed(now)).Run(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
}

// ----------------------------------------------------------------- root folders

type fakeFolders struct {
	rows  []db.RootFolder
	sizes map[int64][]int64
}

func (f *fakeFolders) ListRootFolders(context.Context) ([]db.RootFolder, error) { return f.rows, nil }

func (f *fakeFolders) ListFileSizesInRootFolder(_ context.Context, id int64) ([]int64, error) {
	return f.sizes[id], nil
}

type dirInfo struct{ fs.FileInfo }

func (dirInfo) IsDir() bool { return true }

type fileInfo struct{ fs.FileInfo }

func (fileInfo) IsDir() bool { return false }

type fakeFS struct {
	stat     map[string]error // nil entry: an existing folder
	files    map[string]bool  // existing regular files
	readonly map[string]bool
	free     map[string][2]uint64 // free, total
	hang     chan struct{}        // Stat blocks on it for paths in hangs
	hangs    map[string]bool
}

func (f *fakeFS) Stat(name string) (fs.FileInfo, error) {
	if f.hangs[name] {
		<-f.hang
	}
	if f.files[name] {
		return fileInfo{}, nil
	}
	if err, ok := f.stat[name]; ok {
		if err != nil {
			return nil, err
		}
		return dirInfo{}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
}

func (f *fakeFS) CheckWritable(dir string) error {
	if f.readonly[dir] {
		return &fs.PathError{Op: "open", Path: dir, Err: fs.ErrPermission}
	}
	return nil
}

func (f *fakeFS) DiskSpace(path string) (free, total uint64, err error) {
	v, ok := f.free[path]
	if !ok {
		return 0, 0, library.ErrDiskSpaceUnsupported
	}
	return v[0], v[1], nil
}

const gbytes = 1_000_000_000

func TestRootFoldersCheck(t *testing.T) {
	hang := make(chan struct{})
	defer close(hang)
	old := fsTimeout
	fsTimeout = 20 * time.Millisecond
	defer func() { fsTimeout = old }()

	fsys := &fakeFS{
		stat: map[string]error{
			"/movies": nil, "/full": nil, "/tight": nil, "/empty": nil, "/ro": nil, "/fine": nil, "/locked": fs.ErrPermission,
		},
		files:    map[string]bool{"/file": true},
		readonly: map[string]bool{"/ro": true},
		free: map[string][2]uint64{
			"/movies": {38_400 * gbytes / 1000, 1_200 * gbytes},
			"/full":   {3 * gbytes, 1_000 * gbytes},
			"/tight":  {600 * gbytes, 20_000 * gbytes},
			"/empty":  {10 * gbytes, 1_000 * gbytes},
			"/fine":   {500 * gbytes, 1_000 * gbytes},
		},
		hang: hang, hangs: map[string]bool{"/nfs": true},
	}
	folders := &fakeFolders{
		rows: []db.RootFolder{
			{ID: 1, Path: "/movies"}, {ID: 2, Path: "/gone"}, {ID: 3, Path: "/locked"}, {ID: 4, Path: "/ro"},
			{ID: 5, Path: "/full"}, {ID: 6, Path: "/tight"}, {ID: 7, Path: "/empty"}, {ID: 8, Path: "/fine"},
			{ID: 9, Path: "/file"}, {ID: 10, Path: "/nfs"},
		},
		sizes: map[int64][]int64{
			1: {7 * gbytes, 9_100 * gbytes / 1000, 12 * gbytes},
			5: {8 * gbytes, 10 * gbytes},
		},
	}
	set := DefaultSettings()
	check := RootFoldersCheck(folders, fsys, func(context.Context) Settings { return set })
	got, err := check.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"rootfolder.low_space:1", "rootfolder.missing:2", "rootfolder.unreadable:3", "rootfolder.readonly:4",
		"rootfolder.low_space:5", "rootfolder.low_space:6", "rootfolder.low_space:7", "rootfolder.missing:9",
		"rootfolder.unresponsive:10",
	}
	if !slices.Equal(ids(got), want) {
		t.Fatalf("issues = %v\nwant %v", ids(got), want)
	}
	wellFormed(t, got)
	low := byID(t, got, "rootfolder.low_space:1")
	if wantMsg := "/movies has 38 GB left (3% of the disk). That's room for about 4 films at the usual size here (9.1 GB). " +
		"Gonema keeps downloading, but imports fail once the disk is full."; low.Message != wantMsg || low.Severity != Warning {
		t.Errorf("low space:\n got %q (%s)\nwant %q", low.Message, low.Severity, wantMsg)
	}
	if low.Link != "/settings/library/root-folders/1" || low.Title != "/movies is running out of space" {
		t.Errorf("low space link %q title %q", low.Link, low.Title)
	}
	if full := byID(t, got, "rootfolder.low_space:5"); full.Severity != Error ||
		!strings.HasPrefix(full.Message, "/full has 3.0 GB left, less than a typical film here (9.0 GB).") {
		t.Errorf("full = %s %q", full.Severity, full.Message)
	}
	if tight := byID(t, got, "rootfolder.low_space:6"); !strings.Contains(tight.Message, "(3% of the disk)") {
		t.Errorf("percent threshold: %q", tight.Message)
	}
	if empty := byID(t, got, "rootfolder.low_space:7"); strings.Contains(empty.Message, "room for") {
		t.Errorf("no estimate without films: %q", empty.Message)
	}
	if missing := byID(t, got, "rootfolder.missing:2"); missing.Severity != Error ||
		missing.Message != "Gonema can't find /gone (unmounted drive or missing volume?). Films stored there can't be imported, "+
			"renamed or upgraded until it's back; nothing is removed from your library." {
		t.Errorf("missing = %q", missing.Message)
	}

	// Thresholds come from the settings; 0 turns one off.
	set = Settings{LowSpacePercent: 0, LowSpaceGB: 5, RecycleBinMaxGB: 100}
	got, err = check.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, is := range got {
		if strings.HasPrefix(is.ID, "rootfolder.low_space:") && is.ID != "rootfolder.low_space:5" {
			t.Errorf("unexpected %s with a 5 GB threshold", is.ID)
		}
	}

	none, err := RootFoldersCheck(&fakeFolders{}, fsys, func(context.Context) Settings { return set }).Run(t.Context())
	if err != nil || !slices.Equal(ids(none), []string{"rootfolder.none"}) {
		t.Errorf("no folder = %v, %v", ids(none), err)
	}
}

func TestListFileSizesInRootFolder(t *testing.T) {
	e := newStoreEnv(t)
	for i, sz := range []int64{9, 3, 5} {
		e.movieWithFile(t, int64(100+i), sz)
	}
	sizes, err := e.st.Read().ListFileSizesInRootFolder(t.Context(), e.lib.RootFolderID)
	if err != nil || !slices.Equal(sizes, []int64{3, 5, 9}) {
		t.Errorf("sizes = %v, %v", sizes, err)
	}
}

// ----------------------------------------------------------------- indexers

type indexerSource []acquire.IndexerState

func (s indexerSource) IndexerStates(context.Context) ([]acquire.IndexerState, error) { return s, nil }

func TestIndexersCheck(t *testing.T) {
	t.Parallel()
	now := t0 // 20:50 UTC
	down := func(id int64, name string, lastOK time.Time, err string) acquire.IndexerState {
		return acquire.IndexerState{ID: id, Name: name, Enabled: true, Health: search.Health{
			State: search.Open, ConsecutiveFailures: 3, LastError: err, LastOKAt: lastOK, DisabledUntil: now.Add(40 * time.Minute),
		}}
	}
	ok := func(id int64, name string) acquire.IndexerState {
		return acquire.IndexerState{ID: id, Name: name, Enabled: true, Health: search.Health{LastOKAt: now}}
	}
	timeout := "Cinémathèque: " + indexer.ErrTimeout.Error()
	tests := []struct {
		name   string
		states []acquire.IndexerState
		want   []string
		check  func(t *testing.T, got []Issue)
	}{
		{"none", nil, []string{"indexer.none"}, func(t *testing.T, got []Issue) {
			if got[0].Title != "No indexer is set up" || got[0].Severity != Error {
				t.Errorf("%+v", got[0])
			}
		}},
		{"all off", []acquire.IndexerState{{ID: 1, Name: "Off"}}, []string{"indexer.none"}, func(t *testing.T, got []Issue) {
			if got[0].Title != "Every indexer is turned off" {
				t.Errorf("title %q", got[0].Title)
			}
		}},
		{"healthy", []acquire.IndexerState{ok(1, "A"), ok(2, "B")}, nil, nil},
		{"one paused, others work", []acquire.IndexerState{
			down(3, "Cinémathèque", now.Add(-40*time.Minute), timeout), ok(1, "Nebula NZB"), ok(2, "B"), ok(4, "C"),
		}, []string{"indexer.unreachable:3"}, func(t *testing.T, got []Issue) {
			is := got[0]
			want := "Cinémathèque hasn't answered for 40 minutes. The other 3 indexers keep searching, so nothing is blocked. " +
				"Gonema paused it until 21:30 and will try again then. Last error: the indexer did not answer in time."
			if is.Message != want || is.Severity != Warning || is.Link != "/settings/indexers/3" || is.Title != "Cinémathèque is not answering" {
				t.Errorf("issue:\n got %q (%s, %s, %q)\nwant %q", is.Message, is.Severity, is.Link, is.Title, want)
			}
			if !is.Since.Equal(now.Add(-40 * time.Minute)) {
				t.Errorf("since = %v", is.Since)
			}
		}},
		{"one of two", []acquire.IndexerState{down(3, "X", now.Add(-2*time.Hour), ""), ok(1, "Y")}, []string{"indexer.unreachable:3"},
			func(t *testing.T, got []Issue) {
				if !strings.Contains(got[0].Message, "X hasn't answered for 2 hours. The other indexer keeps searching, so nothing is blocked.") {
					t.Errorf("%q", got[0].Message)
				}
			}},
		{"rate limited", []acquire.IndexerState{down(3, "X", time.Time{}, "X: "+indexer.ErrRateLimited.Error()), ok(1, "Y")},
			[]string{"indexer.unreachable:3"}, func(t *testing.T, got []Issue) {
				if !strings.HasPrefix(got[0].Message, "X says its request limit is reached.") {
					t.Errorf("%q", got[0].Message)
				}
			}},
		{"key rejected", []acquire.IndexerState{down(3, "X", time.Time{}, indexer.ErrUnauthorized.Error()), ok(1, "Y")},
			[]string{"indexer.unreachable:3"}, func(t *testing.T, got []Issue) {
				if !strings.Contains(got[0].Fix[1], "Paste the current API key") || !strings.Contains(got[0].Message, "failed its last 3 requests") {
					t.Errorf("%+v", got[0])
				}
			}},
		{"all down", []acquire.IndexerState{down(1, "A", now.Add(-time.Hour), ""), down(2, "B", now.Add(-time.Hour), "")},
			[]string{"indexer.all_down", "indexer.unreachable:1", "indexer.unreachable:2"}, func(t *testing.T, got []Issue) {
				all := got[0]
				want := "None of your 2 indexers is answering, so searches and RSS find nothing for now. Gonema tries again as soon as " +
					"a pause ends (the first at 21:30); downloads already in the queue are not affected."
				if all.Severity != Error || all.Message != want {
					t.Errorf("all down:\n got %q\nwant %q", all.Message, want)
				}
				if strings.Contains(got[1].Message, "nothing is blocked") {
					t.Errorf("must not reassure when nothing works: %q", got[1].Message)
				}
			}},
		{"pause over, probing", []acquire.IndexerState{{ID: 1, Name: "A", Enabled: true, Health: search.Health{
			State: search.Open, ConsecutiveFailures: 3, DisabledUntil: now.Add(-time.Minute)}}}, []string{"indexer.failing:1"},
			func(t *testing.T, got []Issue) {
				if got[0].Severity != Info {
					t.Errorf("a breaker whose pause ended is back in use: %s", got[0].Severity)
				}
			}},
		{"failing but working", []acquire.IndexerState{{ID: 5, Name: "Shaky", Enabled: true, Health: search.Health{
			ConsecutiveFailures: 2, LastError: "Shaky: " + indexer.ErrServer.Error()}}, ok(1, "B")}, []string{"indexer.failing:5"},
			func(t *testing.T, got []Issue) {
				want := "Shaky failed its last 2 requests. Gonema keeps using it and pauses it for a while if it keeps failing. " +
					"Last error: the indexer reported an internal error."
				if got[0].Severity != Info || got[0].Message != want {
					t.Errorf("got %q (%s)", got[0].Message, got[0].Severity)
				}
			}},
		{"single failure is noise", []acquire.IndexerState{{ID: 5, Name: "S", Enabled: true, Health: search.Health{ConsecutiveFailures: 1}}}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := IndexersCheck(indexerSource(tt.states), fixed(now), time.UTC).Run(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			sortIssues(got)
			if !slices.Equal(ids(got), tt.want) {
				t.Fatalf("issues = %v, want %v", ids(got), tt.want)
			}
			wellFormed(t, got)
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// ----------------------------------------------------------------- download clients

type clientSource []acquire.ClientState

func (s clientSource) ProbeClients(context.Context) ([]acquire.ClientState, error) { return s, nil }

func TestClientsCheck(t *testing.T) {
	t.Parallel()
	qb := func(err error) acquire.ClientState {
		return acquire.ClientState{ID: 2, Name: "qBittorrent", Kind: "qbittorrent", Protocol: "torrent", Host: "10.0.0.5", Port: 8080, Enabled: true, Err: err}
	}
	unreach := download.NewError(download.KindQBittorrent, "test", download.ErrUnreachable, "", errors.New("connection refused"))
	auth := download.NewError(download.KindQBittorrent, "test", download.ErrAuth, "", nil)
	tests := []struct {
		name   string
		states []acquire.ClientState
		want   []string
		check  func(t *testing.T, got []Issue)
	}{
		{"none", nil, []string{"client.none"}, nil},
		{"all off", []acquire.ClientState{{ID: 1, Name: "SAB"}}, []string{"client.none"}, func(t *testing.T, got []Issue) {
			if got[0].Title != "Every download client is turned off" {
				t.Errorf("title %q", got[0].Title)
			}
		}},
		{"healthy", []acquire.ClientState{qb(nil)}, nil, nil},
		{"auth", []acquire.ClientState{qb(auth)}, []string{"client.auth:2"}, func(t *testing.T, got []Issue) {
			want := "qBittorrent rejected the user name, password or API key Gonema uses, so new releases can't be sent to it " +
				"and the queue isn't updated. Downloads already running carry on in qBittorrent."
			if got[0].Message != want || got[0].Severity != Error || got[0].Link != "/settings/download-clients/2" {
				t.Errorf("got %q", got[0].Message)
			}
		}},
		{"unreachable alone", []acquire.ClientState{qb(unreach)}, []string{"client.unreachable:2"}, func(t *testing.T, got []Issue) {
			if got[0].Severity != Error || !strings.HasPrefix(got[0].Message,
				"Gonema can't reach qBittorrent at 10.0.0.5:8080. New torrent releases can't be sent until it answers.") {
				t.Errorf("got %s %q", got[0].Severity, got[0].Message)
			}
		}},
		{"unreachable with a fallback", []acquire.ClientState{qb(unreach),
			{ID: 3, Name: "Transmission", Protocol: "torrent", Enabled: true}, {ID: 4, Name: "SAB", Protocol: "usenet", Enabled: true}},
			[]string{"client.unreachable:2"}, func(t *testing.T, got []Issue) {
				if got[0].Severity != Warning || !strings.Contains(got[0].Message, "New torrent releases go to Transmission meanwhile, so nothing is blocked.") {
					t.Errorf("got %s %q", got[0].Severity, got[0].Message)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ClientsCheck(clientSource(tt.states)).Run(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(ids(got), tt.want) {
				t.Fatalf("issues = %v, want %v", ids(got), tt.want)
			}
			wellFormed(t, got)
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// ----------------------------------------------------------------- recycle bin

type recycleSource struct {
	retention int
	items     []importer.RecycleEntry
}

func (r recycleSource) Settings(context.Context) (importer.Settings, error) {
	s := importer.DefaultSettings()
	s.RecycleRetentionDays = r.retention
	return s, nil
}

func (r recycleSource) ListRecycle(context.Context) ([]importer.RecycleEntry, error) {
	return r.items, nil
}

func TestRecycleBinCheck(t *testing.T) {
	t.Parallel()
	item := func(sz int64, age time.Duration) importer.RecycleEntry {
		e := importer.RecycleEntry{}
		e.Size, e.RecycledAt = sz, t0.Add(-age)
		return e
	}
	day := 24 * time.Hour
	tests := []struct {
		name  string
		src   recycleSource
		max   int
		want  []string
		check func(t *testing.T, got []Issue)
	}{
		{"empty", recycleSource{retention: 30}, 100, nil, nil},
		{"small and recent", recycleSource{retention: 30, items: []importer.RecycleEntry{item(9*gbytes, day)}}, 100, nil, nil},
		{"large", recycleSource{retention: 30, items: []importer.RecycleEntry{item(90*gbytes, day), item(92*gbytes, 2*day)}}, 100,
			[]string{"recycle.large"}, func(t *testing.T, got []Issue) {
				want := "Replaced and removed files (2 files) are kept there so nothing is lost by mistake, and they now take 182 GB. " +
					"Gonema never empties the bin on its own."
				if got[0].Message != want || got[0].Title != "The recycle bin holds 182 GB" || got[0].Severity != Info {
					t.Errorf("got %q / %q", got[0].Title, got[0].Message)
				}
			}},
		{"expired", recycleSource{retention: 30, items: []importer.RecycleEntry{item(4*gbytes, 40*day), item(5*gbytes, 31*day), item(gbytes, day)}}, 100,
			[]string{"recycle.expired"}, func(t *testing.T, got []Issue) {
				if got[0].Title != "2 files in the recycle bin are older than 30 days" ||
					got[0].Message != "They're past the retention you set (30 days) and take 9.0 GB. Gonema doesn't delete them by itself: purging is your call." {
					t.Errorf("got %q / %q", got[0].Title, got[0].Message)
				}
			}},
		{"one expired", recycleSource{retention: 7, items: []importer.RecycleEntry{item(gbytes, 8*day)}}, 100, []string{"recycle.expired"},
			func(t *testing.T, got []Issue) {
				if got[0].Title != "1 file in the recycle bin is older than 7 days" {
					t.Errorf("title %q", got[0].Title)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := RecycleBinCheck(tt.src, func(context.Context) Settings { return Settings{RecycleBinMaxGB: tt.max} }, fixed(t0)).Run(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(ids(got), tt.want) {
				t.Fatalf("issues = %v, want %v", ids(got), tt.want)
			}
			wellFormed(t, got)
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// ----------------------------------------------------------------- database

type fakeDB struct {
	problems []string
	wal      int64
	checks   int
}

func (f *fakeDB) QuickCheck(context.Context) ([]string, error) {
	f.checks++
	return f.problems, nil
}

func (f *fakeDB) WALSize() (int64, error) { return f.wal, nil }

func TestDatabaseCheck(t *testing.T) {
	e := newStoreEnv(t)
	clk := newClock()
	fdb := &fakeDB{problems: []string{"*** in database main ***\nPage 42: btreeInitPage() returns error code 11", "row 3 missing from index x"}}
	check := DatabaseCheck(e.st, fdb, clk.Now)
	got, err := check.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(got), []string{"database.integrity"}) || fdb.checks != 1 {
		t.Fatalf("issues = %v after %d checks", ids(got), fdb.checks)
	}
	wellFormed(t, got)
	if !strings.Contains(got[0].Message, "(and 1 more)") || got[0].Severity != Error {
		t.Errorf("message %q", got[0].Message)
	}

	// Weekly: within the week the stored result is reused, even across restarts.
	fdb.problems = nil
	clk.Advance(24 * time.Hour)
	again := DatabaseCheck(e.st, fdb, clk.Now)
	if got, err = again.Run(t.Context()); err != nil || len(got) != 1 || fdb.checks != 1 {
		t.Fatalf("issues = %v, %v after %d checks", ids(got), err, fdb.checks)
	}
	clk.Advance(IntegrityEvery)
	if got, err = again.Run(t.Context()); err != nil || len(got) != 0 || fdb.checks != 2 {
		t.Fatalf("issues = %v, %v after %d checks", ids(got), err, fdb.checks)
	}

	fdb.wal = 1_200_000_000
	got, err = again.Run(t.Context())
	if err != nil || !slices.Equal(ids(got), []string{"database.wal_large"}) || got[0].Title != "The database journal has grown to 1.2 GB" {
		t.Fatalf("issues = %+v, %v", got, err)
	}
	wellFormed(t, got)

	// The real store passes the real check.
	actual, err := DatabaseCheck(e.st, e.st, clk.Now).Run(t.Context())
	if err != nil || len(actual) != 0 {
		t.Errorf("real database = %v, %v", ids(actual), err)
	}
}

// ----------------------------------------------------------------- settings

func TestSettings(t *testing.T) {
	e := newStoreEnv(t)
	ss := SettingsStore{St: e.st}
	if got := ss.Get(t.Context()); got != DefaultSettings() {
		t.Errorf("defaults = %+v", got)
	}
	in := Settings{LowSpacePercent: 10, LowSpaceGB: 0, RecycleBinMaxGB: 20}
	if out, err := ss.Set(t.Context(), in); err != nil || out != in || ss.Get(t.Context()) != in {
		t.Fatalf("set = %+v, %v", out, err)
	}
	_, err := ss.Set(t.Context(), Settings{LowSpacePercent: 80, LowSpaceGB: -1})
	var ie *InvalidError
	if !errors.Is(err, ErrInvalid) || !errors.As(err, &ie) ||
		ie.UserMessage() != "The low space share must be between 0 (off) and 50 percent. The low space amount must be between 0 (off) "+
			"and 100000 GB. The recycle bin size must be between 1 and 100000 GB." {
		t.Errorf("invalid = %v", err)
	}
}
