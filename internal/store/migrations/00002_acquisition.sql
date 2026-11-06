-- Acquisition (phase 5): new history events.
--
-- history.event gains:
--   movie_added, movie_deleted    library additions and removals (audit trail);
--   grab_overridden               a rejected release grabbed on explicit user request;
--   retry_scheduled               a failed download is retried with the next release;
--   retries_exhausted             no automatic attempt left for the movie today;
--   download_removed              a queue entry removed by the user or gone from the client.
--
-- SQLite cannot alter a CHECK constraint: the table is rebuilt (create, copy, drop,
-- rename) in the migration transaction, then its indexes and append-only triggers
-- are created again. Nothing references history (soft references only), so the
-- rebuild needs no foreign key juggling. DROP TABLE does not fire the DELETE
-- triggers, and row ids are copied as is.
--
-- Down keeps the rows whose event the previous schema knows and drops the others
-- (destructive: run it only on explicit operator action).

-- +goose Up

CREATE TABLE history_new (
    id           INTEGER PRIMARY KEY,
    movie_id     INTEGER,
    event        TEXT    NOT NULL CHECK (event IN (
                     'grabbed', 'grab_failed', 'download_failed', 'imported', 'upgraded',
                     'import_failed', 'file_deleted', 'file_renamed', 'blocklisted',
                     'ignored', 'metadata_changed',
                     'movie_added', 'movie_deleted', 'grab_overridden', 'retry_scheduled',
                     'retries_exhausted', 'download_removed')),
    source_title TEXT    NOT NULL DEFAULT '',
    quality      TEXT,
    custom_score INTEGER,
    download_id  INTEGER,
    data         TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(data) AND json_type(data) = 'object'),
    created_at   INTEGER NOT NULL
);
INSERT INTO history_new (id, movie_id, event, source_title, quality, custom_score, download_id, data, created_at)
SELECT id, movie_id, event, source_title, quality, custom_score, download_id, data, created_at FROM history;
DROP TABLE history;
ALTER TABLE history_new RENAME TO history;
CREATE INDEX history_movie_id_created_at ON history (movie_id, created_at);
CREATE INDEX history_created_at ON history (created_at);
CREATE INDEX history_download_id ON history (download_id);

-- +goose StatementBegin
CREATE TRIGGER history_no_update BEFORE UPDATE ON history
BEGIN
    SELECT RAISE(ABORT, 'history is append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER history_retention_only BEFORE DELETE ON history
WHEN NOT coalesce(
    OLD.created_at < CAST(unixepoch('now', 'subsec') * 1000 AS INTEGER)
        - (SELECT CAST(json_extract(value, '$') AS INTEGER) * 86400000
             FROM settings
            WHERE key = 'history.retention_days'
              AND json_type(value) = 'integer'
              AND json_extract(value, '$') > 0),
    0)
BEGIN
    SELECT RAISE(ABORT, 'history is append-only: deletion outside retention purge');
END;
-- +goose StatementEnd

-- Queue tracking and retries read the downloads of a movie by grab time.
CREATE INDEX downloads_movie_id_grabbed_at ON downloads (movie_id, grabbed_at);

-- +goose Down

DROP INDEX downloads_movie_id_grabbed_at;

CREATE TABLE history_old (
    id           INTEGER PRIMARY KEY,
    movie_id     INTEGER,
    event        TEXT    NOT NULL CHECK (event IN (
                     'grabbed', 'grab_failed', 'download_failed', 'imported', 'upgraded',
                     'import_failed', 'file_deleted', 'file_renamed', 'blocklisted',
                     'ignored', 'metadata_changed')),
    source_title TEXT    NOT NULL DEFAULT '',
    quality      TEXT,
    custom_score INTEGER,
    download_id  INTEGER,
    data         TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(data) AND json_type(data) = 'object'),
    created_at   INTEGER NOT NULL
);
INSERT INTO history_old (id, movie_id, event, source_title, quality, custom_score, download_id, data, created_at)
SELECT id, movie_id, event, source_title, quality, custom_score, download_id, data, created_at FROM history
WHERE event IN ('grabbed', 'grab_failed', 'download_failed', 'imported', 'upgraded',
                'import_failed', 'file_deleted', 'file_renamed', 'blocklisted',
                'ignored', 'metadata_changed');
DROP TABLE history;
ALTER TABLE history_old RENAME TO history;
CREATE INDEX history_movie_id_created_at ON history (movie_id, created_at);
CREATE INDEX history_created_at ON history (created_at);

-- +goose StatementBegin
CREATE TRIGGER history_no_update BEFORE UPDATE ON history
BEGIN
    SELECT RAISE(ABORT, 'history is append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER history_retention_only BEFORE DELETE ON history
WHEN NOT coalesce(
    OLD.created_at < CAST(unixepoch('now', 'subsec') * 1000 AS INTEGER)
        - (SELECT CAST(json_extract(value, '$') AS INTEGER) * 86400000
             FROM settings
            WHERE key = 'history.retention_days'
              AND json_type(value) = 'integer'
              AND json_extract(value, '$') > 0),
    0)
BEGIN
    SELECT RAISE(ABORT, 'history is append-only: deletion outside retention purge');
END;
-- +goose StatementEnd
