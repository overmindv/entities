package repository

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
	"github.com/overmindv/entities/internal/service"
)

//nolint:paralleltest // Тест удерживает таблицу связей до начала обеих записей.
func TestConcurrentPrerequisitesRejectCycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	pool := testCatalogPool(t, "")
	writers := testCatalogPool(t, "")
	store := New(pool)
	catalog := service.New(New(writers))
	actor := uuid.NewString()
	course := prerequisiteCourse(t, ctx, store, actor)
	a, b := uuid.NewString(), uuid.NewString()
	seedPrerequisiteTopics(t, ctx, pool, []string{a, b}, &course, actor)
	pids := prerequisiteWriters(t, ctx, writers)

	barrier, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		_ = barrier.Rollback(cleanup)
	}()
	if _, err := barrier.Exec(ctx, "LOCK TABLE topic_prerequisites IN SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, edge := range []domain.TopicPrerequisite{
		{TopicID: a, PrerequisiteTopicID: b},
		{TopicID: b, PrerequisiteTopicID: a},
	} {
		go func() {
			_, addErr := catalog.AddPrerequisite(ctx, edge, actor)
			results <- addErr
		}()
	}
	waitPrerequisiteWriters(t, ctx, pool, pids)
	if err := barrier.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var added, rejected int
	for range 2 {
		if addErr := <-results; addErr == nil {
			added++
		} else {
			requirePrerequisiteError(t, addErr, apperror.TopicPrerequisiteCycle, http.StatusConflict)
			rejected++
		}
	}
	var edges, events int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM topic_prerequisites WHERE topic_id IN ($1, $2)),
		       (SELECT count(*) FROM outbox_events
		        WHERE actor_user_id=$3 AND event_type='catalog.topic.prerequisite.added')`,
		a, b, actor).Scan(&edges, &events); err != nil {
		t.Fatal(err)
	}
	if added != 1 || rejected != 1 || edges != 1 || events != 1 {
		t.Fatal("expected one committed edge and event:", added, rejected, edges, events)
	}
}

func TestPrerequisiteScopeAndIndirectCycle(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	pool := testCatalogPool(t, "")
	store := New(pool)
	catalog := service.New(store)
	actor := uuid.NewString()
	course, other := prerequisiteCourse(t, ctx, store, actor), prerequisiteCourse(t, ctx, store, actor)
	a, b, c, d := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	backlog, backlogPeer := uuid.NewString(), uuid.NewString()
	seedPrerequisiteTopics(t, ctx, pool, []string{a, b, c}, &course, actor)
	seedPrerequisiteTopics(t, ctx, pool, []string{d}, &other, actor)
	seedPrerequisiteTopics(t, ctx, pool, []string{backlog, backlogPeer}, nil, actor)
	add := func(from, to string) error {
		_, err := catalog.AddPrerequisite(ctx, domain.TopicPrerequisite{TopicID: from, PrerequisiteTopicID: to}, actor)

		return err
	}
	for _, err := range []error{add(a, b), add(b, c), add(backlog, backlogPeer)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	requirePrerequisiteError(t, add(c, a), apperror.TopicPrerequisiteCycle, http.StatusConflict)
	requirePrerequisiteError(t, add(a, d), apperror.InvalidTopicPrerequisite, http.StatusUnprocessableEntity)
	requirePrerequisiteError(t, add(backlog, a), apperror.InvalidTopicPrerequisite, http.StatusUnprocessableEntity)
	requirePrerequisiteError(t, add(a, b), apperror.InvalidTopicPrerequisite, http.StatusConflict)
	if err := store.RemovePrerequisite(ctx, b, c, actor); err != nil {
		t.Fatal(err)
	}
	if err := add(c, a); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTopic(ctx, b, actor); err != nil {
		t.Fatal(err)
	}
	requirePrerequisiteError(t, add(c, b), apperror.TopicNotFound, http.StatusNotFound)
}

func BenchmarkAddPrerequisite(b *testing.B) {
	for _, length := range []int{32, 2_048} {
		b.Run(fmt.Sprint(length), func(b *testing.B) {
			ctx := b.Context()
			pool := testCatalogPool(b, "")
			store := New(pool)
			catalog := service.New(store)
			actor := uuid.NewString()
			course := prerequisiteCourse(b, ctx, store, actor)
			ids := make([]string, length+1)
			for i := range ids {
				ids[i] = uuid.NewString()
			}
			seedPrerequisiteTopics(b, ctx, pool, ids, &course, actor)
			if _, err := pool.Exec(ctx, `
				INSERT INTO topic_prerequisites (topic_id, prerequisite_topic_id)
				SELECT ids[i], ids[i+1] FROM (SELECT $1::uuid[] AS ids) input,
				    LATERAL generate_series(2, cardinality(ids)-1) i`, ids); err != nil {
				b.Fatal(err)
			}
			edge := domain.TopicPrerequisite{TopicID: ids[0], PrerequisiteTopicID: ids[1]}
			for b.Loop() {
				if _, err := catalog.AddPrerequisite(ctx, edge, actor); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				if err := store.RemovePrerequisite(ctx, edge.TopicID, edge.PrerequisiteTopicID, actor); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
			}
		})
	}
}

func prerequisiteCourse(t testing.TB, ctx context.Context, store *Postgres, actor string) string {
	t.Helper()
	item, err := store.CreateCourse(ctx, domain.Course{
		Name:      "Prerequisite fixture",
		Slug:      uuid.NewString(),
		Status:    domain.StatusActive,
		CreatedBy: actor,
	})
	if err != nil {
		t.Fatal(err)
	}

	return item.ID
}

func seedPrerequisiteTopics(t testing.TB, ctx context.Context, pool *pgxpool.Pool, ids []string, course *string, actor string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO topics (id, course_id, title, slug, difficulty, status, created_by, updated_by)
		SELECT id, $2, 'Prerequisite fixture', id::text, 'basic', 'active', $3, $3 FROM unnest($1::uuid[]) AS id`,
		ids, course, actor); err != nil {
		t.Fatal(err)
	}
}

func prerequisiteWriters(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []int32 {
	t.Helper()
	pids := make([]int32, 2)
	for i := range pids {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		if err := conn.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pids[i]); err != nil {
			t.Fatal(err)
		}
	}

	return pids
}

func waitPrerequisiteWriters(t *testing.T, ctx context.Context, pool *pgxpool.Pool, pids []int32) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_locks WHERE pid=ANY($1) AND NOT granted AND locktype IN ('advisory','relation')`,
			pids).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == len(pids) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("both prerequisite writes did not reach the lock")
		case <-ticker.C:
		}
	}
}

func requirePrerequisiteError(t *testing.T, err error, code string, status int) {
	t.Helper()
	got, ok := errors.AsType[*apperror.Error](err)
	if !ok || got.Code != code || got.Status != status {
		t.Fatal("expected prerequisite error:", code, status, "got:", err)
	}
}
