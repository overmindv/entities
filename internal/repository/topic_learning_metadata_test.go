package repository

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/overmindv/entities/internal/catalogsnapshot"
	"github.com/overmindv/entities/internal/domain"
	"github.com/overmindv/entities/internal/httpapi"
	"github.com/overmindv/entities/internal/service"
)

const topicGatewayToken = "fixture-topics-gateway-credential"

func topicHandler(t *testing.T, store *Postgres) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := httpapi.Register(mux, service.New(store), service.New(store.PublicReads()), topicGatewayToken, logger); err != nil {
		t.Fatal(err)
	}

	return mux
}

func topicRequest(t *testing.T, handler http.Handler, actor, role, method, path string, body []byte, status int) []byte {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(body))
	request.Header.Set("X-User-ID", actor)
	request.Header.Set("X-User-Roles", role)
	request.Header.Set("Content-Type", "application/json")
	if role != "" {
		request.Header.Set("Authorization", "Bearer "+topicGatewayToken)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatal(method, path, "got", response.Code, "want", status, response.Body)
	}

	return response.Body.Bytes()
}

func requireTopic(t *testing.T, body []byte) domain.Topic {
	t.Helper()
	var item domain.Topic
	if err := json.Unmarshal(body, &item); err != nil || item.ID == "" {
		t.Fatal("invalid topic response", err)
	}

	return item
}

func requireLearningMetadata(t *testing.T, got *domain.TopicLearningMetadata, minutes int, outcomes []string) {
	t.Helper()
	if got == nil || got.EstimatedMinutes == nil || *got.EstimatedMinutes != minutes || !slices.Equal(got.Outcomes, outcomes) {
		t.Fatal("unexpected learning metadata", got, minutes, outcomes)
	}
}

