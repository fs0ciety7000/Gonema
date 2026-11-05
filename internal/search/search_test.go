package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/indexer"
)

// fakeIndexer is an indexer.Indexer driven by functions.
type fakeIndexer struct {
	info   indexer.Info
	search func(ctx context.Context, q indexer.Query) ([]indexer.Release, error)
	rss    func(ctx context.Context) ([]indexer.Release, error)
	calls  atomic.Int32
}

func (f *fakeIndexer) Info() indexer.Info { return f.info }
func (f *fakeIndexer) Caps(context.Context) (indexer.Caps, error) {
	return indexer.Caps{}, nil
}
func (f *fakeIndexer) Search(ctx context.Context, q indexer.Query) ([]indexer.Release, error) {
	f.calls.Add(1)
	return f.search(ctx, q)
}
func (f *fakeIndexer) RSS(ctx context.Context) ([]indexer.Release, error) {
	f.calls.Add(1)
	return f.rss(ctx)
}
func (f *fakeIndexer) Test(context.Context) error { return nil }

func rel(ix indexer.Info, guid, hash string) indexer.Release {
	return indexer.Release{
		Title: "Fight.Club.1999.1080p.BluRay.x264-GRP", GUID: guid, InfoHash: hash, DownloadURL: "https://x/" + guid,
		IndexerID: ix.ID, IndexerName: ix.Name, IndexerPriority: ix.Priority, Protocol: ix.Protocol, Seeders: -1, Leechers: -1,
	}
}

func info(id int64, prio int) indexer.Info {
	return indexer.Info{ID: id, Name: fmt.Sprintf("ix%d", id), Priority: prio, Protocol: decision.Torrent}
}

func returning(rels ...indexer.Release) func(context.Context, indexer.Query) ([]indexer.Release, error) {
	return func(context.Context, indexer.Query) ([]indexer.Release, error) { return rels, nil }
}

func failing(err error) func(context.Context, indexer.Query) ([]indexer.Release, error) {
	return func(context.Context, indexer.Query) ([]indexer.Release, error) { return nil, err }
}

// sleeping waits d or until ctx ends.
func sleeping(d time.Duration) func(context.Context, indexer.Query) ([]indexer.Release, error) {
	return func(ctx context.Context, _ indexer.Query) ([]indexer.Release, error) {
		select {
		case <-time.After(d):
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

type received struct {
	ev Event
	at time.Duration
}

// drain reads events until the channel closes, or fails the test after limit.
func drain(t *testing.T, ch <-chan Event, limit time.Duration) []received {
	t.Helper()
	start := time.Now()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	var evs []received
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return evs
			}
			evs = append(evs, received{ev, time.Since(start)})
		case <-timer.C:
			t.Fatalf("channel not closed after %s; events: %v", limit, evs)
		}
	}
}

func newTestOrchestrator(o Options) *Orchestrator {
	if o.RateLimit == 0 {
		o.RateLimit = 1000
		o.Burst = 1000
	}
	return New(o)
}

