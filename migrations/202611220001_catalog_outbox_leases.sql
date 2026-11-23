-- +goose Up
ALTER TABLE outbox_events
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN claim_token UUID,
    ADD COLUMN lease_until TIMESTAMPTZ,
    ADD CONSTRAINT outbox_events_claim_pair CHECK (
        (claim_token IS NULL AND lease_until IS NULL)
        OR (claim_token IS NOT NULL AND lease_until IS NOT NULL AND published_at IS NULL)
    ) NOT VALID;

-- +goose Down
ALTER TABLE outbox_events
    DROP CONSTRAINT outbox_events_claim_pair,
    DROP COLUMN lease_until,
    DROP COLUMN claim_token,
    DROP COLUMN next_attempt_at;
