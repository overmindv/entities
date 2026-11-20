package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/overmindv/entities/internal/domain"
)

func testCatalogPool(t testing.TB, lockTimeout string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("ENTITIES_TEST_DSN")
	if dsn == "" {
		t.Skip("ENTITIES_TEST_DSN is not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("ENTITIES_TEST_DSN must point to a dedicated database ending in _test")
	}
	if lockTimeout != "" {
		cfg.ConnConfig.RuntimeParams["lock_timeout"] = lockTimeout
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	return pool
}

//nolint:paralleltest // тест блокирует таблицу общей component-БД
func TestPostgresCatalogOutboxRollback(t *testing.T) {
	ctx := t.Context()
	pool := testCatalogPool(t, "")
	healthy := New(pool)
	blocked := New(testCatalogPool(t, "200ms"))
	actor := uuid.NewString()
	label := "rollback-" + actor
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	u, err := healthy.CreateUniversity(ctx, domain.University{Name: label, Status: domain.StatusDraft, CreatedBy: actor})
	check(err)
	p, err := healthy.CreateProgram(ctx, domain.Program{
		Name: label, DegreeLevel: "bachelor", Status: domain.StatusDraft, CreatedBy: actor, UniversityID: &u.ID,
	})
	check(err)
	c, err := healthy.CreateCourse(ctx, domain.Course{
		Name: label, Slug: label, Status: domain.StatusDraft, CreatedBy: actor, ProgramID: &p.ID,
	})
	check(err)
	a, err := healthy.CreateTopic(ctx, domain.Topic{
		Title: label, Slug: label, Difficulty: "basic", Status: domain.StatusDraft, CreatedBy: actor, CourseID: &c.ID,
	})
	check(err)
	b, err := healthy.CreateTopic(ctx, domain.Topic{
		Title: label + "-ref", Slug: label + "-ref", Difficulty: "basic", Status: domain.StatusDraft, CreatedBy: actor, CourseID: &c.ID,
	})
	check(err)
	pair := domain.TopicPrerequisite{TopicID: b.ID, PrerequisiteTopicID: a.ID}
	newLabel := label + "-new"

	read := func(t *testing.T, query string, args ...any) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}

		return value
	}
	run := func(name, eventType, query string, args []any, mutate func(*Postgres) error) {
		t.Run(name, func(t *testing.T) {
			const countEvents = "SELECT count(*) FROM outbox_events WHERE actor_user_id=$1 AND event_type=$2"
			var beforeEvents, afterEvents int
			if err := pool.QueryRow(ctx, countEvents, actor, eventType).Scan(&beforeEvents); err != nil {
				t.Fatal(err)
			}
			before := read(t, query, args...)
			lock, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = lock.Rollback(cleanup)
			}()
			if _, lockErr := lock.Exec(ctx, "LOCK TABLE outbox_events IN ACCESS EXCLUSIVE MODE"); lockErr != nil {
				t.Fatal(lockErr)
			}
			err = mutate(blocked)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
				t.Fatal("wanted outbox lock timeout", err)
			}
			if err := lock.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if after := read(t, query, args...); after != before {
				t.Fatal("catalog changed despite outbox failure", before, after)
			}
			if err := mutate(healthy); err != nil {
				t.Fatal("mutation failed with available outbox", err)
			}
			if after := read(t, query, args...); after == before {
				t.Fatal("successful mutation left catalog unchanged", before)
			}
			if err := pool.QueryRow(ctx, countEvents, actor, eventType).Scan(&afterEvents); err != nil {
				t.Fatal(err)
			}
			if afterEvents != beforeEvents+1 {
				t.Fatal("expected one event for successful mutation", beforeEvents, afterEvents)
			}
		})
	}

	type fixture struct {
		kind, table, nameColumn, id string
		create, update, hide        func(*Postgres) error
	}
	for _, f := range []fixture{
		{
			kind: "university", table: "universities", nameColumn: "name", id: u.ID,
			create: func(r *Postgres) error {
				_, err := r.CreateUniversity(ctx, domain.University{Name: newLabel, Status: domain.StatusDraft, CreatedBy: actor})

				return err
			},
			update: func(r *Postgres) error {
				changed := u
				changed.Name = label + "-updated"
				_, err := r.UpdateUniversity(ctx, changed)

				return err
			},
			hide: func(r *Postgres) error {
				_, err := r.ChangeUniversityStatus(ctx, u.ID, domain.StatusHidden, actor)

				return err
			},
		},
		{
			kind: "program", table: "programs", nameColumn: "name", id: p.ID,
			create: func(r *Postgres) error {
				item := p
				item.Name = newLabel
				_, err := r.CreateProgram(ctx, item)

				return err
			},
			update: func(r *Postgres) error {
				changed := p
				changed.Name = label + "-updated"
				_, err := r.UpdateProgram(ctx, changed)

				return err
			},
			hide: func(r *Postgres) error {
				_, err := r.ChangeProgramStatus(ctx, p.ID, domain.StatusHidden, actor)

				return err
			},
		},
		{
			kind: "course", table: "courses", nameColumn: "name", id: c.ID,
			create: func(r *Postgres) error {
				item := c
				item.Name, item.Slug = newLabel, newLabel
				_, err := r.CreateCourse(ctx, item)

				return err
			},
			update: func(r *Postgres) error {
				changed := c
				changed.Name = label + "-updated"
				_, err := r.UpdateCourse(ctx, changed)

				return err
			},
			hide: func(r *Postgres) error {
				_, err := r.ChangeCourseStatus(ctx, c.ID, domain.StatusHidden, actor)

				return err
			},
		},
		{
			kind: "topic", table: "topics", nameColumn: "title", id: a.ID,
			create: func(r *Postgres) error {
				item := a
				item.Title, item.Slug = newLabel, newLabel
				_, err := r.CreateTopic(ctx, item)

				return err
			},
			update: func(r *Postgres) error {
				changed := a
				changed.Title = label + "-updated"
				_, err := r.UpdateTopic(ctx, changed)

				return err
			},
			hide: func(r *Postgres) error {
				_, err := r.ChangeTopicStatus(ctx, a.ID, domain.StatusHidden, actor)

				return err
			},
		},
	} {
		event := "catalog." + f.kind
		created := "SELECT count(*)::text FROM " + f.table + " WHERE created_by=$1 AND " + f.nameColumn + "=$2"
		row := "SELECT to_jsonb(item)::text FROM " + f.table + " item WHERE id=$1"
		run(f.kind+"/create", event+".created", created, []any{actor, newLabel}, f.create)
		run(f.kind+"/update", event+".updated", row, []any{f.id}, f.update)
		run(f.kind+"/status", event+".updated", row, []any{f.id}, f.hide)
	}
	edge := "SELECT count(*)::text FROM topic_prerequisites WHERE topic_id=$1 AND prerequisite_topic_id=$2"
	run("prerequisite/add", "catalog.topic.prerequisite.added", edge, []any{b.ID, a.ID}, func(r *Postgres) error {
		_, err := r.AddPrerequisite(ctx, pair, actor)

		return err
	})
	run("prerequisite/remove", "catalog.topic.prerequisite.removed", edge, []any{b.ID, a.ID}, func(r *Postgres) error {
		return r.RemovePrerequisite(ctx, b.ID, a.ID, actor)
	})
	for _, f := range []struct {
		kind, table, id string
		remove          func(*Postgres, context.Context, string, string) error
	}{
		{"topic", "topics", a.ID, (*Postgres).DeleteTopic},
		{"course", "courses", c.ID, (*Postgres).DeleteCourse},
		{"program", "programs", p.ID, (*Postgres).DeleteProgram},
		{"university", "universities", u.ID, (*Postgres).DeleteUniversity},
	} {
		run(f.kind+"/delete", "catalog."+f.kind+".deleted", "SELECT (deleted_at IS NOT NULL)::text FROM "+f.table+" WHERE id=$1",
			[]any{f.id}, func(r *Postgres) error { return f.remove(r, ctx, f.id, actor) })
	}
}

func BenchmarkCreateUniversityWithEvent(b *testing.B) {
	store := New(testCatalogPool(b, ""))
	input := domain.University{Name: "benchmark", Status: domain.StatusDraft, CreatedBy: uuid.NewString()}
	if _, err := store.CreateUniversity(b.Context(), input); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := store.CreateUniversity(b.Context(), input); err != nil {
			b.Fatal(err)
		}
	}
}
