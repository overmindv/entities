package service

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/overmindv/ironhide/internal/apperror"
	"github.com/overmindv/ironhide/internal/domain"
	"github.com/samber/lo"
)

// Store задаёт storage contract для catalog-сущностей Ironhide.
type Store interface {
	Ping(ctx context.Context) error
	CreateUniversity(ctx context.Context, item domain.University) (domain.University, error)
	GetUniversity(ctx context.Context, id string) (domain.University, error)
	ListUniversities(ctx context.Context, options domain.ListOptions) ([]domain.University, error)
	UpdateUniversity(ctx context.Context, item domain.University) (domain.University, error)
	DeleteUniversity(ctx context.Context, id, actorID string) error
	ChangeUniversityStatus(ctx context.Context, id string, status domain.Status, actorID string) (domain.University, error)
	CreateProgram(ctx context.Context, item domain.Program) (domain.Program, error)
	GetProgram(ctx context.Context, id string) (domain.Program, error)
	ListPrograms(ctx context.Context, parentID string, options domain.ListOptions) ([]domain.Program, error)
	UpdateProgram(ctx context.Context, item domain.Program) (domain.Program, error)
	DeleteProgram(ctx context.Context, id, actorID string) error
	ChangeProgramStatus(ctx context.Context, id string, status domain.Status, actorID string) (domain.Program, error)
	CreateCourse(ctx context.Context, item domain.Course) (domain.Course, error)
	GetCourse(ctx context.Context, id string) (domain.Course, error)
	ListCourses(ctx context.Context, parentID string, options domain.ListOptions) ([]domain.Course, error)
	UpdateCourse(ctx context.Context, item domain.Course) (domain.Course, error)
	DeleteCourse(ctx context.Context, id, actorID string) error
	ChangeCourseStatus(ctx context.Context, id string, status domain.Status, actorID string) (domain.Course, error)
	CreateTopic(ctx context.Context, item domain.Topic) (domain.Topic, error)
	GetTopic(ctx context.Context, id string) (domain.Topic, error)
	ListTopics(ctx context.Context, parentID string, options domain.ListOptions) ([]domain.Topic, error)
	UpdateTopic(ctx context.Context, item domain.Topic) (domain.Topic, error)
	DeleteTopic(ctx context.Context, id, actorID string) error
	ChangeTopicStatus(ctx context.Context, id string, status domain.Status, actorID string) (domain.Topic, error)
	AddPrerequisite(ctx context.Context, item domain.TopicPrerequisite, actorID string) (domain.TopicPrerequisite, error)
	RemovePrerequisite(ctx context.Context, topicID, prerequisiteID, actorID string) error
	ListPrerequisites(ctx context.Context, topicID string) ([]domain.TopicPrerequisite, error)
	PrerequisitePathExists(ctx context.Context, fromID, toID string) (bool, error)
	ValidateBinding(ctx context.Context, binding domain.Binding) (bool, error)
}

// Service реализует бизнес-правила каталога поверх storage layer.
type Service struct {
	store Store
}

// New создаёт catalog service с переданным storage contract.
func New(store Store) *Service {
	return &Service{
		store: store,
	}
}

// Store возвращает storage contract для read-only HTTP handlers и healthcheck.
func (s *Service) Store() Store {
	return s.store
}

// CreateUniversity нормализует и создаёт университет от имени администратора.
func (s *Service) CreateUniversity(ctx context.Context, item domain.University, actorID string) (domain.University, error) {
	item.Name = strings.TrimSpace(item.Name)
	item.LogoFileID = normalizeOptionalUUID(item.LogoFileID)
	item.Status = defaultStatus(item.Status)
	item.CreatedBy = actorID
	if !validUUIDPointer(item.LogoFileID) || item.Name == "" {
		return domain.University{}, validation("logo_file_id должен быть UUID, если он передан; name обязателен")
	}

	return s.store.CreateUniversity(ctx, item)
}

// UpdateUniversity нормализует и обновляет университет от имени администратора.
func (s *Service) UpdateUniversity(ctx context.Context, item domain.University, actorID string) (domain.University, error) {
	item.Name = strings.TrimSpace(item.Name)
	item.LogoFileID = normalizeOptionalUUID(item.LogoFileID)
	item.UpdatedBy = actorID
	if !validUUID(item.ID) || !validUUIDPointer(item.LogoFileID) || item.Name == "" {
		return domain.University{}, validation("id и name обязательны; logo_file_id должен быть UUID, если он передан")
	}

	return s.store.UpdateUniversity(ctx, item)
}

