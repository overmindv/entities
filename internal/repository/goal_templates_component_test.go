package repository

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
)

//nolint:paralleltest // Проверяет публикацию на общей ревизии каталога.
func TestGoalTemplatePublication(t *testing.T) {
	pool := testCatalogPool(t, "")
	store := New(pool)
	ctx := t.Context()
	actor := uuid.NewString()
	program := goalTestProgram(t, store, actor)
	other := goalTestProgram(t, store, actor)
	course := goalTestCourse(t, store, actor, program)
	otherCourse := goalTestCourse(t, store, actor, other)
	target := goalTestTopic(t, store, actor, course)
	base := goalTestTopic(t, store, actor, course)
	external := goalTestTopic(t, store, actor, otherCourse)
	absent := uuid.NewString()
	pair := domain.TopicPrerequisite{TopicID: target, PrerequisiteTopicID: base}
	if _, err := store.AddPrerequisite(ctx, pair, actor); err != nil {
		t.Fatal(err)
	}
	firstID, secondID := uuid.NewString(), uuid.NewString()
	if firstID > secondID {
		firstID, secondID = secondID, firstID
	}
	input := domain.GoalTemplateInput{
		Title: "Подготовка к экзамену",
		TargetTopics: []domain.GoalTemplateTarget{
			{TopicID: target, TargetScoreBPS: 7500},
			{TopicID: base, TargetScoreBPS: 8500},
		},
	}
	for _, topic := range []string{external, absent} {
		invalid := domain.GoalTemplateInput{Title: input.Title, TargetTopics: []domain.GoalTemplateTarget{{TopicID: topic, TargetScoreBPS: 1}}}
		_, _, err := store.PublishGoalTemplate(ctx, program, firstID, actor, 1, invalid)
		requireGoalError(t, err, apperror.GoalTemplateInvalidGraph)
	}
	_, _, err := store.PublishGoalTemplate(ctx, program, firstID, actor, 2, input)
	requireGoalError(t, err, apperror.GoalTemplateConflict)

	first, created, err := store.PublishGoalTemplate(ctx, program, firstID, actor, 1, input)
	if err != nil || !created || first.CatalogRevision == 0 || first.PublishedAt.IsZero() {
		t.Fatal("version 1 not published:", first, created, err)
	}
	secondInput := input
	secondInput.Title = "Графы и сортировки"
	second, created, err := store.PublishGoalTemplate(ctx, program, firstID, actor, 2, secondInput)
	if err != nil || !created || second.Version != 2 {
		t.Fatal("version 2 not published:", second, created, err)
	}
	_, _, err = store.PublishGoalTemplate(ctx, program, firstID, actor, 1, secondInput)
	requireGoalError(t, err, apperror.GoalTemplateConflict)
	_, _, err = store.PublishGoalTemplate(ctx, other, firstID, actor, 1, input)
	requireGoalError(t, err, apperror.GoalTemplateConflict)
	_, _, err = store.PublishGoalTemplate(ctx, program, secondID, actor, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.ListGoalTemplates(ctx, program, uuid.Nil.String(), 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != firstID || page.Items[0].Version != 2 || page.NextCursor != firstID {
		t.Fatal("unexpected first page:", page, err)
	}
	page, err = store.ListGoalTemplates(ctx, program, page.NextCursor, 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != secondID || page.NextCursor != "" {
		t.Fatal("unexpected last page:", page, err)
	}
	empty, err := store.ListGoalTemplates(ctx, other, uuid.Nil.String(), 1)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatal("unexpected empty page:", empty, err)
	}

	if _, changeErr := store.ChangeTopicStatus(ctx, base, domain.StatusArchived, actor); changeErr != nil {
		t.Fatal(changeErr)
	}
	for _, topic := range []string{base, target} {
		invalid := domain.GoalTemplateInput{Title: input.Title, TargetTopics: []domain.GoalTemplateTarget{{TopicID: topic, TargetScoreBPS: 1}}}
		_, _, publishErr := store.PublishGoalTemplate(ctx, program, firstID, actor, 3, invalid)
		requireGoalError(t, publishErr, apperror.GoalTemplateInvalidGraph)
	}
	_, err = store.GoalTemplateVersion(ctx, firstID, 3)
	requireGoalError(t, err, apperror.GoalTemplateNotFound)
	history, err := store.GoalTemplateVersion(ctx, firstID, 1)
	if err != nil || !reflect.DeepEqual(first, history) {
		t.Fatal("history was modified:", history, err)
	}
	replay, created, err := store.PublishGoalTemplate(ctx, program, firstID, actor, 1, input)
	if err != nil || created || !reflect.DeepEqual(first, replay) {
		t.Fatal("replay did not return the published version:", replay, created, err)
	}
}

//nolint:paralleltest // Использует общую ревизию каталога.
func TestGoalTemplateRejectsCycle(t *testing.T) {
	pool := testCatalogPool(t, "")
	store := New(pool)
	actor := uuid.NewString()
	program := goalTestProgram(t, store, actor)
	course := goalTestCourse(t, store, actor, program)
	first := goalTestTopic(t, store, actor, course)
	second := goalTestTopic(t, store, actor, course)
	if _, err := pool.Exec(t.Context(), `INSERT INTO topic_prerequisites (topic_id, prerequisite_topic_id) VALUES ($1,$2),($2,$1)`, first, second); err != nil {
		t.Fatal(err)
	}
	input := domain.GoalTemplateInput{Title: "Cycle", TargetTopics: []domain.GoalTemplateTarget{{TopicID: first, TargetScoreBPS: 1}}}
	_, _, err := store.PublishGoalTemplate(t.Context(), program, uuid.NewString(), actor, 1, input)
	requireGoalError(t, err, apperror.GoalTemplateInvalidGraph)
}

//nolint:paralleltest // Меняет ревизию между чтением графа и публикацией.
func TestGoalTemplateDetectsCatalogChange(t *testing.T) {
	pool := testCatalogPool(t, "")
	store := New(pool)
	ctx := t.Context()
	actor := uuid.NewString()
	program := goalTestProgram(t, store, actor)
	topic := goalTestTopic(t, store, actor, goalTestCourse(t, store, actor, program))
	err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		if validateErr := validateGoalTemplateGraph(ctx, tx, program, []string{topic}); validateErr != nil {
			return validateErr
		}
		if _, changeErr := store.ChangeTopicStatus(ctx, topic, domain.StatusArchived, actor); changeErr != nil {
			return changeErr
		}
		_, lockErr := lockGoalTemplateRevision(ctx, tx)

		return lockErr
	})
	requireGoalError(t, err, apperror.GoalTemplateCatalogMoved)
}

