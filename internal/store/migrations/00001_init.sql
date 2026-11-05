-- Gonema initial schema. Source of truth: docs/data-model.md.
--
-- Conventions:
--   * timestamps are INTEGER unix milliseconds UTC; release dates are TEXT 'YYYY-MM-DD';
--   * enums are readable TEXT, validated in Go and by CHECK;
--   * JSON columns are TEXT checked with json_valid (and json_type for arrays/objects);
--   * booleans are declared BOOLEAN (NUMERIC affinity, stored 0/1, CHECK enforced) so
--     that sqlc maps them to Go bool;
--   * quality IDs (bluray-1080p...) are NOT enumerated by CHECK: the catalogue lives in
--     Go (internal/quality) and grows without a table rebuild. Only non-empty is checked.
--
-- Deviations from docs/data-model.md (also listed in the phase 4 report):
--   * movies.path is UNIQUE per root folder (root_folder_id, path), since it is relative
--     to the root folder: two root folders may hold the same relative path.
--   * history.movie_id / history.download_id are soft references (no FOREIGN KEY):
--     history is append-only (triggers below), so an FK action (CASCADE / SET NULL)
--     would abort every movie deletion. History outlives the movie on purpose (audit).
--   * jobs.dedupe_key is unique among ACTIVE jobs only (pending/running, partial unique
--     index), so a finished job does not block re-enqueueing the same key.
--   * quality_profile_items: PK (profile_id, quality); position is shared by the
--     members of a group (0 = most preferred), mirroring quality.Profile.Items.
--   * indexes on FK child columns were added where the doc lists none (cascade and
--     RESTRICT checks would otherwise scan the child table).

-- +goose Up

-- ---------------------------------------------------------------- System

CREATE TABLE settings (
    key   TEXT PRIMARY KEY CHECK (key <> ''),
    value TEXT NOT NULL CHECK (json_valid(value))
);

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE CHECK (length(username) BETWEEN 1 AND 64),
    password_hash TEXT    NOT NULL CHECK (password_hash <> ''),
    role          TEXT    NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
    created_at    INTEGER NOT NULL,
    last_login_at INTEGER
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY CHECK (token_hash <> ''),
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL,
    user_agent TEXT,
    ip         TEXT,
    created_at INTEGER NOT NULL
);
CREATE INDEX sessions_user_id ON sessions (user_id);
CREATE INDEX sessions_expires_at ON sessions (expires_at);

CREATE TABLE api_keys (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL CHECK (name <> ''),
    key_hash     TEXT    NOT NULL UNIQUE CHECK (key_hash <> ''),
    prefix       TEXT    NOT NULL CHECK (prefix <> ''),
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER
);
CREATE INDEX api_keys_user_id ON api_keys (user_id);
CREATE INDEX api_keys_prefix ON api_keys (prefix);

CREATE TABLE saved_views (
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

CREATE TABLE jobs (
    id           INTEGER PRIMARY KEY,
    kind         TEXT    NOT NULL CHECK (kind <> ''),
    payload      TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(payload)),
    status       TEXT    NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending', 'running', 'done', 'failed', 'cancelled')),
    priority     INTEGER NOT NULL DEFAULT 0,
    dedupe_key   TEXT CHECK (dedupe_key IS NULL OR dedupe_key <> ''),
    run_at       INTEGER NOT NULL,
    attempts     INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts >= 1),
    last_error   TEXT,
    started_at   INTEGER,
    finished_at  INTEGER,
    created_at   INTEGER NOT NULL
);
CREATE INDEX jobs_status_run_at_priority ON jobs (status, run_at, priority);
CREATE UNIQUE INDEX jobs_active_dedupe_key ON jobs (dedupe_key)
    WHERE dedupe_key IS NOT NULL AND status IN ('pending', 'running');

-- ---------------------------------------------------------------- Quality and scoring

