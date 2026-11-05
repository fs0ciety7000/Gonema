// Package newznab implements indexer.Indexer with the Newznab API, and holds the
// XML layer shared with Torznab (internal/indexer/torznab).
//
// # Specifications used
//
//   - Newznab API documentation, as republished in the Torznab spec site:
//     https://torznab.github.io/spec-1.3-draft/external/newznab/api.html
//     (functions caps/search/movie, response offset/total, predefined
//     categories 2000-2999, attributes, error codes 100-910).
//   - Torznab specification 1.3 draft:
//     https://torznab.github.io/spec-1.3-draft/torznab/Specification-v1.3.html
//     (caps format, supportedParams, offset/limit, extended attributes, tags).
//   - Revisions 1.1 (supportedParams defaults, ORed id parameters), 1.2
//     (downloadvolumefactor/uploadvolumefactor), 1.3 (release tags), 1.4 (api
//     limits): https://torznab.github.io/spec-1.3-draft/revisions/index.html
//
// All fetched on 2026-11-05.
//
// # Behaviour
//
//   - Every request is a GET on BaseURL+APIPath (default /api) with the apikey
//     query parameter. Each request has its own timeout (default 30 s) on top of
//     the caller's context; an optional token bucket paces requests.
//   - Responses are capped (default 10 MiB, checked on Content-Length and while
//     reading). encoding/xml resolves no external entity and processes no DTD;
//     only the static HTML entity table is accepted. Feeds are capped at 5000
//     items and 256 attributes per item; texts are clipped.
//   - t=caps is cached (default 6 h, concurrent calls share one request). When
//     supportedParams is absent the spec defaults apply: search "q",
//     movie-search "q,imdbid".
//   - Search uses t=movie with imdbid (digits, as in the spec examples) and/or
//     tmdbid when movie-search lists them (both together only when listed, the
//     server ORs them), else t=movie&q= when movie-search supports q, else
//     t=search&q=. The text query is "Title Year" without punctuation.
//     Pagination follows offset/limit until a short page, the announced total,
//     or Query.MaxPages.
//   - RSS is t=search without query on the movie categories (configured, else
//     the 2000-2999 categories of caps, else 2000), one page of LimitMax items.
//   - Errors: <error code description/> answers (sent with HTTP 200 by the spec)
//     map to typed errors: 100 and 103-199 -> ErrUnauthorized, 101 ->
//     ErrSuspended, 102 -> ErrForbidden, 200/201 -> ErrBadRequest, 202/203 ->
//     ErrUnsupported, 910 -> ErrDisabled, 429 or a description containing
//     "limit reached" (the code 500 variant used by some indexers) ->
//     ErrRateLimited, others -> ErrServer. HTTP 401/403 -> ErrUnauthorized, 404
//     or an HTML/non-XML body -> ErrNotIndexer, 429 -> ErrRateLimited with
//     Retry-After. The API key is redacted from every error message.
//
// # Mapping
//
// size attr, else <size>, else enclosure length; download URL from the
// enclosure, else link, else magneturl; info URL from comments, else a
// permalink guid; publish date from pubDate, replaced by usenetdate for Usenet
// (retention counts from the post); seeders, leechers (or peers - seeders),
// grabs, infohash (hex, base32 converted, else from the magnet); volume factors
// (negative or > 100 rejected) give the flags freeleech (download 0), halfleech
// (0 < download < 1) and doubleupload (upload >= 2); tag attributes are added
// as flags (internal...); imdb/imdbid -> "tt" id, tmdbid/tmdb -> TMDB id.
package newznab
