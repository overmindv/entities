package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/overmindv/entities/internal/apperror"
)

func writeCatalog[T any](ctx context.Context, pool *pgxpool.Pool, write func(pgx.Tx) (T, error)) (T, error) {
	var result T
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
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
