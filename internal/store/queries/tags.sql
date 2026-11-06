-- name: CreateTag :one
INSERT INTO tags (label) VALUES (?) RETURNING *;

-- name: GetTagByLabel :one
SELECT * FROM tags WHERE label = ?;

-- name: ListTags :many
SELECT * FROM tags ORDER BY label;

-- name: DeleteTag :execrows
DELETE FROM tags WHERE id = ?;

-- name: AddMovieTag :exec
INSERT INTO movie_tags (movie_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING;

-- name: RemoveMovieTag :execrows
DELETE FROM movie_tags WHERE movie_id = ? AND tag_id = ?;

-- name: ClearMovieTags :exec
DELETE FROM movie_tags WHERE movie_id = ?;

-- name: ListMovieTags :many
SELECT t.* FROM tags t JOIN movie_tags mt ON mt.tag_id = t.id
WHERE mt.movie_id = ? ORDER BY t.label;

-- name: ListMovieIDsByTag :many
SELECT movie_id FROM movie_tags WHERE tag_id = ? ORDER BY movie_id;
