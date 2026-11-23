package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/overmindv/parker"
	"github.com/prometheus/client_golang/prometheus"
)

// catalogEnvelope разделяет данные для ленты и внутренний граф каталога.
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
	FeedVisibility   json.RawMessage `json:"feed_visibility,omitempty"`
}

// CatalogOutbox публикует события из таблицы outbox_events.
type CatalogOutbox struct {
	pool            *pgxpool.Pool
	topic           string
	publishFailures prometheus.Counter
}

// NewCatalogOutbox создаёт outbox-адаптер каталога на переданном пуле.
func NewCatalogOutbox(pool *pgxpool.Pool, topic string) *CatalogOutbox {
	return &CatalogOutbox{
		pool:  pool,
		topic: topic,
		publishFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "entities_outbox_publish_failures_total",
			Help: "Catalog events not acknowledged by the producer in the current process.",
		}),
	}
}

var _ parker.Outbox = (*CatalogOutbox)(nil) // гарантирует реализацию контракта parker

var ErrClaimLost = errors.New("catalog outbox claim lost")

const catalogUpdateSQL = `event_type IN ('catalog.' || aggregate_type || '.created', 'catalog.' || aggregate_type || '.updated')`
const catalogVisibilityFlagSQL = `(feed_visibility->>'visible')::boolean`
const catalogVisibleSQL = `COALESCE(` + catalogVisibilityFlagSQL + `, false)`
const catalogHiddenPayloadSQL = `'{"status":"hidden"}'::jsonb`
const catalogCardPayloadSQL = `CASE WHEN ` + catalogVisibilityFlagSQL + `
	THEN jsonb_strip_nulls(jsonb_build_object(
	    'name', left(payload->>'name',256), 'title', left(payload->>'title',256), 'status', payload->>'status'))
	ELSE ` + catalogHiddenPayloadSQL + ` END`

func catalogActivityType(eventType, aggregateType string, visible bool) string {
	prefix := "catalog." + aggregateType + "."
	if !visible && (eventType == prefix+"created" || eventType == prefix+"updated") {
		return prefix + "unpublished"
	}

	return eventType
}

// FetchPending закрепляет готовые записи; отправка не удерживает транзакцию.
func (o *CatalogOutbox) FetchPending(ctx context.Context, limit int) ([]parker.OutboxRecord, error) {
	if limit <= 0 {
		return nil, nil
	}
	limit = min(100, limit)
	rows, err := o.pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM outbox_events
			WHERE published_at IS NULL AND COALESCE(lease_until, next_attempt_at) <= statement_timestamp()
			ORDER BY COALESCE(lease_until, next_attempt_at), occurred_at, id
			LIMIT $1 FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE outbox_events AS e SET claim_token=gen_random_uuid(),
				lease_until=statement_timestamp()+interval '30 seconds'
			FROM due WHERE e.id=due.id RETURNING e.*
		)
		SELECT id::text, event_type, occurred_at, actor_user_id::text,
		       aggregate_type, aggregate_id::text,
		       CASE WHEN `+catalogUpdateSQL+` THEN `+catalogCardPayloadSQL+` ELSE payload END,
		       catalog_revision, aggregate_version, graph_change, feed_visibility,
		       `+catalogVisibleSQL+`,
		       claim_token::text, lease_until, attempts
		FROM claimed
		ORDER BY occurred_at, id`, limit)
	if err != nil {
		return nil, fmt.Errorf("catalog outbox fetch: %w", err)
	}
	defer rows.Close()

	records := make([]parker.OutboxRecord, 0, limit)
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
			feedVisibility   []byte
			public           bool
			rec              parker.OutboxRecord
		)
		if err := rows.Scan(&id, &eventType, &occurredAt, &actorUserID, &aggregateType, &aggregateID, &payload,
			&catalogRevision, &aggregateVersion, &graphChange, &feedVisibility, &public,
			&rec.ClaimToken, &rec.LeaseUntil, &rec.Attempts); err != nil {
			return nil, fmt.Errorf("catalog outbox scan: %w", err)
		}

		envelope, err := json.Marshal(catalogEnvelope{
			EventType:        catalogActivityType(eventType, aggregateType, public),
			OccurredAt:       occurredAt,
			ActorUserID:      actorUserID,
			AggregateType:    aggregateType,
			AggregateID:      aggregateID,
			Payload:          payload,
			EventID:          id,
			CatalogRevision:  catalogRevision,
			AggregateVersion: aggregateVersion,
			GraphChange:      graphChange,
			FeedVisibility:   feedVisibility,
		})
		if err != nil {
			return nil, fmt.Errorf("catalog outbox marshal: %w", err)
		}

		rec.ID, rec.Topic, rec.Key, rec.Value = id, o.topic, aggregateID, envelope
		records = append(records, rec)
	}

	return records, rows.Err()
}

// MarkSent оставлен для интерфейса Parker; запись подтверждается только с токеном.
func (o *CatalogOutbox) MarkSent(ctx context.Context, ids []string) error {
	if len(ids) != 0 {
		return ErrClaimLost
	}

	return nil
}

func (o *CatalogOutbox) MarkClaimSent(ctx context.Context, rec parker.OutboxRecord) error {
	return o.finishClaim(ctx, rec, true, 0)
}

func (o *CatalogOutbox) MarkClaimFailed(ctx context.Context, rec parker.OutboxRecord) error {
	o.publishFailures.Inc()

	base := retryDelay(rec.Attempts)
	slot := time.Duration(crc32.ChecksumIEEE([]byte(rec.ID)) % 64)
	delay := base + base*slot/64

	return o.finishClaim(ctx, rec, false, delay)
}

func retryDelay(attempts int) time.Duration {
	return time.Second * time.Duration(1<<max(0, min(attempts, 7)))
}

func (o *CatalogOutbox) finishClaim(ctx context.Context, rec parker.OutboxRecord, sent bool, retry time.Duration) error {
	if rec.ClaimToken == "" {
		return ErrClaimLost
	}
	tag, err := o.pool.Exec(ctx, `
		UPDATE outbox_events SET attempts=attempts+1, claim_token=NULL, lease_until=NULL,
			published_at=CASE WHEN $3 THEN statement_timestamp() END,
			next_attempt_at=CASE WHEN $3 THEN next_attempt_at ELSE statement_timestamp()+$4::bigint*interval '1 microsecond' END
		WHERE id=$1 AND claim_token=$2 AND lease_until>statement_timestamp() AND published_at IS NULL`,
		rec.ID, rec.ClaimToken, sent, retry.Microseconds())
	if err != nil {
		return fmt.Errorf("catalog outbox finish claim: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrClaimLost
	}

	return nil
}
