-- Version 1.1: collections page, iCal feed, stop retrying, session origin.
--
-- collections gains parts (JSON array, NULL until TMDB answered once) and
-- parts_refreshed_at: a snapshot of the collection's movies (tmdbId, title,
-- year, releaseDate, posterPath) taken each time Gonema reads the collection
-- from TMDB. The collections list counts the missing and upcoming parts from it
-- without calling TMDB. search_on_add keeps the "search as soon as a film is
-- added" choice of a monitored collection, for the films the collections
-- refresh job adds later.
--
-- downloads gains retry_stopped_at: the user stopped the automatic next-best
-- retries of the chain this download belongs to (retry_of). Set on every
-- download of the chain, cleared on all of them by "resume retrying".
--
-- sessions gains via: how the browser reached Gonema when it last used the
-- session: 'direct' (public address, no trusted proxy), 'local' (loopback or
-- private address, no trusted proxy) or 'proxy' (through a reverse proxy of
-- GONEMA_TRUSTED_PROXIES). Shown in the session list; never an access decision.
--
-- calendar_feeds holds the read-only key of the iCal feed (one per account):
-- only its SHA-256 and last four characters are stored, like the API key. The
-- row also keeps the feed options; key_hash NULL means the feed is off.
--
-- history.event gains retries_stopped and retries_resumed (the history table is
-- rebuilt, as in 00002: SQLite cannot alter a CHECK constraint).
--
-- Down drops the new columns and table; the history rows of the new events are
-- dropped (destructive: run it only on explicit operator action).

-- +goose Up

ALTER TABLE collections ADD COLUMN parts TEXT CHECK (parts IS NULL OR (json_valid(parts) AND json_type(parts) = 'array'));
ALTER TABLE collections ADD COLUMN parts_refreshed_at INTEGER;
ALTER TABLE collections ADD COLUMN search_on_add BOOLEAN NOT NULL DEFAULT 0 CHECK (search_on_add IN (0, 1));

ALTER TABLE downloads ADD COLUMN retry_stopped_at INTEGER;

ALTER TABLE sessions ADD COLUMN via TEXT NOT NULL DEFAULT 'direct' CHECK (via IN ('direct', 'local', 'proxy'));

CREATE TABLE calendar_feeds (
    id                  INTEGER PRIMARY KEY,
    user_id             INTEGER NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    key_hash            BLOB    UNIQUE CHECK (key_hash IS NULL OR length(key_hash) = 32),
    last4               TEXT    NOT NULL DEFAULT '' CHECK (length(last4) IN (0, 4)),
    created_at          INTEGER,
    last_used_at        INTEGER,
    past_days           INTEGER NOT NULL DEFAULT 30 CHECK (past_days BETWEEN 0 AND 365),
    future_days         INTEGER NOT NULL DEFAULT 180 CHECK (future_days BETWEEN 1 AND 730),
    include_unmonitored BOOLEAN NOT NULL DEFAULT 0 CHECK (include_unmonitored IN (0, 1)),
    release_types       TEXT    NOT NULL DEFAULT '["cinema","digital","physical"]'
                                CHECK (json_valid(release_types) AND json_type(release_types) = 'array'),
    CHECK ((key_hash IS NULL) = (last4 = ''))
);

CREATE TABLE history_new (
    id           INTEGER PRIMARY KEY,
    movie_id     INTEGER,
    event        TEXT    NOT NULL CHECK (event IN (
                     'grabbed', 'grab_failed', 'download_failed', 'imported', 'upgraded',
                     'import_failed', 'file_deleted', 'file_renamed', 'blocklisted',
                     'ignored', 'metadata_changed',
                     'movie_added', 'movie_deleted', 'grab_overridden', 'retry_scheduled',
                     'retries_exhausted', 'download_removed',
                     'retries_stopped', 'retries_resumed')),
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
CREATE INDEX history_event_id ON history (event, id);

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

-- +goose Down

CREATE TABLE history_old (
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
INSERT INTO history_old (id, movie_id, event, source_title, quality, custom_score, download_id, data, created_at)
SELECT id, movie_id, event, source_title, quality, custom_score, download_id, data, created_at FROM history
WHERE event NOT IN ('retries_stopped', 'retries_resumed');
DROP TABLE history;
ALTER TABLE history_old RENAME TO history;
CREATE INDEX history_movie_id_created_at ON history (movie_id, created_at);
CREATE INDEX history_created_at ON history (created_at);
CREATE INDEX history_download_id ON history (download_id);
CREATE INDEX history_event_id ON history (event, id);

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

DROP TABLE calendar_feeds;
ALTER TABLE sessions DROP COLUMN via;
ALTER TABLE downloads DROP COLUMN retry_stopped_at;
ALTER TABLE collections DROP COLUMN search_on_add;
ALTER TABLE collections DROP COLUMN parts_refreshed_at;
ALTER TABLE collections DROP COLUMN parts;
