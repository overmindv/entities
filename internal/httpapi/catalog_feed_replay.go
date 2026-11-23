package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/catalogsnapshot"
	"github.com/overmindv/entities/internal/outbox"
)

const feedReplayEnv = "ENTITIES_FEED_REPLAY_TOKEN"
const feedReplayPath = "/v1/internal/catalog/feed-changes"

type CatalogFeedStore interface {
	FeedChanges(context.Context, catalogsnapshot.ChangeRequest) (outbox.FeedPage, error)
}

func RegisterCatalogFeedReplay(router Router, store CatalogFeedStore, token string) error {
	register, err := internalCatalogRoutes(router, token, feedReplayEnv)
	if err != nil || register == nil {
		return err
	}
	register("GET "+feedReplayPath, func(w http.ResponseWriter, r *http.Request) {
		values, limit, watermark, err := catalogPageQuery(r, "after")
		if err != nil {
			writeCatalogPage(w, nil, err)

			return
		}
		var after int64
		if values.Has("after") {
			after, err = parseRevision(values.Get("after"))
			if err != nil {
				writeCatalogPage(w, nil, err)

				return
			}
		}
		page, err := store.FeedChanges(r.Context(), catalogsnapshot.ChangeRequest{
			After:     after,
			Watermark: watermark,
			Limit:     limit,
		})
		if errors.Is(err, outbox.ErrFeedHistoryGap) {
			writeJSON(w, http.StatusConflict, apperror.New("CATALOG_HISTORY_GAP", "история ленты неполная", http.StatusConflict))

			return
		}
		writeCatalogPage(w, page, err)
	})

	return nil
}
