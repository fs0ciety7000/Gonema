-- Queries of the acquisition service (internal/acquire) and of the scoring
-- rules seeding (internal/library).

-- ----------------------------------------------------------------- scoring

-- name: CountScoringRules :one
SELECT count(*) FROM scoring_rules;

-- name: CreateScoringRule :one
INSERT INTO scoring_rules (name, include_in_rename, description)
VALUES (sqlc.arg(name), sqlc.arg(include_in_rename), sqlc.arg(description))
RETURNING *;

-- name: InsertScoringCondition :exec
INSERT INTO scoring_conditions (rule_id, kind, negate, required, value)
VALUES (sqlc.arg(rule_id), sqlc.arg(kind), sqlc.arg(negate), sqlc.arg(required), sqlc.arg(value));

-- ListScoringRules returns the rules in creation order: the order of the score
-- breakdown.
-- name: ListScoringRules :many
SELECT * FROM scoring_rules ORDER BY id;

-- name: ListScoringConditions :many
SELECT * FROM scoring_conditions ORDER BY rule_id, id;

-- name: SetProfileScore :exec
INSERT INTO profile_scores (profile_id, rule_id, score)
VALUES (sqlc.arg(profile_id), sqlc.arg(rule_id), sqlc.arg(score))
ON CONFLICT (profile_id, rule_id) DO UPDATE SET score = excluded.score;

-- name: ListProfileScores :many
SELECT r.name AS rule_name, ps.score
FROM profile_scores ps
JOIN scoring_rules r ON r.id = ps.rule_id
WHERE ps.profile_id = sqlc.arg(profile_id)
ORDER BY r.id;

-- name: ListQualitySizes :many
SELECT * FROM quality_sizes ORDER BY quality;

-- ----------------------------------------------------------------- indexers

-- name: CreateIndexer :one
INSERT INTO indexers (
    name, kind, protocol, base_url, api_path, categories, priority, enable_rss,
    enable_auto_search, enable_interactive, min_seeders, seed_ratio, seed_time_min, tags
) VALUES (
    sqlc.arg(name), sqlc.arg(kind), sqlc.arg(protocol), sqlc.arg(base_url), sqlc.arg(api_path),
    sqlc.arg(categories), sqlc.arg(priority), sqlc.arg(enable_rss), sqlc.arg(enable_auto_search),
    sqlc.arg(enable_interactive), sqlc.narg(min_seeders), sqlc.narg(seed_ratio),
    sqlc.narg(seed_time_min), sqlc.arg(tags)
)
RETURNING *;

-- name: UpdateIndexer :one
UPDATE indexers SET
    name               = sqlc.arg(name),
    kind               = sqlc.arg(kind),
    protocol           = sqlc.arg(protocol),
    base_url           = sqlc.arg(base_url),
    api_path           = sqlc.arg(api_path),
    categories         = sqlc.arg(categories),
    priority           = sqlc.arg(priority),
    enable_rss         = sqlc.arg(enable_rss),
    enable_auto_search = sqlc.arg(enable_auto_search),
    enable_interactive = sqlc.arg(enable_interactive),
    min_seeders        = sqlc.narg(min_seeders),
    seed_ratio         = sqlc.narg(seed_ratio),
    seed_time_min      = sqlc.narg(seed_time_min),
    tags               = sqlc.arg(tags)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetIndexerSecrets :execrows
UPDATE indexers SET secrets = sqlc.narg(secrets) WHERE id = sqlc.arg(id);

-- name: GetIndexer :one
SELECT * FROM indexers WHERE id = ?;

-- name: ListIndexers :many
SELECT * FROM indexers ORDER BY priority, name, id;

-- name: DeleteIndexer :execrows
DELETE FROM indexers WHERE id = ?;

-- name: UpdateIndexerHealth :execrows
UPDATE indexers SET
    failure_count  = sqlc.arg(failure_count),
    disabled_until = sqlc.narg(disabled_until),
    last_error     = sqlc.narg(last_error),
    last_ok_at     = sqlc.narg(last_ok_at)