// CreateProgram нормализует и создаёт программу внутри университета или backlog-программу.
func (s *Service) CreateProgram(ctx context.Context, item domain.Program, actorID string) (domain.Program, error) {
	item.Name = strings.TrimSpace(item.Name)
	item.UniversityID = normalizeOptionalUUID(item.UniversityID)
	item.DegreeLevel = defaultDegree(item.DegreeLevel)
	item.Status = defaultStatus(item.Status)
	item.CreatedBy = actorID
	if !validUUIDPointer(item.UniversityID) || item.Name == "" {
		return domain.Program{}, validation("university_id должен быть UUID, если он передан; name обязателен")
	}

	return s.store.CreateProgram(ctx, item)
}

// UpdateProgram нормализует и обновляет программу от имени администратора.
func (s *Service) UpdateProgram(ctx context.Context, item domain.Program, actorID string) (domain.Program, error) {
	item.Name = strings.TrimSpace(item.Name)
	item.UniversityID = normalizeOptionalUUID(item.UniversityID)
	item.DegreeLevel = defaultDegree(item.DegreeLevel)
	item.UpdatedBy = actorID
	if !validUUID(item.ID) || item.Name == "" {
		return domain.Program{}, validation("id и name обязательны")
	}

	return s.store.UpdateProgram(ctx, item)
}

// CreateCourse нормализует и создаёт курс внутри программы или backlog-курс.
func (s *Service) CreateCourse(ctx context.Context, item domain.Course, actorID string) (domain.Course, error) {
	item.Name = strings.TrimSpace(item.Name)
	item.ProgramID = normalizeOptionalUUID(item.ProgramID)
	item.Slug = Slug(item.Slug, item.Name)
	item.Status = defaultStatus(item.Status)
	item.CreatedBy = actorID
	if !validUUIDPointer(item.ProgramID) || item.Name == "" {
		return domain.Course{}, validation("program_id должен быть UUID, если он передан; name обязателен")
	}

	return s.store.CreateCourse(ctx, item)
}

// UpdateCourse нормализует и обновляет курс от имени администратора.
func (s *Service) UpdateCourse(ctx context.Context, item domain.Course, actorID string) (domain.Course, error) {
	item.Name = strings.TrimSpace(item.Name)
	item.ProgramID = normalizeOptionalUUID(item.ProgramID)
	item.Slug = Slug(item.Slug, item.Name)
	item.UpdatedBy = actorID
	if !validUUID(item.ID) || item.Name == "" {
		return domain.Course{}, validation("id и name обязательны")
	}

	return s.store.UpdateCourse(ctx, item)
}

// CreateTopic нормализует и создаёт тему внутри курса или backlog-тему.
func (s *Service) CreateTopic(ctx context.Context, item domain.Topic, actorID string) (domain.Topic, error) {
	item.Title = strings.TrimSpace(item.Title)
	item.CourseID = normalizeOptionalUUID(item.CourseID)
	item.ParentTopicID = normalizeOptionalUUID(item.ParentTopicID)
	item.Slug = Slug(item.Slug, item.Title)
	item.Difficulty = defaultDifficulty(item.Difficulty)
	item.Status = defaultStatus(item.Status)
	item.CreatedBy = actorID
	if !validUUIDPointer(item.CourseID) || item.Title == "" {
		return domain.Topic{}, validation("course_id должен быть UUID, если он передан; title обязателен")
	}
	// Родителя проверяем до записи, чтобы тема не могла попасть в чужой курс или создать цикл иерархии.
	if err := s.validateParent(ctx, item.ID, item.CourseID, item.ParentTopicID); err != nil {
		return domain.Topic{}, err
	}

	return s.store.CreateTopic(ctx, item)
}

// UpdateTopic нормализует и обновляет тему, сохраняя её текущий course_id.
func (s *Service) UpdateTopic(ctx context.Context, item domain.Topic, actorID string) (domain.Topic, error) {
	current, err := s.store.GetTopic(ctx, item.ID)
	if err != nil {
		return domain.Topic{}, err
	}
	item.CourseID = current.CourseID
	item.ParentTopicID = normalizeOptionalUUID(item.ParentTopicID)
	item.Title = strings.TrimSpace(item.Title)
	item.Slug = Slug(item.Slug, item.Title)
	item.Difficulty = defaultDifficulty(item.Difficulty)
	item.UpdatedBy = actorID
	if item.Title == "" {
		return domain.Topic{}, validation("title обязателен")
	}
	// course_id берём из текущей темы, потому что update меняет только профиль темы, а не переносит её между курсами.
	if err := s.validateParent(ctx, item.ID, item.CourseID, item.ParentTopicID); err != nil {
		return domain.Topic{}, err
	}

	return s.store.UpdateTopic(ctx, item)
}

