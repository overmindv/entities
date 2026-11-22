package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
)

func writeCatalog[T any](ctx context.Context, pool *pgxpool.Pool, write func(pgx.Tx) (T, error)) (T, error) {
	var result T
	err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var err error
		result, err = write(tx)

		return err
	})
	if err != nil {
		var zero T

		return zero, err
	}

	return result, nil
}

func insertCatalogEvent(ctx context.Context, tx pgx.Tx, eventType, aggregateType, aggregateID, actorID string, payload any) error {
	return insertCatalogEventWithReset(ctx, tx, eventType, aggregateType, aggregateID, actorID, payload, false)
}

func insertCatalogEventWithReset(ctx context.Context, tx pgx.Tx, eventType, aggregateType, aggregateID, actorID string, payload any, reset bool) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode catalog event: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (event_type,actor_user_id,aggregate_id,aggregate_type,payload,feed_visibility)
		VALUES ($1,$2,$3,$4,$5,CASE WHEN $6::boolean THEN '{"scope_reset":true}'::jsonb END)`,
		eventType, actorID, aggregateID, aggregateType, data, reset)
	if err != nil {
		return fmt.Errorf("insert catalog event: %w", err)
	}

	return nil
}

func lockPrerequisiteGraph(ctx context.Context, tx pgx.Tx, topicID string) (*string, error) {
	var courseID *string
	err := tx.QueryRow(ctx, `
		SELECT course_id, pg_advisory_xact_lock(hashtextextended(
		    'entities:prerequisites:' || COALESCE(course_id::text, 'backlog'), 0))
		FROM topics WHERE id=$1`, topicID).Scan(&courseID, nil)

	return courseID, err
}

func checkPrerequisite(ctx context.Context, tx pgx.Tx, item domain.TopicPrerequisite, courseID *string) error {
	var count int
	var sameCourse bool
	// Берем новый снимок после ожидания блокировки и удерживаем обе темы до записи связи.
	if err := tx.QueryRow(ctx, `
		WITH endpoints AS (
		    SELECT id, course_id FROM topics
		    WHERE id IN ($1, $2) AND deleted_at IS NULL ORDER BY id FOR SHARE
		)
		SELECT count(*), COALESCE(bool_and(course_id IS NOT DISTINCT FROM $3::uuid), false)
		FROM endpoints`, item.TopicID, item.PrerequisiteTopicID, courseID).Scan(&count, &sameCourse); err != nil {
		return err
	}
	if count != 2 {
		return translateNotFound(pgx.ErrNoRows, apperror.TopicNotFound)
	}
	if !sameCourse {
		return apperror.New(apperror.InvalidTopicPrerequisite, "темы должны принадлежать одному курсу", http.StatusUnprocessableEntity)
	}
	var cycle bool
	// OFFSET сохраняет поиск по индексу для каждой посещенной темы.
	if err := tx.QueryRow(ctx, `
		WITH RECURSIVE graph(id) AS (
		    SELECT prerequisite_topic_id FROM topic_prerequisites WHERE topic_id=$1
		    UNION
		    SELECT p.prerequisite_topic_id FROM graph
		    CROSS JOIN LATERAL (
		        SELECT prerequisite_topic_id FROM topic_prerequisites WHERE topic_id=graph.id OFFSET 0
		    ) p
		)
		SELECT EXISTS(SELECT 1 FROM graph WHERE id=$2)`, item.PrerequisiteTopicID, item.TopicID).Scan(&cycle); err != nil {
		return err
	}
	if cycle {
		return apperror.New(apperror.TopicPrerequisiteCycle, "обнаружен цикл пререквизитов", http.StatusConflict)
	}

	return nil
}

func checkTopicMove(ctx context.Context, tx pgx.Tx, topicID, parentID string, courseID *string) error {
	var root, cycle, invalidParent bool
	if err := tx.QueryRow(ctx, `
		WITH RECURSIVE ancestors AS (
			SELECT id,parent_topic_id,course_id,deleted_at FROM topics WHERE id=$1
			UNION
			SELECT p.id,p.parent_topic_id,p.course_id,p.deleted_at
			FROM ancestors a JOIN topics p ON p.id=a.parent_topic_id
		)
		SELECT EXISTS(SELECT 1 FROM ancestors WHERE parent_topic_id IS NULL),
		       EXISTS(SELECT 1 FROM ancestors WHERE id=$2),
		       NOT EXISTS(SELECT 1 FROM ancestors) OR EXISTS(
		         SELECT 1 FROM ancestors WHERE deleted_at IS NOT NULL OR course_id IS DISTINCT FROM $3::uuid
		       )`, parentID, topicID, courseID).Scan(&root, &cycle, &invalidParent); err != nil {
		return err
	}
	if invalidParent {
		return apperror.New(apperror.InvalidParentTopic, "родительская тема недоступна в этом курсе", 422)
	}
	if cycle || !root {
		return apperror.New(apperror.TopicHierarchyCycle, "обнаружен цикл иерархии тем", 409)
	}

	return nil
}
