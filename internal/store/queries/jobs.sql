-- EnqueueJob inserts a pending job. With a dedupe_key that an active (pending or
-- running) job already holds, nothing is inserted and sql.ErrNoRows is returned.
-- name: EnqueueJob :one
INSERT INTO jobs (kind, payload, priority, dedupe_key, run_at, max_attempts, created_at)
VALUES (sqlc.arg(kind), sqlc.arg(payload), sqlc.arg(priority), sqlc.narg(dedupe_key),
        sqlc.arg(run_at), sqlc.arg(max_attempts), sqlc.arg(created_at))
ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL AND status IN ('pending', 'running') DO NOTHING
RETURNING *;

-- ClaimJob atomically moves the next due pending job (highest priority, then oldest
-- run_at) to running and returns it; sql.ErrNoRows when none is due.
-- name: ClaimJob :one
UPDATE jobs SET
    status     = 'running',
    attempts   = attempts + 1,
    started_at = CAST(sqlc.arg(now) AS INTEGER),
    finished_at = NULL
WHERE id = (
    SELECT id FROM jobs
    WHERE status = 'pending' AND run_at <= CAST(sqlc.arg(now) AS INTEGER)
    ORDER BY priority DESC, run_at, id
    LIMIT 1
)
RETURNING *;

-- name: CompleteJob :execrows
UPDATE jobs SET status = 'done', finished_at = CAST(sqlc.arg(now) AS INTEGER), last_error = NULL
WHERE id = sqlc.arg(id) AND status = 'running';

-- FailJob records an error: the job goes back to pending at retry_at, or to failed
-- once max_attempts is reached.
-- name: FailJob :one
UPDATE jobs SET
    status      = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END,
    run_at      = CASE WHEN attempts >= max_attempts THEN run_at ELSE sqlc.arg(retry_at) END,
    last_error  = CAST(sqlc.arg(last_error) AS TEXT),
    finished_at = CASE WHEN attempts >= max_attempts THEN CAST(sqlc.arg(now) AS INTEGER) ELSE NULL END
WHERE id = sqlc.arg(id) AND status = 'running'
RETURNING *;

-- name: CancelJob :execrows
UPDATE jobs SET status = 'cancelled', finished_at = CAST(sqlc.arg(now) AS INTEGER)
WHERE id = sqlc.arg(id) AND status = 'pending';

-- ResetRunningJobs requeues jobs left running by a previous process (startup only).
-- name: ResetRunningJobs :execrows
UPDATE jobs SET status = 'pending', started_at = NULL WHERE status = 'running';

-- name: GetJob :one
SELECT * FROM jobs WHERE id = ?;

-- name: ListJobsByStatus :many
SELECT * FROM jobs WHERE status = ? ORDER BY run_at, id LIMIT sqlc.arg(limit);

-- name: DeleteFinishedJobsBefore :execrows
DELETE FROM jobs WHERE status IN ('done', 'failed', 'cancelled') AND finished_at < CAST(sqlc.arg(before) AS INTEGER);