// TopicTree возвращает дерево тем для курса или общий backlog-лес без course_id.
func (s *Service) TopicTree(ctx context.Context, courseID string) ([]*domain.TopicTreeNode, error) {
	topics, err := s.store.ListTopics(ctx, courseID, domain.ListOptions{Limit: 10000})
	if err != nil {
		return nil, err
	}

	return BuildTopicTree(topics), nil
}

// AddPrerequisite добавляет связь prerequisite между темами одного курса.
func (s *Service) AddPrerequisite(ctx context.Context, item domain.TopicPrerequisite, actorID string) (domain.TopicPrerequisite, error) {
	if item.TopicID == item.PrerequisiteTopicID || !validUUID(item.TopicID) || !validUUID(item.PrerequisiteTopicID) {
		return domain.TopicPrerequisite{}, apperror.New(apperror.InvalidTopicPrerequisite, "тема не может быть пререквизитом самой себя", http.StatusUnprocessableEntity)
	}
	topic, err := s.store.GetTopic(ctx, item.TopicID)
	if err != nil {
		return domain.TopicPrerequisite{}, err
	}
	prerequisite, err := s.store.GetTopic(ctx, item.PrerequisiteTopicID)
	if err != nil {
		return domain.TopicPrerequisite{}, err
	}
	if !sameOptionalString(topic.CourseID, prerequisite.CourseID) {
		return domain.TopicPrerequisite{}, apperror.New(apperror.InvalidTopicPrerequisite, "темы должны принадлежать одному курсу", http.StatusUnprocessableEntity)
	}
	// Проверяем обратный путь до вставки, чтобы новая связь не замкнула граф пререквизитов.
	cycle, err := s.store.PrerequisitePathExists(ctx, item.PrerequisiteTopicID, item.TopicID)
	if err != nil {
		return domain.TopicPrerequisite{}, err
	}
	if cycle {
		return domain.TopicPrerequisite{}, apperror.New(apperror.TopicPrerequisiteCycle, "обнаружен цикл пререквизитов", http.StatusConflict)
	}

	return s.store.AddPrerequisite(ctx, item, actorID)
}

// ValidateBinding проверяет существование и согласованность связки university -> program -> course -> topic.
func (s *Service) ValidateBinding(ctx context.Context, binding domain.Binding) (domain.ValidationResult, error) {
	binding.UniversityID = normalizeOptionalUUID(binding.UniversityID)
	binding.ProgramID = normalizeOptionalUUID(binding.ProgramID)
	binding.CourseID = normalizeOptionalUUID(binding.CourseID)
	binding.TopicID = normalizeOptionalUUID(binding.TopicID)
	if !validUUIDPointer(binding.UniversityID) || !validUUIDPointer(binding.ProgramID) || !validUUIDPointer(binding.CourseID) || !validUUIDPointer(binding.TopicID) {
		return domain.ValidationResult{}, validation("идентификаторы связки должны быть UUID, если они переданы")
	}
	valid, err := s.store.ValidateBinding(ctx, binding)
	if err != nil {
		return domain.ValidationResult{}, err
	}

	return domain.ValidationResult{Valid: valid}, nil
}

// validateParent проверяет родительскую тему, принадлежность курсу и отсутствие циклов.
func (s *Service) validateParent(ctx context.Context, topicID string, courseID *string, parentID *string) error {
	if parentID == nil {
		return nil
	}
	if *parentID == topicID {
		return apperror.New(apperror.TopicHierarchyCycle, "тема не может быть родителем самой себя", http.StatusConflict)
	}
	visited := map[string]bool{topicID: true}
	currentID := *parentID
	for currentID != "" {
		if visited[currentID] {
			return apperror.New(apperror.TopicHierarchyCycle, "обнаружен цикл иерархии тем", http.StatusConflict)
		}
		visited[currentID] = true
		parent, err := s.store.GetTopic(ctx, currentID)
		if err != nil {
			return apperror.New(apperror.InvalidParentTopic, "родительская тема не найдена", http.StatusUnprocessableEntity)
		}
		if !sameOptionalString(parent.CourseID, courseID) {
			return apperror.New(apperror.InvalidParentTopic, "родительская тема принадлежит другому курсу", http.StatusUnprocessableEntity)
		}
		if parent.ParentTopicID == nil {
			break
		}
		// Поднимаемся по цепочке родителей, чтобы обнаружить цикл любой глубины, а не только прямую ссылку.
		currentID = *parent.ParentTopicID
	}

	return nil
}

