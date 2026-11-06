-- Web UI (phase 7): saved views without users.
--
-- saved_views.user_id becomes nullable: views are shared by everyone until users
-- and sessions arrive (phase 8), then each user's views carry their id. SQLite
-- treats NULLs as distinct in UNIQUE (user_id, page, name), so a partial unique
-- index keeps names unique per page among the shared views.
--
-- SQLite cannot drop NOT NULL: the table is rebuilt (create, copy, drop, rename)
-- in the migration transaction. Nothing references saved_views.
--
-- library_version is a counter bumped by triggers whenever a row the library
-- index can show changes: movies (index columns only, not last_searched_at),
-- movie_files, movie_tags, downloads (insert, delete, status change: not the
-- progress the tracker persists every few seconds), quality profiles and their
-- items (they decide "upgradable"). The library service keeps the index of
-- every movie in memory and reloads it only when the counter moved: reading
-- 10,000 rows takes far longer than the list budget of the UI.
--
-- Down deletes the shared views (user_id NULL): the previous schema cannot hold
-- them (destructive: run it only on explicit operator action).

-- +goose Up

CREATE TABLE saved_views_new (
    id       INTEGER PRIMARY KEY,
    user_id  INTEGER REFERENCES users (id) ON DELETE CASCADE,
    page     TEXT    NOT NULL CHECK (page <> ''),
    name     TEXT    NOT NULL CHECK (name <> ''),
    filters  TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(filters) AND json_type(filters) = 'object'),
    sort     TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(sort) AND json_type(sort) = 'object'),
    columns  TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(columns) AND json_type(columns) = 'array'),
    density  TEXT    NOT NULL DEFAULT 'airy' CHECK (density IN ('airy', 'compact')),
    position INTEGER NOT NULL DEFAULT 0,
    UNIQUE (user_id, page, name)
);
INSERT INTO saved_views_new (id, user_id, page, name, filters, sort, columns, density, position)
SELECT id, user_id, page, name, filters, sort, columns, density, position FROM saved_views;
DROP TABLE saved_views;
ALTER TABLE saved_views_new RENAME TO saved_views;
CREATE UNIQUE INDEX saved_views_shared_page_name ON saved_views (page, name) WHERE user_id IS NULL;
CREATE INDEX saved_views_page_position ON saved_views (page, position);

CREATE TABLE library_version (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    v  INTEGER NOT NULL
);
INSERT INTO library_version (id, v) VALUES (1, 1);

-- +goose StatementBegin
CREATE TRIGGER library_version_movies_insert AFTER INSERT ON movies
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_movies_update AFTER UPDATE OF
    tmdb_id, imdb_id, title, original_title, sort_title, year, status, runtime_min, genres, ratings,
    poster_path, dominant_color, monitored, quality_profile_id, minimum_availability, root_folder_id,
    path, added_at, collection_id, release_cinema, release_digital, release_physical ON movies
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_movies_delete AFTER DELETE ON movies
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_files_insert AFTER INSERT ON movie_files
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_files_update AFTER UPDATE ON movie_files
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_files_delete AFTER DELETE ON movie_files
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_tags_insert AFTER INSERT ON movie_tags
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_tags_delete AFTER DELETE ON movie_tags
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_downloads_insert AFTER INSERT ON downloads
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_downloads_status AFTER UPDATE OF status, movie_id, imported_at ON downloads
WHEN OLD.status IS NOT NEW.status OR OLD.movie_id IS NOT NEW.movie_id OR OLD.imported_at IS NOT NEW.imported_at
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_downloads_delete AFTER DELETE ON downloads
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_profiles_update AFTER UPDATE ON quality_profiles
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_profile_items_insert AFTER INSERT ON quality_profile_items
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_profile_items_update AFTER UPDATE ON quality_profile_items
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_version_profile_items_delete AFTER DELETE ON quality_profile_items
BEGIN UPDATE library_version SET v = v + 1 WHERE id = 1; END;
-- +goose StatementEnd

-- +goose Down

DROP TRIGGER library_version_profile_items_delete;
DROP TRIGGER library_version_profile_items_update;
DROP TRIGGER library_version_profile_items_insert;
DROP TRIGGER library_version_profiles_update;
DROP TRIGGER library_version_downloads_delete;
DROP TRIGGER library_version_downloads_status;
DROP TRIGGER library_version_downloads_insert;
DROP TRIGGER library_version_tags_delete;
DROP TRIGGER library_version_tags_insert;
DROP TRIGGER library_version_files_delete;
DROP TRIGGER library_version_files_update;
DROP TRIGGER library_version_files_insert;
DROP TRIGGER library_version_movies_delete;
DROP TRIGGER library_version_movies_update;
DROP TRIGGER library_version_movies_insert;
DROP TABLE library_version;

DROP INDEX saved_views_page_position;
DROP INDEX saved_views_shared_page_name;
CREATE TABLE saved_views_old (
    id       INTEGER PRIMARY KEY,
    user_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    page     TEXT    NOT NULL CHECK (page <> ''),
    name     TEXT    NOT NULL CHECK (name <> ''),
    filters  TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(filters)),
    sort     TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(sort)),
    columns  TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(columns) AND json_type(columns) = 'array'),
    density  TEXT    NOT NULL DEFAULT 'airy' CHECK (density IN ('airy', 'compact')),
    position INTEGER NOT NULL DEFAULT 0,
    UNIQUE (user_id, page, name)
);
INSERT INTO saved_views_old (id, user_id, page, name, filters, sort, columns, density, position)
SELECT id, user_id, page, name, filters, sort, columns, density, position FROM saved_views
WHERE user_id IS NOT NULL;
DROP TABLE saved_views;
ALTER TABLE saved_views_old RENAME TO saved_views;
