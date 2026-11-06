package library

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// EventLibraryReindexed is published after a full reload of the library index
// during which readers were served the previous version: lists fetched in that
// window (a few hundred milliseconds) may be stale and should be fetched again.
const EventLibraryReindexed events.Type = "library.reindexed"

// maxDeltaMovies is the number of changed movies above which the index is
// reloaded in full rather than patched.
const maxDeltaMovies = 1000

// index returns the current library index, refreshing it when library_version
// moved. Readers never wait for another reader's refresh:
//
//   - per-movie changes (library_changes, migration 00008) are applied as a
//     delta: the few changed rows are read and merged into a copy of the
//     index, then swapped in atomically. Each reader that sees a newer version
//     computes it (a few milliseconds), so its own writes are always visible;
//   - a full reload (first load, quality profile edits, too many or pruned
//     changes) runs in one reader at a time; readers arriving meanwhile get the
//     previous index at once, and EventLibraryReindexed tells clients to fetch
//     again once the reload is done.
func (s *Service) index(ctx context.Context) (*libraryIndex, error) {
	v, err := s.st.Read().GetLibraryVersion(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("library: index version: %w", err))
	}
	cur := s.idx.Load()
	if cur != nil && cur.version >= v {
		return cur, nil
	}
	if cur != nil {
		next, err := s.deltaIndex(ctx, cur)
		if err != nil {
			return nil, err
		}
		if next != nil {
			return s.swapIndex(next), nil
		}
		// A full reload is needed. Serve the previous index if another
		// reader is already reloading.
		if !s.rebuildMu.TryLock() {
			s.staleServed.Store(true)
			return cur, nil
		}
	} else {
		s.rebuildMu.Lock() // nothing to serve yet: the first load is awaited
	}
	defer s.rebuildMu.Unlock()
	if now := s.idx.Load(); now != nil && now.version >= v {
		return now, nil // reloaded while this reader waited for the lock
	}
	next, err := s.loadIndex(ctx)
	if err != nil {
		return nil, err
	}
	out := s.swapIndex(next)
	if s.staleServed.Swap(false) && s.bus != nil {
		s.bus.Publish(events.Event{Type: EventLibraryReindexed, Data: map[string]any{"version": out.version}})
	}
	return out, nil
}

// swapIndex installs next unless a newer index is already installed, and
// returns the installed one.
func (s *Service) swapIndex(next *libraryIndex) *libraryIndex {
	for {
		old := s.idx.Load()
		if old != nil && old.version >= next.version {
			return old
		}
		if s.idx.CompareAndSwap(old, next) {
			return next
		}
	}
}

// loadIndex reads every movie into a new index.
func (s *Service) loadIndex(ctx context.Context) (*libraryIndex, error) {
	idx := &libraryIndex{tags: map[int64][]int64{}}
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		var err error
		if idx.version, err = q.GetLibraryVersion(ctx); err != nil {
			return fmt.Errorf("version: %w", err)
		}
		head, err := q.GetLibraryChangesHead(ctx)
		if err != nil {
			return fmt.Errorf("changes head: %w", err)
		}
		idx.seq = head.Head
		rows, err := q.ListLibraryIndex(ctx)
		if err != nil {
			return fmt.Errorf("rows: %w", err)
		}
		failed, err := q.ListLastFailedDownloads(ctx)
		if err != nil {
			return fmt.Errorf("failed downloads: %w", err)
		}
		failedAt := make(map[int64]int64, len(failed))
		for _, f := range failed {
			failedAt[f.MovieID] = f.GrabbedAt
		}
		idx.rows = make([]MovieSummary, len(rows))
		for i := range rows {
			idx.rows[i].ListLibraryIndexRow = rows[i]
			setFailed(&idx.rows[i], failedAt)
		}
		tags, err := q.ListAllMovieTags(ctx)
		if err != nil {
			return fmt.Errorf("tags: %w", err)
		}
		for _, t := range tags {
			idx.tags[t.MovieID] = append(idx.tags[t.MovieID], t.TagID)
		}
		for i := range idx.rows {
			idx.rows[i].TagIDs = idx.tags[idx.rows[i].ID]
		}
		return nil
	})
	if err != nil {
		return nil, store.MapError(fmt.Errorf("library: load the movie index: %w", err))
	}
	return idx, nil
}

func setFailed(r *MovieSummary, failedAt map[int64]int64) {
	if at, ok := failedAt[r.ID]; ok && r.State != StateDownloading {
		r.LastDownloadFailed = r.FileImportedAt == nil || at > *r.FileImportedAt
	}
}

