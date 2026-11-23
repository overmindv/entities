package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/overmindv/entities/internal/catalogsnapshot"
	"github.com/overmindv/entities/internal/domain"
	"github.com/overmindv/entities/internal/outbox"
)

const privateFeedFixture = "private replay fixture description"
const hiddenFeedFixtureJSON = `{"status":"hidden"}`

//nolint:paralleltest // Проверяем непрерывную историю общей component-БД.
func TestCatalogFeedReplayMatchesKafka(t *testing.T) {
	pool := testCatalogPool(t, "")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	dispatch := outbox.NewCatalogOutbox(pool, "fixture.catalog")
	admin := New(pool)
	actor := uuid.NewString()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	start, err := catalogsnapshot.New(pool).ResolveWatermark(ctx, nil)
	check(err)

	u, err := admin.CreateUniversity(ctx, domain.University{
		Name:      actor,
		Status:    domain.StatusActive,
		CreatedBy: actor,
	})
	check(err)
	program, err := admin.CreateProgram(ctx, domain.Program{
		Name:         actor,
		UniversityID: &u.ID,
		DegreeLevel:  "bachelor",
		Status:       domain.StatusActive,
		CreatedBy:    actor,
	})
	check(err)
	course, err := admin.CreateCourse(ctx, domain.Course{
		Name:      actor,
		Slug:      actor,
		ProgramID: &program.ID,
		Status:    domain.StatusActive,
		CreatedBy: actor,
	})
	check(err)
	create := func(title string, parent *string) domain.Topic {
		t.Helper()
		topic, createErr := admin.CreateTopic(ctx, domain.Topic{
			Title:         title,
			Slug:          uuid.NewString(),
			CourseID:      &course.ID,
			ParentTopicID: parent,
			Description:   privateFeedFixture,
			Difficulty:    "basic",
			Status:        domain.StatusActive,
			CreatedBy:     actor,
		})
		check(createErr)

		return topic
	}
	left := create("left", nil)
	right := create("right", nil)
	child := create("before move", &left.ID)
	_, err = admin.AddPrerequisite(ctx, domain.TopicPrerequisite{TopicID: left.ID, PrerequisiteTopicID: right.ID}, actor)
	check(err)
	child.ParentTopicID, child.Title, child.UpdatedBy = &right.ID, "moved child", actor
	child, err = admin.UpdateTopic(ctx, child)
	check(err)
	_, err = admin.ChangeTopicStatus(ctx, right.ID, domain.StatusHidden, actor)
	check(err)

	writer, err := pool.Begin(ctx)
	check(err)
	defer func() { _ = writer.Rollback(ctx) }()
	_, err = writer.Exec(ctx, "UPDATE topics SET status='active' WHERE id=$1", right.ID)
	check(err)
	check(insertCatalogEvent(ctx, writer, "catalog.topic.updated", "topic", right.ID, actor, right))
	first, err := dispatch.FeedChanges(ctx, catalogsnapshot.ChangeRequest{After: start, Limit: 2})
	check(err)
	if !first.More || first.Watermark != start+9 {
		t.Fatal("uncommitted revision changed the replay watermark", first.Watermark)
	}
	check(writer.Commit(ctx))
	child.Title = "visible after reopen"
	child, err = admin.UpdateTopic(ctx, child)
	check(err)
	check(admin.DeleteTopic(ctx, left.ID, actor))

	pinned := readFeedReplay(t, ctx, dispatch, first, 2)
	if len(pinned) != 8 || pinned[0].AggregateID != u.ID ||
		pinned[1].AggregateID != program.ID || pinned[2].AggregateID != course.ID {
		t.Fatal("replay lost catalog cards or returned graph events", len(pinned))
	}
	var original, moved, hidden outbox.FeedEvent
	for _, event := range pinned {
		switch {
		case event.AggregateID == child.ID && event.AggregateVersion == 1:
			original = event
		case event.AggregateID == child.ID && event.AggregateVersion == 2:
			moved = event
		case event.AggregateID == right.ID && !event.Visibility.Visible:
			hidden = event
		}
	}
	if !original.Visibility.Visible || !slices.Contains(original.Visibility.Ancestors, "topic:"+left.ID) ||
		!moved.Visibility.Visible || !moved.Visibility.ScopeReset ||
		!slices.Contains(moved.Visibility.Ancestors, "topic:"+right.ID) || slices.Contains(moved.Visibility.Ancestors, "topic:"+left.ID) ||
		hidden.EventType != "catalog.topic.unpublished" || hidden.Revision <= moved.Revision ||
		string(hidden.Payload) != `{"status": "hidden"}` && string(hidden.Payload) != hiddenFeedFixtureJSON {
		t.Fatal("late versions would lose the move or hide barrier", original, moved, hidden)
	}

	next, err := dispatch.FeedChanges(ctx, catalogsnapshot.ChangeRequest{After: first.Watermark, Limit: 2})
	check(err)
	later := readFeedReplay(t, ctx, dispatch, next, 2)
	if len(later) != 3 || later[0].AggregateID != right.ID || !later[0].Visibility.Visible ||
		later[1].AggregateID != child.ID || later[1].AggregateVersion <= moved.AggregateVersion ||
		later[2].EventType != "catalog.topic.deleted" || later[2].Visibility.Visible {
		t.Fatal("reopen, fresh child version or delete is missing", later)
	}
	all := append(pinned, later...)
	compareFeedKafka(t, ctx, dispatch, all)
	repeated, err := dispatch.FeedChanges(ctx, catalogsnapshot.ChangeRequest{After: start, Watermark: &next.Watermark, Limit: 200})
	check(err)
	for i, event := range repeated.Events {
		if i >= len(all) || !sameFeedEvent(event, all[i]) {
			t.Fatal("delivery changed replay history", event.EventID)
		}
	}
	if repeated.More || len(repeated.Events) != len(all) {
		t.Fatal("sent events disappeared from history", len(repeated.Events))
	}
	if _, err := dispatch.FeedChanges(ctx, catalogsnapshot.ChangeRequest{
		After:     next.Watermark + 1,
		Watermark: &next.Watermark,
		Limit:     1,
	}); !errors.Is(err, catalogsnapshot.ErrInvalidPage) {
		t.Fatal("accepted cursor beyond fixed watermark", err)
	}
}

