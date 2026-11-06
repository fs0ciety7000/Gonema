-- Phase 8 operations: import journal (migration 00008) and the per-movie
-- deltas of the library index.

-- ----------------------------------------------------------------- import journal

-- name: CreateImportJournal :one
INSERT INTO import_journal (movie_id, download_id, source, destination, staging, mode, size, record,
                            status, message, created_at, updated_at)
VALUES (sqlc.arg(movie_id), sqlc.narg(download_id), sqlc.arg(source), sqlc.arg(destination), sqlc.arg(staging),
        sqlc.arg(mode), sqlc.arg(size), sqlc.arg(record), 'pending', '', sqlc.arg(created_at), sqlc.arg(created_at))
RETURNING *;

-- name: SetImportJournalStatus :one
UPDATE import_journal SET status = sqlc.arg(status), message = sqlc.arg(message), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetImportJournal :one
SELECT * FROM import_journal WHERE id = ?;

-- name: ListImportJournalByStatus :many
SELECT * FROM import_journal WHERE status = ? ORDER BY id;

-- name: ListImportJournal :many
SELECT * FROM import_journal ORDER BY id DESC LIMIT sqlc.arg(limit);

-- name: PruneImportJournal :execrows
DELETE FROM import_journal WHERE status IN ('done', 'dismissed') AND updated_at < sqlc.arg(before);

-- ----------------------------------------------------------------- library index deltas

-- GetLibraryChangesHead returns the newest change sequence (0 when none).
-- name: GetLibraryChangesHead :one
SELECT CAST(coalesce(max(seq), 0) AS INTEGER) AS head, CAST(coalesce(min(seq), 0) AS INTEGER) AS tail
FROM library_changes;

-- ListLibraryChangesSince returns the changes after a sequence, oldest first.
-- movie_id NULL means every movie.
-- name: ListLibraryChangesSince :many
SELECT seq, movie_id FROM library_changes WHERE seq > sqlc.arg(after) ORDER BY seq LIMIT sqlc.arg(limit);

-- ListLibraryIndexByIDs is ListLibraryIndex restricted to some movies (ids: a
-- JSON array of integers).
-- name: ListLibraryIndexByIDs :many
SELECT m.id, m.tmdb_id, m.imdb_id, m.title, m.original_title, m.sort_title, m.year, m.status, m.runtime_min,
       m.genres, m.ratings, m.poster_path, m.dominant_color, m.monitored, m.quality_profile_id,
       m.minimum_availability, m.root_folder_id, m.path, m.added_at, m.collection_id,
       m.release_cinema, m.release_digital, m.release_physical,
       CAST(coalesce(json_extract(m.ratings, '$.tmdb.value'), -1) AS REAL) AS rating_tmdb,
       s.state,
       f.quality AS file_quality, f.size AS file_size, f.custom_score AS file_score,
       f.edition AS file_edition,
       CAST(coalesce((SELECT group_concat(h.value, ' ') FROM json_each(f.media_info, '$.hdr') h), '') AS TEXT) AS file_hdr,
       CAST(CASE WHEN s.state = 'downloading' THEN coalesce(
           (SELECT d.progress FROM downloads d
             WHERE d.movie_id = m.id
               AND d.status IN ('queued', 'downloading', 'paused', 'completed', 'importing', 'warning')
             ORDER BY d.grabbed_at DESC, d.id DESC LIMIT 1), 0)
       ELSE 0 END AS REAL) AS download_progress,
       f.imported_at AS file_imported_at
FROM movies m
JOIN movie_state s ON s.movie_id = m.id
LEFT JOIN movie_files f ON f.movie_id = m.id
WHERE m.id IN (SELECT CAST(value AS INTEGER) FROM json_each(CAST(sqlc.arg(ids) AS TEXT)))
ORDER BY m.id;

-- name: ListLastFailedDownloadsByIDs :many
SELECT d.movie_id, d.grabbed_at FROM downloads d
WHERE d.status = 'failed'
  AND d.movie_id IN (SELECT CAST(value AS INTEGER) FROM json_each(CAST(sqlc.arg(ids) AS TEXT)))
  AND NOT EXISTS (SELECT 1 FROM downloads n
                   WHERE n.movie_id = d.movie_id
                     AND (n.grabbed_at > d.grabbed_at OR (n.grabbed_at = d.grabbed_at AND n.id > d.id)));

-- name: ListMovieTagsByIDs :many
SELECT movie_id, tag_id FROM movie_tags
WHERE movie_id IN (SELECT CAST(value AS INTEGER) FROM json_each(CAST(sqlc.arg(ids) AS TEXT)))
ORDER BY movie_id, tag_id;
