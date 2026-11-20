package repository

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/overmindv/entities/internal/domain"
	"github.com/overmindv/entities/internal/httpapi"
	"github.com/overmindv/entities/internal/service"
)

func TestCatalogVisibilityPublicAndAdmin(t *testing.T) {
	ctx := t.Context()
	admin := New(testCatalogPool(t, ""))
	actor, suffix := uuid.NewString(), uuid.NewString()
	university, err := admin.CreateUniversity(ctx, domain.University{
		Name:      "University " + suffix,
		Status:    domain.StatusActive,
		CreatedBy: actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	program, err := admin.CreateProgram(ctx, domain.Program{
		UniversityID: &university.ID,
		Name:         "Program " + suffix,
		DegreeLevel:  "other",
		Status:       domain.StatusActive,
		CreatedBy:    actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	course, err := admin.CreateCourse(ctx, domain.Course{
		ProgramID: &program.ID,
		Name:      "Course " + suffix,
		Slug:      suffix,
		Status:    domain.StatusActive,
		CreatedBy: actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	makeTopic := func(name string, parent *string, status domain.Status) domain.Topic {
		t.Helper()
		topic, err := admin.CreateTopic(ctx, domain.Topic{
			CourseID:      &course.ID,
			ParentTopicID: parent,
			Title:         suffix + " " + name,
			Slug:          name + "-" + suffix,
			Difficulty:    "basic",
			Status:        status,
			CreatedBy:     actor,
		})
		if err != nil {
			t.Fatal(err)
		}

		return topic
	}
	root := makeTopic("root", nil, domain.StatusActive)
	middle := makeTopic("middle", &root.ID, domain.StatusActive)
	child := makeTopic("child", &middle.ID, domain.StatusActive)
	draft := makeTopic("draft", nil, domain.StatusDraft)
	for _, id := range []string{root.ID, draft.ID} {
		if _, err := admin.AddPrerequisite(ctx, domain.TopicPrerequisite{
			TopicID:             child.ID,
			PrerequisiteTopicID: id,
		}, actor); err != nil {
			t.Fatal(err)
		}
	}
	const topicURL = "/v1/topics/"
	mux := http.NewServeMux()
	token := strings.Repeat("t", 32)
	if err := httpapi.Register(mux, service.New(admin), service.New(admin.PublicReads()), token, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	do := func(path string, headers func(http.Header)) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if headers != nil {
			headers(req.Header)
		}
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Header().Get("Cache-Control") != "no-store" {
			t.Error("catalog response may be cached", path)
		}

		return res.Code, res.Body.String()
	}
	claims := func(h http.Header) {
		h.Set("X-User-ID", actor)
		h.Set("X-User-Roles", "admin")
	}
	trusted := func(h http.Header) {
		claims(h)
		h.Set("Authorization", "Bearer "+token)
	}
	assertStatus := func(path string, headers func(http.Header), want int) {
		t.Helper()
		code, body := do(path, headers)
		if code != want {
			t.Error(path, code, "want", want, body)
		}
	}
	assertStatus(topicURL+child.ID, nil, http.StatusOK)
	for _, headers := range []func(http.Header){nil, claims, func(h http.Header) {
		trusted(h)
		h.Set("X-User-Roles", "student")
	}, func(h http.Header) {
		trusted(h)
		h.Add("Authorization", "Bearer "+token)
	}, func(h http.Header) {
		trusted(h)
		h.Add("X-User-Roles", "student")
	}} {
		assertStatus(topicURL+draft.ID, headers, http.StatusNotFound)
	}
	assertStatus(topicURL+draft.ID, trusted, http.StatusOK)
	code, prerequisites := do(topicURL+child.ID+"/prerequisites", nil)
	if code != http.StatusOK || !strings.Contains(prerequisites, root.ID) || strings.Contains(prerequisites, draft.ID) {
		t.Error("public prerequisites expose draft or omit active target", code, prerequisites)
	}
	if _, err := admin.ChangeTopicStatus(ctx, root.ID, domain.StatusHidden, actor); err != nil {
		t.Fatal(err)
	}
	assertStatus(topicURL+child.ID, nil, http.StatusNotFound)
	assertStatus(topicURL+child.ID, trusted, http.StatusOK)
	assertStatus("/v1/validate/topics/"+child.ID, nil, http.StatusNotFound)
	if found, err := admin.PublicReads().ValidateBinding(ctx, domain.Binding{TopicID: &child.ID}); err != nil || found {
		t.Error("hidden binding confirmed", found, err)
	}
	code, results := do("/v1/topics?course_id="+course.ID+"&search="+suffix+"&status=active&limit=1", nil)
	if code != http.StatusOK || strings.TrimSpace(results) != "[]" {
		t.Error("hidden topic in search", code, results)
	}
	code, tree := do("/v1/courses/"+course.ID+"/topic-tree", nil)
	if code != http.StatusOK || strings.TrimSpace(tree) != "[]" {
		t.Error("hidden descendants in tree", code, tree)
	}
	if _, err := admin.ChangeTopicStatus(ctx, root.ID, domain.StatusActive, actor); err != nil {
		t.Fatal(err)
	}
	backlog, err := admin.CreateTopic(ctx, domain.Topic{
		Title:      "Backlog",
		Slug:       suffix,
		Difficulty: "basic",
		Status:     domain.StatusActive,
		CreatedBy:  actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(topicURL+backlog.ID, nil, http.StatusOK)
	for _, parent := range []string{child.ID, backlog.ID} {
		if _, err := admin.pool.Exec(ctx, "UPDATE topics SET parent_topic_id=$1 WHERE id=$2", parent, root.ID); err != nil {
			t.Fatal(err)
		}
		assertStatus(topicURL+child.ID, nil, http.StatusNotFound)
	}
	if _, err := admin.pool.Exec(ctx, "UPDATE topics SET parent_topic_id=NULL WHERE id=$1", root.ID); err != nil {
		t.Fatal(err)
	}
	assertStatus(topicURL+child.ID, nil, http.StatusOK)
	if _, err := admin.ChangeCourseStatus(ctx, course.ID, domain.StatusArchived, actor); err != nil {
		t.Fatal(err)
	}
	assertStatus("/v1/courses/"+course.ID, nil, http.StatusNotFound)
	assertStatus("/v1/courses/"+course.ID+"/topic-tree", nil, http.StatusNotFound)
	assertStatus(topicURL+child.ID, nil, http.StatusNotFound)
	assertStatus(topicURL+child.ID, trusted, http.StatusOK)
	if _, err := admin.ChangeCourseStatus(ctx, course.ID, domain.StatusActive, actor); err != nil {
		t.Fatal(err)
	}
	if err := admin.DeleteUniversity(ctx, university.ID, actor); err != nil {
		t.Fatal(err)
	}
	assertStatus("/v1/programs/"+program.ID, nil, http.StatusNotFound)
	assertStatus(topicURL+child.ID, nil, http.StatusNotFound)
	assertStatus(topicURL+child.ID, trusted, http.StatusOK)
}

func BenchmarkCatalogVisibility(b *testing.B) {
	ctx := b.Context()
	pool := testCatalogPool(b, "")
	suffix := uuid.NewString()
	var topicID, courseID string
	if err := pool.QueryRow(ctx, `
        WITH new_courses AS (
            INSERT INTO courses (slug,name,status,created_by,updated_by)
            SELECT $1::text||'-'||n,'benchmark course','active',$1::text::uuid,$1::text::uuid
              FROM generate_series(1,100) n
            RETURNING id
        ), roots AS (
            INSERT INTO topics (course_id,slug,title,status,created_by,updated_by)
            SELECT id,'root','benchmark root','active',$1::text::uuid,$1::text::uuid FROM new_courses
            RETURNING id,course_id
        ), nodes AS (
            INSERT INTO topics (course_id,parent_topic_id,slug,title,order_index,status,created_by,updated_by)
            SELECT r.course_id,r.id,'topic-'||n,'benchmark topic '||n,n,'active',$1::text::uuid,$1::text::uuid
              FROM roots r CROSS JOIN generate_series(1,249) n
            RETURNING id,course_id
        )
        SELECT id,course_id FROM nodes LIMIT 1`, suffix).Scan(&topicID, &courseID); err != nil {
		b.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "ANALYZE topics"); err != nil {
		b.Fatal(err)
	}
	base := New(pool)
	for _, variant := range []struct {
		name  string
		store *Postgres
	}{
		{name: "base", store: base},
		{name: "public", store: base.PublicReads()},
	} {
		b.Run(variant.name+"/detail", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := variant.store.GetTopic(b.Context(), topicID); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(variant.name+"/course_page", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				rows, err := variant.store.ListTopics(b.Context(), courseID, domain.ListOptions{
					Search: "benchmark",
					Limit:  50,
				})
				if len(rows) != 50 || err != nil {
					b.Fatal("incomplete catalog page", len(rows), err)
				}
			}
		})
		b.Run(variant.name+"/catalog_page", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				rows, err := variant.store.ListTopics(b.Context(), "", domain.ListOptions{
					Search: "benchmark",
					Limit:  50,
				})
				if len(rows) != 50 || err != nil {
					b.Fatal("incomplete catalog page", len(rows), err)
				}
			}
		})
	}
}
