package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
)

const (
	goalTemplatePath        = "/v1/internal/catalog/goal-templates"
	goalTemplateTimeout     = 5 * time.Second
	maxGoalTemplatePage     = 50
	defaultGoalTemplatePage = 25
)

type GoalTemplateReader interface {
	GoalTemplateVersion(context.Context, string, int64) (domain.GoalTemplateVersion, error)
	ListGoalTemplates(context.Context, string, string, int) (domain.GoalTemplatePage, error)
}

func (h *Handler) publishGoalTemplate(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	version, err := goalTemplateVersionParam(r)
	if err != nil {
		h.writeError(w, err)

		return
	}
	var input domain.GoalTemplateInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(&input); decodeErr != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
		h.writeError(w, goalTemplateBadRequest("некорректный JSON шаблона цели"))

		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), goalTemplateTimeout)
	defer cancel()
	result, created, err := h.service.PublishGoalTemplate(ctx, r.PathValue("id"), r.PathValue("template_id"), actor, version, input)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	h.respond(w, status, result, err)
}

func RegisterGoalTemplateReads(router Router, store GoalTemplateReader, token string) error {
	register, err := internalCatalogRoutes(router, token)
	if err != nil || register == nil {
		return err
	}
	register("GET "+goalTemplatePath, func(w http.ResponseWriter, r *http.Request) {
		program, after, limit, queryErr := goalTemplatePageParams(r)
		if queryErr != nil {
			writeGoalTemplateRead(w, nil, queryErr)

			return
		}
		result, readErr := store.ListGoalTemplates(r.Context(), program, after, limit)
		writeGoalTemplateRead(w, result, readErr)
	})
	register("GET "+goalTemplatePath+"/{template_id}/versions/{version}", func(w http.ResponseWriter, r *http.Request) {
		version, versionErr := goalTemplateVersionParam(r)
		if versionErr != nil {
			writeGoalTemplateRead(w, nil, versionErr)

			return
		}
		id, ok := readGoalTemplateUUID(r.PathValue("template_id"))
		if !ok || r.URL.RawQuery != "" {
			writeGoalTemplateRead(w, nil, goalTemplateBadRequest("некорректный запрос версии шаблона"))

			return
		}
		result, readErr := store.GoalTemplateVersion(r.Context(), id, version)
		writeGoalTemplateRead(w, result, readErr)
	})

	return nil
}

func goalTemplateVersionParam(r *http.Request) (int64, error) {
	version, err := parseRevision(r.PathValue("version"))
	if err != nil || version < 1 {
		return 0, goalTemplateBadRequest("некорректный номер версии шаблона")
	}

	return version, nil
}

func goalTemplatePageParams(r *http.Request) (program, after string, limit int, err error) {
	badRequest := goalTemplateBadRequest("некорректные параметры страницы шаблонов")
	if len(r.URL.RawQuery) > 512 {
		return "", "", 0, badRequest
	}
	values, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil {
		return "", "", 0, badRequest
	}
	for key, value := range values {
		if len(value) != 1 || (key != "program_id" && key != "after" && key != limitParam) {
			return "", "", 0, badRequest
		}
	}
	program, ok := readGoalTemplateUUID(values.Get("program_id"))
	if !ok {
		return "", "", 0, badRequest
	}
	after = uuid.Nil.String()
	if raw, exists := values["after"]; exists {
		after, ok = readGoalTemplateUUID(raw[0])
		if !ok {
			return "", "", 0, badRequest
		}
	}
	limit = defaultGoalTemplatePage
	if raw, exists := values[limitParam]; exists {
		parsed, limitErr := parseRevision(raw[0])
		if limitErr != nil || parsed < 1 || parsed > maxGoalTemplatePage {
			return "", "", 0, badRequest
		}
		limit = int(parsed)
	}

	return program, after, limit, nil
}

func readGoalTemplateUUID(raw string) (string, bool) {
	id, err := uuid.Parse(raw)

	return id.String(), len(raw) == 36 && err == nil && id != uuid.Nil
}

func goalTemplateBadRequest(message string) *apperror.Error {
	return apperror.New(apperror.ValidationError, message, http.StatusBadRequest)
}

func writeGoalTemplateRead(w http.ResponseWriter, data any, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, data)

		return
	}
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		writeJSON(w, appErr.Status, appErr)

		return
	}
	writeJSON(w, http.StatusServiceUnavailable, apperror.New("SERVICE_UNAVAILABLE", "каталог временно недоступен", http.StatusServiceUnavailable))
}
