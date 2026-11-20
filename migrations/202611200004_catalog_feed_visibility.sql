-- +goose Up
ALTER TABLE outbox_events ADD COLUMN feed_visibility JSONB;

-- +goose StatementBegin
CREATE FUNCTION catalog_feed_ancestors(kind TEXT, subject UUID) RETURNS TEXT[]
LANGUAGE plpgsql STABLE AS $$
DECLARE
    ancestors TEXT[];
BEGIN
    CASE kind
        WHEN 'university' THEN
            SELECT ARRAY[]::TEXT[] INTO ancestors FROM catalog_public_universities WHERE id=subject;
        WHEN 'program' THEN
            SELECT array_remove(ARRAY['university:' || university_id], NULL)
              INTO ancestors FROM catalog_public_programs WHERE id=subject;
        WHEN 'course' THEN
            SELECT array_remove(ARRAY['program:' || c.program_id, 'university:' || p.university_id], NULL)
              INTO ancestors FROM catalog_public_courses c LEFT JOIN programs p ON p.id=c.program_id
             WHERE c.id=subject;
        WHEN 'topic' THEN
            WITH RECURSIVE chain AS (
                SELECT id,parent_topic_id,course_id,0 AS depth
                  FROM catalog_public_topics WHERE id=subject
                UNION ALL
                SELECT p.id,p.parent_topic_id,p.course_id,t.depth+1
                  FROM chain t JOIN topics p ON p.id=t.parent_topic_id
                 WHERE t.depth < 124
            )
            SELECT array_remove(ARRAY[
                'course:' || t.course_id, 'program:' || c.program_id, 'university:' || p.university_id
            ], NULL) || ARRAY(SELECT 'topic:' || id FROM chain WHERE depth>0 ORDER BY depth)
              INTO ancestors FROM chain t
              LEFT JOIN courses c ON c.id=t.course_id
              LEFT JOIN programs p ON p.id=c.program_id
             WHERE t.depth=0 AND EXISTS (SELECT 1 FROM chain WHERE parent_topic_id IS NULL);
        ELSE
            RETURN NULL;
    END CASE;
    RETURN ancestors;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION stamp_catalog_feed_visibility() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    ancestors TEXT[];
    scope_reset BOOLEAN := COALESCE((NEW.feed_visibility->>'scope_reset')::BOOLEAN, FALSE);
BEGIN
    IF NEW.event_type NOT IN ('catalog.' || NEW.aggregate_type || '.created',
                              'catalog.' || NEW.aggregate_type || '.updated',
                              'catalog.' || NEW.aggregate_type || '.deleted') THEN
        NEW.feed_visibility := NULL;
        RETURN NEW;
    END IF;
    IF NEW.event_type <> 'catalog.' || NEW.aggregate_type || '.deleted' THEN
        SELECT catalog_feed_ancestors(NEW.aggregate_type, NEW.aggregate_id) INTO ancestors;
    END IF;
    NEW.feed_visibility := CASE WHEN ancestors IS NULL THEN '{"visible":false}'::jsonb
        ELSE jsonb_build_object('visible', TRUE, 'ancestors', ancestors, 'scope_reset', scope_reset) END;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- Блокировку часов каталога берет триггер версии до чтения видимости.
CREATE TRIGGER outbox_visibility_after_version BEFORE INSERT ON outbox_events
FOR EACH ROW EXECUTE FUNCTION stamp_catalog_feed_visibility();

-- +goose Down
DROP TRIGGER outbox_visibility_after_version ON outbox_events;
DROP FUNCTION stamp_catalog_feed_visibility();
DROP FUNCTION catalog_feed_ancestors(TEXT, UUID);
ALTER TABLE outbox_events DROP COLUMN feed_visibility;
