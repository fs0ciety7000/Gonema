// Package parser turns a release or file name into a ParsedRelease.
//
// The package is pure: no I/O, no clock, no global mutable state. Parse is
// deterministic, never panics and never fails; fields it cannot read keep their
// zero value. It is safe for concurrent use.
//
// # Design
//
// Parsing uses a hand-written tokenizer and immutable lookup tables instead of
// regular expressions. Release names are short sequences of words drawn from a
// closed vocabulary (1080p, BluRay, x264, DDP5.1...), so one pass that splits on
// separators and looks every word up in a map is both simpler to extend and much
// faster than a cascade of regexes: about 2.5 µs and a handful of small
// allocations per name on the corpus, against a 50 µs budget. All scratch space
// (tokens, matches) lives on the stack.
//
// Parse runs in four steps:
//
//  1. Strip what is not part of the name: directories, a known file extension
//     (Container), a leading "[Group]" (anime), trailing "[...]" blocks (CRC
//     Hash, tracker tags such as [rarbg], or a group such as [YTS.MX]), posting
//     suffixes (-Obfuscated, -postbot, -xpost, -Rakuten...) and the release
//     group after the last hyphen. Usenet obfuscated names (random hex or base62
//     strings) stop here and yield an empty release.
//  2. Split the rest into tokens on . _ - space and brackets, and match tags
//     greedily (three-token, two-token, then single-token tables, plus a few
//     computed families: glued channels "DDP5.1", "v2", "REPACK2", "25th").
//  3. Find the title and the year. The first strong tag (resolution, source,
//     codec, audio, HDR) starts the technical part; ambiguous words (WEB, TS,
//     CAM, 4K) count only when followed by another tag. The year is the last
//     year-like token (1900..2100) before the technical part, never the first
//     token, so "2001.A.Space.Odyssey.1968", "Blade.Runner.2049.2017" and
//     "1917.2019" work; without one, the first year after it is used
//     ("...BDRip.x264.2001"). The title is everything before the year (or the
//     technical part), minus weak tags glued to its end
//     ("Inception.German.2010" -> "Inception"). An "AKA" splits alternate titles.
//  4. Apply every tag after the title and resolve Source and Modifier by
//     priority (cam > telesync > telecine > screener > bluray > webrip > webdl >
//     hdtv > dvd; a streaming service alone implies webdl).
//
// # Decisions
//
//   - Languages are only those tagged. An untagged name has no language and
//     LanguageInferred is false: the parser never assumes English.
//   - MULTi, DL, DUAL and ML set Multi. "<LANG>.SUBBED" / "<LANG>.SUBS" are
//     subtitles, not audio languages.
//   - Hardcoded subtitles: HC, HCSUB(S), HARDSUB(S), SUBFRENCH (French scene
//     rule) and the scene <lang>SUB tags (KORSUB, SWESUB, FINSUB...). VOSTFR,
//     SUBBED and MULTISUBS are soft subtitles (Subs).
//   - A bare "HDR" is HDR10. Bit depth is reported only when tagged.
//   - UHD and 4K imply 2160p only when no explicit resolution is present.
//   - BluRay with an untouched codec tag (AVC, HEVC, VC-1, MPEG-2) and no
//     encoder tag (x264, x265...) is a remux in German scene naming (German
//     language present) and a full disc (brdisk) otherwise. COMPLETE.BLURAY,
//     BDMV, BD25/50 and ISO are full discs. DVDR, DVD5 and DVD9 are full DVDs.
//   - Revision.Version starts at 1; each PROPER, REPACK or RERIP adds one
//     (REPACK2 adds two); "v2" sets it. Real is set by REAL.PROPER and friends.
//   - Streaming tags that are common words (MAX, NOW, STAN, MA) are only read
//     after the title; iT and iP are case-sensitive.
//
// # Known limitations
//
//   - A title ending with a language word loses it when followed by the year
//     ("The.Good.German.2006" -> "The Good").
//   - A title containing a year, without a release year, takes it as the year
//     ("Blade.Runner.2049.GERMAN.DL..." -> "Blade Runner", 2049).
//   - Without a year, a title word that is also a tag ends the title early.
//   - Paths are split on / and \, so a name containing a slash loses its prefix.
package parser