func readFeedReplay(t *testing.T, ctx context.Context, dispatch *outbox.CatalogOutbox, page outbox.FeedPage, limit int) []outbox.FeedEvent {
	t.Helper()
	watermark := page.Watermark
	var events []outbox.FeedEvent
	for {
		if page.Watermark != watermark || len(page.Events) > limit {
			t.Fatal("replay changed fixed bounds")
		}
		for _, event := range page.Events {
			wire, err := json.Marshal(event)
			if err != nil || bytes.Contains(wire, []byte(privateFeedFixture)) || bytes.Contains(wire, []byte("graph_change")) ||
				bytes.Contains(wire, []byte("description")) || event.Revision > page.Through {
				t.Fatal("replay leaked private data or a future event", event.EventID, err)
			}
		}
		events = append(events, page.Events...)
		if !page.More {
			if page.Through != watermark {
				t.Fatal("last replay page did not reach watermark")
			}

			return events
		}
		next, err := dispatch.FeedChanges(ctx, catalogsnapshot.ChangeRequest{
			After:     page.Through,
			Watermark: &watermark,
			Limit:     limit,
		})
		if err != nil || next.Through <= page.Through {
			t.Fatal("replay cursor stalled", err)
		}
		page = next
	}
}

func compareFeedKafka(t *testing.T, ctx context.Context, dispatch *outbox.CatalogOutbox, events []outbox.FeedEvent) {
	t.Helper()
	remaining := make(map[string]outbox.FeedEvent, len(events))
	for _, event := range events {
		remaining[event.EventID] = event
	}
	for range 100 {
		claims, err := dispatch.FetchPending(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, claim := range claims {
			event, ok := remaining[claim.ID]
			if !ok {
				continue
			}
			var kafka outbox.FeedEvent
			if err := json.Unmarshal(claim.Value, &kafka); err != nil {
				t.Fatal(err)
			}
			if !kafka.Visibility.Visible && (strings.HasSuffix(kafka.EventType, ".unpublished") || strings.HasSuffix(kafka.EventType, ".deleted")) {
				kafka.ActorUserID = ""
				kafka.Payload = json.RawMessage(hiddenFeedFixtureJSON)
			}
			if claim.Key != event.AggregateID || !sameFeedEvent(kafka, event) {
				t.Fatal("Feed replay changed the Kafka projection input", event.EventID)
			}
			if err := dispatch.MarkClaimSent(ctx, claim); err != nil {
				t.Fatal(err)
			}
			delete(remaining, claim.ID)
		}
		if len(claims) == 0 || len(remaining) == 0 {
			break
		}
	}
	if len(remaining) != 0 {
		t.Fatal("did not compare all replayed events", len(remaining))
	}
}

func sameFeedEvent(left, right outbox.FeedEvent) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)

	return errA == nil && errB == nil && bytes.Equal(a, b)
}
