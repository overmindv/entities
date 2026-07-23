package service

import (
	"context"
	"testing"

	"github.com/overmindv/ironhide/internal/domain"
)

// recordingStore запоминает входы service layer для unit-тестов нормализации.
type recordingStore struct {
	Store
	university domain.University
	program    domain.Program
	course     domain.Course
	topic      domain.Topic
	binding    domain.Binding
}

// CreateUniversity сохраняет последний university input.
func (s *recordingStore) CreateUniversity(_ context.Context, item domain.University) (domain.University, error) {
	s.university = item

	return item, nil
}

// CreateProgram сохраняет последний program input.
func (s *recordingStore) CreateProgram(_ context.Context, item domain.Program) (domain.Program, error) {
	s.program = item

	return item, nil
}

// CreateCourse сохраняет последний course input.
func (s *recordingStore) CreateCourse(_ context.Context, item domain.Course) (domain.Course, error) {
	s.course = item

	return item, nil
}

// CreateTopic сохраняет последний topic input.
func (s *recordingStore) CreateTopic(_ context.Context, item domain.Topic) (domain.Topic, error) {
	s.topic = item

	return item, nil
}

// ValidateBinding сохраняет последнюю binding-проверку и возвращает valid=true.
func (s *recordingStore) ValidateBinding(_ context.Context, binding domain.Binding) (bool, error) {
	s.binding = binding

	return true, nil
}

// TestCreateNormalizesEmptyOptionalUUIDs проверяет, что пустые optional UUID не уходят в PostgreSQL как пустые строки.
func TestCreateNormalizesEmptyOptionalUUIDs(t *testing.T) {
	t.Parallel()
	empty := ""
	store := &recordingStore{}
	catalog := New(store)

	if _, err := catalog.CreateUniversity(context.Background(), domain.University{Name: "University", LogoFileID: &empty}, "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatal(err)
	}
	if store.university.LogoFileID != nil {
		t.Fatalf("logo_file_id должен быть nil, получено %q", *store.university.LogoFileID)
	}

	if _, err := catalog.CreateProgram(context.Background(), domain.Program{Name: "Program", UniversityID: &empty}, "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatal(err)
	}
	if store.program.UniversityID != nil {
		t.Fatalf("university_id должен быть nil, получено %q", *store.program.UniversityID)
	}

	if _, err := catalog.CreateCourse(context.Background(), domain.Course{Name: "Course", ProgramID: &empty}, "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatal(err)
	}
	if store.course.ProgramID != nil {
		t.Fatalf("program_id должен быть nil, получено %q", *store.course.ProgramID)
	}

	if _, err := catalog.CreateTopic(context.Background(), domain.Topic{Title: "Topic", CourseID: &empty, ParentTopicID: &empty}, "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatal(err)
	}
	if store.topic.CourseID != nil || store.topic.ParentTopicID != nil {
		t.Fatalf("topic optional IDs должны быть nil, получено course=%v parent=%v", store.topic.CourseID, store.topic.ParentTopicID)
	}
}

// TestValidateBindingNormalizesEmptyOptionalUUIDs проверяет нормализацию пустых ID перед проверкой связки.
func TestValidateBindingNormalizesEmptyOptionalUUIDs(t *testing.T) {
	t.Parallel()
	empty := ""
	store := &recordingStore{}
	catalog := New(store)

	result, err := catalog.ValidateBinding(context.Background(), domain.Binding{
		UniversityID: &empty,
		ProgramID:    &empty,
		CourseID:     &empty,
		TopicID:      &empty,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatal("binding должен остаться валидным после нормализации пустых optional UUID")
	}
	if store.binding.UniversityID != nil || store.binding.ProgramID != nil || store.binding.CourseID != nil || store.binding.TopicID != nil {
		t.Fatalf("binding optional IDs должны быть nil, получено %+v", store.binding)
	}
}

// TestSlugGeneratesStableValue проверяет стабильную генерацию slug из русскоязычного названия.
func TestSlugGeneratesStableValue(t *testing.T) {
	t.Parallel()
	if got := Slug("", "Производная функции"); got != "proizvodnaa-funkcii" {
		t.Fatalf("неожиданный slug: %s", got)
	}
}

// TestBuildTopicTree проверяет построение дерева тем из плоского списка.
func TestBuildTopicTree(t *testing.T) {
	t.Parallel()
	parentID := "parent"
	topics := []domain.Topic{
		{ID: "child", ParentTopicID: &parentID, OrderIndex: 2},
		{ID: parentID, OrderIndex: 1},
	}
	tree := BuildTopicTree(topics)
	if len(tree) != 1 || len(tree[0].Children) != 1 || tree[0].Children[0].Topic.ID != "child" {
		t.Fatalf("неверно построено дерево: %#v", tree)
	}
}
