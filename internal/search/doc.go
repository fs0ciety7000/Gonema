// Package search fans movie searches out to indexers and streams the results.
//
// # Failure isolation
//
// Every indexer runs in its own goroutine with its own deadline (default 15 s),
// token bucket (default 1 request/s, burst 3) and circuit breaker. The indexer
// call itself runs in a further goroutine, so an implementation that ignores
// its context cannot hold a search past the deadline. A slow or failing indexer
// never delays the batches of the others: each batch is sent as soon as its
// indexer answers.
//
// # Streaming
//
// Search returns a channel of events: *ResultBatch (new releases of one
// indexer, plus duplicates of releases already sent), *IndexerDone,
// *IndexerFailed (with the end of the breaker's open period) and, last,
// *Finished (all results merged). The channel is always closed; canceling the
// context cancels the requests in flight and closes it without Finished.
//
// # Deduplication
//
// Releases are identified by info hash when known, else by GUID (Key). The first
// copy received is streamed; later copies from other indexers are reported as
// Duplicates. Finished.Results keeps the copy of the preferred indexer (lowest
// priority value, then most seeders) with every source in Release.Sources.
//
// # Circuit breaker
//
// Breaker is a pure state machine (the time is a parameter). Closed -> Open
// after Threshold consecutive failures (default 3), or at once on a rate-limit
// answer (for at least its Retry-After). The open period starts at 5 min and
// doubles at each consecutive open, capped at 6 h. When it ends, one probe
// request is let through (half-open): success closes and resets the escalation,
// failure reopens for the next step. Caller cancellation, the client-side rate
// limit and invalid queries are not counted. Health exposes the state for the
// indexers table (failure_count, disabled_until, last_error, last_ok_at);
// Restore reloads it at startup.
//
// # RSS
//
// DiffRSS is pure: it returns the releases of a feed new since an RSSState
// (last GUID, then last publish date) and the next state; SyncRSS fetches the
// feed under the breaker, rate limit and deadline. Persisting the state is the
// caller's job.
package search
