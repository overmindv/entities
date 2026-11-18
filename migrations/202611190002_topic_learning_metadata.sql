-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION catalog_learning_outcomes_valid(outcomes TEXT[]) RETURNS BOOLEAN
LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT CASE
        WHEN cardinality(outcomes) = 0 THEN true
        WHEN cardinality(outcomes) > 8 OR array_ndims(outcomes) <> 1 OR array_lower(outcomes, 1) <> 1 THEN false
        ELSE (
            SELECT count(DISTINCT outcome) = cardinality(outcomes)
                AND bool_and(octet_length(outcome) BETWEEN 1 AND 256
                    AND btrim(outcome) = outcome AND outcome !~ '[[:cntrl:]]')
            FROM unnest(outcomes) AS list(outcome)
        )
    END;
$$;
-- +goose StatementEnd

ALTER TABLE topics
    ADD COLUMN estimated_study_minutes SMALLINT CHECK (estimated_study_minutes BETWEEN 1 AND 1200),
    ADD COLUMN learning_outcomes TEXT[] NOT NULL DEFAULT '{}'
        CONSTRAINT topics_learning_outcomes_valid CHECK (catalog_learning_outcomes_valid(learning_outcomes));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION catalog_graph_topic(item topics) RETURNS JSONB
LANGUAGE sql STABLE STRICT AS $$
    SELECT jsonb_build_object('kind', 'topic', 'topic_id', item.id, 'tombstone', item.deleted_at IS NOT NULL)
        || CASE WHEN item.deleted_at IS NULL THEN jsonb_build_object('topic', jsonb_build_object(
            'title', left(item.title, 256),
            'course_id', item.course_id,
            'parent_topic_id', item.parent_topic_id,
            'order_index', item.order_index,
            'difficulty', item.difficulty,
            'status', item.status
        ) || CASE WHEN item.estimated_study_minutes IS NOT NULL OR cardinality(item.learning_outcomes) > 0
            THEN jsonb_build_object('learning_metadata', jsonb_build_object(
                'estimated_minutes', item.estimated_study_minutes,
                'outcomes', item.learning_outcomes
            )) ELSE '{}'::jsonb END) ELSE '{}'::jsonb END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION catalog_graph_topic(item topics) RETURNS JSONB
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

ALTER TABLE topics DROP COLUMN learning_outcomes, DROP COLUMN estimated_study_minutes;
DROP FUNCTION catalog_learning_outcomes_valid(TEXT[]);
