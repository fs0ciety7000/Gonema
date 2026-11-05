// Package tmdb implements metadata.Provider with The Movie Database API v3
// (https://developer.themoviedb.org/reference).
//
// Every Gonema user brings their own TMDB credential. Both kinds TMDB issues are
// accepted and told apart by shape: the "API Read Access Token" (a JWT, starts
// with "eyJ") is sent as "Authorization: Bearer", the v3 "API Key" (32 hex
// characters) as the api_key query parameter. ValidateKey checks a credential
// during onboarding.
//
// # Behaviour
//
//   - Every call takes a context; each HTTP attempt has its own timeout.
//   - A token bucket (default 20 requests/s, burst 10) is shared by all goroutines
//     using the client. TMDB's documented ceiling is about 40 requests/s.
//   - 429 answers are retried after Retry-After (seconds or HTTP date) when it is
//     at most Options.MaxRetryAfter; 5xx answers, timeouts and transport errors
//     are retried with exponential backoff and jitter, up to Options.MaxAttempts.
//     401 and 404 are never retried.
//   - Successful responses are cached in memory (TTL, entry and byte bounds),
//     keyed by path and query without credentials; concurrent identical requests
//     share one HTTP call (singleflight). /configuration is cached for a day.
//   - Credentials never appear in errors or logs: api_key is redacted from URLs
//     and any echo of the credential is replaced by "REDACTED".
//
// # Mapping decisions
//
//   - Details use append_to_response=credits,videos,alternative_titles,
//     release_dates,external_ids,keywords (one request per movie).
//   - Release dates: see metadata.SelectReleases (configured region, then US, then
//     earliest; theatrical 3 -> cinema with fallback to 2 then 1; 4 digital;
//     5 physical). Status: metadata.DeriveStatus with the client clock.
//   - Ratings: TMDB vote_average/vote_count. TMDB does not publish IMDb ratings, so
//     Ratings.IMDb is always nil here.
//   - Videos: YouTube trailers and teasers only; trailers first, official first,
//     then higher resolution, then most recent.
//   - Search uses the "year" parameter (any release date of the movie, any
//     region), which tolerates releases named after a regional year.
//   - Region is sent to the upcoming and now_playing lists, where TMDB uses it to
//     pick the release dates; popular and trending are global.
package tmdb
