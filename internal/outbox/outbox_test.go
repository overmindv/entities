package outbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/overmindv/parker"
)

func TestRetryDelayIsBounded(t *testing.T) {
	t.Parallel()

	for tries, expected := range map[int]time.Duration{
		-1: time.Second, 0: time.Second, 1: 2 * time.Second, 6: 64 * time.Second, 50: 128 * time.Second,
	} {
		if got := retryDelay(tries); got != expected {
			t.Fatal("unexpected retry delay:", tries, got, expected)
		}
	}
}

func TestOutboxMetricsDoNotExposeEventData(t *testing.T) {
	t.Parallel()

	store := NewCatalogOutbox(nil, "private.fixture")
	handler := NewMonitor(store).Handler()
	if err := store.MarkClaimFailed(t.Context(), parker.OutboxRecord{ID: "fixture-event"}); !errors.Is(err, ErrClaimLost) {
		t.Fatal("expected missing claim rejection:", err)
	}
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, httptest.NewRequestWithContext(context.Background(), http.MethodGet, MetricsPath, nil))
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), "entities_outbox_publish_failures_total 1") ||
		strings.Contains(result.Body.String(), "fixture-event") || strings.Contains(result.Body.String(), "private.fixture") {
		t.Fatal("unexpected metric response")
	}
}
