-- name: CreateRootFolder :one
INSERT INTO root_folders (path) VALUES (?) RETURNING *;

-- name: GetRootFolder :one
SELECT * FROM root_folders WHERE id = ?;

-- name: ListRootFolders :many
SELECT * FROM root_folders ORDER BY path;

-- name: UpdateRootFolderPath :one
UPDATE root_folders SET path = sqlc.arg(path) WHERE id = sqlc.arg(id) RETURNING *;

-- name: UpdateRootFolderSpace :execrows
UPDATE root_folders
SET free_space = sqlc.narg(free_space), total_space = sqlc.narg(total_space), checked_at = CAST(sqlc.arg(checked_at) AS INTEGER)
WHERE id = sqlc.arg(id);

-- name: DeleteRootFolder :execrows
DELETE FROM root_folders WHERE id = ?;
