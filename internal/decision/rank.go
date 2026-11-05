package decision

import (
	"cmp"
	"math"
	"slices"
	"strings"
)

// Rank returns a copy of evs sorted from best to worst. The order is total and
// deterministic: the result does not depend on the input order. Keys, in order:
//
//  1. accepted before rejected;
//  2. quality rank in the profile, higher first;
//  3. custom score, higher first;
//  4. revision (version, then REAL), higher first;
//  5. preferred protocol first, when prefer is set;
//  6. indexer priority, lower number first;
//  7. seeders, more first; Usenet counts as fully available (above any torrent),
//     unknown seeders as 0. Every key is a property of one release, which is what
//     makes the order transitive;
//  8. size closest to the preferred size first, unknown last;
//  9. publish date, newer first, unknown last: a newer Usenet post is less likely
//     to be incomplete or taken down, a newer torrent is as good once seeders are
//     compared;
//  10. tie-breaks: GUID, title, indexer ID, download URL, info hash, size.
func Rank(evs []Evaluated, prefer Protocol) []Evaluated {
	// Sort indexes: an Evaluated is several hundred bytes, too big to swap around.
	idx := make([]int, len(evs))
	for i := range idx {
		idx[i] = i
	}
	slices.SortFunc(idx, func(i, j int) int { return compareEvaluated(&evs[i], &evs[j], prefer) })
	out := make([]Evaluated, len(evs))
	for k, i := range idx {
		out[k] = evs[i]
	}
	return out
}

// compareEvaluated returns a negative number when a ranks before b.
func compareEvaluated(a, b *Evaluated, prefer Protocol) int {
	da, db := &a.Decision, &b.Decision
	ca, cb := &a.Candidate, &b.Candidate
	if c := cmpBoolFirst(da.Accepted, db.Accepted); c != 0 {
		return c
	}
	if c := cmp.Compare(db.QualityRank, da.QualityRank); c != 0 {
		return c
	}
	if c := cmp.Compare(db.Score.Total, da.Score.Total); c != 0 {
		return c
	}
	if c := CompareRevision(cb.Parsed.Revision, ca.Parsed.Revision); c != 0 {
		return c
	}
	if prefer != "" {
		if c := cmpBoolFirst(ca.Protocol == prefer, cb.Protocol == prefer); c != 0 {
			return c
		}
	}
	if c := cmp.Compare(ca.IndexerPriority, cb.IndexerPriority); c != 0 {
		return c
	}
	if c := cmp.Compare(seedKey(cb), seedKey(ca)); c != 0 {
		return c
	}
	if c := cmpUnknownLast(da.SizeDelta < 0, db.SizeDelta < 0); c != 0 {
		return c
	}
	if c := cmp.Compare(da.SizeDelta, db.SizeDelta); c != 0 {
		return c
	}
	if c := cmpUnknownLast(ca.PublishDate.IsZero(), cb.PublishDate.IsZero()); c != 0 {
		return c
	}
	if c := cb.PublishDate.Compare(ca.PublishDate); c != 0 {
		return c
	}
	return cmp.Or(
		strings.Compare(ca.GUID, cb.GUID),
		strings.Compare(ca.Title, cb.Title),
		cmp.Compare(ca.IndexerID, cb.IndexerID),
		strings.Compare(ca.DownloadURL, cb.DownloadURL),
		strings.Compare(ca.InfoHash, cb.InfoHash),
		cmp.Compare(ca.Size, cb.Size),
		// Fields that the criteria above may not separate (protocol when no
		// preference is set, seeders of Usenet releases): keep the order total.
		strings.Compare(string(ca.Protocol), string(cb.Protocol)),
		cmp.Compare(cb.Seeders, ca.Seeders),
	)
}

// seedKey is the availability of a release for ranking.
func seedKey(c *Candidate) int {
	switch c.Protocol {
	case Usenet:
		return math.MaxInt
	case Torrent:
		return max(c.Seeders, 0)
	default:
		return 0
	}
}

// cmpBoolFirst orders true before false.
func cmpBoolFirst(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return -1
	default:
		return 1
	}
}

// cmpUnknownLast orders known (false) before unknown (true).
func cmpUnknownLast(aUnknown, bUnknown bool) int { return cmpBoolFirst(!aUnknown, !bUnknown) }

// NextBest returns the best accepted release of a ranked list that does not match
// any of the failed entries (releases whose download failed or that were
// blocklisted after the search). Use it to retry after a failed download.
func NextBest(ranked []Evaluated, failed []BlocklistEntry) (Evaluated, bool) {
	hashes := make(map[string]struct{})
	guids := make(map[string]struct{})
	titles := make(map[string]struct{})
	groups := make(map[string]struct{})
	for i := range failed {
		f := &failed[i]
		if f.Kind == BlockGroup {
			addLower(groups, f.ReleaseGroup)
			continue
		}
		addLower(hashes, f.InfoHash)
		addLower(titles, f.SourceTitle)
		if f.GUID != "" {
			guids[f.GUID] = struct{}{}
		}
	}
	for i := range ranked {
		ev := &ranked[i]
		if !ev.Decision.Accepted {
			continue
		}
		if blockReason(&ev.Candidate, hashes, guids, titles, groups) == "" {
			return *ev, true
		}
	}
	return Evaluated{}, false
}
