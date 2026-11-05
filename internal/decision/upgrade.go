package decision

import (
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

// CompareRevision orders revisions: higher version first, then REAL over not REAL.
// It returns -1, 0 or +1 like cmp.Compare.
func CompareRevision(a, b parser.Revision) int {
	switch {
	case a.Version != b.Version:
		if a.Version < b.Version {
			return -1
		}
		return 1
	case a.Real == b.Real:
		return 0
	case b.Real:
		return -1
	default:
		return 1
	}
}

// CutoffMet reports whether the current file is final for the profile: its
// quality rank is at least the cutoff rank AND its custom score reaches
// CutoffScore. A quality absent from the profile never meets the cutoff.
func CutoffMet(cur FileQuality, p *quality.Profile) bool {
	rank, _ := p.Rank(cur.Quality)
	return rank > 0 && rank >= p.CutoffRank() && cur.CustomScore >= p.CutoffScore
}

// IsUpgrade reports whether cand should replace cur, and why. In order:
//
//  1. Upgrades disabled: never.
//  2. Higher quality rank in the profile: an upgrade unless the cutoff is met.
//  3. Lower quality rank: never.
//  4. Same quality (same ID) and a higher revision (PROPER, REPACK, REAL): an
//     upgrade, even once the cutoff is met, since it fixes the same content.
//     A lower revision is never an upgrade, whatever the score.
//  5. Same rank otherwise (same quality or one group): an upgrade when the cutoff
//     is not met and the score beats the current one by at least MinUpgradeStep.
//
// IsUpgrade is irreflexive: a file never upgrades itself.
func IsUpgrade(cur, cand FileQuality, p *quality.Profile) (UpgradeReason, bool) {
	if !p.UpgradeAllowed {
		return "", false
	}
	rc, _ := p.Rank(cur.Quality)
	rn, _ := p.Rank(cand.Quality)
	switch {
	case rn > rc:
		if CutoffMet(cur, p) {
			return "", false
		}
		return UpgradeQuality, true
	case rn < rc:
		return "", false
	}
	if cand.Quality == cur.Quality {
		switch CompareRevision(cand.Revision, cur.Revision) {
		case 1:
			return UpgradeRevision, true
		case -1:
			return "", false
		}
	}
	if !CutoffMet(cur, p) && cand.CustomScore >= cur.CustomScore+p.UpgradeStep() {
		return UpgradeScore, true
	}
	return "", false
}
