-- Web UI lot 3 (phase 7): quality profile editor and scoring rules.
--
-- quality_profiles.preferred_protocol ranks releases of that protocol first
-- among equals for the movies of the profile; '' follows the global setting
-- (Settings > Acquisition).
--
-- quality_profile_sizes holds the size limits a profile sets for some of its
-- qualities, in MB per minute of runtime, overriding quality_sizes (global)
-- and the defaults of package quality. max_mb_per_min 0 means no upper bound.
--
-- scoring_conditions.kind gains 'indexer' (name of the indexer offering the
-- release). SQLite cannot change a CHECK constraint: the table is rebuilt
-- (create, copy, drop, rename) in the migration transaction. Only its rule_id
-- references another table; nothing references scoring_conditions.
--
-- Down drops the profile sizes and the protocol, and deletes the conditions of
-- kind 'indexer' (the previous schema cannot hold them) and the rules left
-- without any condition: destructive, run it only on explicit operator action.

-- +goose Up

ALTER TABLE quality_profiles ADD COLUMN preferred_protocol TEXT NOT NULL DEFAULT ''
    CHECK (preferred_protocol IN ('', 'usenet', 'torrent'));

CREATE TABLE quality_profile_sizes (
    profile_id           INTEGER NOT NULL REFERENCES quality_profiles (id) ON DELETE CASCADE,
    quality              TEXT    NOT NULL CHECK (quality <> ''),
    min_mb_per_min       REAL    NOT NULL CHECK (min_mb_per_min >= 0),
    preferred_mb_per_min REAL    NOT NULL CHECK (preferred_mb_per_min >= min_mb_per_min),
    max_mb_per_min       REAL    NOT NULL CHECK (max_mb_per_min = 0 OR max_mb_per_min >= preferred_mb_per_min),
    PRIMARY KEY (profile_id, quality)
) WITHOUT ROWID;

CREATE TABLE scoring_conditions_new (
    id       INTEGER PRIMARY KEY,
    rule_id  INTEGER NOT NULL REFERENCES scoring_rules (id) ON DELETE CASCADE,
    kind     TEXT    NOT NULL CHECK (kind IN (
                 'title_regex', 'release_group', 'source', 'resolution', 'codec', 'hdr',
                 'audio', 'edition', 'language', 'indexer_flag', 'indexer', 'size_range', 'year_range')),
    negate   BOOLEAN NOT NULL DEFAULT 0 CHECK (negate IN (0, 1)),
    required BOOLEAN NOT NULL DEFAULT 0 CHECK (required IN (0, 1)),
    value    TEXT    NOT NULL CHECK (json_valid(value))
);
INSERT INTO scoring_conditions_new (id, rule_id, kind, negate, required, value)
SELECT id, rule_id, kind, negate, required, value FROM scoring_conditions;
DROP TABLE scoring_conditions;
ALTER TABLE scoring_conditions_new RENAME TO scoring_conditions;
CREATE INDEX scoring_conditions_rule_id ON scoring_conditions (rule_id);

-- +goose Down

CREATE TABLE scoring_conditions_old (
    id       INTEGER PRIMARY KEY,
    rule_id  INTEGER NOT NULL REFERENCES scoring_rules (id) ON DELETE CASCADE,
    kind     TEXT    NOT NULL CHECK (kind IN (
                 'title_regex', 'release_group', 'source', 'resolution', 'codec', 'hdr',
                 'audio', 'edition', 'language', 'indexer_flag', 'size_range', 'year_range')),
    negate   BOOLEAN NOT NULL DEFAULT 0 CHECK (negate IN (0, 1)),
    required BOOLEAN NOT NULL DEFAULT 0 CHECK (required IN (0, 1)),
    value    TEXT    NOT NULL CHECK (json_valid(value))
);
INSERT INTO scoring_conditions_old (id, rule_id, kind, negate, required, value)
SELECT id, rule_id, kind, negate, required, value FROM scoring_conditions WHERE kind <> 'indexer';
DROP TABLE scoring_conditions;
ALTER TABLE scoring_conditions_old RENAME TO scoring_conditions;
CREATE INDEX scoring_conditions_rule_id ON scoring_conditions (rule_id);
DELETE FROM scoring_rules WHERE id NOT IN (SELECT rule_id FROM scoring_conditions);

DROP TABLE quality_profile_sizes;
ALTER TABLE quality_profiles DROP COLUMN preferred_protocol;
