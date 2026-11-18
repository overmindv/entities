package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/overmindv/parker"
)

// catalogEnvelope — конверт события каталога для топика feed consumer-а.
// Поля соответствуют колонкам outbox_events; payload сохраняется как есть.
type catalogEnvelope struct {
	EventType        string          `json:"event_type"`
	OccurredAt       time.Time       `json:"occurred_at"`
	ActorUserID      string          `json:"actor_user_id"`
	AggregateType    string          `json:"aggregate_type"`
	AggregateID      string          `json:"aggregate_id"`
	Payload          json.RawMessage `json:"payload"`
	EventID          string          `json:"event_id"`
	CatalogRevision  *int64          `json:"catalog_revision,omitempty"`
	AggregateVersion *int64          `json:"aggregate_version,omitempty"`
	GraphChange      json.RawMessage `json:"graph_change,omitempty"`
}

// CatalogOutbox реализует parker.Outbox поверх существующей таблицы
// outbox_events сущностей каталога. Релей публикует ВСЕ pending-события;
// семантическая фильтрация (created/activated) выполняется в feed consumer-е.
type CatalogOutbox struct {
	pool  *pgxpool.Pool
	topic string
}

// NewCatalogOutbox создаёт outbox-адаптер каталога на переданном пуле.
func NewCatalogOutbox(pool *pgxpool.Pool, topic string) *CatalogOutbox {
	return &CatalogOutbox{pool: pool, topic: topic}
}

var _ parker.Outbox = (*CatalogOutbox)(nil) // гарантирует реализацию контракта parker

// FetchPending вычитывает неопубликованные outbox-события с блокировкой строк.
func (o *CatalogOutbox) FetchPending(ctx context.Context, limit int) ([]parker.OutboxRecord, error) {
	rows, err := o.pool.Query(ctx, `
		SELECT id::text, event_type, occurred_at, actor_user_id::text,
		       aggregate_type, aggregate_id::text, payload,
		       catalog_revision, aggregate_version, graph_change
		FROM outbox_events
		WHERE published_at IS NULL
		ORDER BY occurred_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("catalog outbox fetch: %w", err)
	}
	defer rows.Close()

	var records []parker.OutboxRecord
	for rows.Next() {
		var (
			id               string
			eventType        string
			occurredAt       time.Time
			actorUserID      string
			aggregateType    string
			aggregateID      string
			payload          []byte
			catalogRevision  *int64
			aggregateVersion *int64
			graphChange      []byte
		)
		if err := rows.Scan(&id, &eventType, &occurredAt, &actorUserID, &aggregateType, &aggregateID, &payload,
			&catalogRevision, &aggregateVersion, &graphChange); err != nil {
			return nil, fmt.Errorf("catalog outbox scan: %w", err)
		}

		envelope, err := json.Marshal(catalogEnvelope{
			EventType:        eventType,
			OccurredAt:       occurredAt,
			ActorUserID:      actorUserID,
			AggregateType:    aggregateType,
			AggregateID:      aggregateID,
			Payload:          payload,
			EventID:          id,
			CatalogRevision:  catalogRevision,
			AggregateVersion: aggregateVersion,
			GraphChange:      graphChange,
		})
		if err != nil {
			return nil, fmt.Errorf("catalog outbox marshal: %w", err)
		}

		records = append(records, parker.OutboxRecord{
			ID:    id,
			Topic: o.topic,
			Key:   aggregateID,
			Value: envelope,
		})
	}
	return records, rows.Err()
}

// MarkSent помечает события опубликованными.
func (o *CatalogOutbox) MarkSent(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := o.pool.Exec(ctx, `
		UPDATE outbox_events
		SET published_at = now(), attempts = attempts + 1
		WHERE id::text = ANY($1) AND published_at IS NULL`, ids)
	if err != nil {
		return fmt.Errorf("catalog outbox mark sent: %w", err)
	}
	return nil
}
