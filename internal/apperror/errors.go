package apperror

import "fmt"

const (
	UniversityNotFound       = "UNIVERSITY_NOT_FOUND"
	ProgramNotFound          = "PROGRAM_NOT_FOUND"
	CourseNotFound           = "COURSE_NOT_FOUND"
	TopicNotFound            = "TOPIC_NOT_FOUND"
	UniversityAlreadyExists  = "UNIVERSITY_ALREADY_EXISTS"
	ProgramAlreadyExists     = "PROGRAM_ALREADY_EXISTS"
	CourseAlreadyExists      = "COURSE_ALREADY_EXISTS"
	TopicAlreadyExists       = "TOPIC_ALREADY_EXISTS"
	InvalidParentTopic       = "INVALID_PARENT_TOPIC"
	TopicHierarchyCycle      = "TOPIC_HIERARCHY_CYCLE"
	InvalidTopicPrerequisite = "INVALID_TOPIC_PREREQUISITE"
	TopicPrerequisiteCycle   = "TOPIC_PREREQUISITE_CYCLE"
	PermissionDenied         = "PERMISSION_DENIED"
	ValidationError          = "VALIDATION_ERROR"
	InternalError            = "INTERNAL_ERROR"
)

// Error описывает публичную JSON-ошибку Entities с машинным code.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

// Error возвращает строковое представление ошибки для Go error contract.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// New создаёт публичную ошибку Entities с HTTP status.
func New(code, message string, status int) *Error {
	return &Error{
		Code:    code,
		Message: message,
		Status:  status,
	}
}
