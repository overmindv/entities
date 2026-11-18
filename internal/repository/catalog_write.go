package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode catalog event: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (event_type,actor_user_id,aggregate_id,aggregate_type,payload)
		VALUES ($1,$2,$3,$4,$5)`, eventType, actorID, aggregateID, aggregateType, data)
	if err != nil {
		return fmt.Errorf("insert catalog event: %w", err)
	}

	return nil
}
