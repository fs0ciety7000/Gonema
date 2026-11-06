-- name: DeleteAlternateTitles :exec
DELETE FROM alternate_titles WHERE movie_id = sqlc.arg(movie_id) AND source = sqlc.arg(source);

-- name: InsertAlternateTitle :one
INSERT INTO alternate_titles (movie_id, title, clean_title, country, source)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: ListAlternateTitles :many
SELECT * FROM alternate_titles WHERE movie_id = ? ORDER BY id;

-- name: ListMatcherTitles :many
SELECT id, clean_title, year FROM movies ORDER BY id;

-- name: ListMatcherAlternateTitles :many
SELECT a.id, a.movie_id, a.clean_title, m.year
FROM alternate_titles a
JOIN movies m ON m.id = a.movie_id
ORDER BY a.id;