CREATE TABLE quality_sizes (
    quality              TEXT PRIMARY KEY CHECK (quality <> ''),
    min_mb_per_min       REAL NOT NULL DEFAULT 0 CHECK (min_mb_per_min >= 0),
    preferred_mb_per_min REAL CHECK (preferred_mb_per_min IS NULL OR preferred_mb_per_min >= min_mb_per_min),
    max_mb_per_min       REAL CHECK (max_mb_per_min IS NULL OR max_mb_per_min >= min_mb_per_min),
    CHECK (preferred_mb_per_min IS NULL OR max_mb_per_min IS NULL OR preferred_mb_per_min <= max_mb_per_min)
);

CREATE TABLE quality_profiles (
    id               INTEGER PRIMARY KEY,
    name             TEXT    NOT NULL UNIQUE CHECK (name <> ''),
    upgrade_allowed  BOOLEAN NOT NULL DEFAULT 1 CHECK (upgrade_allowed IN (0, 1)),
    -- quality ID or group name of quality_profile_items (checked in Go).
    cutoff_quality   TEXT    NOT NULL CHECK (cutoff_quality <> ''),
    min_score        INTEGER NOT NULL DEFAULT 0,
    cutoff_score     INTEGER NOT NULL DEFAULT 0,
    min_upgrade_step INTEGER NOT NULL DEFAULT 1 CHECK (min_upgrade_step >= 0),
    languages        TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(languages) AND json_type(languages) = 'array')
);

CREATE TABLE quality_profile_items (
    profile_id INTEGER NOT NULL REFERENCES quality_profiles (id) ON DELETE CASCADE,
    quality    TEXT    NOT NULL CHECK (quality <> ''),
    -- index of the item in the profile, 0 = most preferred; shared by a group's members.
    position   INTEGER NOT NULL CHECK (position >= 0),
    group_name TEXT CHECK (group_name IS NULL OR group_name <> ''),
    allowed    BOOLEAN NOT NULL DEFAULT 1 CHECK (allowed IN (0, 1)),
    PRIMARY KEY (profile_id, quality)
) WITHOUT ROWID;
CREATE INDEX quality_profile_items_position ON quality_profile_items (profile_id, position);

