-- ----------------------------------------------------------------- notifications

-- name: CreateNotification :one
INSERT INTO notifications (name, kind, settings, events, tags, enabled)
VALUES (sqlc.arg(name), sqlc.arg(kind), sqlc.arg(settings), sqlc.arg(events), sqlc.arg(tags), sqlc.arg(enabled))
RETURNING *;

-- name: UpdateNotification :one
UPDATE notifications SET
    name     = sqlc.arg(name),
    kind     = sqlc.arg(kind),
    settings = sqlc.arg(settings),
    events   = sqlc.arg(events),
    tags     = sqlc.arg(tags),
    enabled  = sqlc.arg(enabled)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetNotificationSecrets :execrows
UPDATE notifications SET secrets = sqlc.narg(secrets) WHERE id = sqlc.arg(id);

-- name: GetNotification :one
SELECT * FROM notifications WHERE id = ?;

-- name: ListNotifications :many
SELECT * FROM notifications ORDER BY name, id;

-- name: ListEnabledNotifications :many
SELECT * FROM notifications WHERE enabled = 1 ORDER BY id;

-- name: DeleteNotification :execrows
DELETE FROM notifications WHERE id = ?;

-- name: RecordNotificationOK :execrows
UPDATE notifications SET failure_count = 0, last_error = NULL, last_ok_at = sqlc.arg(at)
WHERE id = sqlc.arg(id);

-- RecordNotificationFailure increments the failure count and returns it.
-- name: RecordNotificationFailure :one
UPDATE notifications SET
    failure_count  = failure_count + 1,
    last_error     = sqlc.arg(last_error),
    last_failed_at = sqlc.arg(at)
WHERE id = sqlc.arg(id)
RETURNING failure_count;

-- name: ResetNotificationHealth :execrows
UPDATE notifications SET failure_count = 0, last_error = NULL WHERE id = ?;

-- ----------------------------------------------------------------- media servers

-- name: CreateMediaServer :one
INSERT INTO media_servers (name, kind, base_url, path_mappings, refresh_on, options, enabled)
VALUES (sqlc.arg(name), sqlc.arg(kind), sqlc.arg(base_url), sqlc.arg(path_mappings), sqlc.arg(refresh_on),
        sqlc.arg(options), sqlc.arg(enabled))
RETURNING *;

-- name: UpdateMediaServer :one
UPDATE media_servers SET
    name          = sqlc.arg(name),
    kind          = sqlc.arg(kind),
    base_url      = sqlc.arg(base_url),
    path_mappings = sqlc.arg(path_mappings),
    refresh_on    = sqlc.arg(refresh_on),
    options       = sqlc.arg(options),
    enabled       = sqlc.arg(enabled)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetMediaServerSecrets :execrows
UPDATE media_servers SET secrets = sqlc.narg(secrets) WHERE id = sqlc.arg(id);

-- name: GetMediaServer :one
SELECT * FROM media_servers WHERE id = ?;

-- name: ListMediaServers :many
SELECT * FROM media_servers ORDER BY name, id;

-- name: ListEnabledMediaServers :many
SELECT * FROM media_servers WHERE enabled = 1 ORDER BY id;

-- name: DeleteMediaServer :execrows
DELETE FROM media_servers WHERE id = ?;

-- name: RecordMediaServerOK :execrows
UPDATE media_servers SET failure_count = 0, last_error = NULL, last_ok_at = sqlc.arg(at)
WHERE id = sqlc.arg(id);

-- RecordMediaServerFailure increments the failure count and returns it.
-- name: RecordMediaServerFailure :one
UPDATE media_servers SET
    failure_count  = failure_count + 1,
    last_error     = sqlc.arg(last_error),
    last_failed_at = sqlc.arg(at)
WHERE id = sqlc.arg(id)
RETURNING failure_count;

-- name: ResetMediaServerHealth :execrows
UPDATE media_servers SET failure_count = 0, last_error = NULL WHERE id = ?;

-- ----------------------------------------------------------------- event context

-- LatestMovieHistory returns the last history row of an event for a movie (the
-- previous quality of an upgrade, for notifications).
-- name: LatestMovieHistory :one
SELECT * FROM history
WHERE movie_id = sqlc.arg(movie_id) AND event = sqlc.arg(event)
ORDER BY id DESC
LIMIT 1;

-- CountGrabsBetween counts the grabs of a movie in [since, until]: the attempt
-- number of a failed download in the daily retry budget.
-- name: CountGrabsBetween :one
SELECT count(*) FROM downloads
WHERE movie_id = sqlc.arg(movie_id) AND grabbed_at >= sqlc.arg(since) AND grabbed_at <= sqlc.arg(until);
