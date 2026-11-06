# TMDB fixtures

```json
{"_fixture": "handwritten-from-docs"}
```

No TMDB credential was available when these fixtures were written (the API
answered 401), so **none of them is a recorded response**. They are built from the
example responses published in the official reference
(https://developer.themoviedb.org/reference, OpenAPI examples fetched on
2026-11-05), trimmed to keep the files small:

| File | Source in the reference |
|---|---|
| `configuration.json` | configuration-details |
| `authentication.json` | authentication-validate-key |
| `search_fight_club.json` | search-movie (first 5 results) |
| `movie_550.json` | movie-details shape; top-level values of Fight Club taken from the search-movie example and completed by hand (genres, runtime, imdb_id, tagline, homepage); appended `credits` (first 6 cast and crew), `videos`, `alternative_titles`, `release_dates`, `external_ids`, `keywords` are the movie-* examples for id 550. Three extra videos were added by hand to exercise filtering: a YouTube teaser, a YouTube featurette and a Vimeo trailer. |
| `find_tt0137523.json` | find-by-id (the documented example returns Scream VI) |
| `find_empty.json` | find-by-id shape, empty result |
| `collection_10.json` | collection-details (parts use `name`/`original_name` as documented) |
| `person_31_movie_credits.json` | person-movie-credits (first 8 cast, 6 crew) |
| `person_31.json` | person-details (Tom Hanks, trimmed) |
| `search_person.json` | search-person shape: Tom Hanks with movie and TV `known_for` entries, plus a hand-written second result without known movies |
| `popular.json`, `now_playing.json`, `upcoming.json`, `trending_week.json` | the list examples (first 4 results) |
| `error_401.json`, `error_404.json`, `error_429.json` | status codes 7, 34 and 25 of the errors page |

When a credential is available, `TestLive*` (opt-in with `GONEMA_TMDB_LIVE=1`)
checks the client against the real API. Recorded fixtures must never contain the
credential (responses carry none; request URLs are not stored).
