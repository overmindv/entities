package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
	"github.com/overmindv/entities/internal/outbox"
)

func TestFeedStampAfterConcurrentParentHide(t *testing.T) {
	pool := testCatalogPool(t, "")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	actor := uuid.NewString()
	course, err := New(pool).CreateCourse(ctx, domain.Course{
		Name:      "Feed visibility fixture",
		Slug:      uuid.NewString(),
		Status:    domain.StatusActive,
		CreatedBy: actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hidden.Rollback(t.Context()) }()
	if _, err := hidden.Exec(ctx, "UPDATE courses SET status='hidden' WHERE id=$1", course.ID); err != nil {
		t.Fatal(err)
	}
	if err := insertCatalogEvent(ctx, hidden, "catalog.course.updated", "course", course.ID, actor, map[string]string{"status": "hidden"}); err != nil {
		t.Fatal(err)
	}

	child, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Rollback(t.Context()) }()
	topicID := uuid.NewString()
	if _, err := child.Exec(ctx, `
		INSERT INTO topics (id,course_id,slug,title,status,created_by,updated_by)
		VALUES ($1,$2,$3,'concurrent child','active',$4,$4)`, topicID, course.ID, uuid.NewString(), actor); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := child.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var insertErr error
	go func() {
		defer close(done)
		insertErr = insertCatalogEvent(ctx, child, "catalog.topic.created", "topic", topicID, actor, map[string]string{"status": "active"})
	}()
	defer func() {
		cancel()
		<-done
	}()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1)) > 0", pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := hidden.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	<-done
	if insertErr != nil {
		t.Fatal(insertErr)
	}
	if err := child.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var visible bool
	var ordered bool
	if err := pool.QueryRow(ctx, `
		SELECT (child.feed_visibility->>'visible')::boolean, child.catalog_revision > parent.catalog_revision
		  FROM outbox_events child CROSS JOIN outbox_events parent
		 WHERE child.aggregate_id=$1 AND child.event_type='catalog.topic.created'
		   AND parent.aggregate_id=$2 AND parent.event_type='catalog.course.updated'`, topicID, course.ID).Scan(&visible, &ordered); err != nil {
		t.Fatal(err)
	}
	if visible || !ordered {
		t.Error("child event did not observe the earlier parent hide", visible, ordered)
	}
}

func TestConcurrentTopicMovesRejectCycleAndKeepEventSmall(t *testing.T) {
	pool := testCatalogPool(t, "")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	admin := New(pool)
	actor := uuid.NewString()
	course, err := admin.CreateCourse(ctx, domain.Course{
		Name:      "Topic move fixture",
		Slug:      uuid.NewString(),
		Status:    domain.StatusActive,
		CreatedBy: actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	topics := make([]domain.Topic, 2)
	for i := range topics {
		topics[i], err = admin.CreateTopic(ctx, domain.Topic{
			CourseID:   &course.ID,
			Slug:       uuid.NewString(),
			Title:      "Topic move fixture",
			Difficulty: "basic",
			Status:     domain.StatusActive,
			CreatedBy:  actor,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	type outcome struct {
		topic  domain.Topic
		parent string
		err    error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	for i, topic := range topics {
		parent := topics[1-i].ID
		topic.ParentTopicID = &parent
		topic.UpdatedBy = actor
		topic.Description = strings.Repeat("test description ", 30_000)
		go func() {
			<-start
			item, err := admin.UpdateTopic(ctx, topic)
			results <- outcome{topic: item, parent: parent, err: err}
		}()
	}
	close(start)
	var moved outcome
	var conflicts int
	for range topics {
		result := <-results
		if result.err == nil {
			moved = result
		} else if appErr, ok := errors.AsType[*apperror.Error](result.err); ok && appErr.Code == apperror.TopicHierarchyCycle {
			conflicts++
		} else {
			t.Error(result.err)
		}
	}
	if conflicts != 1 || moved.topic.ID == "" {
		t.Fatal("expected one move and one cycle conflict", conflicts)
	}
	records, err := outbox.NewCatalogOutbox(pool, "fixture.catalog").FetchPending(ctx, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Key != moved.topic.ID {
			continue
		}
		var envelope struct {
			Type       string `json:"event_type"`
			Visibility struct {
				Visible    bool     `json:"visible"`
				ScopeReset bool     `json:"scope_reset"`
				Ancestors  []string `json:"ancestors"`
			} `json:"feed_visibility"`
		}
		if err := json.Unmarshal(record.Value, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Type != "catalog.topic.updated" {
			continue
		}
		if len(record.Value) > 4<<10 || !envelope.Visibility.Visible || !envelope.Visibility.ScopeReset ||
			!strings.Contains(strings.Join(envelope.Visibility.Ancestors, ","), "topic:"+moved.parent) {
			t.Error("expected bounded public event with new parent and scope reset", len(record.Value), envelope.Visibility)
		}
		return
	}
	t.Fatal("move event missing")
}