CREATE TABLE scoring_rules (
    id                INTEGER PRIMARY KEY,
    name              TEXT    NOT NULL UNIQUE CHECK (name <> ''),
    include_in_rename BOOLEAN NOT NULL DEFAULT 0 CHECK (include_in_rename IN (0, 1)),
    description       TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE scoring_conditions (
    id       INTEGER PRIMARY KEY,
    rule_id  INTEGER NOT NULL REFERENCES scoring_rules (id) ON DELETE CASCADE,
    kind     TEXT    NOT NULL CHECK (kind IN (
                 'title_regex', 'release_group', 'source', 'resolution', 'codec', 'hdr',
                 'audio', 'edition', 'language', 'indexer_flag', 'size_range', 'year_range')),
    negate   BOOLEAN NOT NULL DEFAULT 0 CHECK (negate IN (0, 1)),
    required BOOLEAN NOT NULL DEFAULT 0 CHECK (required IN (0, 1)),
    value    TEXT    NOT NULL CHECK (json_valid(value))
);
CREATE INDEX scoring_conditions_rule_id ON scoring_conditions (rule_id);

CREATE TABLE profile_scores (
    profile_id INTEGER NOT NULL REFERENCES quality_profiles (id) ON DELETE CASCADE,
    rule_id    INTEGER NOT NULL REFERENCES scoring_rules (id) ON DELETE CASCADE,
    score      INTEGER NOT NULL,
    PRIMARY KEY (profile_id, rule_id)
) WITHOUT ROWID;
CREATE INDEX profile_scores_rule_id ON profile_scores (rule_id);

-- ---------------------------------------------------------------- Library

CREATE TABLE root_folders (
    id          INTEGER PRIMARY KEY,
    path        TEXT    NOT NULL UNIQUE CHECK (path <> ''),
    free_space  INTEGER CHECK (free_space IS NULL OR free_space >= 0),
    total_space INTEGER CHECK (total_space IS NULL OR total_space >= 0),
    checked_at  INTEGER
);

CREATE TABLE tags (
    id    INTEGER PRIMARY KEY,
    label TEXT NOT NULL UNIQUE CHECK (length(label) BETWEEN 1 AND 64)
);

CREATE TABLE collections (
    id                   INTEGER PRIMARY KEY,
    tmdb_id              INTEGER NOT NULL UNIQUE CHECK (tmdb_id > 0),
    name                 TEXT    NOT NULL CHECK (name <> ''),
    overview             TEXT    NOT NULL DEFAULT '',
    poster_path          TEXT,
    backdrop_path        TEXT,
    -- monitored = add the saga's movies automatically, with the settings below.
    monitored            BOOLEAN NOT NULL DEFAULT 0 CHECK (monitored IN (0, 1)),
    quality_profile_id   INTEGER REFERENCES quality_profiles (id) ON DELETE RESTRICT,
    root_folder_id       INTEGER REFERENCES root_folders (id) ON DELETE RESTRICT,
    minimum_availability TEXT    NOT NULL DEFAULT 'released'
                                 CHECK (minimum_availability IN ('announced', 'in_cinemas', 'released')),
    refreshed_at         INTEGER,
    CHECK (monitored = 0 OR (quality_profile_id IS NOT NULL AND root_folder_id IS NOT NULL))
);
CREATE INDEX collections_quality_profile_id ON collections (quality_profile_id);
CREATE INDEX collections_root_folder_id ON collections (root_folder_id);

CREATE TABLE movies (
    id                    INTEGER PRIMARY KEY,
    tmdb_id               INTEGER NOT NULL UNIQUE CHECK (tmdb_id > 0),
    imdb_id               TEXT CHECK (imdb_id IS NULL OR imdb_id GLOB 'tt[0-9]*'),
    title                 TEXT    NOT NULL CHECK (title <> ''),
    original_title        TEXT    NOT NULL DEFAULT '',
    sort_title            TEXT    NOT NULL,
    clean_title           TEXT    NOT NULL,
    original_language     TEXT    NOT NULL DEFAULT '',
    year                  INTEGER NOT NULL DEFAULT 0 CHECK (year = 0 OR year BETWEEN 1870 AND 2200),
    overview              TEXT    NOT NULL DEFAULT '',
    runtime_min           INTEGER NOT NULL DEFAULT 0 CHECK (runtime_min >= 0),
    status                TEXT    NOT NULL DEFAULT 'announced'
                                  CHECK (status IN ('announced', 'in_cinemas', 'released', 'deleted')),
    release_cinema        TEXT CHECK (release_cinema IS NULL OR date(release_cinema) IS release_cinema),
    release_digital       TEXT CHECK (release_digital IS NULL OR date(release_digital) IS release_digital),
    release_physical      TEXT CHECK (release_physical IS NULL OR date(release_physical) IS release_physical),
    certification         TEXT    NOT NULL DEFAULT '',
    genres                TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(genres) AND json_type(genres) = 'array'),
    ratings               TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(ratings) AND json_type(ratings) = 'object'),
    poster_path           TEXT,
    backdrop_path         TEXT,
    dominant_color        TEXT CHECK (dominant_color IS NULL OR (length(dominant_color) = 7 AND dominant_color GLOB '#[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]')),
    extra                 TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(extra) AND json_type(extra) = 'object'),
    collection_id         INTEGER REFERENCES collections (id) ON DELETE SET NULL,
    monitored             BOOLEAN NOT NULL DEFAULT 1 CHECK (monitored IN (0, 1)),
    quality_profile_id    INTEGER NOT NULL REFERENCES quality_profiles (id) ON DELETE RESTRICT,
    minimum_availability  TEXT    NOT NULL DEFAULT 'released'
                                  CHECK (minimum_availability IN ('announced', 'in_cinemas', 'released')),
    root_folder_id        INTEGER NOT NULL REFERENCES root_folders (id) ON DELETE RESTRICT,
    path                  TEXT    NOT NULL CHECK (path <> ''),
    added_at              INTEGER NOT NULL,
    metadata_refreshed_at INTEGER,
    last_searched_at      INTEGER,
    UNIQUE (root_folder_id, path)
);
CREATE INDEX movies_monitored_status ON movies (monitored, status);
CREATE INDEX movies_year ON movies (year);
CREATE INDEX movies_sort_title ON movies (sort_title); -- rowid is implicit: serves ORDER BY sort_title, id
CREATE INDEX movies_added_at ON movies (added_at);
CREATE INDEX movies_collection_id ON movies (collection_id);
CREATE INDEX movies_imdb_id ON movies (imdb_id);
CREATE INDEX movies_quality_profile_id ON movies (quality_profile_id);

