package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/overmindv/entities/internal/catalogsnapshot"
)

type snapshotStub struct {
	page   catalogsnapshot.SnapshotRequest
	events catalogsnapshot.ChangeRequest
	calls  int
	err    error
}

const expectedNoStore = "no-store"

func (s *snapshotStub) Snapshot(_ context.Context, request catalogsnapshot.SnapshotRequest) (catalogsnapshot.SnapshotPage, error) {
	s.page = request
	s.calls++

	return catalogsnapshot.SnapshotPage{Watermark: 17, Records: []catalogsnapshot.GraphRow{}}, s.err
}

func (s *snapshotStub) Changes(_ context.Context, request catalogsnapshot.ChangeRequest) (catalogsnapshot.ChangePage, error) {
	s.events = request
	s.calls++

	return catalogsnapshot.ChangePage{Watermark: 19, Through: 19, Changes: []catalogsnapshot.Change{}}, s.err
}

func TestCatalogSnapshotRoutes(t *testing.T) {
	t.Parallel()

	const snapshot = "/v1/internal/catalog/snapshot"
	const changes = "/v1/internal/catalog/changes"
	token := strings.Repeat("q", 32)
	mux := http.NewServeMux()
	store := &snapshotStub{}
	if err := RegisterCatalogSnapshot(mux, store, token); err != nil {
		t.Fatal(err)
	}
	request := func(path string, auth ...string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		for _, value := range auth {
			req.Header.Add("Authorization", value)
		}
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		if recorder.Header().Get("Cache-Control") != expectedNoStore {
			t.Fatal("private catalog reply is cacheable")
		}

		return recorder
	}
	auth := "Bearer " + token
	for _, headers := range [][]string{nil, {"Bearer invalid"}, {auth, auth}, {auth + " "}} {
		if result := request(snapshot, headers...); result.Code != http.StatusForbidden {
			t.Fatal("snapshot allowed an invalid service credential", result.Code)
		}
	}
	for _, path := range []string{
		snapshot + "?limit=0", snapshot + "?limit=201", snapshot + "?limit=200&limit=1",
		snapshot + "?watermark=-1", snapshot + "?watermark=%2B10", snapshot + "?watermark=9223372036854775808",
		snapshot + "?other=anything", snapshot + "?cursor=" + strings.Repeat("a", 512),
		changes, changes + "?after=-1", changes + "?after=0&after=2",
	} {
		if result := request(path, auth); result.Code != http.StatusBadRequest {
			t.Fatal("invalid query succeeded", result.Code)
		}
	}
	if store.calls != 0 {
		t.Fatal("invalid request reached the store", store.calls)
	}
	result := request(snapshot+"?watermark=17&limit=200&cursor=abc", auth)
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"records":[]`) ||
		store.page.Watermark == nil || *store.page.Watermark != 17 || store.page.Cursor != "abc" || store.page.Limit != 200 {
		t.Fatal("snapshot request was not forwarded correctly", result.Code, store.page)
	}
	result = request(changes+"?after=17", auth)
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"through":19`) ||
		store.events.After != 17 || store.events.Watermark != nil || store.events.Limit != catalogsnapshot.DefaultLimit {
		t.Fatal("changes request was not forwarded correctly", result.Code, store.events)
	}
	store.err = catalogsnapshot.ErrInvalidPage
	if result := request(snapshot, auth); result.Code != http.StatusBadRequest {
		t.Fatal("expected invalid cursor response", result.Code)
	}
	store.err = errors.New("private database connection details")
	if result := request(snapshot, auth); result.Code != http.StatusServiceUnavailable || strings.Contains(result.Body.String(), "private") {
		t.Fatal("store failure leaked into response", result.Code)
	}
}

func TestCatalogSnapshotRequiresConfiguredToken(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	if err := RegisterCatalogSnapshot(mux, &snapshotStub{}, ""); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), "GET", "/v1/internal/catalog/snapshot", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatal("snapshot enabled without a token")
	}
	for _, value := range []string{"short", strings.Repeat("x", 512) + "y", strings.Repeat("x", 32) + "\n"} {
		if err := RegisterCatalogSnapshot(mux, &snapshotStub{}, value); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
}
