package search

import (
	"slices"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/indexer"
)

func TestDiffRSS(t *testing.T) {
	t.Parallel()
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	item := func(guid string, m int) indexer.Release {
		r := indexer.Release{GUID: guid, Title: guid}
		if m >= 0 {
			r.PublishDate = at(m)
		}
		return r
	}
	now := at(100)
	tests := []struct {
		name     string
		feed     []indexer.Release
		st       RSSState
		want     []string
		wantGUID string
		wantPub  time.Time
		gap      bool
	}{
		{"first sync takes all", []indexer.Release{item("c", 3), item("b", 2), item("a", 1)},
			RSSState{}, []string{"c", "b", "a"}, "c", at(3), false},
		{"items before the last guid", []indexer.Release{item("d", 4), item("c", 3), item("b", 2)},
			RSSState{LastGUID: "c", LastPubAt: at(3)}, []string{"d"}, "d", at(4), false},
		{"nothing new", []indexer.Release{item("c", 3), item("b", 2)},
			RSSState{LastGUID: "c", LastPubAt: at(3)}, nil, "c", at(3), false},
		{"unsorted feed is sorted by date", []indexer.Release{item("b", 2), item("d", 4), item("c", 3)},
			RSSState{LastGUID: "c", LastPubAt: at(3)}, []string{"d"}, "d", at(4), false},
		{"guid gone: by date", []indexer.Release{item("e", 5), item("d", 4), item("b", 2)},
			RSSState{LastGUID: "c", LastPubAt: at(3)}, []string{"e", "d"}, "e", at(5), false},
		{"gap: the whole feed is newer", []indexer.Release{item("f", 6), item("e", 5)},
			RSSState{LastGUID: "c", LastPubAt: at(3)}, []string{"f", "e"}, "f", at(6), true},
		{"duplicates in feed", []indexer.Release{item("d", 4), item("d", 4), item("c", 3)},
			RSSState{LastGUID: "c", LastPubAt: at(3)}, []string{"d"}, "d", at(4), false},
		{"undated feed keeps feed order", []indexer.Release{item("y", -1), item("x", -1), item("w", -1)},
			RSSState{LastGUID: "x"}, []string{"y"}, "y", time.Time{}, false},
		{"empty feed keeps the state", nil,
			RSSState{LastGUID: "c", LastPubAt: at(3)}, nil, "c", at(3), false},
		{"older items never move the date back", []indexer.Release{item("z", 1)},
			RSSState{LastGUID: "c", LastPubAt: at(3)}, nil, "z", at(3), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := DiffRSS(tt.feed, tt.st, now)
			var got []string
			for _, r := range res.Releases {
				got = append(got, r.GUID)
			}
			if !slices.Equal(got, tt.want) || res.State.LastGUID != tt.wantGUID ||
				!res.State.LastPubAt.Equal(tt.wantPub) || !res.State.LastSyncAt.Equal(now) || res.Gap != tt.gap {
				t.Errorf("got %v %+v gap=%v, want %v guid %s pub %v gap=%v", got, res.State, res.Gap, tt.want, tt.wantGUID, tt.wantPub, tt.gap)
			}
		})
	}
}
