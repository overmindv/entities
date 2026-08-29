package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
	"github.com/overmindv/entities/internal/service"
)

// Router — минимальный контракт регистрации HTTP-роутов. Реализуется как
// *parker.HTTPServer (в проде), так и *http.ServeMux (в тестах).
type Router interface {
	Handle(pattern string, handler http.Handler)
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// Handler обслуживает внутренний HTTP JSON API Entities.
type Handler struct {
	service *service.Service
	logger  *slog.Logger
}

// Register регистрирует бизнес-роуты Entities на роутер parker.
// health/ready/request-id/access-log/metrics предоставляет parker.
func Register(router Router, catalog *service.Service, logger *slog.Logger) {
	handler := &Handler{
		service: catalog,
		logger:  logger,
	}
	router.HandleFunc("GET /v1/universities", handler.listUniversities)
	router.HandleFunc("POST /v1/universities", handler.createUniversity)
	router.HandleFunc("GET /v1/universities/{id}", handler.getUniversity)
	router.HandleFunc("PUT /v1/universities/{id}", handler.updateUniversity)
	router.HandleFunc("DELETE /v1/universities/{id}", handler.deleteUniversity)
	router.HandleFunc("PATCH /v1/universities/{id}/status", handler.changeUniversityStatus)
	router.HandleFunc("GET /v1/programs", handler.listPrograms)
	router.HandleFunc("POST /v1/programs", handler.createProgram)
	router.HandleFunc("GET /v1/programs/{id}", handler.getProgram)
	router.HandleFunc("PUT /v1/programs/{id}", handler.updateProgram)
	router.HandleFunc("DELETE /v1/programs/{id}", handler.deleteProgram)
	router.HandleFunc("PATCH /v1/programs/{id}/status", handler.changeProgramStatus)
	router.HandleFunc("GET /v1/courses", handler.listCourses)
	router.HandleFunc("POST /v1/courses", handler.createCourse)
	router.HandleFunc("GET /v1/courses/{id}", handler.getCourse)
	router.HandleFunc("PUT /v1/courses/{id}", handler.updateCourse)
	router.HandleFunc("DELETE /v1/courses/{id}", handler.deleteCourse)
	router.HandleFunc("PATCH /v1/courses/{id}/status", handler.changeCourseStatus)
	router.HandleFunc("GET /v1/topics", handler.listTopics)
	router.HandleFunc("POST /v1/topics", handler.createTopic)
	router.HandleFunc("GET /v1/topics/{id}", handler.getTopic)
	router.HandleFunc("PUT /v1/topics/{id}", handler.updateTopic)
	router.HandleFunc("DELETE /v1/topics/{id}", handler.deleteTopic)
	router.HandleFunc("PATCH /v1/topics/{id}/status", handler.changeTopicStatus)
	router.HandleFunc("GET /v1/topic-tree", handler.topicTreeByQuery)
	router.HandleFunc("GET /v1/courses/{id}/topic-tree", handler.topicTree)
	router.HandleFunc("GET /v1/topics/{id}/prerequisites", handler.listPrerequisites)
	router.HandleFunc("POST /v1/topics/{id}/prerequisites", handler.addPrerequisite)
	router.HandleFunc("DELETE /v1/topics/{id}/prerequisites/{prerequisite_id}", handler.removePrerequisite)
	router.HandleFunc("GET /v1/validate/{entity}/{id}", handler.validateEntity)
	router.HandleFunc("POST /v1/validate/binding", handler.validateBinding)
}

// createUniversity создаёт университет после проверки роли admin/superuser.
func (h *Handler) createUniversity(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	var input domain.University
	if !decode(w, r, &input) {
		return
	}
	result, err := h.service.CreateUniversity(r.Context(), input, actor)
	h.respond(w, http.StatusCreated, result, err)
}

// getUniversity возвращает университет по ID.
func (h *Handler) getUniversity(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Store().GetUniversity(r.Context(), r.PathValue("id"))
	h.respond(w, http.StatusOK, result, err)
}

// listUniversities возвращает список университетов с фильтрами.
func (h *Handler) listUniversities(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Store().ListUniversities(r.Context(), listOptions(r))
	h.respond(w, http.StatusOK, result, err)
}

// updateUniversity обновляет университет после проверки роли admin/superuser.
func (h *Handler) updateUniversity(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	var input domain.University
	if !decode(w, r, &input) {
		return
	}
	input.ID = r.PathValue("id")
	result, err := h.service.UpdateUniversity(r.Context(), input, actor)
	h.respond(w, http.StatusOK, result, err)
}

// deleteUniversity выполняет soft delete университета после проверки роли admin/superuser.
func (h *Handler) deleteUniversity(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	err := h.service.Store().DeleteUniversity(r.Context(), r.PathValue("id"), actor)
	h.respond(w, http.StatusOK, map[string]bool{"deleted": err == nil}, err)
}

// changeUniversityStatus меняет status университета после проверки роли admin/superuser.
func (h *Handler) changeUniversityStatus(w http.ResponseWriter, r *http.Request) {
	actor, status, ok := h.statusInput(w, r)
	if !ok {
		return
	}
	result, err := h.service.Store().ChangeUniversityStatus(r.Context(), r.PathValue("id"), status, actor)
	h.respond(w, http.StatusOK, result, err)
}

// createProgram создаёт программу после проверки роли admin/superuser.
func (h *Handler) createProgram(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	var input domain.Program
	if !decode(w, r, &input) {
		return
	}
	result, err := h.service.CreateProgram(r.Context(), input, actor)
	h.respond(w, http.StatusCreated, result, err)
}

// getProgram возвращает программу по ID.
func (h *Handler) getProgram(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Store().GetProgram(r.Context(), r.PathValue("id"))
	h.respond(w, http.StatusOK, result, err)
}

// listPrograms возвращает программы университета или backlog-программы.
func (h *Handler) listPrograms(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Store().ListPrograms(r.Context(), r.URL.Query().Get("university_id"), listOptions(r))
	h.respond(w, http.StatusOK, result, err)
}

// updateProgram обновляет программу после проверки роли admin/superuser.
func (h *Handler) updateProgram(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	var input domain.Program
	if !decode(w, r, &input) {
		return
	}
	input.ID = r.PathValue("id")
	result, err := h.service.UpdateProgram(r.Context(), input, actor)
	h.respond(w, http.StatusOK, result, err)
}

// deleteProgram выполняет soft delete программы после проверки роли admin/superuser.
func (h *Handler) deleteProgram(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	err := h.service.Store().DeleteProgram(r.Context(), r.PathValue("id"), actor)
	h.respond(w, http.StatusOK, map[string]bool{"deleted": err == nil}, err)
}

// changeProgramStatus меняет status программы после проверки роли admin/superuser.
func (h *Handler) changeProgramStatus(w http.ResponseWriter, r *http.Request) {
	actor, status, ok := h.statusInput(w, r)
	if !ok {
		return
	}
	result, err := h.service.Store().ChangeProgramStatus(r.Context(), r.PathValue("id"), status, actor)
	h.respond(w, http.StatusOK, result, err)
}

// createCourse создаёт курс после проверки роли admin/superuser.
func (h *Handler) createCourse(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	var input domain.Course
	if !decode(w, r, &input) {
		return
	}
	result, err := h.service.CreateCourse(r.Context(), input, actor)
	h.respond(w, http.StatusCreated, result, err)
}

// getCourse возвращает курс по ID.
func (h *Handler) getCourse(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Store().GetCourse(r.Context(), r.PathValue("id"))
	h.respond(w, http.StatusOK, result, err)
}

// listCourses возвращает курсы программы или backlog-курсы.
func (h *Handler) listCourses(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Store().ListCourses(r.Context(), r.URL.Query().Get("program_id"), listOptions(r))
	h.respond(w, http.StatusOK, result, err)
}

// updateCourse обновляет курс после проверки роли admin/superuser.
func (h *Handler) updateCourse(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	var input domain.Course
	if !decode(w, r, &input) {
		return
	}
	input.ID = r.PathValue("id")
	result, err := h.service.UpdateCourse(r.Context(), input, actor)
	h.respond(w, http.StatusOK, result, err)
}

// deleteCourse выполняет soft delete курса после проверки роли admin/superuser.
func (h *Handler) deleteCourse(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	err := h.service.Store().DeleteCourse(r.Context(), r.PathValue("id"), actor)
	h.respond(w, http.StatusOK, map[string]bool{"deleted": err == nil}, err)
}

// changeCourseStatus меняет status курса после проверки роли admin/superuser.
func (h *Handler) changeCourseStatus(w http.ResponseWriter, r *http.Request) {
	actor, status, ok := h.statusInput(w, r)
	if !ok {
		return
	}
	result, err := h.service.Store().ChangeCourseStatus(r.Context(), r.PathValue("id"), status, actor)
	h.respond(w, http.StatusOK, result, err)
}

// createTopic создаёт тему после проверки роли admin/superuser.
func (h *Handler) createTopic(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	var input domain.Topic
	if !decode(w, r, &input) {
		return
	}
	result, err := h.service.CreateTopic(r.Context(), input, actor)
	h.respond(w, http.StatusCreated, result, err)
}

// getTopic возвращает тему по ID.
func (h *Handler) getTopic(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Store().GetTopic(r.Context(), r.PathValue("id"))
	h.respond(w, http.StatusOK, result, err)
}

// listTopics возвращает темы курса или backlog-темы.
func (h *Handler) listTopics(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Store().ListTopics(r.Context(), r.URL.Query().Get("course_id"), listOptions(r))
	h.respond(w, http.StatusOK, result, err)
}

// updateTopic обновляет тему после проверки роли admin/superuser.
func (h *Handler) updateTopic(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	var input domain.Topic
	if !decode(w, r, &input) {
		return
	}
	input.ID = r.PathValue("id")
	result, err := h.service.UpdateTopic(r.Context(), input, actor)
	h.respond(w, http.StatusOK, result, err)
}

// deleteTopic выполняет soft delete темы после проверки роли admin/superuser.
func (h *Handler) deleteTopic(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	err := h.service.Store().DeleteTopic(r.Context(), r.PathValue("id"), actor)
	h.respond(w, http.StatusOK, map[string]bool{"deleted": err == nil}, err)
}

// changeTopicStatus меняет status темы после проверки роли admin/superuser.
func (h *Handler) changeTopicStatus(w http.ResponseWriter, r *http.Request) {
	actor, status, ok := h.statusInput(w, r)
	if !ok {
		return
	}
	result, err := h.service.Store().ChangeTopicStatus(r.Context(), r.PathValue("id"), status, actor)
	h.respond(w, http.StatusOK, result, err)
}

// topicTree возвращает дерево тем конкретного курса.
func (h *Handler) topicTree(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.TopicTree(r.Context(), r.PathValue("id"))
	h.respond(w, http.StatusOK, result, err)
}

// topicTreeByQuery возвращает дерево тем по query course_id или общий backlog-лес.
func (h *Handler) topicTreeByQuery(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.TopicTree(r.Context(), r.URL.Query().Get("course_id"))
	h.respond(w, http.StatusOK, result, err)
}

// addPrerequisite добавляет prerequisite после проверки роли admin/superuser.
func (h *Handler) addPrerequisite(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	var input struct {
		PrerequisiteTopicID string `json:"prerequisite_topic_id"`
	}
	if !decode(w, r, &input) {
		return
	}
	result, err := h.service.AddPrerequisite(r.Context(), domain.TopicPrerequisite{TopicID: r.PathValue("id"), PrerequisiteTopicID: input.PrerequisiteTopicID}, actor)
	h.respond(w, http.StatusCreated, result, err)
}

// removePrerequisite удаляет prerequisite после проверки роли admin/superuser.
func (h *Handler) removePrerequisite(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	err := h.service.Store().RemovePrerequisite(r.Context(), r.PathValue("id"), r.PathValue("prerequisite_id"), actor)
	h.respond(w, http.StatusOK, map[string]bool{"deleted": err == nil}, err)
}

// listPrerequisites возвращает prerequisites темы.
func (h *Handler) listPrerequisites(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Store().ListPrerequisites(r.Context(), r.PathValue("id"))
	h.respond(w, http.StatusOK, result, err)
}

// validateEntity проверяет существование одной catalog-сущности.
func (h *Handler) validateEntity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var err error
	switch r.PathValue("entity") {
	case "universities":
		_, err = h.service.Store().GetUniversity(r.Context(), id)
	case "programs":
		_, err = h.service.Store().GetProgram(r.Context(), id)
	case "courses":
		_, err = h.service.Store().GetCourse(r.Context(), id)
	case "topics":
		_, err = h.service.Store().GetTopic(r.Context(), id)
	default:
		err = apperror.New(apperror.ValidationError, "неизвестный тип сущности", http.StatusBadRequest)
	}
	h.respond(w, http.StatusOK, domain.ValidationResult{Valid: err == nil}, err)
}