func TestSearchStreamsAndIsolatesFailures(t *testing.T) {
	t.Parallel()
	const timeout = 200 * time.Millisecond
	fast := &fakeIndexer{info: info(1, 25)}
	fast.search = returning(rel(fast.info, "a", ""), rel(fast.info, "b", ""))
	slow := &fakeIndexer{info: info(2, 25), search: sleeping(10 * time.Second)}
	broken := &fakeIndexer{info: info(3, 25), search: failing(&indexer.Error{Kind: indexer.ErrServer, Indexer: "ix3", Message: "boom"})}
	stubborn := &fakeIndexer{info: info(4, 25)} // ignores its context
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	stubborn.search = func(context.Context, indexer.Query) ([]indexer.Release, error) {
		<-release
		return nil, nil
	}

	s := newTestOrchestrator(Options{Timeout: timeout})
	evs := drain(t, s.Search(t.Context(), indexer.Query{IMDbID: "tt0137523"}, []indexer.Indexer{slow, stubborn, broken, fast}), 5*time.Second)

	var batchAt time.Duration = -1
	failed := map[int64]error{}
	var fin *Finished
	for _, r := range evs {
		switch ev := r.ev.(type) {
		case *ResultBatch:
			if ev.Indexer.ID != 1 || len(ev.Releases) != 2 {
				t.Errorf("unexpected batch %+v", ev)
			}
			batchAt = r.at
		case *IndexerDone:
			if ev.Indexer.ID != 1 || ev.Count != 2 {
				t.Errorf("unexpected done %+v", ev)
			}
		case *IndexerFailed:
			failed[ev.Indexer.ID] = ev.Err
			if !ev.WillRetryAt.IsZero() {
				t.Errorf("one failure must not open the breaker: %+v", ev)
			}
		case *Finished:
			fin = ev
		}
	}
	if batchAt < 0 || batchAt >= timeout {
		t.Errorf("fast batch at %s, want before the %s timeout of the slow indexers", batchAt, timeout)
	}
	if _, ok := evs[len(evs)-1].ev.(*Finished); !ok || fin == nil {
		t.Fatalf("last event is %T, want *Finished", evs[len(evs)-1].ev)
	}
	if fin.Succeeded != 1 || fin.Failed != 3 || len(fin.Results) != 2 {
		t.Errorf("finished = %+v", fin)
	}
	for id, kind := range map[int64]error{2: indexer.ErrTimeout, 3: indexer.ErrServer, 4: indexer.ErrTimeout} {
		if !errors.Is(failed[id], kind) {
			t.Errorf("indexer %d: err = %v, want %v", id, failed[id], kind)
		}
	}
	if last := evs[len(evs)-1].at; last > 2*time.Second {
		t.Errorf("search took %s", last)
	}
	if h, _ := s.Health(2); h.ConsecutiveFailures != 1 || !strings.Contains(h.LastError, "did not answer in time") {
		t.Errorf("health of slow indexer = %+v", h)
	}
}

