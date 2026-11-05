-- name: CreateQualityProfile :one
INSERT INTO quality_profiles (name, upgrade_allowed, cutoff_quality, min_score, cutoff_score, min_upgrade_step, languages)
VALUES (?, ?, ?, ?, ?, ?, ?)
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
    languages        = sqlc.arg(languages)
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
