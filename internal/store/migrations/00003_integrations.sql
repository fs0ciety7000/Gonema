-- Integrations (phase 6): health of notifications and media servers.
--
-- notifications and media_servers gain the outcome of their last calls, shown in
-- the settings pages and turned into health issues after repeated failures:
--   failure_count   consecutive failed deliveries (or refreshes), 0 after a success;
--   last_error      plain-language reason of the last failure, secrets redacted;
--   last_ok_at      time of the last success (Unix ms);
--   last_failed_at  time of the last failure (Unix ms).
-- media_servers also gains options, a JSON object of kind-specific switches
-- (Kodi: {"cleanOnDelete": true} runs a library clean after a deletion).
--
-- Down drops the columns (the health history is lost, the settings are kept).

-- +goose Up

ALTER TABLE notifications ADD COLUMN failure_count INTEGER NOT NULL DEFAULT 0 CHECK (failure_count >= 0);
ALTER TABLE notifications ADD COLUMN last_error TEXT;
ALTER TABLE notifications ADD COLUMN last_ok_at INTEGER;
ALTER TABLE notifications ADD COLUMN last_failed_at INTEGER;

ALTER TABLE media_servers ADD COLUMN options TEXT NOT NULL DEFAULT '{}'
    CHECK (json_valid(options) AND json_type(options) = 'object');
ALTER TABLE media_servers ADD COLUMN failure_count INTEGER NOT NULL DEFAULT 0 CHECK (failure_count >= 0);
ALTER TABLE media_servers ADD COLUMN last_error TEXT;
ALTER TABLE media_servers ADD COLUMN last_ok_at INTEGER;
ALTER TABLE media_servers ADD COLUMN last_failed_at INTEGER;

-- +goose Down

ALTER TABLE media_servers DROP COLUMN last_failed_at;
ALTER TABLE media_servers DROP COLUMN last_ok_at;
ALTER TABLE media_servers DROP COLUMN last_error;
ALTER TABLE media_servers DROP COLUMN failure_count;
ALTER TABLE media_servers DROP COLUMN options;

ALTER TABLE notifications DROP COLUMN last_failed_at;
ALTER TABLE notifications DROP COLUMN last_ok_at;
ALTER TABLE notifications DROP COLUMN last_error;
ALTER TABLE notifications DROP COLUMN failure_count;
