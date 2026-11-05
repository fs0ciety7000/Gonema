// Package indexer defines the provider-agnostic model of a release search source
// (Usenet or BitTorrent indexer) and the Indexer interface implemented by the
// Newznab (internal/indexer/newznab) and Torznab (internal/indexer/torznab)
// adapters.
//
// A Release carries everything the decision engine needs (decision.Candidate):
// title, size, GUID, info and download URLs, publish date, indexer identity and
// priority, protocol, seeders and leechers, info hash, indexer flags (freeleech,
// internal...), categories, and the TMDB and IMDb ids when the indexer gives them.
// Release.Candidate converts it.
//
// # Errors
//
// Adapters return *Error values. Each one matches exactly one sentinel with
// errors.Is (ErrUnauthorized, ErrRateLimited, ErrNotIndexer...), and its text is a
// plain-language sentence meant to be shown during onboarding ("the indexer
// rejected the API key"). Credentials never appear in error texts: the API key is
// redacted from URLs and from any message echoed by the indexer. RetryAfter gives
// the delay advised by a rate-limited indexer.
package indexer