WHERE id = sqlc.arg(id);

-- name: GetRSSState :one
SELECT * FROM rss_state WHERE indexer_id = ?;

-- name: UpsertRSSState :exec
INSERT INTO rss_state (indexer_id, last_guid, last_pub_at, last_sync_at)
VALUES (sqlc.arg(indexer_id), sqlc.narg(last_guid), sqlc.narg(last_pub_at), sqlc.narg(last_sync_at))
ON CONFLICT (indexer_id) DO UPDATE SET
    last_guid    = excluded.last_guid,
    last_pub_at  = excluded.last_pub_at,
    last_sync_at = excluded.last_sync_at;

-- ----------------------------------------------------------------- download clients

-- name: CreateDownloadClient :one
INSERT INTO download_clients (
    name, kind, protocol, host, port, use_tls, url_base, category, priority, enabled,
    remove_completed, remove_failed, tags
) VALUES (
    sqlc.arg(name), sqlc.arg(kind), sqlc.arg(protocol), sqlc.arg(host), sqlc.arg(port),
    sqlc.arg(use_tls), sqlc.arg(url_base), sqlc.arg(category), sqlc.arg(priority),
    sqlc.arg(enabled), sqlc.arg(remove_completed), sqlc.arg(remove_failed), sqlc.arg(tags)
)
RETURNING *;

-- name: UpdateDownloadClient :one
UPDATE download_clients SET
    name             = sqlc.arg(name),
    kind             = sqlc.arg(kind),
    protocol         = sqlc.arg(protocol),
    host             = sqlc.arg(host),
    port             = sqlc.arg(port),
    use_tls          = sqlc.arg(use_tls),
    url_base         = sqlc.arg(url_base),
    category         = sqlc.arg(category),
    priority         = sqlc.arg(priority),
    enabled          = sqlc.arg(enabled),
    remove_completed = sqlc.arg(remove_completed),
    remove_failed    = sqlc.arg(remove_failed),
    tags             = sqlc.arg(tags)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetDownloadClientSecrets :execrows
UPDATE download_clients SET secrets = sqlc.narg(secrets) WHERE id = sqlc.arg(id);

-- name: GetDownloadClient :one
SELECT * FROM download_clients WHERE id = ?;

-- name: ListDownloadClients :many
SELECT * FROM download_clients ORDER BY priority, name, id;

-- name: DeleteDownloadClient :execrows
DELETE FROM download_clients WHERE id = ?;

-- name: UpdateDownloadClientHealth :execrows
UPDATE download_clients SET failure_count = sqlc.arg(failure_count), last_error = sqlc.narg(last_error)
WHERE id = sqlc.arg(id);

-- ----------------------------------------------------------------- remote path mappings

-- name: CreateRemotePathMapping :one
INSERT INTO remote_path_mappings (host, remote_path, local_path)
VALUES (sqlc.arg(host), sqlc.arg(remote_path), sqlc.arg(local_path))
RETURNING *;

-- name: UpdateRemotePathMapping :one
UPDATE remote_path_mappings SET
    host        = sqlc.arg(host),
    remote_path = sqlc.arg(remote_path),
    local_path  = sqlc.arg(local_path)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetRemotePathMapping :one
SELECT * FROM remote_path_mappings WHERE id = ?;

-- name: ListRemotePathMappings :many
SELECT * FROM remote_path_mappings ORDER BY host, remote_path, id;

-- name: DeleteRemotePathMapping :execrows
DELETE FROM remote_path_mappings WHERE id = ?;

-- ----------------------------------------------------------------- downloads

-- name: InsertDownload :one
INSERT INTO downloads (
    movie_id, client_id, client_ref, indexer_id, release_title, release_guid, info_url,
    protocol, size, quality, revision, custom_score, decision, status, retry_of,
    grabbed_at, manual
) VALUES (
    sqlc.arg(movie_id), sqlc.narg(client_id), sqlc.narg(client_ref), sqlc.narg(indexer_id),
    sqlc.arg(release_title), sqlc.arg(release_guid), sqlc.arg(info_url), sqlc.arg(protocol),
    sqlc.arg(size), sqlc.arg(quality), sqlc.arg(revision), sqlc.arg(custom_score),
    sqlc.arg(decision), 'queued', sqlc.narg(retry_of), sqlc.arg(grabbed_at), sqlc.arg(manual)
)
RETURNING *;

