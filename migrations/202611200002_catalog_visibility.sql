-- +goose Up
CREATE VIEW catalog_public_universities AS
SELECT * FROM universities WHERE status = 'active' AND deleted_at IS NULL;

CREATE VIEW catalog_public_programs AS
SELECT p.* FROM programs AS p
WHERE p.status = 'active' AND p.deleted_at IS NULL
  AND (p.university_id IS NULL OR EXISTS (
      SELECT 1 FROM catalog_public_universities AS u WHERE u.id = p.university_id));

CREATE VIEW catalog_public_courses AS
SELECT c.* FROM courses AS c
WHERE c.status = 'active' AND c.deleted_at IS NULL
  AND (c.program_id IS NULL OR EXISTS (
      SELECT 1 FROM catalog_public_programs AS p WHERE p.id = c.program_id));

-- +goose StatementBegin
CREATE FUNCTION catalog_public_topic_parents(parent_id UUID, child_course_id UUID)
RETURNS BOOLEAN LANGUAGE plpgsql STABLE AS $$
DECLARE
    ancestor UUID := parent_id;
    seen UUID[] := '{}';
BEGIN
    WHILE ancestor IS NOT NULL LOOP
        IF ancestor = ANY(seen) THEN
            RETURN FALSE;
        END IF;
        seen := array_append(seen, ancestor);
        SELECT t.parent_topic_id INTO ancestor FROM topics AS t
         WHERE t.id = ancestor AND t.course_id IS NOT DISTINCT FROM child_course_id
           AND t.status = 'active' AND t.deleted_at IS NULL;
        IF NOT FOUND THEN
            RETURN FALSE;
        END IF;
    END LOOP;
    RETURN TRUE;
END;
$$;
-- +goose StatementEnd

CREATE VIEW catalog_public_topics AS
SELECT t.* FROM topics AS t
WHERE t.status = 'active' AND t.deleted_at IS NULL
  AND (t.course_id IS NULL OR EXISTS (
      SELECT 1 FROM catalog_public_courses AS c WHERE c.id = t.course_id))
  AND (t.parent_topic_id IS NULL OR catalog_public_topic_parents(t.parent_topic_id, t.course_id));

-- +goose Down
DROP VIEW catalog_public_topics;
DROP FUNCTION catalog_public_topic_parents(UUID, UUID);
DROP VIEW catalog_public_courses;
DROP VIEW catalog_public_programs;
DROP VIEW catalog_public_universities;