func TestSearchDedupe(t *testing.T) {
	t.Parallel()
	a := &fakeIndexer{info: info(1, 25)}
	b := &fakeIndexer{info: info(2, 10)}
	ra := []indexer.Release{rel(a.info, "a-1", "aaaa"), rel(a.info, "shared-guid", ""), rel(a.info, "a-only", "")}
	ra[0].Seeders = 5
	rb := []indexer.Release{rel(b.info, "b-1", "aaaa"), rel(b.info, "shared-guid", ""), rel(b.info, "b-only", "")}
	rb[0].Seeders = 9
	a.search = returning(ra...)
	gotA := make(chan struct{})
	b.search = func(ctx context.Context, _ indexer.Query) ([]indexer.Release, error) {
		select {
		case <-gotA: // answer after a, so that a's copies are sent first
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return rb, nil
	}
	s := newTestOrchestrator(Options{})
	ch := s.Search(t.Context(), indexer.Query{Title: "Fight Club"}, []indexer.Indexer{a, b})

	var batches []*ResultBatch
	var fin *Finished
	for ev := range ch {
		switch ev := ev.(type) {
		case *ResultBatch:
			batches = append(batches, ev)
			if ev.Indexer.ID == 1 {
				close(gotA)
			}
		case *Finished:
			fin = ev
		}
	}
	if len(batches) != 2 || len(batches[0].Releases) != 3 || len(batches[1].Releases) != 1 || len(batches[1].Duplicates) != 2 {
		t.Fatalf("batches = %+v", batches)
	}
	if d := batches[1].Duplicates[0]; d.Key != "btih:aaaa" || d.Source.IndexerID != 2 || d.Source.GUID != "b-1" {
		t.Errorf("duplicate = %+v", d)
	}
	if fin == nil || len(fin.Results) != 4 {
		t.Fatalf("finished = %+v", fin)
	}
	// The merged copy is the one of the preferred indexer (priority 10), with both sources.
	r := fin.Results[0]
	if r.IndexerID != 2 || r.GUID != "b-1" || r.Seeders != 9 || len(r.Sources) != 2 ||
		r.Sources[0].IndexerID != 1 || r.Sources[1].IndexerID != 2 {
		t.Errorf("merged = %+v", r)
	}
	if fin.Results[1].IndexerID != 2 || len(fin.Results[1].Sources) != 2 || fin.Results[2].GUID != "a-only" || fin.Results[2].Sources != nil {
		t.Errorf("results = %+v", fin.Results)
	}
}

func TestSearchCancellation(t *testing.T) {
	t.Parallel()
	var sawCancel atomic.Bool
	blocking := &fakeIndexer{info: info(1, 25)}
	blocking.search = func(ctx context.Context, _ indexer.Query) ([]indexer.Release, error) {
		<-ctx.Done()
		sawCancel.Store(errors.Is(ctx.Err(), context.Canceled))
		return nil, ctx.Err()
	}
	s := newTestOrchestrator(Options{Timeout: time.Minute})
	ctx, cancel := context.WithCancel(t.Context())
	ch := s.Search(ctx, indexer.Query{IMDbID: "tt0137523"}, []indexer.Indexer{blocking})
	time.AfterFunc(20*time.Millisecond, cancel)
	evs := drain(t, ch, 2*time.Second)
	for _, r := range evs {
		if _, ok := r.ev.(*Finished); ok {
			t.Error("Finished sent after cancellation")
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for !sawCancel.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !sawCancel.Load() {
		t.Fatal("the in-flight request was not canceled")
	}
	// Cancellation is not a failure of the indexer.
	time.Sleep(10 * time.Millisecond)
	if h, _ := s.Health(1); h.ConsecutiveFailures != 0 {
		t.Errorf("health = %+v", h)
	}
}

func TestSearchConsumerStopsReading(t *testing.T) {
	t.Parallel()
	var ixs []indexer.Indexer
	for i := range 20 {
		f := &fakeIndexer{info: info(int64(i+1), 25)}
		f.search = returning(rel(f.info, fmt.Sprint("g", i), ""))
		ixs = append(ixs, f)
	}
	s := newTestOrchestrator(Options{})
	ctx, cancel := context.WithCancel(t.Context())
	ch := s.Search(ctx, indexer.Query{IMDbID: "tt1"}, ixs)
	<-ch // read one event, then give up
	cancel()
	drain(t, ch, 2*time.Second)
}

func TestSearchNoIndexers(t *testing.T) {
	t.Parallel()
	evs := drain(t, New(Options{}).Search(t.Context(), indexer.Query{}, nil), time.Second)
	if len(evs) != 1 {
		t.Fatalf("events = %v", evs)
	}
	if f, ok := evs[0].ev.(*Finished); !ok || len(f.Results) != 0 {
		t.Errorf("event = %#v", evs[0].ev)
	}
}

func TestSearchPartialResults(t *testing.T) {
	t.Parallel()
	f := &fakeIndexer{info: info(1, 25)}
	f.search = func(context.Context, indexer.Query) ([]indexer.Release, error) {
		return []indexer.Release{rel(f.info, "p1", "")}, &indexer.Error{Kind: indexer.ErrRateLimited, RetryAfter: time.Hour}
	}
	now := t0
	s := newTestOrchestrator(Options{Now: func() time.Time { return now }})
	evs := drain(t, s.Search(t.Context(), indexer.Query{IMDbID: "tt1"}, []indexer.Indexer{f}), time.Second)
	if len(evs) != 3 {
		t.Fatalf("events = %v", evs)
	}
	if b, ok := evs[0].ev.(*ResultBatch); !ok || len(b.Releases) != 1 {
		t.Errorf("first event = %#v", evs[0].ev)
	}
	if fe, ok := evs[1].ev.(*IndexerFailed); !ok || !fe.WillRetryAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("second event = %#v", evs[1].ev)
	}
}

// clock is a manual clock safe for concurrent use.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestSearchBreaker(t *testing.T) {
	t.Parallel()
	clk := &clock{t: t0}
	var healthy atomic.Bool
	f := &fakeIndexer{info: info(1, 25)}
	f.search = func(context.Context, indexer.Query) ([]indexer.Release, error) {
		if healthy.Load() {
			return []indexer.Release{rel(f.info, "ok", "")}, nil
		}
		return nil, &indexer.Error{Kind: indexer.ErrServer, Message: "HTTP 503"}
	}
	s := newTestOrchestrator(Options{Now: clk.now, Breaker: BreakerPolicy{Threshold: 2, BaseCooldown: time.Minute}})
	failure := func() *IndexerFailed {
		t.Helper()
		for _, r := range drain(t, s.Search(t.Context(), indexer.Query{IMDbID: "tt1"}, []indexer.Indexer{f}), time.Second) {
			if fe, ok := r.ev.(*IndexerFailed); ok {
				return fe
			}
		}
		return nil
	}
	if fe := failure(); fe == nil || !fe.WillRetryAt.IsZero() {
		t.Fatalf("first failure = %+v", fe)
	}
	if fe := failure(); fe == nil || !fe.WillRetryAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("second failure = %+v", fe)
	}
	// Open: the indexer is skipped without being called.
	calls := f.calls.Load()
	fe := failure()
	if fe == nil || !errors.Is(fe.Err, ErrCircuitOpen) || !fe.WillRetryAt.Equal(t0.Add(time.Minute)) || f.calls.Load() != calls {
		t.Fatalf("open: %+v, calls %d -> %d", fe, calls, f.calls.Load())
	}
	if h, _ := s.Health(1); h.State != Open || h.ConsecutiveFailures != 2 || !h.DisabledUntil.Equal(t0.Add(time.Minute)) {
		t.Errorf("health = %+v", h)
	}
	// After the cooldown, a failed probe reopens for twice as long.
	clk.advance(time.Minute)
	if fe := failure(); fe == nil || errors.Is(fe.Err, ErrCircuitOpen) || !fe.WillRetryAt.Equal(t0.Add(3*time.Minute)) {
		t.Fatalf("failed probe = %+v", fe)
	}
	// A successful probe closes.
	clk.advance(2 * time.Minute)
	healthy.Store(true)
	if fe := failure(); fe != nil {
		t.Fatalf("probe failed: %+v", fe)
	}
	if h, _ := s.Health(1); h.State != Closed || h.ConsecutiveFailures != 0 || !h.LastOKAt.Equal(t0.Add(3*time.Minute)) {
		t.Errorf("health = %+v", h)
	}
	// Restore and Reset.
	s.Restore(1, Health{State: Open, DisabledUntil: t0.Add(time.Hour)})
	if fe := failure(); fe == nil || !errors.Is(fe.Err, ErrCircuitOpen) {
		t.Fatalf("restored: %+v", fe)
	}
	s.Reset(1)
	if fe := failure(); fe != nil {
		t.Fatalf("after reset: %+v", fe)
	}
}