// BuildTopicTree строит отсортированное дерево тем из плоского списка.
func BuildTopicTree(topics []domain.Topic) []*domain.TopicTreeNode {
	nodes := lo.SliceToMap(topics, func(topic domain.Topic) (string, *domain.TopicTreeNode) {
		return topic.ID, &domain.TopicTreeNode{Topic: topic, Children: make([]*domain.TopicTreeNode, 0)}
	})
	roots := make([]*domain.TopicTreeNode, 0)
	for _, topic := range topics {
		node := nodes[topic.ID]
		if topic.ParentTopicID == nil || nodes[*topic.ParentTopicID] == nil {
			roots = append(roots, node)
			continue
		}
		parent := nodes[*topic.ParentTopicID]
		parent.Children = append(parent.Children, node)
	}
	var sortNodes func(items []*domain.TopicTreeNode)
	sortNodes = func(items []*domain.TopicTreeNode) {
		sort.SliceStable(items, func(i, j int) bool {
			return items[i].Topic.OrderIndex < items[j].Topic.OrderIndex
		})
		for _, item := range items {
			sortNodes(item.Children)
		}
	}
	sortNodes(roots)

	return roots
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slug генерирует URL-safe slug из заданного значения или fallback.
func Slug(value, fallback string) string {
	source := strings.TrimSpace(value)
	if source == "" {
		source = fallback
	}
	transliterated := strings.Map(func(r rune) rune {
		if r <= unicode.MaxASCII {
			return unicode.ToLower(r)
		}
		replacements := map[rune]rune{'а': 'a', 'б': 'b', 'в': 'v', 'г': 'g', 'д': 'd', 'е': 'e', 'ё': 'e', 'ж': 'z', 'з': 'z', 'и': 'i', 'й': 'i', 'к': 'k', 'л': 'l', 'м': 'm', 'н': 'n', 'о': 'o', 'п': 'p', 'р': 'r', 'с': 's', 'т': 't', 'у': 'u', 'ф': 'f', 'х': 'h', 'ц': 'c', 'ч': 'c', 'ш': 's', 'щ': 's', 'ы': 'y', 'э': 'e', 'ю': 'u', 'я': 'a'}
		return replacements[unicode.ToLower(r)]
	}, source)
	result := strings.Trim(nonSlug.ReplaceAllString(transliterated, "-"), "-")
	if result == "" {
		result = uuid.NewString()
	}

	return result
}

// NormalizeList ограничивает pagination и подставляет безопасные значения по умолчанию.
func NormalizeList(options domain.ListOptions) domain.ListOptions {
	if options.Limit <= 0 || options.Limit > 200 {
		options.Limit = 50
	}
	if options.Offset < 0 {
		options.Offset = 0
	}

	return options
}

// validUUID проверяет строку на UUID format.
func validUUID(value string) bool {
	_, err := uuid.Parse(value)

	return err == nil
}

// validUUIDPointer проверяет optional UUID pointer и разрешает nil/empty backlog-значение.
func validUUIDPointer(value *string) bool {
	if value == nil || strings.TrimSpace(*value) == "" {
		return true
	}

	return validUUID(*value)
}

// normalizeOptionalUUID переводит пустой optional UUID в nil и trims непустое значение.
func normalizeOptionalUUID(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}

// sameOptionalString сравнивает nullable строки с учётом пустой строки как отсутствующего значения.
func sameOptionalString(left, right *string) bool {
	if left == nil || strings.TrimSpace(*left) == "" {
		return right == nil || strings.TrimSpace(*right) == ""
	}
	if right == nil {
		return false
	}

	return *left == *right
}

// defaultStatus возвращает допустимый status или draft по умолчанию.
func defaultStatus(status domain.Status) domain.Status {
	switch status {
	case domain.StatusDraft, domain.StatusActive, domain.StatusHidden, domain.StatusArchived:
		return status
	default:
		return domain.StatusDraft
	}
}

// defaultDegree возвращает допустимый degree_level или other по умолчанию.
func defaultDegree(value string) string {
	switch value {
	case "bachelor", "master", "specialist", "phd", "other":
		return value
	default:
		return "other"
	}
}

// defaultDifficulty возвращает допустимую difficulty или basic по умолчанию.
func defaultDifficulty(value string) string {
	switch value {
	case "intro", "basic", "medium", "hard", "advanced":
		return value
	default:
		return "basic"
	}
}

// validation создаёт публичную validation error для service layer.
func validation(message string) error {
	return apperror.New(apperror.ValidationError, message, http.StatusUnprocessableEntity)
}