func requireGoalError(t *testing.T, err error, code string) {
	t.Helper()

	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatal("expected", code, "got", err)
	}
}

func goalTestProgram(t *testing.T, store *Postgres, actor string) string {
	t.Helper()

	program, err := store.CreateProgram(t.Context(), domain.Program{
		Name: uuid.NewString(), DegreeLevel: "bachelor", Status: domain.StatusActive, CreatedBy: actor,
	})
	if err != nil {
		t.Fatal(err)
	}

	return program.ID
}

func goalTestCourse(t *testing.T, store *Postgres, actor, program string) string {
	t.Helper()

	course, err := store.CreateCourse(t.Context(), domain.Course{
		ProgramID: &program, Name: "Course", Slug: uuid.NewString(), Status: domain.StatusActive, CreatedBy: actor,
	})
	if err != nil {
		t.Fatal(err)
	}

	return course.ID
}

func goalTestTopic(t *testing.T, store *Postgres, actor, course string) string {
	t.Helper()

	topic, err := store.CreateTopic(t.Context(), domain.Topic{
		CourseID: &course, Title: "Topic", Slug: uuid.NewString(), Status: domain.StatusActive, Difficulty: "basic", CreatedBy: actor,
	})
	if err != nil {
		t.Fatal(err)
	}

	return topic.ID
}
