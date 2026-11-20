-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS topics_public_page_idx
    ON topics (order_index, title, id) WHERE status = 'active' AND deleted_at IS NULL;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_index WHERE NOT indisvalid AND indexrelid = 'topics_public_page_idx'::regclass
    ) THEN
        RAISE EXCEPTION 'Rebuild the invalid catalog pagination index before retrying';
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS topics_public_page_idx;
