-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE catalog_status AS ENUM ('draft', 'active', 'hidden', 'archived');
CREATE TYPE degree_level AS ENUM ('bachelor', 'master', 'specialist', 'phd', 'other');
CREATE TYPE topic_difficulty AS ENUM ('intro', 'basic', 'medium', 'hard', 'advanced');

CREATE TABLE universities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    short_name TEXT NOT NULL DEFAULT '',
    city TEXT NOT NULL DEFAULT '',
    country TEXT NOT NULL DEFAULT '',
    website_url TEXT NOT NULL DEFAULT '',
    logo_file_id UUID,
    status catalog_status NOT NULL DEFAULT 'draft',
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

CREATE TABLE programs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    university_id UUID REFERENCES universities(id),
    name TEXT NOT NULL,
    short_name TEXT NOT NULL DEFAULT '',
    faculty TEXT NOT NULL DEFAULT '',
    degree_level degree_level NOT NULL DEFAULT 'other',
    start_year INTEGER,
    status catalog_status NOT NULL DEFAULT 'draft',
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT programs_start_year_check CHECK (start_year IS NULL OR start_year BETWEEN 1900 AND 2200)
);

CREATE TABLE courses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    program_id UUID REFERENCES programs(id),
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    semester INTEGER,
    year_number INTEGER,
    status catalog_status NOT NULL DEFAULT 'draft',
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT courses_semester_check CHECK (semester IS NULL OR semester BETWEEN 1 AND 20),
    CONSTRAINT courses_year_number_check CHECK (year_number IS NULL OR year_number BETWEEN 1 AND 10)
);

CREATE TABLE topics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id UUID REFERENCES courses(id),
    parent_topic_id UUID REFERENCES topics(id),
    title TEXT NOT NULL,
    slug TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    order_index INTEGER NOT NULL DEFAULT 0,
    difficulty topic_difficulty NOT NULL DEFAULT 'basic',
    status catalog_status NOT NULL DEFAULT 'draft',
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT topics_not_own_parent CHECK (parent_topic_id IS NULL OR parent_topic_id <> id)
);

CREATE TABLE topic_prerequisites (
    topic_id UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    prerequisite_topic_id UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (topic_id, prerequisite_topic_id),
    CONSTRAINT topic_prerequisites_not_self CHECK (topic_id <> prerequisite_topic_id)
);

CREATE TABLE outbox_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_user_id UUID NOT NULL,
    aggregate_id UUID NOT NULL,
    aggregate_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    published_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX universities_active_name_uidx ON universities (lower(name)) WHERE deleted_at IS NULL AND status = 'active';
CREATE INDEX universities_name_idx ON universities (name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX programs_active_name_uidx ON programs (COALESCE(university_id, '00000000-0000-0000-0000-000000000000'::uuid), lower(name)) WHERE deleted_at IS NULL AND status = 'active';
CREATE INDEX programs_university_name_idx ON programs (university_id, name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX courses_program_slug_uidx ON courses (COALESCE(program_id, '00000000-0000-0000-0000-000000000000'::uuid), slug) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX topics_course_slug_uidx ON topics (COALESCE(course_id, '00000000-0000-0000-0000-000000000000'::uuid), slug) WHERE deleted_at IS NULL;
CREATE INDEX topics_course_parent_idx ON topics (course_id, parent_topic_id) WHERE deleted_at IS NULL;
CREATE INDEX topic_prerequisites_topic_idx ON topic_prerequisites (topic_id);
CREATE INDEX topic_prerequisites_prerequisite_idx ON topic_prerequisites (prerequisite_topic_id);
CREATE INDEX outbox_events_pending_idx ON outbox_events (occurred_at) WHERE published_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS topic_prerequisites;
DROP TABLE IF EXISTS topics;
DROP TABLE IF EXISTS courses;
DROP TABLE IF EXISTS programs;
DROP TABLE IF EXISTS universities;
DROP TYPE IF EXISTS topic_difficulty;
DROP TYPE IF EXISTS degree_level;
DROP TYPE IF EXISTS catalog_status;