CREATE TABLE alternate_titles (
    id          INTEGER PRIMARY KEY,
    movie_id    INTEGER NOT NULL REFERENCES movies (id) ON DELETE CASCADE,
    title       TEXT    NOT NULL CHECK (title <> ''),
    clean_title TEXT    NOT NULL CHECK (clean_title <> ''),
    country     TEXT    NOT NULL DEFAULT '',
    source      TEXT    NOT NULL DEFAULT 'tmdb' CHECK (source IN ('tmdb', 'user'))
);
CREATE INDEX alternate_titles_clean_title ON alternate_titles (clean_title);
CREATE INDEX alternate_titles_movie_id ON alternate_titles (movie_id);

CREATE TABLE movie_files (
    id            INTEGER PRIMARY KEY,
    movie_id      INTEGER NOT NULL UNIQUE REFERENCES movies (id) ON DELETE CASCADE,
    relative_path TEXT    NOT NULL CHECK (relative_path <> ''),
    size          INTEGER NOT NULL CHECK (size >= 0),
    quality       TEXT    NOT NULL CHECK (quality <> ''),
    revision      INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    edition       TEXT    NOT NULL DEFAULT '',
    languages     TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(languages) AND json_type(languages) = 'array'),
    release_group TEXT    NOT NULL DEFAULT '',
    scene_name    TEXT    NOT NULL DEFAULT '',
    parsed        TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(parsed)),
    media_info    TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(media_info)),
    custom_score  INTEGER NOT NULL DEFAULT 0,
    imported_at   INTEGER NOT NULL
);

CREATE TABLE movie_tags (
    movie_id INTEGER NOT NULL REFERENCES movies (id) ON DELETE CASCADE,
    tag_id   INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (movie_id, tag_id)
) WITHOUT ROWID;
CREATE INDEX movie_tags_tag_id ON movie_tags (tag_id);

-- ---------------------------------------------------------------- Search and download

CREATE TABLE indexers (
    id                 INTEGER PRIMARY KEY,
    name               TEXT    NOT NULL UNIQUE CHECK (name <> ''),
    kind               TEXT    NOT NULL CHECK (kind IN ('newznab', 'torznab')),
    protocol           TEXT    NOT NULL CHECK (protocol IN ('usenet', 'torrent')),
    base_url           TEXT    NOT NULL CHECK (base_url <> ''),
    api_path           TEXT    NOT NULL DEFAULT '/api',
    categories         TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(categories) AND json_type(categories) = 'array'),
    secrets            BLOB,   -- secretbox ciphertext (API key)
    priority           INTEGER NOT NULL DEFAULT 25,
    enable_rss         BOOLEAN NOT NULL DEFAULT 1 CHECK (enable_rss IN (0, 1)),
    enable_auto_search BOOLEAN NOT NULL DEFAULT 1 CHECK (enable_auto_search IN (0, 1)),
    enable_interactive BOOLEAN NOT NULL DEFAULT 1 CHECK (enable_interactive IN (0, 1)),
    min_seeders        INTEGER CHECK (min_seeders IS NULL OR min_seeders >= 0),
    seed_ratio         REAL    CHECK (seed_ratio IS NULL OR seed_ratio >= 0),
    seed_time_min      INTEGER CHECK (seed_time_min IS NULL OR seed_time_min >= 0),
    tags               TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(tags) AND json_type(tags) = 'array'),
    failure_count      INTEGER NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    disabled_until     INTEGER,
    last_error         TEXT,
    last_ok_at         INTEGER,
    CHECK ((kind = 'newznab' AND protocol = 'usenet') OR (kind = 'torznab' AND protocol = 'torrent'))
);

