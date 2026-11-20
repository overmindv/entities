package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/overmindv/entities/internal/domain"
	"github.com/overmindv/entities/internal/service"
)

const actorID = "11111111-1111-1111-1111-111111111111"

// catalogCreateStore сохраняет write inputs из HTTP handler для проверки actor и optional UUID.
type catalogCreateStore struct {
	service.Store
	university domain.University
	course     domain.Course
}

// CreateUniversity возвращает университет, как если бы его создал PostgreSQL.
func (s *catalogCreateStore) CreateUniversity(_ context.Context, item domain.University) (domain.University, error) {
	s.university = item
	item.ID = "33333333-3333-3333-3333-333333333333"

	return item, nil
}

// CreateCourse возвращает курс, как если бы его создал PostgreSQL.
func (s *catalogCreateStore) CreateCourse(_ context.Context, item domain.Course) (domain.Course, error) {
	s.course = item
	item.ID = "44444444-4444-4444-4444-444444444444"

	return item, nil
}

// TestAdminAllowsAdminAndSuperuserRoles проверяет роли, которым разрешены write-операции каталога.
func TestAdminAllowsAdminAndSuperuserRoles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		roles string
		want  bool
	}{
		{
			name:  "admin",
			roles: "admin",
			want:  true,
		},
		{
			name:  "superuser",
			roles: "superuser",
			want:  true,
		},
		{
			name:  "student",
			roles: "student",
			want:  false,
		},
	}

	token := strings.Repeat("a", 32)
	expected := sha256.Sum256([]byte(token))
	handler := &Handler{gatewayToken: &expected, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequest(http.MethodPost, "/v1/universities", nil)
			request.Header.Set("X-User-ID", actorID)
			request.Header.Set("X-User-Roles", test.roles)
			request.Header.Set("Authorization", "Bearer "+token)
			recorder := httptest.NewRecorder()

			actor, ok := handler.admin(recorder, request)
			if ok != test.want {
				t.Fatalf("admin() ok = %v, want %v", ok, test.want)
			}
			if ok && actor != actorID {
				t.Fatalf("неожиданный actor: %q", actor)
			}
		})
	}
}

// TestAdminRejectsNonUUIDActor проверяет, что audit actor обязан быть UUID для PostgreSQL.
func TestAdminRejectsNonUUIDActor(t *testing.T) {
	t.Parallel()

	token := strings.Repeat("a", 32)
	expected := sha256.Sum256([]byte(token))
	handler := &Handler{gatewayToken: &expected, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodPost, "/v1/universities", nil)
	request.Header.Set("X-User-ID", "admin-id")
	request.Header.Set("X-User-Roles", "admin")
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()

	if _, ok := handler.admin(recorder, request); ok {
		t.Fatal("admin() должен отклонять non-UUID actor")
	}
}

// TestCreateEndpointsAcceptAdminAndEmptyOptionalUUIDs проверяет HTTP create endpoints с admin actor.
func TestCreateEndpointsAcceptAdminAndEmptyOptionalUUIDs(t *testing.T) {
	t.Parallel()

	store := &catalogCreateStore{}
	mux := http.NewServeMux()
	token := strings.Repeat("a", 32)
	if err := Register(mux, service.New(store), service.New(store), token, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	handler := mux

	university := postCatalogJSON(t, handler, "/v1/universities", map[string]any{"name": "University", "logo_file_id": ""})
	if university.Code != http.StatusCreated {
		t.Fatalf("create university status = %d body = %s", university.Code, university.Body.String())
	}
	if store.university.LogoFileID != nil || store.university.CreatedBy != actorID {
		t.Fatalf("unexpected university input: %+v", store.university)
	}

	course := postCatalogJSON(t, handler, "/v1/courses", map[string]any{"name": "Course", "program_id": ""})
	if course.Code != http.StatusCreated {
		t.Fatalf("create course status = %d body = %s", course.Code, course.Body.String())
	}
	if store.course.ProgramID != nil || store.course.CreatedBy != actorID {
		t.Fatalf("unexpected course input: %+v", store.course)
	}
}

// postCatalogJSON отправляет write-запрос в HTTP handler Entities с admin headers.
func postCatalogJSON(t *testing.T, handler http.Handler, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-User-ID", "11111111-1111-1111-1111-111111111111")
	request.Header.Set("X-User-Roles", "admin")
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	return recorder
}
