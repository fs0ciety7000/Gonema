// Package quality holds the quality catalog, size limits, quality profiles and
// custom scoring rules.
//
// The package is pure: no I/O, no clock, no global mutable state. Every value it
// returns is a fresh copy or immutable (Rule), so it is safe for concurrent use.
//
// # Catalog
//
// The catalog of qualities is fixed (see the ID constants). FromParsed maps a
// parser.ParsedRelease to one of them:
//
//   - cam, telesync, telecine and screener ignore the resolution;
//   - DVD is dvd, dvd-r (full DVD: DVDR, DVD5, DVD9) or regional (R5);
//   - HDTV maps by resolution; 480p, 576p or no resolution is sdtv; a raw
//     transport stream is rawhd;
//   - WEBRip, WEB-DL and Bluray map by resolution; no resolution means an SD
//     encode (480p): "BDRip.x264" is bluray-480p, "WEB.H264" webdl-480p;
//   - remuxes and full discs are 1080p unless tagged 2160p;
//   - an unknown source is unknown, whatever the resolution.
//
// # Profiles
//
// A Profile lists items from most to least preferred. An item is one quality or a
// named group of qualities the profile treats as equivalent; the rank of a quality
// in a profile is len(Items) - index, so qualities of one group share a rank and a
// quality absent from the profile has rank 0. Profile.Validate reports every
// problem at once (duplicate qualities, cutoff not allowed, unknown languages,
// scores for unknown rules...).
//
// # Scoring
//
// A Rule is a named set of conditions, built by NewRule which validates every
// value and compiles title_regex patterns (case-insensitive RE2). A rule matches
// when:
//
//  1. every Required condition matches, and
//  2. for each condition kind present among the non-required conditions, at least
//     one non-required condition of that kind matches.
//
// Negate inverts one condition before it is combined. Conditions of one kind are
// therefore alternatives (OR) and distinct kinds are all needed (AND), which is
// what users expect from "release group A, B or C" and "h265 at 720p or 1080p".
// A rule whose conditions are all Required is a plain AND.
//
// Profiles give each rule a score (Profile.Scores, by rule name). Score sums the
// scores of the matched rules and returns a Breakdown listing each matched rule
// and its contribution.
//
// # Defaults
//
// DefaultSizes, StarterRules and StarterProfiles are the defaults seeded into a
// new installation. They are only starting points: users edit them freely.
package quality
