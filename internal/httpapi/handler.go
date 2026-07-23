package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/overmindv/ironhide/internal/apperror"
	"github.com/overmindv/ironhide/internal/domain"
	"github.com/overmindv/ironhide/internal/service"
)

// Handler обслуживает внутренний HTTP JSON API Ironhide.
type Handler struct {
	service *service.Service
	logger  *slog.Logger
}

// New собирает router Ironhide с health, read и write endpoints.
func New(catalog *service.Service, logger *slog.Logger, requestLoggers ...*slog.Logger) http.Handler {
	handler := &Handler{
		service: catalog,
		logger:  logger,
	}
	requestLogger := logger
	if len(requestLoggers) > 0 && requestLoggers[0] != nil {
		requestLogger = requestLoggers[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handler.health)
	mux.HandleFunc("GET /ready", handler.ready)
	mux.HandleFunc("GET /v1/universities", handler.listUniversities)
	mux.HandleFunc("POST /v1/universities", handler.createUniversity)
	mux.HandleFunc("GET /v1/universities/{id}", handler.getUniversity)
	mux.HandleFunc("PUT /v1/universities/{id}", handler.updateUniversity)
	mux.HandleFunc("DELETE /v1/universities/{id}", handler.deleteUniversity)
	mux.HandleFunc("PATCH /v1/universities/{id}/status", handler.changeUniversityStatus)
	mux.HandleFunc("GET /v1/programs", handler.listPrograms)
	mux.HandleFunc("POST /v1/programs", handler.createProgram)
	mux.HandleFunc("GET /v1/programs/{id}", handler.getProgram)
	mux.HandleFunc("PUT /v1/programs/{id}", handler.updateProgram)
	mux.HandleFunc("DELETE /v1/programs/{id}", handler.deleteProgram)
	mux.HandleFunc("PATCH /v1/programs/{id}/status", handler.changeProgramStatus)
	mux.HandleFunc("GET /v1/courses", handler.listCourses)
	mux.HandleFunc("POST /v1/courses", handler.createCourse)
	mux.HandleFunc("GET /v1/courses/{id}", handler.getCourse)
	mux.HandleFunc("PUT /v1/courses/{id}", handler.updateCourse)
	mux.HandleFunc("DELETE /v1/courses/{id}", handler.deleteCourse)
	mux.HandleFunc("PATCH /v1/courses/{id}/status", handler.changeCourseStatus)
	mux.HandleFunc("GET /v1/topics", handler.listTopics)
	mux.HandleFunc("POST /v1/topics", handler.createTopic)
	mux.HandleFunc("GET /v1/topics/{id}", handler.getTopic)
	mux.HandleFunc("PUT /v1/topics/{id}", handler.updateTopic)
	mux.HandleFunc("DELETE /v1/topics/{id}", handler.deleteTopic)
	mux.HandleFunc("PATCH /v1/topics/{id}/status", handler.changeTopicStatus)
	mux.HandleFunc("GET /v1/topic-tree", handler.topicTreeByQuery)
	mux.HandleFunc("GET /v1/courses/{id}/topic-tree", handler.topicTree)
	mux.HandleFunc("GET /v1/topics/{id}/prerequisites", handler.listPrerequisites)
	mux.HandleFunc("POST /v1/topics/{id}/prerequisites", handler.addPrerequisite)
	mux.HandleFunc("DELETE /v1/topics/{id}/prerequisites/{prerequisite_id}", handler.removePrerequisite)
	mux.HandleFunc("GET /v1/validate/{entity}/{id}", handler.validateEntity)
	mux.HandleFunc("POST /v1/validate/binding", handler.validateBinding)

	return requestIDMiddleware(loggingMiddleware(requestLogger, recoverMiddleware(logger, mux), "/health", "/ready"))
}

// health возвращает liveness без проверки внешних зависимостей.
func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ready проверяет доступность PostgreSQL для readiness.
func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Store().Ping(r.Context()); err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
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
		// actorID обязан быть UUID, потому что Ironhide пишет его в audit/outbox поля PostgreSQL.
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

type requestIDKey struct{}

const requestIDHeader = "X-Request-ID"

// requestIDMiddleware переносит входящий request_id в context и response header.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get(requestIDHeader))
		if requestID == "" || len(requestID) > 128 {
			requestID = uuid.NewString()
		}
		w.Header().Set(requestIDHeader, requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID)))
	})
}

// requestID достаёт request_id из context для логов Ironhide.
func requestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)

	return value
}

// responseRecorder сохраняет status и размер ответа для request logging.
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

// WriteHeader запоминает HTTP status перед записью в исходный ResponseWriter.
func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Write запоминает размер body, чтобы запись request log показывала объём ответа.
func (r *responseRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	written, err := r.ResponseWriter.Write(body)
	r.bytes += written

	return written, err
}

// loggingMiddleware пишет отдельные HTTP request logs и пропускает служебные health endpoints.
func loggingMiddleware(log *slog.Logger, next http.Handler, ignoredPaths ...string) http.Handler {
	ignored := make(map[string]struct{}, len(ignoredPaths))
	for _, path := range ignoredPaths {
		ignored[path] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if log == nil {
			return
		}
		if _, skip := ignored[r.URL.Path]; skip {
			return
		}
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		log.InfoContext(r.Context(), "ironhide http request",
			"request_id", requestID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"response_bytes", recorder.bytes,
			"duration", time.Since(started),
		)
	})
}

// recoverMiddleware ловит unexpected panic и возвращает обезличенную внутреннюю ошибку.
func recoverMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("паника при обработке запроса", "request_id", requestID(r.Context()), "error", recovered)
				writeJSON(w, http.StatusInternalServerError, apperror.New(apperror.InternalError, "внутренняя ошибка", http.StatusInternalServerError))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
