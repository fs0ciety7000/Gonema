-- Queries of the library service (internal/library) and the API.

-- ListMoviesFilteredByTitle is keyset-paginated by (sort_title, id) ascending: pass
-- the last (sort_title, id) of the previous page, or ('', 0) first. Every filter is
-- optional: NULL disables it. year_min / year_max are always applied (0 / 9999 to
-- disable).
-- name: ListMoviesFilteredByTitle :many
SELECT m.id, m.tmdb_id, m.imdb_id, m.title, m.sort_title, m.year, m.status, m.runtime_min,
       m.poster_path, m.dominant_color, m.genres, m.monitored, m.quality_profile_id,
       m.minimum_availability, m.root_folder_id, m.path, m.added_at, m.collection_id, s.state
FROM movies m
JOIN movie_state s ON s.movie_id = m.id
WHERE (CAST(sqlc.narg(monitored) AS BOOLEAN) IS NULL OR m.monitored = CAST(sqlc.narg(monitored) AS BOOLEAN))
  AND (CAST(sqlc.narg(status) AS TEXT) IS NULL OR m.status = CAST(sqlc.narg(status) AS TEXT))
  AND (CAST(sqlc.narg(root_folder_id) AS INTEGER) IS NULL OR m.root_folder_id = CAST(sqlc.narg(root_folder_id) AS INTEGER))
  AND (CAST(sqlc.narg(genre) AS TEXT) IS NULL OR EXISTS (SELECT 1 FROM json_each(m.genres) g WHERE g.value = CAST(sqlc.narg(genre) AS TEXT)))
  AND m.year BETWEEN CAST(sqlc.arg(year_min) AS INTEGER) AND CAST(sqlc.arg(year_max) AS INTEGER)
  AND (m.sort_title > CAST(sqlc.arg(after_sort_title) AS TEXT)
       OR (m.sort_title = CAST(sqlc.arg(after_sort_title) AS TEXT) AND m.id > CAST(sqlc.arg(after_id) AS INTEGER)))
ORDER BY m.sort_title, m.id
LIMIT sqlc.arg(limit);

-- ListMoviesFilteredByYear is keyset-paginated by (year, id) descending (newest
-- first): pass the last (year, id) of the previous page, or (9999, MaxInt64) first.
-- name: ListMoviesFilteredByYear :many
SELECT m.id, m.tmdb_id, m.imdb_id, m.title, m.sort_title, m.year, m.status, m.runtime_min,
       m.poster_path, m.dominant_color, m.genres, m.monitored, m.quality_profile_id,
       m.minimum_availability, m.root_folder_id, m.path, m.added_at, m.collection_id, s.state
FROM movies m
JOIN movie_state s ON s.movie_id = m.id
WHERE (CAST(sqlc.narg(monitored) AS BOOLEAN) IS NULL OR m.monitored = CAST(sqlc.narg(monitored) AS BOOLEAN))
  AND (CAST(sqlc.narg(status) AS TEXT) IS NULL OR m.status = CAST(sqlc.narg(status) AS TEXT))
  AND (CAST(sqlc.narg(root_folder_id) AS INTEGER) IS NULL OR m.root_folder_id = CAST(sqlc.narg(root_folder_id) AS INTEGER))
  AND (CAST(sqlc.narg(genre) AS TEXT) IS NULL OR EXISTS (SELECT 1 FROM json_each(m.genres) g WHERE g.value = CAST(sqlc.narg(genre) AS TEXT)))
  AND m.year BETWEEN CAST(sqlc.arg(year_min) AS INTEGER) AND CAST(sqlc.arg(year_max) AS INTEGER)
  AND (m.year < CAST(sqlc.arg(before_year) AS INTEGER)
       OR (m.year = CAST(sqlc.arg(before_year) AS INTEGER) AND m.id < CAST(sqlc.arg(before_id) AS INTEGER)))
ORDER BY m.year DESC, m.id DESC
LIMIT sqlc.arg(limit);

-- ListMoviesFilteredByAdded is keyset-paginated by (added_at, id) descending
-- (newest first): pass the last (added_at, id), or (MaxInt64, MaxInt64) first.
-- name: ListMoviesFilteredByAdded :many
SELECT m.id, m.tmdb_id, m.imdb_id, m.title, m.sort_title, m.year, m.status, m.runtime_min,
       m.poster_path, m.dominant_color, m.genres, m.monitored, m.quality_profile_id,
       m.minimum_availability, m.root_folder_id, m.path, m.added_at, m.collection_id, s.state
FROM movies m
JOIN movie_state s ON s.movie_id = m.id
WHERE (CAST(sqlc.narg(monitored) AS BOOLEAN) IS NULL OR m.monitored = CAST(sqlc.narg(monitored) AS BOOLEAN))
  AND (CAST(sqlc.narg(status) AS TEXT) IS NULL OR m.status = CAST(sqlc.narg(status) AS TEXT))
  AND (CAST(sqlc.narg(root_folder_id) AS INTEGER) IS NULL OR m.root_folder_id = CAST(sqlc.narg(root_folder_id) AS INTEGER))
  AND (CAST(sqlc.narg(genre) AS TEXT) IS NULL OR EXISTS (SELECT 1 FROM json_each(m.genres) g WHERE g.value = CAST(sqlc.narg(genre) AS TEXT)))
  AND m.year BETWEEN CAST(sqlc.arg(year_min) AS INTEGER) AND CAST(sqlc.arg(year_max) AS INTEGER)
  AND (m.added_at < CAST(sqlc.arg(before_added_at) AS INTEGER)
       OR (m.added_at = CAST(sqlc.arg(before_added_at) AS INTEGER) AND m.id < CAST(sqlc.arg(before_id) AS INTEGER)))
ORDER BY m.added_at DESC, m.id DESC
LIMIT sqlc.arg(limit);

-- ListMatcherMovies feeds the in-memory release matcher (internal/match).
-- name: ListMatcherMovies :many
SELECT id, title, original_title, year, imdb_id FROM movies ORDER BY id;

-- name: ListMatcherAlternateTitleRows :many
SELECT movie_id, title FROM alternate_titles ORDER BY movie_id, id;

-- ListMoviesForRefresh is read by the daily metadata refresh to pick the movies
-- whose metadata is due (see library.RefreshDue).
-- name: ListMoviesForRefresh :many
SELECT id, status, release_cinema, release_digital, release_physical, metadata_refreshed_at
FROM movies ORDER BY id;

-- name: CountMoviesInRootFolder :one
SELECT count(*) FROM movies WHERE root_folder_id = ?;

-- name: ListRecentJobs :many
SELECT * FROM jobs WHERE id < sqlc.arg(before_id) ORDER BY id DESC LIMIT sqlc.arg(limit);
