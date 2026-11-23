-- +goose Up

CREATE TABLE program_goal_templates (
    id UUID PRIMARY KEY,
    program_id UUID NOT NULL REFERENCES programs (id),
    latest_version BIGINT NOT NULL CHECK (latest_version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp()
);

CREATE INDEX program_goal_templates_program_id_idx ON program_goal_templates (program_id, id);

CREATE TABLE program_goal_template_versions (
    template_id UUID NOT NULL REFERENCES program_goal_templates (id),
    version BIGINT NOT NULL CHECK (version > 0),
    title TEXT NOT NULL CHECK (octet_length(title) BETWEEN 1 AND 160),
    description TEXT NOT NULL CHECK (octet_length(description) <= 2000),
    catalog_revision BIGINT NOT NULL CHECK (catalog_revision >= 0),
    published_by UUID NOT NULL,
    published_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (template_id, version)
);

ALTER TABLE program_goal_templates
    ADD CONSTRAINT program_goal_templates_published_head_fk
    FOREIGN KEY (id, latest_version) REFERENCES program_goal_template_versions (template_id, version)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE program_goal_template_targets (
    template_id UUID NOT NULL,
    version BIGINT NOT NULL,
    topic_id UUID NOT NULL REFERENCES topics (id),
    position SMALLINT NOT NULL CHECK (position BETWEEN 0 AND 15),
    target_score_bps SMALLINT NOT NULL CHECK (target_score_bps BETWEEN 1 AND 10000),
    PRIMARY KEY (template_id, version, position),
    UNIQUE (template_id, version, topic_id),
    FOREIGN KEY (template_id, version) REFERENCES program_goal_template_versions (template_id, version)
);
