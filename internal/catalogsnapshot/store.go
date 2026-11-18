package catalogsnapshot

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	DefaultLimit = 100
	MaxLimit     = 200
)

var ErrInvalidPage = errors.New("invalid catalog page")

type SnapshotRequest struct {
	Watermark *int64
	Cursor    string
	Limit     int
}

type ChangeRequest struct {
	After     int64
	Watermark *int64
	Limit     int
}

type GraphRow struct {
	Revision int64           `json:"catalog_revision"`
	Record   json.RawMessage `json:"record"`
}

type SnapshotPage struct {
	Watermark  int64      `json:"watermark"`
	Records    []GraphRow `json:"records"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

type Change struct {
	EventID          string `json:"event_id"`
	AggregateVersion int64  `json:"aggregate_version"`
	GraphRow
}

type ChangePage struct {
	Watermark int64    `json:"watermark"`
	Through   int64    `json:"through"`
	Changes   []Change `json:"changes"`
	More      bool     `json:"more"`
}

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

type graphKey struct {
	kind   byte
	topic  uuid.UUID
	target uuid.UUID
}

const cursorSize = 1 + 8 + 2*len(uuid.Nil)

func parseCursor(cursor string) (graphKey, int64, error) {
	if cursor == "" {
		return graphKey{}, 0, nil
	}
	if len(cursor) != base64.RawURLEncoding.EncodedLen(cursorSize) {
		return graphKey{}, 0, ErrInvalidPage
	}
	var raw [cursorSize]byte
	n, err := base64.RawURLEncoding.Strict().Decode(raw[:], []byte(cursor))
	if err != nil || n != cursorSize || (raw[0] != 1 && raw[0] != 2) {
		return graphKey{}, 0, ErrInvalidPage
	}
	watermark := int64(binary.BigEndian.Uint64(raw[1:9]))
	key := graphKey{
		kind:   raw[0],
		topic:  uuid.UUID(raw[9:25]),
		target: uuid.UUID(raw[25:]),
	}
	if watermark < 0 || (key.kind == 1 && key.target != uuid.Nil) {
		return graphKey{}, 0, ErrInvalidPage
	}

	return key, watermark, nil
}

func (k graphKey) cursor(watermark int64) string {
	var raw [cursorSize]byte
	raw[0] = k.kind
	binary.BigEndian.PutUint64(raw[1:9], uint64(watermark))
	copy(raw[9:25], k.topic[:])
	copy(raw[25:], k.target[:])

	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func (s *Store) watermark(ctx context.Context, requested *int64) (int64, error) {
	var current int64
	if err := s.pool.QueryRow(ctx, "SELECT revision FROM catalog_clock WHERE singleton").Scan(&current); err != nil {
		return 0, err
	}
	if requested == nil {
		return current, nil
	}
	if *requested < 0 || *requested > current {
		return 0, ErrInvalidPage
	}

	return *requested, nil
}

const snapshotSQL = `
	SELECT kind, topic_id, target_id, valid_from, document
	FROM catalog_graph_versions
	WHERE (kind, topic_id, target_id) > ($1, $2, $3)
	  AND valid_from <= $4 AND (valid_until IS NULL OR valid_until > $4)
	ORDER BY kind, topic_id, target_id
	LIMIT $5`

func (s *Store) Snapshot(ctx context.Context, request SnapshotRequest) (SnapshotPage, error) {
	if request.Limit < 1 || request.Limit > MaxLimit || (request.Cursor != "" && request.Watermark == nil) {
		return SnapshotPage{}, ErrInvalidPage
	}
	key, version, err := parseCursor(request.Cursor)
	if err != nil {
		return SnapshotPage{}, err
	}
	if request.Cursor != "" && version != *request.Watermark {
		return SnapshotPage{}, ErrInvalidPage
	}
	watermark, err := s.watermark(ctx, request.Watermark)
	if err != nil {
		return SnapshotPage{}, err
	}
	// Версия не меняется между страницами, даже если предыдущие строки уже закрыты новой записью.
	rows, err := s.pool.Query(ctx, snapshotSQL, int16(key.kind), key.topic.String(), key.target.String(), watermark, request.Limit+1)
	if err != nil {
		return SnapshotPage{}, err
	}
	defer rows.Close()

	page := SnapshotPage{
		Watermark: watermark,
		Records:   make([]GraphRow, 0, request.Limit),
	}
	var last graphKey
	for rows.Next() {
		if len(page.Records) == request.Limit {
			page.NextCursor = last.cursor(watermark)
			break
		}
		var kind int16
		var result GraphRow
		if err := rows.Scan(&kind, &last.topic, &last.target, &result.Revision, &result.Record); err != nil {
			return SnapshotPage{}, err
		}
		last.kind = byte(kind)
		page.Records = append(page.Records, result)
	}

	return page, rows.Err()
}

const changesSQL = `
	SELECT id::text, aggregate_version, catalog_revision, graph_change
	FROM outbox_events
	WHERE catalog_revision > $1 AND catalog_revision <= $2 AND graph_change IS NOT NULL
	ORDER BY catalog_revision
	LIMIT $3`

func (s *Store) Changes(ctx context.Context, request ChangeRequest) (ChangePage, error) {
	if request.Limit < 1 || request.Limit > MaxLimit || request.After < 0 {
		return ChangePage{}, ErrInvalidPage
	}
	watermark, err := s.watermark(ctx, request.Watermark)
	if err != nil {
		return ChangePage{}, err
	}
	if request.After > watermark {
		return ChangePage{}, ErrInvalidPage
	}
	rows, err := s.pool.Query(ctx, changesSQL, request.After, watermark, request.Limit+1)
	if err != nil {
		return ChangePage{}, err
	}
	defer rows.Close()
	page := ChangePage{
		Watermark: watermark,
		Through:   watermark,
		Changes:   make([]Change, 0, request.Limit),
	}
	for rows.Next() {
		if len(page.Changes) == request.Limit {
			page.More = true
			page.Through = page.Changes[len(page.Changes)-1].Revision
			break
		}
		var change Change
		if err := rows.Scan(&change.EventID, &change.AggregateVersion, &change.Revision, &change.Record); err != nil {
			return ChangePage{}, err
		}
		page.Changes = append(page.Changes, change)
	}

	return page, rows.Err()
}
