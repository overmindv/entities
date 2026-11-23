package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/overmindv/entities/internal/catalogsnapshot"
	"github.com/overmindv/entities/internal/outbox"
)

type feedReplayStub struct {
	req   catalogsnapshot.ChangeRequest
	calls int
	err   error
}

func (s *feedReplayStub) FeedChanges(_ context.Context, req catalogsnapshot.ChangeRequest) (outbox.FeedPage, error) {
	s.req = req
	s.calls++

	return outbox.FeedPage{Watermark: 73, Through: 73, Events: []outbox.FeedEvent{}}, s.err
}

func TestCatalogFeedReplayRoute(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	store := &feedReplayStub{}
	snapshotToken := strings.Repeat("s", 32)
	feedToken := strings.Repeat("f", 32)
	if err := RegisterCatalogSnapshot(mux, &snapshotStub{}, snapshotToken); err != nil {
		t.Fatal(err)
	}
	if err := RegisterCatalogFeedReplay(mux, store, feedToken); err != nil {
		t.Fatal(err)
	}
	request := func(path string, headers ...string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		for _, auth := range headers {
			req.Header.Add("Authorization", auth)
		}
		result := httptest.NewRecorder()
		mux.ServeHTTP(result, req)
		if result.Header().Get("Cache-Control") != expectedNoStore {
			t.Fatal("private catalog response can be cached")
		}

		return result
	}
	for _, headers := range [][]string{nil, {"Bearer " + snapshotToken}, {"Bearer " + feedToken, "Bearer " + feedToken}} {
		if result := request(feedReplayPath, headers...); result.Code != http.StatusForbidden {
			t.Fatal("wrong token could read the replay", result.Code)
		}
	}
	if result := request("/v1/internal/catalog/snapshot", "Bearer "+feedToken); result.Code != http.StatusForbidden {
		t.Fatal("replay token could read the snapshot", result.Code)
	}
	const bearerPrefix = "Bearer "
	for _, query := range []string{"?limit=201", "?after=1&after=2", "?after=-1", "?watermark=%2B2", "?cursor=abc"} {
		if result := request(feedReplayPath+query, bearerPrefix+feedToken); result.Code != http.StatusBadRequest {
			t.Fatal("invalid replay query succeeded", result.Code)
		}
	}
	if store.calls != 0 {
		t.Fatal("invalid request reached the replay store")
	}
	if result := request(feedReplayPath+"?after=72&watermark=73&limit=1", bearerPrefix+feedToken); result.Code != http.StatusOK ||
		!strings.Contains(result.Body.String(), `"events":[]`) || store.req.After != 72 || store.req.Limit != 1 ||
		store.req.Watermark == nil || *store.req.Watermark != 73 {
		t.Fatal("replay parameters were not passed through", result.Code, store.req)
	}
	store.err = outbox.ErrFeedHistoryGap
	if result := request(feedReplayPath, bearerPrefix+feedToken); result.Code != http.StatusConflict ||
		!strings.Contains(result.Body.String(), "CATALOG_HISTORY_GAP") || strings.Contains(result.Body.String(), `"events"`) {
		t.Fatal("history gap was not exposed as a failed export", result.Code)
	}
	store.err = errors.New("private database message")
	if result := request(feedReplayPath, bearerPrefix+feedToken); result.Code != http.StatusServiceUnavailable ||
		strings.Contains(result.Body.String(), "private database") {
		t.Fatal("store error leaked into replay response", result.Code)
	}
}

func TestCatalogFeedReplayDisabledByDefault(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	if err := RegisterCatalogFeedReplay(mux, &feedReplayStub{}, ""); err != nil {
		t.Fatal(err)
	}
	result := httptest.NewRecorder()
	mux.ServeHTTP(result, httptest.NewRequestWithContext(t.Context(), http.MethodGet, feedReplayPath, nil))
	if result.Code != http.StatusNotFound {
		t.Fatal("replay registered without a token")
	}
	if err := RegisterCatalogFeedReplay(mux, &feedReplayStub{}, "too-short"); err == nil ||
		!strings.Contains(err.Error(), feedReplayEnv) {
		t.Fatal("invalid replay token accepted")
	}
}
