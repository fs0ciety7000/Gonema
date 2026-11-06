-- Version 1.1 (migration 00009): collections page, iCal feed, stop retrying,
-- session origin, cancellable jobs.

-- ----------------------------------------------------------------- collections

-- SetCollectionParts stores the snapshot of a collection's movies read from
-- TMDB (JSON array).
-- name: SetCollectionParts :exec
UPDATE collections SET parts = sqlc.arg(parts), parts_refreshed_at = sqlc.arg(parts_refreshed_at)
WHERE id = sqlc.arg(id);

-- ListCollectionsNeedingParts lists the collections with at least one library
-- movie whose parts snapshot is missing or older than stale_before.
-- name: ListCollectionsNeedingParts :many
SELECT c.id, c.tmdb_id FROM collections c
WHERE (c.parts IS NULL OR coalesce(c.parts_refreshed_at, 0) < sqlc.arg(stale_before))
  AND EXISTS (SELECT 1 FROM movies m WHERE m.collection_id = c.id)
ORDER BY coalesce(c.parts_refreshed_at, 0), c.id
LIMIT sqlc.arg(limit);

-- ----------------------------------------------------------------- downloads

-- SetDownloadsRetryStopped sets (or clears, with NULL) the stop flag of the
-- downloads of a retry chain (ids: JSON array).
-- name: SetDownloadsRetryStopped :execrows
UPDATE downloads SET retry_stopped_at = sqlc.narg(retry_stopped_at)
WHERE id IN (SELECT CAST(value AS INTEGER) FROM json_each(CAST(sqlc.arg(ids) AS TEXT)));

-- ----------------------------------------------------------------- sessions

-- UpdateSessionClient records the last use of a session and how it connected.
-- name: UpdateSessionClient :exec
UPDATE sessions SET last_seen_at = sqlc.arg(last_seen_at), ip = sqlc.arg(ip), via = sqlc.arg(via)
WHERE id = sqlc.arg(id);

-- ----------------------------------------------------------------- calendar feed

-- name: GetCalendarFeed :one
SELECT * FROM calendar_feeds WHERE user_id = ?;

-- name: GetCalendarFeedByKey :one
SELECT * FROM calendar_feeds WHERE key_hash = ?;

-- SetCalendarFeedKey creates the feed of a user or replaces its key; the
-- options are kept.
-- name: SetCalendarFeedKey :one
INSERT INTO calendar_feeds (user_id, key_hash, last4, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(key_hash), sqlc.arg(last4), sqlc.arg(created_at))
ON CONFLICT (user_id) DO UPDATE SET
    key_hash     = excluded.key_hash,
    last4        = excluded.last4,
    created_at   = excluded.created_at,
    last_used_at = NULL
RETURNING *;

-- DisableCalendarFeed forgets the key of the feed; the options are kept.
-- name: DisableCalendarFeed :execrows
UPDATE calendar_feeds SET key_hash = NULL, last4 = '', last_used_at = NULL
WHERE user_id = ? AND key_hash IS NOT NULL;

-- SetCalendarFeedOptions creates the feed row of a user (off) or changes its
-- options.
-- name: SetCalendarFeedOptions :one
INSERT INTO calendar_feeds (user_id, past_days, future_days, include_unmonitored, release_types)
VALUES (sqlc.arg(user_id), sqlc.arg(past_days), sqlc.arg(future_days), sqlc.arg(include_unmonitored), sqlc.arg(release_types))
ON CONFLICT (user_id) DO UPDATE SET
    past_days           = excluded.past_days,
    future_days         = excluded.future_days,
    include_unmonitored = excluded.include_unmonitored,
    release_types       = excluded.release_types
RETURNING *;

-- name: TouchCalendarFeed :exec
UPDATE calendar_feeds SET last_used_at = ? WHERE id = ?;

-- ----------------------------------------------------------------- jobs

-- CancelRunningJob records that a running job was cancelled by the user.
-- name: CancelRunningJob :execrows
UPDATE jobs SET status = 'cancelled', finished_at = CAST(sqlc.arg(now) AS INTEGER)
WHERE id = sqlc.arg(id) AND status = 'running';

-- ----------------------------------------------------------------- library import scans

-- name: CreateLibraryScan :one
INSERT INTO library_import_scans (root_folder_id, path, status, created_at, updated_at)
VALUES (sqlc.arg(root_folder_id), sqlc.arg(path), 'pending', sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- name: SetLibraryScanJob :exec
UPDATE library_import_scans SET job_id = sqlc.arg(job_id) WHERE id = sqlc.arg(id);

-- name: UpdateLibraryScan :exec
UPDATE library_import_scans SET
    status = sqlc.arg(status), total = sqlc.arg(total), done = sqlc.arg(done),
    folders = sqlc.arg(folders), error = sqlc.arg(error), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: GetLibraryScan :one
SELECT * FROM library_import_scans WHERE id = ?;

-- ListLibraryScans lists the recent scans without their folders.
-- name: ListLibraryScans :many
SELECT id, root_folder_id, path, status, job_id, total, done, error, created_at, updated_at
FROM library_import_scans ORDER BY id DESC LIMIT sqlc.arg(limit);

-- PruneLibraryScans keeps the newest scans.
-- name: PruneLibraryScans :execrows
DELETE FROM library_import_scans
WHERE id NOT IN (SELECT id FROM library_import_scans ORDER BY id DESC LIMIT sqlc.arg(keep));
