-- Phase 8 (hardening), operations: import journal and library change log.
--
-- import_journal records the intent of every import BEFORE a file is touched:
-- source, destination (and the staging name used when the new file replaces
-- one of the same name), transfer mode, expected size, and the movie file and
-- history rows to write (record, JSON). The row is marked done in the same
-- transaction as the movie file row, so a pending row at startup means the
-- process stopped between the file operation and the database commit. The
-- importer reconciles pending rows at startup: it completes the database when
-- the file arrived, marks the row failed when the source is untouched (the
-- download is imported again), and marks it 'attention' (a health issue, no
-- file touched) when the state is ambiguous. 'dismissed' is set by the user
-- after checking by hand. Done rows are pruned after 30 days.
--
-- library_changes lists the movies whose library index row may have changed,
-- one row per change (movie_id NULL: every movie, e.g. a quality profile edit
-- changes "upgradable"). Triggers mirror the library_version triggers of
-- migration 00004. The library service applies these per-movie deltas to its
-- in-memory index instead of reloading every movie. A trigger keeps the last
-- 2,000 to 3,000 changes; an index older than that reloads in full.
--
-- Down drops both tables and their triggers (the data is operational only).

-- +goose Up

CREATE TABLE import_journal (
    id          INTEGER PRIMARY KEY,
    movie_id    INTEGER NOT NULL REFERENCES movies (id) ON DELETE CASCADE,
    download_id INTEGER REFERENCES downloads (id) ON DELETE SET NULL,
    source      TEXT    NOT NULL CHECK (source <> ''),
    destination TEXT    NOT NULL CHECK (destination <> ''),
    staging     TEXT    NOT NULL DEFAULT '',
    mode        TEXT    NOT NULL CHECK (mode IN ('move', 'copy', 'hardlink')),
    size        INTEGER NOT NULL CHECK (size >= 0),
    record      TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(record)),
    status      TEXT    NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'done', 'failed', 'attention', 'dismissed')),
    message     TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX import_journal_status_id ON import_journal (status, id);

CREATE TABLE library_changes (
    seq      INTEGER PRIMARY KEY AUTOINCREMENT,
    movie_id INTEGER
);

-- +goose StatementBegin
CREATE TRIGGER library_changes_prune AFTER INSERT ON library_changes
WHEN NEW.seq % 1000 = 0
BEGIN DELETE FROM library_changes WHERE seq <= NEW.seq - 2000; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_movies_insert AFTER INSERT ON movies
BEGIN INSERT INTO library_changes (movie_id) VALUES (NEW.id); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_movies_update AFTER UPDATE OF
    tmdb_id, imdb_id, title, original_title, sort_title, year, status, runtime_min, genres, ratings,
    poster_path, dominant_color, monitored, quality_profile_id, minimum_availability, root_folder_id,
    path, added_at, collection_id, release_cinema, release_digital, release_physical ON movies
BEGIN INSERT INTO library_changes (movie_id) VALUES (NEW.id); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_movies_delete AFTER DELETE ON movies
BEGIN INSERT INTO library_changes (movie_id) VALUES (OLD.id); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_files_insert AFTER INSERT ON movie_files
BEGIN INSERT INTO library_changes (movie_id) VALUES (NEW.movie_id); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_files_update AFTER UPDATE ON movie_files
BEGIN
    INSERT INTO library_changes (movie_id) VALUES (NEW.movie_id);
    INSERT INTO library_changes (movie_id) SELECT OLD.movie_id WHERE OLD.movie_id IS NOT NEW.movie_id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_files_delete AFTER DELETE ON movie_files
BEGIN INSERT INTO library_changes (movie_id) VALUES (OLD.movie_id); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_tags_insert AFTER INSERT ON movie_tags
BEGIN INSERT INTO library_changes (movie_id) VALUES (NEW.movie_id); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_tags_delete AFTER DELETE ON movie_tags
BEGIN INSERT INTO library_changes (movie_id) VALUES (OLD.movie_id); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_downloads_insert AFTER INSERT ON downloads
BEGIN INSERT INTO library_changes (movie_id) VALUES (NEW.movie_id); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_downloads_status AFTER UPDATE OF status, movie_id, imported_at ON downloads
WHEN OLD.status IS NOT NEW.status OR OLD.movie_id IS NOT NEW.movie_id OR OLD.imported_at IS NOT NEW.imported_at
BEGIN
    INSERT INTO library_changes (movie_id) VALUES (NEW.movie_id);
    INSERT INTO library_changes (movie_id) SELECT OLD.movie_id WHERE OLD.movie_id IS NOT NEW.movie_id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_downloads_delete AFTER DELETE ON downloads
BEGIN INSERT INTO library_changes (movie_id) VALUES (OLD.movie_id); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_profiles_update AFTER UPDATE ON quality_profiles
BEGIN INSERT INTO library_changes (movie_id) VALUES (NULL); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_profile_items_insert AFTER INSERT ON quality_profile_items
BEGIN INSERT INTO library_changes (movie_id) VALUES (NULL); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_profile_items_update AFTER UPDATE ON quality_profile_items
BEGIN INSERT INTO library_changes (movie_id) VALUES (NULL); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER library_changes_profile_items_delete AFTER DELETE ON quality_profile_items
BEGIN INSERT INTO library_changes (movie_id) VALUES (NULL); END;
-- +goose StatementEnd

-- +goose Down

DROP TRIGGER library_changes_profile_items_delete;
DROP TRIGGER library_changes_profile_items_update;
DROP TRIGGER library_changes_profile_items_insert;
DROP TRIGGER library_changes_profiles_update;
DROP TRIGGER library_changes_downloads_delete;
DROP TRIGGER library_changes_downloads_status;
DROP TRIGGER library_changes_downloads_insert;
DROP TRIGGER library_changes_tags_delete;
DROP TRIGGER library_changes_tags_insert;
DROP TRIGGER library_changes_files_delete;
DROP TRIGGER library_changes_files_update;
DROP TRIGGER library_changes_files_insert;
DROP TRIGGER library_changes_movies_delete;
DROP TRIGGER library_changes_movies_update;
DROP TRIGGER library_changes_movies_insert;
DROP TRIGGER library_changes_prune;
DROP TABLE library_changes;
DROP INDEX import_journal_status_id;
DROP TABLE import_journal;
