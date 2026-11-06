-- name: CreateQualityProfile :one
INSERT INTO quality_profiles (name, upgrade_allowed, cutoff_quality, min_score, cutoff_score, min_upgrade_step, languages, preferred_protocol)
VALUES (sqlc.arg(name), sqlc.arg(upgrade_allowed), sqlc.arg(cutoff_quality), sqlc.arg(min_score), sqlc.arg(cutoff_score),
        sqlc.arg(min_upgrade_step), sqlc.arg(languages), sqlc.arg(preferred_protocol))
RETURNING *;

-- name: GetQualityProfile :one
SELECT * FROM quality_profiles WHERE id = ?;

-- name: GetQualityProfileByName :one
SELECT * FROM quality_profiles WHERE name = ?;

-- name: ListQualityProfiles :many
SELECT * FROM quality_profiles ORDER BY name;

-- name: UpdateQualityProfile :one
UPDATE quality_profiles SET
    name             = sqlc.arg(name),
    upgrade_allowed  = sqlc.arg(upgrade_allowed),
    cutoff_quality   = sqlc.arg(cutoff_quality),
    min_score        = sqlc.arg(min_score),
    cutoff_score     = sqlc.arg(cutoff_score),
    min_upgrade_step = sqlc.arg(min_upgrade_step),
    languages        = sqlc.arg(languages),
    preferred_protocol = sqlc.arg(preferred_protocol)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteQualityProfile :execrows
DELETE FROM quality_profiles WHERE id = ?;

-- name: DeleteQualityProfileItems :exec
DELETE FROM quality_profile_items WHERE profile_id = ?;

-- name: InsertQualityProfileItem :exec
INSERT INTO quality_profile_items (profile_id, quality, position, group_name, allowed)
VALUES (sqlc.arg(profile_id), sqlc.arg(quality), sqlc.arg(position), sqlc.narg(group_name), sqlc.arg(allowed));

-- name: ListQualityProfileItems :many
SELECT * FROM quality_profile_items WHERE profile_id = ? ORDER BY position, quality;

-- name: ListQualityProfileSizes :many
SELECT * FROM quality_profile_sizes WHERE profile_id = ? ORDER BY quality;

-- name: DeleteQualityProfileSizes :exec
DELETE FROM quality_profile_sizes WHERE profile_id = ?;

-- name: InsertQualityProfileSize :exec
INSERT INTO quality_profile_sizes (profile_id, quality, min_mb_per_min, preferred_mb_per_min, max_mb_per_min)
VALUES (sqlc.arg(profile_id), sqlc.arg(quality), sqlc.arg(min_mb_per_min), sqlc.arg(preferred_mb_per_min), sqlc.arg(max_mb_per_min));

-- GetQualityProfileUsage counts what refers to a profile: a profile in use
-- cannot be deleted.
-- name: GetQualityProfileUsage :one
SELECT
    (SELECT count(*) FROM movies m WHERE m.quality_profile_id = sqlc.arg(id)) AS movies,
    (SELECT count(*) FROM collections c WHERE c.quality_profile_id = sqlc.arg(id)) AS collections,
    (SELECT count(*) FROM import_lists l WHERE l.quality_profile_id = sqlc.arg(id)) AS import_lists;

-- name: CountQualityProfiles :one
SELECT count(*) FROM quality_profiles;