-- name: GetDownload :one
SELECT * FROM downloads WHERE id = ?;

-- ListActiveDownloads returns the downloads the queue tracker follows.
-- name: ListActiveDownloads :many
SELECT * FROM downloads
WHERE status IN ('queued', 'downloading', 'paused', 'completed', 'importing', 'warning')
ORDER BY grabbed_at, id;

-- name: ListMovieDownloads :many
SELECT * FROM downloads WHERE movie_id = ? ORDER BY grabbed_at DESC, id DESC;

-- UpdateDownloadState writes the tracked state of a download.
-- name: UpdateDownloadState :one
UPDATE downloads SET
    status          = sqlc.arg(status),
    progress        = sqlc.arg(progress),
    speed_bps       = sqlc.arg(speed_bps),
    eta_s           = sqlc.narg(eta_s),
    output_path     = sqlc.narg(output_path),
    status_messages = sqlc.arg(status_messages),
    completed_at    = sqlc.narg(completed_at),
    imported_at     = sqlc.narg(imported_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- CountGrabsSince counts the grabs of a movie since a time: the daily budget of
-- attempts (automatic retries stop once it is spent).
-- name: CountGrabsSince :one
SELECT count(*) FROM downloads
WHERE movie_id = sqlc.arg(movie_id) AND grabbed_at >= sqlc.arg(since);

-- name: GetCurrentMovieFile :one
SELECT * FROM movie_files WHERE movie_id = ?;

-- ListSearchableMovies lists the monitored movies that miss a file or can be
-- upgraded, least recently searched first.
-- name: ListSearchableMovies :many
SELECT m.id, m.last_searched_at, s.state
FROM movies m
JOIN movie_state s ON s.movie_id = m.id
WHERE m.monitored = 1 AND m.status <> 'deleted' AND s.state IN ('missing', 'upgradable')
ORDER BY coalesce(m.last_searched_at, 0), m.id;

-- ----------------------------------------------------------------- blocklist

-- name: InsertBlocklist :one
INSERT INTO blocklist (movie_id, kind, source_title, release_group, indexer_id, protocol, info_hash, reason, created_at)
VALUES (sqlc.narg(movie_id), sqlc.arg(kind), sqlc.arg(source_title), sqlc.narg(release_group),
        sqlc.narg(indexer_id), sqlc.narg(protocol), sqlc.narg(info_hash), sqlc.arg(reason), sqlc.arg(created_at))
RETURNING *;

-- ListBlocklistForMovie returns the entries that apply to a movie: its own and
-- the global ones (movie_id NULL).
-- name: ListBlocklistForMovie :many
SELECT * FROM blocklist WHERE movie_id = sqlc.arg(movie_id) OR movie_id IS NULL ORDER BY id;

-- ListBlocklist is keyset-paginated, newest first: pass the last id of the
-- previous page, or math.MaxInt64 first. NULL filters are ignored.
-- name: ListBlocklist :many
SELECT * FROM blocklist
WHERE id < sqlc.arg(before_id)
  AND (CAST(sqlc.narg(movie_id) AS INTEGER) IS NULL OR movie_id = CAST(sqlc.narg(movie_id) AS INTEGER))
  AND (CAST(sqlc.narg(kind) AS TEXT) IS NULL OR kind = CAST(sqlc.narg(kind) AS TEXT))
ORDER BY id DESC
LIMIT sqlc.arg(limit);

-- name: GetBlocklist :one
SELECT * FROM blocklist WHERE id = ?;

-- name: DeleteBlocklist :execrows
DELETE FROM blocklist WHERE id = ?;

-- name: ClearMovieBlocklist :execrows
DELETE FROM blocklist WHERE movie_id = ?;
