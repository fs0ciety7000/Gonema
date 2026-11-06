-- name: InsertMovie :one
INSERT INTO movies (
    tmdb_id, imdb_id, title, original_title, sort_title, clean_title, original_language,
    year, overview, runtime_min, status, release_cinema, release_digital, release_physical,
    certification, genres, ratings, poster_path, backdrop_path, dominant_color, extra,
    collection_id, monitored, quality_profile_id, minimum_availability, root_folder_id,
    path, added_at, metadata_refreshed_at
) VALUES (
    sqlc.arg(tmdb_id), sqlc.narg(imdb_id), sqlc.arg(title), sqlc.arg(original_title),
    sqlc.arg(sort_title), sqlc.arg(clean_title), sqlc.arg(original_language), sqlc.arg(year),
    sqlc.arg(overview), sqlc.arg(runtime_min), sqlc.arg(status), sqlc.narg(release_cinema),
    sqlc.narg(release_digital), sqlc.narg(release_physical), sqlc.arg(certification),
    sqlc.arg(genres), sqlc.arg(ratings), sqlc.narg(poster_path), sqlc.narg(backdrop_path),
    sqlc.narg(dominant_color), sqlc.arg(extra), sqlc.narg(collection_id), sqlc.arg(monitored),
    sqlc.arg(quality_profile_id), sqlc.arg(minimum_availability), sqlc.arg(root_folder_id),
    sqlc.arg(path), sqlc.arg(added_at), sqlc.narg(metadata_refreshed_at)
)
RETURNING *;

-- UpsertMovie inserts a movie or, when tmdb_id exists, refreshes its metadata
-- columns only: user settings (monitored, profile, root folder, path...) are kept.
-- name: UpsertMovie :one
INSERT INTO movies (
    tmdb_id, imdb_id, title, original_title, sort_title, clean_title, original_language,
    year, overview, runtime_min, status, release_cinema, release_digital, release_physical,
    certification, genres, ratings, poster_path, backdrop_path, dominant_color, extra,
    collection_id, monitored, quality_profile_id, minimum_availability, root_folder_id,
    path, added_at, metadata_refreshed_at
) VALUES (
    sqlc.arg(tmdb_id), sqlc.narg(imdb_id), sqlc.arg(title), sqlc.arg(original_title),
    sqlc.arg(sort_title), sqlc.arg(clean_title), sqlc.arg(original_language), sqlc.arg(year),
    sqlc.arg(overview), sqlc.arg(runtime_min), sqlc.arg(status), sqlc.narg(release_cinema),
    sqlc.narg(release_digital), sqlc.narg(release_physical), sqlc.arg(certification),
    sqlc.arg(genres), sqlc.arg(ratings), sqlc.narg(poster_path), sqlc.narg(backdrop_path),
    sqlc.narg(dominant_color), sqlc.arg(extra), sqlc.narg(collection_id), sqlc.arg(monitored),
    sqlc.arg(quality_profile_id), sqlc.arg(minimum_availability), sqlc.arg(root_folder_id),
    sqlc.arg(path), sqlc.arg(added_at), sqlc.narg(metadata_refreshed_at)
)
ON CONFLICT (tmdb_id) DO UPDATE SET
    imdb_id               = excluded.imdb_id,
    title                 = excluded.title,
    original_title        = excluded.original_title,
    sort_title            = excluded.sort_title,
    clean_title           = excluded.clean_title,
    original_language     = excluded.original_language,
    year                  = excluded.year,
    overview              = excluded.overview,
    runtime_min           = excluded.runtime_min,
    status                = excluded.status,
    release_cinema        = excluded.release_cinema,
    release_digital       = excluded.release_digital,
    release_physical      = excluded.release_physical,
    certification         = excluded.certification,
    genres                = excluded.genres,
    ratings               = excluded.ratings,
    poster_path           = excluded.poster_path,
    backdrop_path         = excluded.backdrop_path,
    dominant_color        = excluded.dominant_color,
    extra                 = excluded.extra,
    collection_id         = excluded.collection_id,
    metadata_refreshed_at = excluded.metadata_refreshed_at
RETURNING *;

-- name: GetMovie :one
SELECT * FROM movies WHERE id = ?;