CREATE TABLE rss_state (
    indexer_id   INTEGER PRIMARY KEY REFERENCES indexers (id) ON DELETE CASCADE,
    last_guid    TEXT,
    last_pub_at  INTEGER,
    last_sync_at INTEGER
);

CREATE TABLE download_clients (
    id               INTEGER PRIMARY KEY,
    name             TEXT    NOT NULL UNIQUE CHECK (name <> ''),
    kind             TEXT    NOT NULL CHECK (kind IN ('sabnzbd', 'nzbget', 'qbittorrent', 'deluge', 'transmission')),
    protocol         TEXT    NOT NULL CHECK (protocol IN ('usenet', 'torrent')),
    host             TEXT    NOT NULL CHECK (host <> ''),
    port             INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    use_tls          BOOLEAN NOT NULL DEFAULT 0 CHECK (use_tls IN (0, 1)),
    url_base         TEXT    NOT NULL DEFAULT '',
    secrets          BLOB,   -- secretbox ciphertext (password / API key)
    category         TEXT    NOT NULL DEFAULT '',
    priority         INTEGER NOT NULL DEFAULT 1,
    enabled          BOOLEAN NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    remove_completed BOOLEAN NOT NULL DEFAULT 0 CHECK (remove_completed IN (0, 1)),
    remove_failed    BOOLEAN NOT NULL DEFAULT 1 CHECK (remove_failed IN (0, 1)),
    tags             TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(tags) AND json_type(tags) = 'array'),
    failure_count    INTEGER NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    last_error       TEXT,
    CHECK ((kind IN ('sabnzbd', 'nzbget') AND protocol = 'usenet')
        OR (kind IN ('qbittorrent', 'deluge', 'transmission') AND protocol = 'torrent'))
);

CREATE TABLE remote_path_mappings (
    id          INTEGER PRIMARY KEY,
    host        TEXT NOT NULL CHECK (host <> ''),
    remote_path TEXT NOT NULL CHECK (remote_path <> ''),
    local_path  TEXT NOT NULL CHECK (local_path <> ''),
    UNIQUE (host, remote_path)
);

CREATE TABLE downloads (
    id              INTEGER PRIMARY KEY,
    -- a queue entry belongs to its movie; the app removes it from the client first.
    movie_id        INTEGER NOT NULL REFERENCES movies (id) ON DELETE CASCADE,
    -- SET NULL: deleting a client or indexer keeps the record (traceability).
    client_id       INTEGER REFERENCES download_clients (id) ON DELETE SET NULL,
    client_ref      TEXT,
    indexer_id      INTEGER REFERENCES indexers (id) ON DELETE SET NULL,
    release_title   TEXT    NOT NULL CHECK (release_title <> ''),
    release_guid    TEXT    NOT NULL DEFAULT '',
    info_url        TEXT    NOT NULL DEFAULT '',
    protocol        TEXT    NOT NULL CHECK (protocol IN ('usenet', 'torrent')),
    size            INTEGER NOT NULL DEFAULT 0 CHECK (size >= 0),
    quality         TEXT    NOT NULL CHECK (quality <> ''),
    revision        INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    custom_score    INTEGER NOT NULL DEFAULT 0,
    decision        TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(decision) AND json_type(decision) = 'object'),
    status          TEXT    NOT NULL DEFAULT 'queued' CHECK (status IN (
                        'queued', 'downloading', 'paused', 'completed', 'importing',
                        'imported', 'failed', 'warning', 'removed')),
    progress        REAL    NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 1),
    speed_bps       INTEGER NOT NULL DEFAULT 0 CHECK (speed_bps >= 0),
    eta_s           INTEGER,
    output_path     TEXT,
    status_messages TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(status_messages) AND json_type(status_messages) = 'array'),
    retry_of        INTEGER REFERENCES downloads (id) ON DELETE SET NULL,
    grabbed_at      INTEGER NOT NULL,
    completed_at    INTEGER,
    imported_at     INTEGER,
    manual          BOOLEAN NOT NULL DEFAULT 0 CHECK (manual IN (0, 1)),
    UNIQUE (client_id, client_ref)
);
CREATE INDEX downloads_movie_id_status ON downloads (movie_id, status);
CREATE INDEX downloads_status ON downloads (status);
CREATE INDEX downloads_indexer_id ON downloads (indexer_id);
CREATE INDEX downloads_retry_of ON downloads (retry_of);

