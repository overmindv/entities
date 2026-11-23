package repository

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
	"github.com/overmindv/entities/internal/logobindings"
	"github.com/overmindv/entities/internal/media"
	"github.com/overmindv/entities/internal/service"
	"github.com/samber/lo"
)

//nolint:paralleltest // Проверяет локальные Media и Entities с отдельными fixture-БД.
func TestLogoBindingWithMedia(t *testing.T) {
	address := os.Getenv("ENTITIES_TEST_LOGO_MEDIA_URL")
	if address == "" {
		t.Skip("run infra/scripts/test-logo-bindings.sh")
	}
	origin, parseErr := url.Parse(address)
	if parseErr != nil || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" {
		t.Fatal("expected an isolated loopback Media fixture")
	}
	store := New(testCatalogPool(t, ""))
	token := os.Getenv("ENTITIES_TEST_LOGO_MEDIA_TOKEN")
	direct, err := media.NewClient(address, token)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(unavailable.Close)
	offline, err := media.NewClient(unavailable.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	catalog := service.NewWithLogoChecker(store, direct)
	ctx, stop := context.WithTimeout(t.Context(), 30*time.Second)
	backend := &firstLogoWriteUnavailable{Client: direct, offline: offline}
	worker := logobindings.NewWorker(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)))
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Run(ctx) }()
	defer func() {
		stop()
		if runErr := <-stopped; runErr != nil {
			t.Error(runErr)
		}
	}()

	actor, file := os.Getenv("ENTITIES_TEST_LOGO_OWNER"), os.Getenv("ENTITIES_TEST_LOGO_FILE")
	input := domain.University{
		Name:       "Media logo fixture " + uuid.NewString(),
		LogoFileID: &file,
	}
	_, err = catalog.CreateUniversity(ctx, input, uuid.NewString())
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code != apperror.UniversityLogoNotReady {
		t.Fatal("foreign logo reached the catalog", err)
	}
	item, err := catalog.CreateUniversity(ctx, input, actor)
	if err != nil {
		t.Fatal(err)
	}
	waitForLogo(t, ctx, direct, item.ID, 1, &file)
	if backend.attempts.Load() < 2 {
		t.Fatal("worker did not retry the failed delivery")
	}

	if err := direct.ReplaceLogo(ctx, item.ID, actor, nil, 7); err != nil {
		t.Fatal("could not detach logo in the Media fixture", err)
	}
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	for {
		tag, err := store.pool.Exec(ctx, `
			UPDATE university_logo_intents SET next_attempt_at = statement_timestamp()
			WHERE id = $1 AND version = 1 AND applied_at IS NOT NULL AND claim_token IS NULL`, item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if tag.RowsAffected() == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("initial delivery was not acknowledged")
		case <-ticker.C:
		}
	}
	waitForLogo(t, ctx, direct, item.ID, 8, &file)
	if err := store.DeleteUniversity(ctx, item.ID, actor); err != nil {
		t.Fatal(err)
	}
	waitForLogo(t, ctx, direct, item.ID, 9, nil)
}

type firstLogoWriteUnavailable struct {
	*media.Client
	offline  *media.Client
	attempts atomic.Int32
}

func (m *firstLogoWriteUnavailable) ReplaceLogo(ctx context.Context, id, actor string, fileID *string, version int64) error {
	if m.attempts.Add(1) == 1 {
		return m.offline.ReplaceLogo(ctx, id, actor, fileID, version)
	}

	return m.Client.ReplaceLogo(ctx, id, actor, fileID, version)
}

func waitForLogo(t *testing.T, ctx context.Context, client *media.Client, id string, version int64, expected *string) {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := client.LookupLogo(ctx, id)
		if err == nil && state.Version == version && lo.FromPtr(state.FileID) == lo.FromPtr(expected) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("logo did not reach the expected binding state", state, err)
		case <-ticker.C:
		}
	}
}
