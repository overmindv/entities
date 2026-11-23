package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/overmindv/entities/internal/catalogsnapshot"
)

var (
	ErrFeedHistoryGap  = errors.New("catalog feed history is incomplete")
	errFeedHistoryData = errors.New("invalid catalog feed event")
)

type FeedPage struct {
	Watermark int64       `json:"watermark"`
	Through   int64       `json:"through"`
	Events    []FeedEvent `json:"events"`
	More      bool        `json:"more"`
}

type FeedEvent struct {
	EventID          string          `json:"event_id"`
	EventType        string          `json:"event_type"`
	OccurredAt       time.Time       `json:"occurred_at"`
	ActorUserID      string          `json:"actor_user_id,omitempty"`
	AggregateType    string          `json:"aggregate_type"`
	AggregateID      string          `json:"aggregate_id"`
	AggregateVersion int64           `json:"aggregate_version"`
	Revision         int64           `json:"catalog_revision"`
	Payload          json.RawMessage `json:"payload"`
	Visibility       FeedVisibility  `json:"feed_visibility"`
}

type FeedVisibility struct {
	Visible    bool     `json:"visible"`
	Ancestors  []string `json:"ancestors"`
	ScopeReset bool     `json:"scope_reset"`
}

const maxFeedAncestors = 127
const maxFeedAncestorBytes = 8 << 10
const catalogTopicKind = "topic"
const catalogPrerequisiteAdded = "catalog.topic.prerequisite.added"

// Проверяем все версии, включая события пререквизитов, чтобы не пропустить барьер при потере строки.
const feedChangesSQL = `
	SELECT catalog_revision, aggregate_version, id::text, event_type, occurred_at,
	       actor_user_id::text, aggregate_type, aggregate_id::text,
	       CASE WHEN ` + catalogUpdateSQL + ` THEN ` + catalogCardPayloadSQL + ` ELSE ` + catalogHiddenPayloadSQL + ` END,
	       ` + catalogVisibleSQL + `,
	       COALESCE((feed_visibility->>'scope_reset')::boolean, false),
	       CASE WHEN ` + catalogVisibleSQL + ` AND octet_length((feed_visibility->'ancestors')::text) <= $4
	            THEN feed_visibility->'ancestors' END
	FROM outbox_events WHERE catalog_revision > $1 AND catalog_revision <= $2
	ORDER BY catalog_revision LIMIT $3`

func (o *CatalogOutbox) FeedChanges(ctx context.Context, request catalogsnapshot.ChangeRequest) (FeedPage, error) {
	if request.Limit < 1 || request.Limit > catalogsnapshot.MaxLimit || request.After < 0 {
		return FeedPage{}, catalogsnapshot.ErrInvalidPage
	}
	watermark, err := catalogsnapshot.New(o.pool).ResolveWatermark(ctx, request.Watermark)
	if err != nil {
		return FeedPage{}, err
	}
	if request.After > watermark {
		return FeedPage{}, catalogsnapshot.ErrInvalidPage
	}
	rows, err := o.pool.Query(ctx, feedChangesSQL, request.After, watermark, request.Limit+1, maxFeedAncestorBytes)
	if err != nil {
		return FeedPage{}, err
	}
	defer rows.Close()

	return readFeedChanges(rows, request, watermark)
}

func readFeedChanges(rows pgx.Rows, request catalogsnapshot.ChangeRequest, watermark int64) (FeedPage, error) {
	page := FeedPage{Watermark: watermark, Through: request.After, Events: make([]FeedEvent, 0, request.Limit)}
	scanned := 0
	for rows.Next() {
		var event FeedEvent
		var public, reset bool
		var ancestors []byte
		if err := rows.Scan(&event.Revision, &event.AggregateVersion, &event.EventID, &event.EventType, &event.OccurredAt,
			&event.ActorUserID, &event.AggregateType, &event.AggregateID, &event.Payload, &public, &reset, &ancestors); err != nil {
			return FeedPage{}, err
		}
		if event.Revision-page.Through != 1 {
			return FeedPage{}, ErrFeedHistoryGap
		}
		if scanned == request.Limit {
			page.More = true

			return page, nil
		}
		scanned++
		page.Through = event.Revision
		keep, err := prepareFeedEvent(&event, public, reset, ancestors)
		if err != nil {
			return FeedPage{}, err
		}
		if keep {
			page.Events = append(page.Events, event)
		}
	}
	if err := rows.Err(); err != nil {
		return FeedPage{}, err
	}
	if page.Through != watermark {
		return FeedPage{}, ErrFeedHistoryGap
	}

	return page, nil
}

func prepareFeedEvent(event *FeedEvent, public, reset bool, ancestors []byte) (bool, error) {
	if (event.EventType == catalogPrerequisiteAdded || event.EventType == "catalog.topic.prerequisite.removed") &&
		event.AggregateType == catalogTopicKind {
		return false, nil
	}
	if !feedAggregate(event.AggregateType) || !feedUUID(event.AggregateID) || !feedUUID(event.EventID) ||
		event.AggregateVersion < 1 || event.OccurredAt.IsZero() {
		return false, errFeedHistoryData
	}
	prefix := "catalog." + event.AggregateType + "."
	event.EventType = catalogActivityType(event.EventType, event.AggregateType, public)
	switch event.EventType {
	case prefix + "deleted", prefix + "unpublished":
		event.ActorUserID = ""

		return true, nil
	case prefix + "created", prefix + "updated":
	default:
		return false, errFeedHistoryData
	}
	if (event.ActorUserID != "" && !feedUUID(event.ActorUserID)) || !validFeedTitle(event.Payload) {
		return false, errFeedHistoryData
	}
	if err := json.Unmarshal(ancestors, &event.Visibility.Ancestors); err != nil ||
		!validFeedAncestors(event.AggregateType+":"+event.AggregateID, event.Visibility.Ancestors) {
		return false, errFeedHistoryData
	}
	event.Visibility.Visible, event.Visibility.ScopeReset = true, reset

	return true, nil
}

func validFeedTitle(raw json.RawMessage) bool {
	var fields struct {
		Name   string `json:"name"`
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil || fields.Status != "active" {
		return false
	}

	return strings.TrimSpace(fields.Name) != "" || strings.TrimSpace(fields.Title) != ""
}

func feedAggregate(kind string) bool {
	switch kind {
	case "university", "program", "course", catalogTopicKind:
		return true
	default:
		return false
	}
}

func feedUUID(raw string) bool {
	parsed, err := uuid.Parse(raw)

	return err == nil && parsed != uuid.Nil && raw == parsed.String()
}

func validFeedAncestors(self string, ancestors []string) bool {
	if ancestors == nil || len(ancestors) > maxFeedAncestors {
		return false
	}
	seen := make(map[string]struct{}, len(ancestors))
	for _, key := range ancestors {
		kind, id, ok := strings.Cut(key, ":")
		if !ok || key == self || !feedAggregate(kind) || !feedUUID(id) {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}

	return true
}
