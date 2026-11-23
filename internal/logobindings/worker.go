package logobindings

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"log/slog"
	"time"

	"github.com/overmindv/entities/internal/domain"
	"github.com/overmindv/entities/internal/media"
	"github.com/samber/lo"
)

const (
	pollInterval  = time.Second
	batchSize     = 8
	attemptWindow = 8 * time.Second
)

type Store interface {
	ClaimLogoBinding(ctx context.Context) (domain.LogoBindingIntent, bool, error)
	ClaimLogoReconciliation(ctx context.Context) (domain.LogoBindingIntent, bool, error)
	CompleteLogoBinding(ctx context.Context, intent domain.LogoBindingIntent) (bool, error)
	RetryLogoBinding(ctx context.Context, intent domain.LogoBindingIntent, delay time.Duration) (bool, error)
	RepairLogoBinding(ctx context.Context, intent domain.LogoBindingIntent, observedVersion int64) (bool, error)
}

type Media interface {
	ReplaceLogo(ctx context.Context, universityID, actorID string, fileID *string, version int64) error
	LookupLogo(ctx context.Context, universityID string) (domain.LogoBindingState, error)
}

type Worker struct {
	store Store
	media Media
	log   *slog.Logger
}

func NewWorker(store Store, media Media, log *slog.Logger) *Worker {
	return &Worker{
		store: store,
		media: media,
		log:   log,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		for range batchSize {
			found, err := w.deliver(ctx)
			if err != nil && ctx.Err() == nil {
				w.log.Error("logo binding delivery failed", "error", err)
			}
			if !found || ctx.Err() != nil {
				break
			}
		}
		if err := w.reconcile(ctx); err != nil && ctx.Err() == nil {
			w.log.Error("logo binding reconciliation failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}

	return nil
}

func (w *Worker) deliver(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, attemptWindow)
	defer cancel()
	intent, found, err := w.store.ClaimLogoBinding(ctx)
	if err != nil || !found {
		return false, err
	}
	if replaceErr := w.media.ReplaceLogo(ctx, intent.UniversityID, intent.ActorID, intent.FileID, intent.Version); replaceErr != nil {
		if errors.Is(replaceErr, media.ErrVersionConflict) {
			state, readErr := w.media.LookupLogo(ctx, intent.UniversityID)
			if readErr == nil {
				return true, w.repair(ctx, intent, state.Version)
			}
			replaceErr = errors.Join(replaceErr, readErr)
		}

		return true, w.retry(ctx, intent, replaceErr)
	}
	_, err = w.store.CompleteLogoBinding(ctx, intent)

	return true, err
}

func (w *Worker) reconcile(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, attemptWindow)
	defer cancel()
	intent, found, err := w.store.ClaimLogoReconciliation(ctx)
	if err != nil || !found {
		return err
	}
	state, err := w.media.LookupLogo(ctx, intent.UniversityID)
	if err != nil {
		return w.retry(ctx, intent, err)
	}
	if state.Version != intent.Version || lo.FromPtr(state.FileID) != lo.FromPtr(intent.FileID) {
		return w.repair(ctx, intent, state.Version)
	}
	_, err = w.store.CompleteLogoBinding(ctx, intent)

	return err
}

func (w *Worker) repair(ctx context.Context, intent domain.LogoBindingIntent, observedVersion int64) error {
	_, err := w.store.RepairLogoBinding(ctx, intent, observedVersion)
	if err != nil {
		return w.retry(ctx, intent, err)
	}

	return nil
}

func (w *Worker) retry(ctx context.Context, intent domain.LogoBindingIntent, err error) error {
	rescheduled, retryErr := w.store.RetryLogoBinding(ctx, intent, delay(intent))
	if retryErr != nil {
		return errors.Join(err, retryErr)
	}
	if rescheduled {
		return fmt.Errorf("retry logo binding version %d: %w", intent.Version, err)
	}

	return nil
}

func delay(intent domain.LogoBindingIntent) time.Duration {
	step := min(max(intent.Attempts, 0), 9)
	jitter := time.Duration(crc32.ChecksumIEEE([]byte(intent.UniversityID))%1000) * time.Millisecond

	return (time.Second << step) + jitter
}
