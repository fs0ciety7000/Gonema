-- Web UI lot 2: the history screen filters by event kind and pages by id
-- (newest first). history_event_id serves "event IN (...) AND id < ? ORDER BY
-- id DESC" without scanning the whole table. Indexes only: down is lossless.

-- +goose Up

CREATE INDEX history_event_id ON history (event, id);

-- +goose Down

DROP INDEX history_event_id;
