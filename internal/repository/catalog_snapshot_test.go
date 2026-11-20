package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/overmindv/entities/internal/catalogsnapshot"
	"github.com/overmindv/entities/internal/domain"
	"github.com/overmindv/entities/internal/outbox"
)

type graphRecord struct {
	Kind         string `json:"kind"`
	TopicID      string `json:"topic_id"`
	Prerequisite string `json:"prerequisite_topic_id"`
	Tombstone    bool   `json:"tombstone"`
	Topic        *struct {
		Title string `json:"title"`
	} `json:"topic"`
}

func decodeGraph(t *testing.T, row catalogsnapshot.GraphRow) graphRecord {
	t.Helper()
	var record graphRecord
	if err := json.Unmarshal(row.Record, &record); err != nil {
		t.Fatal(err)
	}

	return record
}

func (r graphRecord) key() string {
	return r.Kind + "/" + r.TopicID + "/" + r.Prerequisite
}

func readSnapshot(t *testing.T, ctx context.Context, store *catalogsnapshot.Store, page catalogsnapshot.SnapshotPage) map[string]catalogsnapshot.GraphRow {
	t.Helper()
	const limit = 83
	state := make(map[string]catalogsnapshot.GraphRow)
	watermark := page.Watermark
	for {
		if len(page.Records) > limit || page.Watermark != watermark {
			t.Fatal("page changed watermark or exceeded the limit")
		}
		for _, row := range page.Records {
			key := decodeGraph(t, row).key()
			if _, exists := state[key]; exists || row.Revision > watermark {
				t.Fatal("duplicate or future snapshot row", key, row.Revision)
			}
			state[key] = row
		}
		if page.NextCursor == "" {
			return state
		}
		if len(page.Records) == 0 {
			t.Fatal("empty continuation page")
		}
		next, err := store.Snapshot(ctx, catalogsnapshot.SnapshotRequest{
			Watermark: &watermark,
			Cursor:    page.NextCursor,
			Limit:     limit,
		})
		if err != nil || next.NextCursor == page.NextCursor {
			t.Fatal("snapshot did not advance", err)
		}
		page = next
	}
}

