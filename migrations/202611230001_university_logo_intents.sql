-- +goose Up

CREATE TABLE university_logo_intents (
    id UUID PRIMARY KEY REFERENCES universities (id),
    file_id UUID,
    actor_user_id UUID NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 30),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    claim_token UUID,
    lease_until TIMESTAMPTZ,
    applied_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    CHECK ((claim_token IS NULL) = (lease_until IS NULL))
);

CREATE INDEX university_logo_intents_ready_idx
    ON university_logo_intents ((applied_at IS NOT NULL), (COALESCE(lease_until, next_attempt_at)), id);
