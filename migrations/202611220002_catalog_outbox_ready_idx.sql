-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY outbox_events_ready_idx
    ON outbox_events (COALESCE(lease_until, next_attempt_at), occurred_at, id)
    WHERE published_at IS NULL;

-- +goose Down
DROP INDEX CONCURRENTLY outbox_events_ready_idx;
