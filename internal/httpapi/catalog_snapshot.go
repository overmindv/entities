package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/catalogsnapshot"
)

type CatalogSnapshotStore interface {
	Snapshot(context.Context, catalogsnapshot.SnapshotRequest) (catalogsnapshot.SnapshotPage, error)
	Changes(context.Context, catalogsnapshot.ChangeRequest) (catalogsnapshot.ChangePage, error)
}

func RegisterCatalogSnapshot(router Router, store CatalogSnapshotStore, token string) error {
	if token == "" {
		return nil
	}
	if len(token) < 32 || len(token) > 512 || strings.ContainsFunc(token, func(r rune) bool { return r < 0x21 || r > 0x7e }) {
		return errors.New("ENTITIES_SNAPSHOT_TOKEN must contain 32-512 printable ASCII characters without spaces")
	}
	expected := sha256.Sum256([]byte(token))
	register := func(pattern string, handler http.HandlerFunc) {
		endpoint := http.TimeoutHandler(handler, 5*time.Second, `{"code":"SERVICE_UNAVAILABLE","message":"Каталог временно недоступен"}`)
		router.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			values := r.Header.Values("Authorization")
			if len(values) != 1 || !snapshotAuthorized(values[0], expected) {
				writeJSON(w, http.StatusForbidden, apperror.New(apperror.PermissionDenied, "доступ запрещен", http.StatusForbidden))
				return
			}
			endpoint.ServeHTTP(w, r)
		})
	}
	register("GET /v1/internal/catalog/snapshot", func(w http.ResponseWriter, r *http.Request) {
		values, limit, watermark, err := catalogPageQuery(r, "cursor")
		if err != nil {
			writeCatalogPage(w, nil, err)
			return
		}
		result, err := store.Snapshot(r.Context(), catalogsnapshot.SnapshotRequest{
			Watermark: watermark,
			Cursor:    values.Get("cursor"),
			Limit:     limit,
		})
		writeCatalogPage(w, result, err)
	})
	register("GET /v1/internal/catalog/changes", func(w http.ResponseWriter, r *http.Request) {
		values, limit, watermark, err := catalogPageQuery(r, "after")
		if err != nil {
			writeCatalogPage(w, nil, err)
			return
		}
		after, err := parseRevision(values.Get("after"))
		if err != nil {
			writeCatalogPage(w, nil, err)
			return
		}
		result, err := store.Changes(r.Context(), catalogsnapshot.ChangeRequest{
			After:     after,
			Watermark: watermark,
			Limit:     limit,
		})
		writeCatalogPage(w, result, err)
	})

	return nil
}

func snapshotAuthorized(header string, expected [sha256.Size]byte) bool {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || len(token) > 512 {
		return false
	}
	provided := sha256.Sum256([]byte(token))

	return subtle.ConstantTimeCompare(expected[:], provided[:]) == 1
}

func catalogPageQuery(r *http.Request, cursorParam string) (url.Values, int, *int64, error) {
	if len(r.URL.RawQuery) > 512 {
		return nil, 0, nil, catalogsnapshot.ErrInvalidPage
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, 0, nil, catalogsnapshot.ErrInvalidPage
	}
	for key, value := range values {
		if len(value) != 1 || (key != "limit" && key != "watermark" && key != cursorParam) {
			return nil, 0, nil, catalogsnapshot.ErrInvalidPage
		}
	}
	limit := catalogsnapshot.DefaultLimit
	if raw, ok := values["limit"]; ok {
		n, err := parseRevision(raw[0])
		if err != nil || n < 1 || n > catalogsnapshot.MaxLimit {
			return nil, 0, nil, catalogsnapshot.ErrInvalidPage
		}
		limit = int(n)
	}
	var watermark *int64
	if raw, ok := values["watermark"]; ok {
		n, err := parseRevision(raw[0])
		if err != nil {
			return nil, 0, nil, err
		}
		watermark = &n
	}

	return values, limit, watermark, nil
}

func parseRevision(raw string) (int64, error) {
	revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || revision < 0 || strconv.FormatInt(revision, 10) != raw {
		return 0, catalogsnapshot.ErrInvalidPage
	}

	return revision, nil
}

func writeCatalogPage(w http.ResponseWriter, data any, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, data)
	case errors.Is(err, catalogsnapshot.ErrInvalidPage):
		writeJSON(w, http.StatusBadRequest, apperror.New(apperror.ValidationError, "некорректная страница каталога", http.StatusBadRequest))
	default:
		writeJSON(w, http.StatusServiceUnavailable, apperror.New("SERVICE_UNAVAILABLE", "каталог временно недоступен", http.StatusServiceUnavailable))
	}
}