CREATE TABLE blocklist (
    id            INTEGER PRIMARY KEY,
    -- NULL movie_id: global entry (e.g. a release group blocked everywhere).
    movie_id      INTEGER REFERENCES movies (id) ON DELETE CASCADE,
    kind          TEXT    NOT NULL CHECK (kind IN ('release', 'group')),
    source_title  TEXT    NOT NULL DEFAULT '',
    release_group TEXT,
    indexer_id    INTEGER REFERENCES indexers (id) ON DELETE SET NULL,
    protocol      TEXT CHECK (protocol IS NULL OR protocol IN ('usenet', 'torrent')),
    info_hash     TEXT,
    reason        TEXT    NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    CHECK (kind <> 'group' OR release_group IS NOT NULL)
);
CREATE INDEX blocklist_movie_id ON blocklist (movie_id);
CREATE INDEX blocklist_info_hash ON blocklist (info_hash);
CREATE INDEX blocklist_release_group ON blocklist (release_group);
CREATE INDEX blocklist_indexer_id ON blocklist (indexer_id);

-- Append-only audit trail. movie_id and download_id are soft references (see header).
CREATE TABLE history (
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
CREATE INDEX history_movie_id_created_at ON history (movie_id, created_at);
CREATE INDEX history_created_at ON history (created_at);

-- +goose StatementBegin
CREATE TRIGGER history_no_update BEFORE UPDATE ON history
BEGIN
    SELECT RAISE(ABORT, 'history is append-only');
END;
-- +goose StatementEnd

-- Deletion is the retention purge only: allowed for rows older than the
-- 'history.retention_days' setting (a positive JSON integer). Without that setting,
-- nothing can be deleted. The cutoff is computed from the SQLite clock.
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

-- ---------------------------------------------------------------- Discovery

CREATE TABLE import_lists (
    id                   INTEGER PRIMARY KEY,
    name                 TEXT    NOT NULL UNIQUE CHECK (name <> ''),
    kind                 TEXT    NOT NULL CHECK (kind IN (
                             'tmdb_popular', 'tmdb_trending', 'tmdb_upcoming',
                             'tmdb_collection', 'tmdb_person', 'tmdb_list')),
    settings             TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(settings) AND json_type(settings) = 'object'),
    quality_profile_id   INTEGER NOT NULL REFERENCES quality_profiles (id) ON DELETE RESTRICT,
    root_folder_id       INTEGER NOT NULL REFERENCES root_folders (id) ON DELETE RESTRICT,
    minimum_availability TEXT    NOT NULL DEFAULT 'released'
                                 CHECK (minimum_availability IN ('announced', 'in_cinemas', 'released')),
    monitored            BOOLEAN NOT NULL DEFAULT 1 CHECK (monitored IN (0, 1)),
    auto_add             BOOLEAN NOT NULL DEFAULT 0 CHECK (auto_add IN (0, 1)),
    search_on_add        BOOLEAN NOT NULL DEFAULT 0 CHECK (search_on_add IN (0, 1)),
    sync_interval_min    INTEGER NOT NULL DEFAULT 720 CHECK (sync_interval_min >= 5),
    last_sync_at         INTEGER
);
CREATE INDEX import_lists_quality_profile_id ON import_lists (quality_profile_id);
CREATE INDEX import_lists_root_folder_id ON import_lists (root_folder_id);

CREATE TABLE import_list_exclusions (
    tmdb_id    INTEGER PRIMARY KEY CHECK (tmdb_id > 0),
    title      TEXT    NOT NULL DEFAULT '',
    year       INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

-- ---------------------------------------------------------------- Integrations

CREATE TABLE notifications (
    id       INTEGER PRIMARY KEY,
    name     TEXT    NOT NULL UNIQUE CHECK (name <> ''),
    kind     TEXT    NOT NULL CHECK (kind IN (
                 'webhook', 'discord', 'telegram', 'pushover', 'ntfy', 'gotify', 'slack', 'email')),
    settings TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(settings) AND json_type(settings) = 'object'),
    secrets  BLOB,   -- secretbox ciphertext
    events   TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(events) AND json_type(events) = 'array'),
    tags     TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(tags) AND json_type(tags) = 'array'),
    enabled  BOOLEAN NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))
);

