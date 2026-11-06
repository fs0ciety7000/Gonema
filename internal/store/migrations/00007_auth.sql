-- Authentication (phase 8): the admin account, its browser sessions and its
-- API key. Migration 00001 created users, sessions and api_keys ahead of time;
-- nothing ever wrote to them before this version.
--
-- users gains password_changed_at. password_hash is a PHC string
-- "$argon2id$v=19$m=...,t=...,p=...$salt$hash". One admin account for now;
-- usernames are matched without regard to case (COLLATE NOCASE in the queries).
--
-- sessions is recreated: the cookie holds a random 256-bit token and only its
-- SHA-256 is stored (token_hash), so a copy of the database does not give a
-- usable session. id is the public handle used to list and revoke sessions. A
-- session ends at expires_at (absolute) or after an idle period measured from
-- last_seen_at (checked in Go). ip and user_agent are shown in the session list.
--
-- api_keys is recreated: one key per user (unique user_id); only its SHA-256
-- and its last four characters are stored. The key is shown once, at creation.
--
-- Down restores the tables of 00001: every session and API key is lost (the
-- account is kept, without password_changed_at).

-- +goose Up

ALTER TABLE users ADD COLUMN password_changed_at INTEGER NOT NULL DEFAULT 0;

DROP TABLE sessions;
CREATE TABLE sessions (
    id           INTEGER PRIMARY KEY,
    token_hash   BLOB    NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL CHECK (expires_at > created_at),
    ip           TEXT    NOT NULL DEFAULT '' CHECK (length(ip) <= 64),
    user_agent   TEXT    NOT NULL DEFAULT '' CHECK (length(user_agent) <= 256)
);
CREATE INDEX sessions_user_id ON sessions (user_id);
CREATE INDEX sessions_expires_at ON sessions (expires_at);

DROP TABLE api_keys;
CREATE TABLE api_keys (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    key_hash     BLOB    NOT NULL UNIQUE CHECK (length(key_hash) = 32),
    last4        TEXT    NOT NULL CHECK (length(last4) = 4),
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER
);

-- +goose Down

DROP TABLE api_keys;
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

DROP TABLE sessions;
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

ALTER TABLE users DROP COLUMN password_changed_at;