// deltaIndex returns cur patched with the movies changed since cur.seq, or nil
// when only a full reload can bring it up to date.
func (s *Service) deltaIndex(ctx context.Context, cur *libraryIndex) (*libraryIndex, error) {
	var next *libraryIndex
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		v, err := q.GetLibraryVersion(ctx)
		if err != nil {
			return fmt.Errorf("version: %w", err)
		}
		head, err := q.GetLibraryChangesHead(ctx)
		if err != nil {
			return fmt.Errorf("changes head: %w", err)
		}
		if head.Head <= cur.seq || head.Tail > cur.seq+1 {
			// No change recorded although the version moved (a change source
			// without a delta trigger), or changes were pruned: reload.
			return nil
		}
		changes, err := q.ListLibraryChangesSince(ctx, db.ListLibraryChangesSinceParams{After: cur.seq, Limit: maxDeltaMovies * 4})
		if err != nil {
			return fmt.Errorf("changes: %w", err)
		}
		if int64(len(changes)) == maxDeltaMovies*4 {
			return nil
		}
		ids := make([]int64, 0, len(changes))
		seq := cur.seq
		for _, c := range changes {
			if c.MovieID == nil {
				return nil // every movie may have changed
			}
			ids = append(ids, *c.MovieID)
			seq = max(seq, c.Seq)
		}
		slices.Sort(ids)
		ids = slices.Compact(ids)
		if len(ids) > maxDeltaMovies {
			return nil
		}
		next, err = patchIndex(ctx, q, cur, ids)
		if err != nil {
			return err
		}
		next.version, next.seq = v, seq
		return nil
	})
	if err != nil {
		return nil, store.MapError(fmt.Errorf("library: update the movie index: %w", err))
	}
	return next, nil
}

// patchIndex returns a copy of cur where the rows of ids (sorted, unique) are
// read again: replaced, inserted (new movie) or removed (deleted movie). cur
// is not modified: pages already returned keep pointing at its rows.
func patchIndex(ctx context.Context, q *db.Queries, cur *libraryIndex, ids []int64) (*libraryIndex, error) {
	list := idList(ids)
	rows, err := q.ListLibraryIndexByIDs(ctx, list)
	if err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	failed, err := q.ListLastFailedDownloadsByIDs(ctx, list)
	if err != nil {
		return nil, fmt.Errorf("failed downloads: %w", err)
	}
	failedAt := make(map[int64]int64, len(failed))
	for _, f := range failed {
		failedAt[f.MovieID] = f.GrabbedAt
	}
	tagRows, err := q.ListMovieTagsByIDs(ctx, list)
	if err != nil {
		return nil, fmt.Errorf("tags: %w", err)
	}
	next := &libraryIndex{tags: make(map[int64][]int64, len(cur.tags)+len(ids))}
	for id, t := range cur.tags {
		next.tags[id] = t
	}
	for _, id := range ids {
		delete(next.tags, id)
	}
	for _, t := range tagRows {
		next.tags[t.MovieID] = append(next.tags[t.MovieID], t.TagID)
	}
	fresh := make([]MovieSummary, len(rows))
	for i := range rows {
		fresh[i].ListLibraryIndexRow = db.ListLibraryIndexRow(rows[i])
		setFailed(&fresh[i], failedAt)
		fresh[i].TagIDs = next.tags[fresh[i].ID]
	}

	// Merge by id: cur.rows and fresh are in id order; ids lists the rows to
	// drop from cur (replaced by fresh, or deleted).
	out := make([]MovieSummary, 0, len(cur.rows)+len(fresh))
	i, j, k := 0, 0, 0
	for i < len(cur.rows) || j < len(fresh) {
		switch {
		case j < len(fresh) && (i == len(cur.rows) || fresh[j].ID <= cur.rows[i].ID):
			if i < len(cur.rows) && cur.rows[i].ID == fresh[j].ID {
				i++
			}
			out = append(out, fresh[j])
			j++
		default:
			id := cur.rows[i].ID
			for k < len(ids) && ids[k] < id {
				k++
			}
			if k == len(ids) || ids[k] != id {
				out = append(out, cur.rows[i])
			}
			i++
		}
	}
	next.rows = out
	return next, nil
}

// idList encodes ids as a JSON array for the json_each queries.
func idList(ids []int64) string {
	b := make([]byte, 0, len(ids)*6+2)
	b = append(b, '[')
	for i, id := range ids {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendInt(b, id, 10)
	}
	return string(append(b, ']'))
}
