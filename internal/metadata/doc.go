// Package metadata defines the provider-agnostic movie metadata model used by
// Gonema and the Provider interface implemented by metadata sources (TMDB in
// internal/metadata/tmdb).
//
// The types carry everything the movies, alternate_titles and collections tables
// need (docs/data-model.md): identifiers, titles, dates per release type,
// certification, ratings, image paths, and an Extra block (cast, crew, videos,
// keywords) stored as JSON for display only.
//
// The functions of this package are pure: release date selection and status
// derivation take the current time as a parameter.
//
// # Errors
//
// Providers return errors that match, with errors.Is:
//   - ErrNotFound: the movie, collection or person does not exist (or was removed).
//   - ErrUnauthorized: the credential is missing, malformed or rejected. The error
//     text is meant to be shown during onboarding.
//   - ErrRateLimited: the provider still refused after retries; errors.As with
//     *RateLimitError gives the advised delay.
//
// Other failures (network, timeouts, 5xx after retries, invalid JSON) are wrapped
// with context and never contain credentials.
package metadata