CREATE TABLE media_servers (
    id            INTEGER PRIMARY KEY,
    name          TEXT    NOT NULL UNIQUE CHECK (name <> ''),
    kind          TEXT    NOT NULL CHECK (kind IN ('plex', 'jellyfin', 'kodi')),
    base_url      TEXT    NOT NULL CHECK (base_url <> ''),
    secrets       BLOB,   -- secretbox ciphertext
    path_mappings TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(path_mappings) AND json_type(path_mappings) = 'array'),
    refresh_on    TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(refresh_on) AND json_type(refresh_on) = 'array'),
    enabled       BOOLEAN NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))
);

-- ---------------------------------------------------------------- Derived library state
--
-- movie_state derives the library status of each movie:
--   downloading  an active queue entry exists (queued, downloading, paused, completed,
--                importing, warning), whether or not a file exists;
--   missing      no movie_files row;
--   upgradable   upgrades are allowed and the cutoff is not met: the file's quality is
--                absent from the profile, ranks below the cutoff item, or its stored
--                custom_score is below cutoff_score (same rule as decision.CutoffMet);
--   available    otherwise.
-- Not covered here (computed in Go): minimum availability / release dates against the
-- clock (a "missing" movie may not be released yet), revision-only upgrades (PROPER),
-- and custom_score staleness when scoring rules change (the score is recomputed by Go).
CREATE VIEW movie_state AS
SELECT
    m.id AS movie_id,
    f.id AS movie_file_id,
    CAST(CASE
        WHEN EXISTS (SELECT 1 FROM downloads d
                      WHERE d.movie_id = m.id
                        AND d.status IN ('queued', 'downloading', 'paused', 'completed', 'importing', 'warning'))
            THEN 'downloading'
        WHEN f.id IS NULL THEN 'missing'
        WHEN p.upgrade_allowed = 1 AND NOT coalesce(
                 (SELECT fi.position FROM quality_profile_items fi
                   WHERE fi.profile_id = p.id AND fi.quality = f.quality)
                 <= (SELECT min(ci.position) FROM quality_profile_items ci
                      WHERE ci.profile_id = p.id
                        AND (ci.quality = p.cutoff_quality OR ci.group_name = p.cutoff_quality))
                 AND f.custom_score >= p.cutoff_score,
                 0)
            THEN 'upgradable'
        ELSE 'available'
    END AS TEXT) AS state
FROM movies m
JOIN quality_profiles p ON p.id = m.quality_profile_id
LEFT JOIN movie_files f ON f.movie_id = m.id;

-- +goose Down

DROP VIEW movie_state;
DROP TABLE media_servers;
DROP TABLE notifications;
DROP TABLE import_list_exclusions;
DROP TABLE import_lists;
DROP TRIGGER history_retention_only;
DROP TRIGGER history_no_update;
DROP TABLE history;
DROP TABLE blocklist;
DROP TABLE downloads;
DROP TABLE remote_path_mappings;
DROP TABLE download_clients;
DROP TABLE rss_state;
DROP TABLE indexers;
DROP TABLE movie_tags;
DROP TABLE movie_files;
DROP TABLE alternate_titles;
DROP TABLE movies;
DROP TABLE collections;
DROP TABLE tags;
DROP TABLE root_folders;
DROP TABLE profile_scores;
DROP TABLE scoring_conditions;
DROP TABLE scoring_rules;
DROP TABLE quality_profile_items;
DROP TABLE quality_profiles;
DROP TABLE quality_sizes;
DROP TABLE jobs;
DROP TABLE saved_views;
DROP TABLE api_keys;
DROP TABLE sessions;
DROP TABLE users;
DROP TABLE settings;