func TestSearchRateLimitIsNotAFailure(t *testing.T) {
	t.Parallel()
	f := &fakeIndexer{info: info(1, 25)}
	f.search = returning()
	s := New(Options{Timeout: 50 * time.Millisecond, RateLimit: 0.001, Burst: 1})
	run := func() Event {
		evs := drain(t, s.Search(t.Context(), indexer.Query{IMDbID: "tt1"}, []indexer.Indexer{f}), time.Second)
		return evs[0].ev
	}
	if _, ok := run().(*IndexerDone); !ok {
		t.Fatal("first search not done")
	}
	ev, ok := run().(*IndexerFailed)
	if !ok || !errors.Is(ev.Err, indexer.ErrTimeout) || !strings.Contains(ev.Err.Error(), "rate limit") {
		t.Fatalf("second search: %#v", ev)
	}
	if h, _ := s.Health(1); h.ConsecutiveFailures != 0 || f.calls.Load() != 1 {
		t.Errorf("health = %+v, calls = %d", h, f.calls.Load())
	}
}

func TestSyncRSS(t *testing.T) {
	t.Parallel()
	ix := info(1, 25)
	mk := func(guid string, minute int) indexer.Release {
		r := rel(ix, guid, "")
		r.PublishDate = t0.Add(time.Duration(minute) * time.Minute)
		return r
	}
	f := &fakeIndexer{info: ix}
	feed := []indexer.Release{mk("g4", 4), mk("g3", 3), mk("g2", 2), mk("g1", 1)}
	f.rss = func(context.Context) ([]indexer.Release, error) { return feed, nil }
	clk := &clock{t: t0.Add(time.Hour)}
	s := newTestOrchestrator(Options{Now: clk.now})

	res, err := s.SyncRSS(t.Context(), f, RSSState{LastGUID: "g2", LastPubAt: t0.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Releases) != 2 || res.Releases[0].GUID != "g4" || res.State.LastGUID != "g4" ||
		!res.State.LastPubAt.Equal(t0.Add(4*time.Minute)) || !res.State.LastSyncAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("result = %+v", res)
	}

	f.rss = func(context.Context) ([]indexer.Release, error) {
		return nil, &indexer.Error{Kind: indexer.ErrUnauthorized}
	}
	st := RSSState{LastGUID: "g4"}
	res, err = s.SyncRSS(t.Context(), f, st)
	if !errors.Is(err, indexer.ErrUnauthorized) || res.State != st {
		t.Errorf("error case: %+v, %v", res, err)
	}
}