//nolint:paralleltest // тест воспроизводит журнал общей component-БД
func TestPostgresCatalogSnapshotReplay(t *testing.T) {
	pool := testCatalogPool(t, "")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	catalog := New(pool)
	snapshots := catalogsnapshot.New(pool)
	actor := uuid.NewString()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	createTopic := func(name string) domain.Topic {
		t.Helper()
		item, err := catalog.CreateTopic(ctx, domain.Topic{
			Title:      name,
			Slug:       actor + "-" + name,
			Difficulty: "basic",
			Status:     domain.StatusActive,
			CreatedBy:  actor,
		})
		check(err)

		return item
	}
	a, b, c := createTopic("alpha"), createTopic("beta"), createTopic("gamma")
	_, err := catalog.AddPrerequisite(ctx, domain.TopicPrerequisite{TopicID: a.ID, PrerequisiteTopicID: b.ID}, actor)
	check(err)
	_, err = catalog.AddPrerequisite(ctx, domain.TopicPrerequisite{TopicID: c.ID, PrerequisiteTopicID: b.ID}, actor)
	check(err)

	rename := func(title string) pgx.Tx {
		t.Helper()
		tx, beginErr := pool.Begin(ctx)
		check(beginErr)
		t.Cleanup(func() {
			cleanup, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
			defer cleanupCancel()
			_ = tx.Rollback(cleanup)
		})
		_, err = tx.Exec(ctx, "UPDATE topics SET title=$2 WHERE id=$1", a.ID, title)
		check(err)
		changed := a
		changed.Title = title
		err = insertCatalogEvent(ctx, tx, "catalog.topic.updated", "topic", changed.ID, actor, changed)
		check(err)

		return tx
	}

	writer := rename("changed")
	first, err := snapshots.Snapshot(ctx, catalogsnapshot.SnapshotRequest{Limit: 1})
	check(err)
	if len(first.Records) != 1 || first.NextCursor == "" {
		t.Fatal("missing first page")
	}
	check(writer.Commit(ctx))
	check(rename("rolled back").Rollback(ctx))
	check(catalog.RemovePrerequisite(ctx, a.ID, b.ID, actor))
	_, err = catalog.AddPrerequisite(ctx, domain.TopicPrerequisite{TopicID: a.ID, PrerequisiteTopicID: c.ID}, actor)
	check(err)
	check(catalog.DeleteTopic(ctx, b.ID, actor))
	d := createTopic("delta")
	_, err = catalog.CreateUniversity(ctx, domain.University{Name: actor, Status: domain.StatusDraft, CreatedBy: actor})
	check(err)

	state := readSnapshot(t, ctx, snapshots, first)
	oldTopic := decodeGraph(t, state["topic/"+a.ID+"/"])
	oldEdge := decodeGraph(t, state["prerequisite/"+a.ID+"/"+b.ID])
	if oldTopic.Topic == nil || oldTopic.Topic.Title != "alpha" || oldEdge.Tombstone {
		t.Fatal("snapshot lost pre-change state", oldTopic, oldEdge)
	}
	if _, ok := state["topic/"+d.ID+"/"]; ok {
		t.Fatal("new topic leaked into earlier snapshot")
	}

	replay, err := snapshots.Changes(ctx, catalogsnapshot.ChangeRequest{After: first.Watermark, Limit: 2})
	check(err)
	upper := replay.Watermark
	var changes []catalogsnapshot.Change
	for {
		if len(replay.Changes) > 2 || replay.Watermark != upper {
			t.Fatal("replay changed upper watermark or exceeded limit")
		}
		changes = append(changes, replay.Changes...)
		if !replay.More {
			if replay.Through != upper {
				t.Fatal("did not advance beyond non-graph events", replay.Through, upper)
			}

			break
		}
		next, pageErr := snapshots.Changes(ctx, catalogsnapshot.ChangeRequest{
			After:     replay.Through,
			Watermark: &upper,
			Limit:     2,
		})
		check(pageErr)
		if next.Through <= replay.Through {
			t.Fatal("replay did not advance")
		}
		replay = next
	}
	if len(changes) != 5 || upper != first.Watermark+6 ||
		changes[0].Revision != first.Watermark+1 || changes[0].AggregateVersion != 3 || changes[2].AggregateVersion != 5 {
		t.Fatal("lost update or used a revision on rollback", first.Watermark, upper, changes)
	}

	seen := make(map[string]bool)
	apply := func(change catalogsnapshot.Change) {
		if seen[change.EventID] {
			return
		}
		if validationErr := uuid.Validate(change.EventID); validationErr != nil {
			t.Fatal(validationErr)
		}
		seen[change.EventID] = true
		key := decodeGraph(t, change.GraphRow).key()
		if state[key].Revision < change.Revision {
			state[key] = change.GraphRow
		}
	}
	for i := len(changes) - 1; i >= 0; i-- {
		apply(changes[i])
		apply(changes[i])
	}

	current, err := snapshots.Snapshot(ctx, catalogsnapshot.SnapshotRequest{Limit: 83})
	check(err)
	fresh := readSnapshot(t, ctx, snapshots, current)
	if len(fresh) != len(state) || current.Watermark != upper {
		t.Fatal("replay did not recover all rows", len(state), len(fresh))
	}
	for key, row := range fresh {
		if recovered, ok := state[key]; !ok || row.Revision != recovered.Revision || !bytes.Equal(row.Record, recovered.Record) {
			t.Fatal("replay differs from fresh snapshot", key)
		}
	}
	if !decodeGraph(t, fresh["prerequisite/"+a.ID+"/"+b.ID]).Tombstone ||
		!decodeGraph(t, fresh["topic/"+b.ID+"/"]).Tombstone {
		t.Fatal("removed relationship or deleted topic has no tombstone")
	}
	keptEdge := decodeGraph(t, fresh["prerequisite/"+c.ID+"/"+b.ID])
	if keptEdge.Tombstone || keptEdge.Prerequisite != b.ID {
		t.Fatal("snapshot changed a link that is still present", keptEdge)
	}

	eventID := changes[0].EventID
	var pending int
	check(pool.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE published_at IS NULL").Scan(&pending))
	if pending < 1 || pending > 10000 {
		t.Fatal("unexpected number of test outbox rows", pending)
	}
	dispatch := outbox.NewCatalogOutbox(pool, "catalog.activity.v1")
	for range 2 {
		events, fetchErr := dispatch.FetchPending(ctx, pending)
		check(fetchErr)
		found := false
		for _, event := range events {
			if event.ID != eventID {
				continue
			}
			var envelope struct {
				EventType string `json:"event_type"`
				EventID   string `json:"event_id"`
				Revision  int64  `json:"catalog_revision"`
			}
			check(json.Unmarshal(event.Value, &envelope))
			if envelope.EventID != eventID || envelope.EventType != "catalog.topic.updated" ||
				envelope.Revision != changes[0].Revision || event.Key != a.ID {
				t.Fatal("Kafka envelope differs from replay event", envelope)
			}
			found = true

			break
		}
		if !found {
			t.Fatal("missing pending event", eventID)
		}
	}
	check(dispatch.MarkSent(ctx, []string{eventID}))
	published, err := snapshots.Changes(ctx, catalogsnapshot.ChangeRequest{After: first.Watermark, Limit: 1})
	check(err)
	if len(published.Changes) != 1 || published.Changes[0].EventID != eventID {
		t.Fatal("published event lost from replay")
	}
}
