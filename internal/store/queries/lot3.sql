-- Queries of the web UI lot 3: scoring rule editor, file rescoring, home
-- dashboard and onboarding.

-- ----------------------------------------------------------------- scoring rules

-- name: GetScoringRule :one
SELECT * FROM scoring_rules WHERE id = ?;

-- name: UpdateScoringRule :one
UPDATE scoring_rules SET
    name              = sqlc.arg(name),
    include_in_rename = sqlc.arg(include_in_rename),
    description       = sqlc.arg(description)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteScoringRule :execrows
DELETE FROM scoring_rules WHERE id = ?;

-- name: DeleteScoringConditions :exec
DELETE FROM scoring_conditions WHERE rule_id = ?;

-- name: DeleteProfileScore :exec
DELETE FROM profile_scores WHERE profile_id = sqlc.arg(profile_id) AND rule_id = sqlc.arg(rule_id);

-- name: ListAllProfileScores :many
SELECT profile_id, rule_id, score FROM profile_scores ORDER BY profile_id, rule_id;

-- ListMovieFilesForScoring returns what scoring a file needs: its release
-- name, parsed release, size and the profile of its movie.
-- name: ListMovieFilesForScoring :many
SELECT f.id, f.movie_id, f.scene_name, f.parsed, f.size, f.custom_score, m.quality_profile_id
FROM movie_files f
JOIN movies m ON m.id = f.movie_id
ORDER BY f.id;

-- name: UpdateMovieFileScore :execrows
UPDATE movie_files SET custom_score = sqlc.arg(custom_score) WHERE id = sqlc.arg(id);

-- ----------------------------------------------------------------- dashboard

-- CountHistoryEventsSince counts the history rows of some events (a JSON
-- array) at or after a time.
-- name: CountHistoryEventsSince :many
SELECT event, count(*) AS n FROM history
WHERE created_at >= sqlc.arg(since)
  AND event IN (SELECT value FROM json_each(sqlc.arg(events)))
GROUP BY event;

-- name: GetLastRSSSync :one
SELECT CAST(coalesce(max(last_sync_at), 0) AS INTEGER) AS last_sync_at FROM rss_state;

-- ----------------------------------------------------------------- onboarding

-- name: GetSetupCounts :one
SELECT
    (SELECT count(*) FROM root_folders) AS root_folders,
    (SELECT count(*) FROM quality_profiles) AS quality_profiles,
    (SELECT count(*) FROM indexers) AS indexers,
    (SELECT count(*) FROM download_clients WHERE enabled = 1) AS download_clients,
    (SELECT count(*) FROM movies) AS movies,
    (SELECT count(*) FROM downloads) AS downloads;
