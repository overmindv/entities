-- +goose Up
LOCK TABLE topics, topic_prerequisites IN SHARE MODE;

CREATE TABLE catalog_clock (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    revision BIGINT NOT NULL CHECK (revision >= 0)
);
INSERT INTO catalog_clock (revision) VALUES (0);

CREATE TABLE catalog_aggregate_versions (
    aggregate_type TEXT NOT NULL,
    aggregate_id UUID NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    PRIMARY KEY (aggregate_type, aggregate_id)
);

CREATE TABLE catalog_graph_versions (
    kind SMALLINT NOT NULL CHECK (kind IN (1, 2)),
    topic_id UUID NOT NULL,
    target_id UUID NOT NULL,
    valid_from BIGINT NOT NULL CHECK (valid_from >= 0),
    valid_until BIGINT CHECK (valid_until > valid_from),
    document JSONB NOT NULL,
    PRIMARY KEY (kind, topic_id, target_id, valid_from)
);
CREATE UNIQUE INDEX catalog_graph_current_uidx
    ON catalog_graph_versions (kind, topic_id, target_id) WHERE valid_until IS NULL;

ALTER TABLE outbox_events
    ADD COLUMN catalog_revision BIGINT CHECK (catalog_revision > 0),
    ADD COLUMN aggregate_version BIGINT CHECK (aggregate_version > 0),
    ADD COLUMN graph_change JSONB,
    ADD CONSTRAINT outbox_catalog_version_pair CHECK ((catalog_revision IS NULL) = (aggregate_version IS NULL)),
    ADD CONSTRAINT outbox_graph_has_version CHECK (graph_change IS NULL OR catalog_revision IS NOT NULL);
CREATE UNIQUE INDEX outbox_catalog_revision_uidx
    ON outbox_events (catalog_revision) WHERE catalog_revision IS NOT NULL;
CREATE INDEX outbox_graph_revision_idx
    ON outbox_events (catalog_revision) WHERE graph_change IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION catalog_graph_topic(item topics) RETURNS JSONB
LANGUAGE sql STABLE STRICT AS $$
    SELECT jsonb_build_object('kind', 'topic', 'topic_id', item.id, 'tombstone', item.deleted_at IS NOT NULL)
        || CASE WHEN item.deleted_at IS NULL THEN jsonb_build_object('topic', jsonb_build_object(
            'title', left(item.title, 256),
            'course_id', item.course_id,
            'parent_topic_id', item.parent_topic_id,
            'order_index', item.order_index,
            'difficulty', item.difficulty,
            'status', item.status
        )) ELSE '{}'::jsonb END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION catalog_graph_edge(source UUID, target UUID, removed BOOLEAN) RETURNS JSONB
LANGUAGE sql STABLE STRICT AS $$
    SELECT jsonb_build_object('kind', 'prerequisite', 'topic_id', source,
        'prerequisite_topic_id', target, 'tombstone', removed);
$$;
-- +goose StatementEnd

INSERT INTO catalog_graph_versions (kind, topic_id, target_id, valid_from, document)
SELECT 1, t.id, '00000000-0000-0000-0000-000000000000', 0, catalog_graph_topic(t)
FROM topics AS t;

INSERT INTO catalog_graph_versions (kind, topic_id, target_id, valid_from, document)
SELECT 2, topic_id, prerequisite_topic_id, 0, catalog_graph_edge(topic_id, prerequisite_topic_id, false)
FROM topic_prerequisites;

-- +goose StatementBegin
CREATE FUNCTION version_catalog_event() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    record_kind SMALLINT;
    record_target UUID := '00000000-0000-0000-0000-000000000000';
BEGIN
    -- Версия выдается под транзакционной блокировкой до commit, без пропусков при rollback.
    UPDATE catalog_clock SET revision = revision + 1 WHERE singleton
    RETURNING revision INTO STRICT NEW.catalog_revision;

    INSERT INTO catalog_aggregate_versions AS v (aggregate_type, aggregate_id, version)
    VALUES (NEW.aggregate_type, NEW.aggregate_id, 1)
    ON CONFLICT (aggregate_type, aggregate_id) DO UPDATE SET version = v.version + 1
    RETURNING version INTO NEW.aggregate_version;

    NEW.graph_change := NULL;
    CASE NEW.event_type
        WHEN 'catalog.topic.created', 'catalog.topic.updated', 'catalog.topic.deleted' THEN
            record_kind := 1;
            SELECT catalog_graph_topic(t) INTO STRICT NEW.graph_change FROM topics AS t WHERE t.id = NEW.aggregate_id;
        WHEN 'catalog.topic.prerequisite.added', 'catalog.topic.prerequisite.removed' THEN
            record_kind := 2;
            record_target := (NEW.payload->>'prerequisite_topic_id')::uuid;
            NEW.graph_change := catalog_graph_edge(NEW.aggregate_id, record_target,
                NEW.event_type = 'catalog.topic.prerequisite.removed');
        ELSE
            RETURN NEW;
    END CASE;

    UPDATE catalog_graph_versions SET valid_until = NEW.catalog_revision
    WHERE (kind, topic_id, target_id) = (record_kind, NEW.aggregate_id, record_target)
      AND valid_until IS NULL;

    INSERT INTO catalog_graph_versions (kind, topic_id, target_id, valid_from, document)
    VALUES (record_kind, NEW.aggregate_id, record_target, NEW.catalog_revision, NEW.graph_change);

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER outbox_version_catalog_event BEFORE INSERT ON outbox_events
FOR EACH ROW EXECUTE FUNCTION version_catalog_event();

-- +goose Down
DROP TRIGGER outbox_version_catalog_event ON outbox_events;
DROP FUNCTION version_catalog_event();
DROP INDEX outbox_graph_revision_idx;
DROP INDEX outbox_catalog_revision_uidx;
ALTER TABLE outbox_events
    DROP CONSTRAINT outbox_graph_has_version,
    DROP CONSTRAINT outbox_catalog_version_pair,
    DROP COLUMN graph_change,
    DROP COLUMN aggregate_version,
    DROP COLUMN catalog_revision;
DROP TABLE catalog_graph_versions;
DROP FUNCTION catalog_graph_edge(UUID, UUID, BOOLEAN);
DROP FUNCTION catalog_graph_topic(topics);
DROP TABLE catalog_aggregate_versions;
DROP TABLE catalog_clock;