//nolint:paralleltest // Проверка версий из общей тестовой БД.
func TestPostgresLearningMetadataContract(t *testing.T) {
	pool := testCatalogPool(t, "")
	ctx := t.Context()
	store := New(pool)
	snapshot := catalogsnapshot.New(pool)
	handler := topicHandler(t, store)
	actor := uuid.NewString()
	initial, err := snapshot.Snapshot(ctx, catalogsnapshot.SnapshotRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"title":"Study ` + actor + `","slug":"` + actor +
		`","status":"active","learning_metadata":{"estimated_minutes":55,"outcomes":["  Compare approaches  "]}}`)
	topicRequest(t, handler, actor, "student", http.MethodPost, "/v1/topics", payload, http.StatusForbidden)

	created := requireTopic(t, topicRequest(t, handler, actor, "admin", http.MethodPost, "/v1/topics", payload, http.StatusCreated))
	requireLearningMetadata(t, created.LearningMetadata, 55, []string{"Compare approaches"})
	path := "/v1/topics/" + created.ID
	for _, input := range []struct {
		metadata string
		status   int
	}{
		{metadata: `{"estimated_minutes":1201}`, status: http.StatusUnprocessableEntity},
		{metadata: `{"outcomes":["ok"," OK "]}`, status: http.StatusUnprocessableEntity},
		{metadata: `{"outcomes":[null]}`, status: http.StatusUnprocessableEntity},
		{metadata: `{"estimated_minuts":30}`, status: http.StatusBadRequest},
		{metadata: `[]`, status: http.StatusBadRequest},
	} {
		invalid := []byte(`{"title":"Study test","learning_metadata":` + input.metadata + `}`)
		topicRequest(t, handler, actor, "admin", http.MethodPut, path, invalid, input.status)
	}

	renamedBody := `"title":"Study ` + actor + ` renamed","slug":"` + actor + `-renamed"`
	renamed := requireTopic(t, topicRequest(t, handler, actor, "admin", http.MethodPut, path,
		[]byte("{"+renamedBody+"}"), http.StatusOK))
	requireLearningMetadata(t, renamed.LearningMetadata, 55, []string{"Compare approaches"})
	found := requireTopic(t, topicRequest(t, handler, actor, "", http.MethodGet, path, nil, http.StatusOK))
	requireLearningMetadata(t, found.LearningMetadata, 55, []string{"Compare approaches"})
	filter := domain.ListOptions{Limit: 1, Search: actor}
	listed, err := store.ListTopics(ctx, "", filter)
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatal("wrong topic on requested page", len(listed), err)
	}
	requireLearningMetadata(t, listed[0].LearningMetadata, 55, []string{"Compare approaches"})

	watermark := initial.Watermark + 2
	first, err := snapshot.Snapshot(ctx, catalogsnapshot.SnapshotRequest{Watermark: &watermark, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	cleared := requireTopic(t, topicRequest(t, handler, actor, "admin", http.MethodPut, path,
		[]byte("{"+renamedBody+`,"learning_metadata":{}}`), http.StatusOK))
	if cleared.LearningMetadata != nil {
		t.Fatal("explicit clear retained learning metadata")
	}
	previous := readSnapshot(t, ctx, snapshot, first)["topic/"+created.ID+"/"]
	requireLearningMetadata(t, graphLearningMetadata(t, previous.Record), 55, []string{"Compare approaches"})

	page, err := snapshot.Changes(ctx, catalogsnapshot.ChangeRequest{After: initial.Watermark, Limit: 10})
	if err != nil || len(page.Changes) != 3 || page.Through != initial.Watermark+3 {
		t.Fatal("invalid or unauthorized updates wrote events", page.Through, len(page.Changes), err)
	}
	for i, change := range page.Changes {
		var payload []byte
		if queryErr := pool.QueryRow(ctx, "SELECT payload FROM outbox_events WHERE id=$1", change.EventID).Scan(&payload); queryErr != nil {
			t.Fatal(queryErr)
		}
		fromPayload := requireTopic(t, payload).LearningMetadata
		fromGraph := graphLearningMetadata(t, change.Record)
		if i < 2 {
			requireLearningMetadata(t, fromPayload, 55, []string{"Compare approaches"})
			requireLearningMetadata(t, fromGraph, 55, []string{"Compare approaches"})
		} else if fromPayload != nil || fromGraph != nil {
			t.Fatal("cleared metadata remained in new event")
		}
	}
	for _, test := range []struct {
		query string
		arg   any
	}{
		{query: "UPDATE topics SET estimated_study_minutes=$2 WHERE id=$1", arg: 0},
		{query: "UPDATE topics SET learning_outcomes=$2 WHERE id=$1", arg: []string{strings.Repeat("x", 257)}},
		{query: "UPDATE topics SET learning_outcomes=$2 WHERE id=$1", arg: []string{"Duplicate", "Duplicate"}},
	} {
		_, updateErr := pool.Exec(ctx, test.query, created.ID, test.arg)
		var pgErr *pgconn.PgError
		if !errors.As(updateErr, &pgErr) || pgErr.Code != "23514" {
			t.Fatal("database allowed invalid metadata", updateErr)
		}
	}

	hiddenBody := []byte("{" + renamedBody + `,"learning_metadata":{"outcomes":["Private lesson"]}}`)
	topicRequest(t, handler, actor, "admin", http.MethodPut, path, hiddenBody, http.StatusOK)
	if _, statusErr := store.ChangeTopicStatus(ctx, created.ID, domain.StatusHidden, actor); statusErr != nil {
		t.Fatal(statusErr)
	}
	topicRequest(t, handler, actor, "", http.MethodGet, path, nil, http.StatusNotFound)
	public, err := store.PublicReads().ListTopics(ctx, "", filter)
	if err != nil || len(public) != 0 {
		t.Fatal("public page exposed a hidden topic", len(public), err)
	}
	draft := requireTopic(t, topicRequest(t, handler, actor, "admin", http.MethodGet, path, nil, http.StatusOK))
	if draft.LearningMetadata == nil || !slices.Equal(draft.LearningMetadata.Outcomes, []string{"Private lesson"}) {
		t.Fatal("hidden topic lost editor metadata")
	}
}

func graphLearningMetadata(t *testing.T, raw json.RawMessage) *domain.TopicLearningMetadata {
	t.Helper()
	var document struct {
		Topic struct {
			LearningMetadata *domain.TopicLearningMetadata `json:"learning_metadata"`
		} `json:"topic"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}

	return document.Topic.LearningMetadata
}

//nolint:paralleltest // Используется курс из общей тестовой БД.
func TestPostgresAppliesPilotMetadataToOneCourse(t *testing.T) {
	pool := testCatalogPool(t, "")
	ctx := t.Context()
	store := New(pool)
	const fixturePath = "../../examples/pilot/algorithms-learning-metadata.json"
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		CourseSlug string `json:"course_slug"`
		Topics     []struct {
			Slug     string                       `json:"slug"`
			Metadata domain.TopicLearningMetadata `json:"learning_metadata"`
		} `json:"topics"`
	}
	if decodeErr := json.Unmarshal(raw, &fixture); decodeErr != nil || len(fixture.Topics) != 5 {
		t.Fatal("invalid pilot fixture", decodeErr)
	}
	var courseID string
	if queryErr := pool.QueryRow(ctx, "SELECT id::text FROM courses WHERE slug=$1 AND deleted_at IS NULL ORDER BY id LIMIT 1",
		fixture.CourseSlug).Scan(&courseID); queryErr != nil {
		t.Fatal(queryErr)
	}
	topics, err := store.ListTopics(ctx, courseID, domain.ListOptions{Limit: 20})
	if err != nil || len(topics) != len(fixture.Topics) {
		t.Fatal("fixture does not match the selected course", len(topics), err)
	}
	bySlug := make(map[string]domain.Topic, len(topics))
	for _, topic := range topics {
		bySlug[topic.Slug] = topic
	}
	handler := topicHandler(t, store)
	actor := uuid.NewString()
	for _, entry := range fixture.Topics {
		topic, exists := bySlug[entry.Slug]
		if !exists || entry.Metadata.EstimatedMinutes == nil || len(entry.Metadata.Outcomes) == 0 {
			t.Fatal("pilot metadata missing for", entry.Slug)
		}
		topic.LearningMetadata = &entry.Metadata
		payload, err := json.Marshal(topic)
		if err != nil {
			t.Fatal(err)
		}
		path := "/v1/topics/" + topic.ID
		result := requireTopic(t, topicRequest(t, handler, actor, "admin", http.MethodPut, path, payload, http.StatusOK))
		requireLearningMetadata(t, result.LearningMetadata, *entry.Metadata.EstimatedMinutes, entry.Metadata.Outcomes)
		if result.CourseID == nil || *result.CourseID != courseID || result.Description != topic.Description {
			t.Fatal("pilot update changed topic ownership or description", result.ID)
		}
	}
}
