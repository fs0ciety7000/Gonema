package search

import (
	"context"
	"slices"
	"time"

	"github.com/fs0ciety7000/gonema/internal/indexer"
)

// RSSState is the RSS position of an indexer (rss_state table).
type RSSState struct {
	LastGUID   string    `json:"lastGuid"`
	LastPubAt  time.Time `json:"lastPubAt"`
	LastSyncAt time.Time `json:"lastSyncAt"`
}

// RSSResult is the outcome of an RSS sync.
type RSSResult struct {
	// Releases are the releases new since the previous state, newest first.
	Releases []indexer.Release
	// State is the state to persist once the releases are processed.
	State RSSState
	// Gap is true when the previous position is no longer in the feed and even
	// the oldest item is newer than it: releases may have been missed (the feed
	// is too short for the sync interval).
	Gap bool
}

// DiffRSS returns the releases of feed new since st, and the next state. It is
// pure. The feed is ordered newest first by publish date when every item has
// one, else kept in feed order (Newznab feeds are newest first). The first sync
// (empty state) returns the whole feed. When st.LastGUID is in the feed, the
// items before it are new; otherwise the items published after st.LastPubAt
// (or without date) are.
func DiffRSS(feed []indexer.Release, st RSSState, now time.Time) RSSResult {
	items := make([]indexer.Release, 0, len(feed))
	seen := make(map[string]struct{}, len(feed))
	dated := true
	for i := range feed {
		if _, dup := seen[feed[i].GUID]; dup {
			continue
		}
		seen[feed[i].GUID] = struct{}{}
		items = append(items, feed[i])
		dated = dated && !feed[i].PublishDate.IsZero()
	}
	if dated {
		slices.SortStableFunc(items, func(a, b indexer.Release) int { return b.PublishDate.Compare(a.PublishDate) })
	}

	res := RSSResult{State: st}
	res.State.LastSyncAt = now
	if len(items) == 0 {
		return res
	}
	res.State.LastGUID = items[0].GUID
	for i := range items {
		if items[i].PublishDate.After(res.State.LastPubAt) {
			res.State.LastPubAt = items[i].PublishDate
		}
	}

	switch at := slices.IndexFunc(items, func(r indexer.Release) bool { return r.GUID == st.LastGUID }); {
	case st.LastGUID == "" && st.LastPubAt.IsZero():
		res.Releases = items
	case at >= 0:
		res.Releases = items[:at]
	default:
		for i := range items {
			if items[i].PublishDate.IsZero() || items[i].PublishDate.After(st.LastPubAt) {
				res.Releases = append(res.Releases, items[i])
			}
		}
		oldest := items[len(items)-1].PublishDate
		res.Gap = !st.LastPubAt.IsZero() && dated && oldest.After(st.LastPubAt)
	}
	if len(res.Releases) == 0 {
		res.Releases = nil
	}
	return res
}

// SyncRSS fetches the RSS feed of ix under its breaker, rate limit and timeout,
// and returns the releases new since st with the next state. Persisting the
// state is the caller's job, after processing the releases. On error the state
// is unchanged.
func (s *Orchestrator) SyncRSS(ctx context.Context, ix indexer.Indexer, st RSSState) (RSSResult, error) {
	oc := s.call(ctx, ix.Info(), ix.RSS)
	if oc.err != nil {
		return RSSResult{State: st}, oc.err
	}
	return DiffRSS(oc.releases, st, s.now()), nil
}