-- name: GetMovieByTmdbID :one
SELECT * FROM movies WHERE tmdb_id = ?;

-- name: GetMovieByImdbID :one
SELECT * FROM movies WHERE imdb_id = CAST(sqlc.arg(imdb_id) AS TEXT) ORDER BY id LIMIT 1;

-- name: CountMovies :one
SELECT count(*) FROM movies;

-- ListMoviesBySortTitle is keyset-paginated: pass the last (sort_title, id) of the
-- previous page, or ('', 0) for the first page.
-- name: ListMoviesBySortTitle :many
SELECT m.id, m.tmdb_id, m.imdb_id, m.title, m.sort_title, m.year, m.status, m.runtime_min,
       m.poster_path, m.dominant_color, m.monitored, m.quality_profile_id,
       m.minimum_availability, m.root_folder_id, m.path, m.added_at, s.state
FROM movies m
JOIN movie_state s ON s.movie_id = m.id
WHERE m.sort_title > sqlc.arg(after_sort_title)
   OR (m.sort_title = sqlc.arg(after_sort_title) AND m.id > sqlc.arg(after_id))
ORDER BY m.sort_title, m.id
LIMIT sqlc.arg(limit);

-- ListMoviesByAddedDesc is keyset-paginated, newest first: pass the last
-- (added_at, id) of the previous page, or (math.MaxInt64, math.MaxInt64) first.
-- name: ListMoviesByAddedDesc :many
SELECT m.id, m.tmdb_id, m.imdb_id, m.title, m.sort_title, m.year, m.status, m.runtime_min,
       m.poster_path, m.dominant_color, m.monitored, m.quality_profile_id,
       m.minimum_availability, m.root_folder_id, m.path, m.added_at, s.state
FROM movies m
JOIN movie_state s ON s.movie_id = m.id
WHERE m.added_at < sqlc.arg(before_added_at)
   OR (m.added_at = sqlc.arg(before_added_at) AND m.id < sqlc.arg(before_id))
ORDER BY m.added_at DESC, m.id DESC
LIMIT sqlc.arg(limit);

-- name: GetMovieState :one
SELECT movie_id, movie_file_id, state FROM movie_state WHERE movie_id = ?;

-- name: UpdateMovieMetadata :one
UPDATE movies SET
    imdb_id               = sqlc.narg(imdb_id),
    title                 = sqlc.arg(title),
    original_title        = sqlc.arg(original_title),
    sort_title            = sqlc.arg(sort_title),
    clean_title           = sqlc.arg(clean_title),
    original_language     = sqlc.arg(original_language),
    year                  = sqlc.arg(year),
    overview              = sqlc.arg(overview),
    runtime_min           = sqlc.arg(runtime_min),
    status                = sqlc.arg(status),
    release_cinema        = sqlc.narg(release_cinema),
    release_digital       = sqlc.narg(release_digital),
    release_physical      = sqlc.narg(release_physical),
    certification         = sqlc.arg(certification),
    genres                = sqlc.arg(genres),
    ratings               = sqlc.arg(ratings),
    poster_path           = sqlc.narg(poster_path),
    backdrop_path         = sqlc.narg(backdrop_path),
    dominant_color        = sqlc.narg(dominant_color),
    extra                 = sqlc.arg(extra),
    collection_id         = sqlc.narg(collection_id),
    metadata_refreshed_at = CAST(sqlc.arg(metadata_refreshed_at) AS INTEGER)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: UpdateMovieSettings :one
UPDATE movies SET
    monitored            = sqlc.arg(monitored),
    quality_profile_id   = sqlc.arg(quality_profile_id),
    minimum_availability = sqlc.arg(minimum_availability),
    root_folder_id       = sqlc.arg(root_folder_id),
    path                 = sqlc.arg(path)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetMovieMonitored :execrows
UPDATE movies SET monitored = sqlc.arg(monitored) WHERE id = sqlc.arg(id);

-- name: SetMovieLastSearched :execrows
UPDATE movies SET last_searched_at = CAST(sqlc.arg(last_searched_at) AS INTEGER) WHERE id = sqlc.arg(id);

-- name: DeleteMovie :execrows
DELETE FROM movies WHERE id = ?;
