-- Queries of the library service (internal/library) and the API.

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
