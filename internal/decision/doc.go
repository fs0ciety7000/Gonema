// Package decision decides whether a release should be grabbed for a movie, why
// not, whether it upgrades the current file, and how releases rank.
//
// The package is pure: no I/O, no clock (Options.Now is passed in), no global
// mutable state. An Evaluator is immutable and safe for concurrent use.
//
// # Evaluation
//
// NewEvaluator validates a Config (movie, profile, rules, sizes, current file,
// blocklist, indexer settings, options) once per search; Evaluate then decides on
// each candidate. Evaluate never stops at the first problem: it collects every
// applicable Rejection, in the order of the Code constants, so the user sees all
// reasons at once. Each rejection has a stable Code, a specific plain-English
// Message and a Permanent flag (false when the reason may go away by itself:
// availability date, seeders, queue, Usenet minimum age).
//
// Checks, in order:
//
//   - title_mismatch: the clean release title (or one of its AKA titles) must equal
//     the clean movie title or one of its alternate titles (parser.CleanTitle).
//   - year_mismatch: the release year must be within YearTolerance (±1) of the
//     movie year. A release without a year passes with a note.
//   - unknown_quality, quality_not_allowed: the quality (quality.FromParsed) must be
//     known and allowed by the profile.
//   - wrong_language: see Languages below.
//   - hardcoded_subs: releases with burned-in subtitles (parser.HardcodedSubs) are
//     rejected unless Options.AllowHardcodedSubs. This is an explicit check rather
//     than a scoring rule because hardcoded subtitles cannot be removed: they are a
//     hard no for most users, and the parser already detects them reliably. The
//     starter rule "Hardcoded subs" (a small penalty) then ranks them last when
//     they are allowed.
//   - below_min_score: the custom score must reach Profile.MinScore.
//   - size_too_small, size_too_large: size limits are per minute of runtime; the
//     check is skipped, with a note, when the size or the runtime is unknown.
//   - blocklisted: by info hash, indexer GUID or exact title (case-insensitive) for
//     release entries, by release group for group entries.
//   - not_monitored: automatic searches only.
//   - not_available: see Available.
//   - not_enough_seeders: torrents below the indexer minimum (DefaultMinSeeders
//     when the indexer has no settings); unknown (negative) seeders pass.
//   - minimum_age, retention: Usenet posts younger than UsenetMinimumAge or older
//     than UsenetRetentionDays.
//   - already_grabbed: set by the caller (queue and grab history).
//   - upgrades_disabled, cutoff_met, not_upgrade: when the movie has a file; see
//     IsUpgrade.
//
// With Options.Override (a manual grab), the rejections are kept and the
// decision is marked Overridden: Accepted stays false, Grabbable is true.
//
// # Languages
//
// Profile languages are BCP 47 codes, "original" (the original language of the
// movie) or "any". The audio languages of a release are its tagged languages;
// MULTi/DL/DUAL releases also carry the original language; an untagged release
// is assumed to be in the original language, or English when that is unknown
// (scene convention: only foreign-language releases are tagged). A release is
// wanted when one of its languages is wanted. When "original" is wanted but the
// original language is unknown, untagged and multi-language releases pass.
//
// # Upgrades
//
// IsUpgrade compares the quality rank in the profile first, then the revision of
// the same quality (a PROPER or REPACK of the same quality is an upgrade, even
// past the cutoff, because it fixes the same content), then the custom score,
// which must improve by at least MinUpgradeStep. CutoffMet requires both the
// cutoff quality rank and CutoffScore. With upgrades disabled, nothing replaces
// an existing file, propers included. A release carrying a wanted language also
// upgrades a file that has none of the wanted languages.
//
// # Ranking
//
// Rank sorts evaluated releases by a total, deterministic order (see Rank);
// NextBest picks the next release to try after a failed download.
package decision