// validateBinding проверяет полную или частичную связку catalog-сущностей.
func (h *Handler) validateBinding(w http.ResponseWriter, r *http.Request) {
	var input domain.Binding
	if !decode(w, r, &input) {
		return
	}
	result, err := h.service.ValidateBinding(r.Context(), input)
	h.respond(w, http.StatusOK, result, err)
}

// admin извлекает actor из внутренних headers и проверяет роль admin/superuser.
func (h *Handler) admin(w http.ResponseWriter, r *http.Request) (string, bool) {
	actorID := r.Header.Get("X-User-ID")
	roles := strings.Split(r.Header.Get("X-User-Roles"), ",")
	isAdmin := false
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if strings.EqualFold(role, "admin") || strings.EqualFold(role, "superuser") {
			isAdmin = true
			break
		}
	}
	if !isAdmin || uuid.Validate(actorID) != nil {
		// actorID обязан быть UUID, потому что Entities пишет его в audit/outbox поля PostgreSQL.
		h.writeError(w, apperror.New(apperror.PermissionDenied, "операция доступна только администратору", http.StatusForbidden))
		return "", false
	}

	return actorID, true
}

// statusInput читает status mutation input и проверяет допустимое значение enum.
func (h *Handler) statusInput(w http.ResponseWriter, r *http.Request) (string, domain.Status, bool) {
	actor, ok := h.admin(w, r)
	if !ok {
		return "", "", false
	}
	var input struct {
		Status domain.Status `json:"status"`
	}
	if !decode(w, r, &input) {
		return "", "", false
	}
	switch input.Status {
	case domain.StatusDraft, domain.StatusActive, domain.StatusHidden, domain.StatusArchived:
		return actor, input.Status, true
	default:
		h.writeError(w, apperror.New(apperror.ValidationError, "недопустимый status", http.StatusUnprocessableEntity))
		return "", "", false
	}
}

// respond пишет успешный JSON response или делегирует ошибку writeError.
func (h *Handler) respond(w http.ResponseWriter, status int, payload any, err error) {
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, status, payload)
}

// writeError преобразует service/repository error в публичный JSON response.
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		writeJSON(w, appErr.Status, appErr)
		return
	}
	h.logger.Error("ошибка обработки запроса", "error", err)
	writeJSON(w, http.StatusInternalServerError, apperror.New(apperror.InternalError, "внутренняя ошибка", http.StatusInternalServerError))
}

// listOptions читает query filters и нормализует pagination для списков.
func listOptions(r *http.Request) domain.ListOptions {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	return service.NormalizeList(domain.ListOptions{
		Search: r.URL.Query().Get("search"),
		Status: r.URL.Query().Get("status"),
		Limit:  limit,
		Offset: offset,
	})
}

// decode читает JSON body с ограничением размера и пишет validation error при ошибке.
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, apperror.New(apperror.ValidationError, "некорректный JSON", http.StatusBadRequest))
		return false
	}

	return true
}

// writeJSON пишет payload как JSON с указанным HTTP status.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
