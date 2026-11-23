package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
	"github.com/overmindv/entities/internal/service"
)

type goalTemplateStoreStub struct {
	service.Store
	version   domain.GoalTemplateVersion
	published int
	listed    int
}

func (s *goalTemplateStoreStub) PublishGoalTemplate(_ context.Context, program, id, _ string, version int64, input domain.GoalTemplateInput) (domain.GoalTemplateVersion, bool, error) {
	s.published++
	s.version = domain.GoalTemplateVersion{ID: id, ProgramID: program, Version: version, GoalTemplateInput: input}

	return s.version, s.published == 1, nil
}

func (s *goalTemplateStoreStub) GoalTemplateVersion(_ context.Context, id string, version int64) (domain.GoalTemplateVersion, error) {
	if s.version.ID != id || s.version.Version != version {
		return domain.GoalTemplateVersion{}, apperror.New(apperror.GoalTemplateNotFound, "not found", http.StatusNotFound)
	}

	return s.version, nil
}

func (s *goalTemplateStoreStub) ListGoalTemplates(_ context.Context, program, after string, limit int) (domain.GoalTemplatePage, error) {
	s.listed++
	if program != s.version.ProgramID || after != uuid.Nil.String() || limit != defaultGoalTemplatePage {
		return domain.GoalTemplatePage{}, goalTemplateBadRequest("unexpected query")
	}

	return domain.GoalTemplatePage{Items: []domain.GoalTemplateVersion{s.version}}, nil
}

func TestGoalTemplateRoutes(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	store := &goalTemplateStoreStub{}
	catalog := service.New(store)
	gateway := strings.Repeat("g", 32)
	reader := strings.Repeat("r", 32)
	if err := Register(mux, catalog, catalog, gateway, nil); err != nil {
		t.Fatal(err)
	}
	if err := RegisterGoalTemplateReads(mux, store, reader); err != nil {
		t.Fatal(err)
	}
	program, template := uuid.NewString(), uuid.NewString()
	publish := "/v1/programs/" + program + "/goal-templates/" + template + "/versions/1"
	read := goalTemplatePath + "/" + template + "/versions/1"
	page := goalTemplatePath + "?program_id=" + program
	body := `{"title":" Goal ","target_topics":[{"topic_id":"` + uuid.NewString() + `","target_score_bps":5000}]}`
	do := func(method, path, token, role, data string, wantStatus int) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(data))
		req.Header.Set("X-User-ID", actorID)
		req.Header.Set("X-User-Roles", role)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != wantStatus || res.Header().Get("Cache-Control") != expectedNoStore {
			t.Fatal(method, path, "returned", res.Code, res.Body.String())
		}

		return res
	}
	for _, token := range []string{"", reader} {
		do(http.MethodPut, publish, token, "admin", body, http.StatusForbidden)
	}
	do(http.MethodPut, publish, gateway, "student", body, http.StatusForbidden)
	do(http.MethodPut, strings.TrimSuffix(publish, "1")+"0", gateway, "admin", body, http.StatusBadRequest)
	for _, invalid := range []string{body + "{}", strings.Repeat(" ", 16<<10) + body, `{"extra":true}`} {
		do(http.MethodPut, publish, gateway, "admin", invalid, http.StatusBadRequest)
	}
	if store.published != 0 {
		t.Fatal("invalid publication reached the store")
	}
	do(http.MethodPut, publish, gateway, "admin", body, http.StatusCreated)
	do(http.MethodPut, publish, gateway, "admin", body, http.StatusOK)
	if store.version.Title != "Goal" {
		t.Fatal("template was not normalized")
	}

	do(http.MethodGet, page, gateway, "admin", "", http.StatusForbidden)
	do(http.MethodGet, page+"&limit=51", reader, "", "", http.StatusBadRequest)
	do(http.MethodGet, page+"&program_id="+program, reader, "", "", http.StatusBadRequest)
	do(http.MethodGet, page+"&after=", reader, "", "", http.StatusBadRequest)
	if store.listed != 0 {
		t.Fatal("invalid list reached the store")
	}
	do(http.MethodGet, page, reader, "", "", http.StatusOK)
	do(http.MethodGet, read, reader, "", "", http.StatusOK)
	do(http.MethodGet, read+"1", reader, "", "", http.StatusNotFound)
}
