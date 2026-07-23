package domain

import "time"

// Status описывает lifecycle-состояние catalog-сущности.
type Status string

const (
	// StatusDraft означает черновик, который ещё не опубликован.
	StatusDraft Status = "draft"
	// StatusActive означает опубликованную активную сущность.
	StatusActive Status = "active"
	// StatusHidden означает скрытую сущность без удаления.
	StatusHidden Status = "hidden"
	// StatusArchived означает архивную сущность.
	StatusArchived Status = "archived"
)

// University описывает университет как верхний уровень каталога.
type University struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	ShortName  string     `json:"short_name"`
	City       string     `json:"city"`
	Country    string     `json:"country"`
	WebsiteURL string     `json:"website_url"`
	LogoFileID *string    `json:"logo_file_id"`
	Status     Status     `json:"status"`
	CreatedBy  string     `json:"created_by"`
	UpdatedBy  string     `json:"updated_by"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
}

// Program описывает образовательную программу внутри университета или backlog-программу без университета.
type Program struct {
	ID           string     `json:"id"`
	UniversityID *string    `json:"university_id"`
	Name         string     `json:"name"`
	ShortName    string     `json:"short_name"`
	Faculty      string     `json:"faculty"`
	DegreeLevel  string     `json:"degree_level"`
	StartYear    *int       `json:"start_year"`
	Status       Status     `json:"status"`
	CreatedBy    string     `json:"created_by"`
	UpdatedBy    string     `json:"updated_by"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
}

// Course описывает курс внутри программы или backlog-курс без программы.
type Course struct {
	ID          string     `json:"id"`
	ProgramID   *string    `json:"program_id"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Description string     `json:"description"`
	Semester    *int       `json:"semester"`
	YearNumber  *int       `json:"year_number"`
	Status      Status     `json:"status"`
	CreatedBy   string     `json:"created_by"`
	UpdatedBy   string     `json:"updated_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// Topic описывает тему внутри курса или backlog-тему без курса.
type Topic struct {
	ID            string     `json:"id"`
	CourseID      *string    `json:"course_id"`
	ParentTopicID *string    `json:"parent_topic_id"`
	Title         string     `json:"title"`
	Slug          string     `json:"slug"`
	Description   string     `json:"description"`
	OrderIndex    int        `json:"order_index"`
	Difficulty    string     `json:"difficulty"`
	Status        Status     `json:"status"`
	CreatedBy     string     `json:"created_by"`
	UpdatedBy     string     `json:"updated_by"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
}

// TopicTreeNode описывает узел дерева тем.
type TopicTreeNode struct {
	Topic    Topic            `json:"topic"`
	Children []*TopicTreeNode `json:"children"`
}

// TopicPrerequisite описывает направленную связь "тема требует другую тему".
type TopicPrerequisite struct {
	TopicID             string    `json:"topic_id"`
	PrerequisiteTopicID string    `json:"prerequisite_topic_id"`
	CreatedAt           time.Time `json:"created_at"`
}

// ListOptions задаёт поиск, фильтр статуса и pagination для catalog lists.
type ListOptions struct {
	Search string
	Status string
	Limit  int
	Offset int
}

// Binding описывает проверяемую связку university -> program -> course -> topic.
type Binding struct {
	UniversityID *string `json:"university_id"`
	ProgramID    *string `json:"program_id"`
	CourseID     *string `json:"course_id"`
	TopicID      *string `json:"topic_id"`
}

// ValidationResult возвращает результат проверки связки или существования сущности.
type ValidationResult struct {
	Valid bool `json:"valid"`
}
