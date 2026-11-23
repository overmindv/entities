package logobindings

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/overmindv/entities/internal/domain"
)

type fixtureStore struct {
	intent      domain.LogoBindingIntent
	completions int
	retries     int
	repairs     int
	observed    int64
	reschedule  time.Duration
}

func (f *fixtureStore) ClaimLogoBinding(context.Context) (domain.LogoBindingIntent, bool, error) {
	return f.intent, f.intent.ClaimToken != "", nil
}

func (f *fixtureStore) ClaimLogoReconciliation(ctx context.Context) (domain.LogoBindingIntent, bool, error) {
	return f.ClaimLogoBinding(ctx)
}

func (f *fixtureStore) RepairLogoBinding(_ context.Context, intent domain.LogoBindingIntent, observed int64) (bool, error) {
	if intent.ClaimToken != f.intent.ClaimToken || intent.Version != f.intent.Version {
		return false, errors.New("wrong binding repaired")
	}
	f.repairs++
	f.observed = observed

	return true, nil
}

func (f *fixtureStore) CompleteLogoBinding(_ context.Context, intent domain.LogoBindingIntent) (bool, error) {
	if intent.ClaimToken != f.intent.ClaimToken || intent.Version != f.intent.Version {
		return false, errors.New("wrong binding acknowledged")
	}
	f.completions++

	return true, nil
}

func (f *fixtureStore) RetryLogoBinding(_ context.Context, intent domain.LogoBindingIntent, delay time.Duration) (bool, error) {
	if intent.ClaimToken != f.intent.ClaimToken || intent.Version != f.intent.Version {
		return false, errors.New("wrong binding rescheduled")
	}
	f.retries++
	f.reschedule = delay

	return true, nil
}

type fixtureMedia func(context.Context, string, string, *string, int64) error

func (f fixtureMedia) ReplaceLogo(ctx context.Context, universityID, actorID string, fileID *string, version int64) error {
	return f(ctx, universityID, actorID, fileID, version)
}

func (f fixtureMedia) LookupLogo(context.Context, string) (domain.LogoBindingState, error) {
	return domain.LogoBindingState{}, errors.New("unexpected binding lookup")
}

type fixtureLookup struct {
	fixtureMedia
	state domain.LogoBindingState
	err   error
}

func (f fixtureLookup) LookupLogo(context.Context, string) (domain.LogoBindingState, error) {
	return f.state, f.err
}

func TestLogoDeliveryAcknowledgesOrRetries(t *testing.T) {
	t.Parallel()

	offline := errors.New("media unavailable")
	for _, test := range []struct {
		name        string
		err         error
		completions int
		retries     int
	}{
		{name: "applied", completions: 1},
		{name: "failed", err: offline, retries: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := &fixtureStore{intent: domain.LogoBindingIntent{
				UniversityID: "university",
				ActorID:      "actor",
				Version:      7,
				ClaimToken:   "first",
				Attempts:     2,
			}}
			call := fixtureMedia(func(ctx context.Context, universityID, actorID string, id *string, version int64) error {
				if _, ok := ctx.Deadline(); !ok || universityID != store.intent.UniversityID ||
					actorID != store.intent.ActorID || id != nil || version != store.intent.Version {
					t.Error("worker did not send the claimed binding")
				}

				return test.err
			})
			found, err := NewWorker(store, call, nil).deliver(t.Context())
			if !found || !errors.Is(err, test.err) || store.completions != test.completions || store.retries != test.retries {
				t.Error("unexpected delivery result", found, err, store.completions, store.retries)
			}
			if test.retries > 0 && (store.reschedule < 4*time.Second || store.reschedule >= 5*time.Second) {
				t.Error("unexpected retry delay", store.reschedule)
			}
		})
	}
}

func TestLogoRetryDelayIsBounded(t *testing.T) {
	t.Parallel()

	delay := delay(domain.LogoBindingIntent{UniversityID: "university", Attempts: 30})
	if delay < 512*time.Second || delay >= 513*time.Second {
		t.Error("retry delay exceeds the cap", delay)
	}
}

func TestLogoReconciliationQueuesOnlyDrift(t *testing.T) {
	t.Parallel()

	file, other := "logo", "previous logo"
	offline := errors.New("media unavailable")
	for _, test := range []struct {
		name        string
		state       domain.LogoBindingState
		err         error
		completions int
		repairs     int
		retries     int
	}{
		{name: "matches", state: domain.LogoBindingState{Version: 7, FileID: &file}, completions: 1},
		{name: "lost row", state: domain.LogoBindingState{Version: 7}, repairs: 1},
		{name: "media ahead", state: domain.LogoBindingState{Version: 15, FileID: &other}, repairs: 1},
		{name: "read error", err: offline, retries: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := &fixtureStore{intent: domain.LogoBindingIntent{
				UniversityID: "university", FileID: &file, Version: 7, ClaimToken: "first",
			}}
			backend := fixtureLookup{state: test.state, err: test.err}
			err := NewWorker(store, backend, nil).reconcile(t.Context())
			if !errors.Is(err, test.err) || store.completions != test.completions ||
				store.repairs != test.repairs || store.retries != test.retries ||
				(test.repairs == 1 && store.observed != test.state.Version) {
				t.Error("unexpected reconciliation outcome", err, store.completions, store.repairs, store.retries)
			}
		})
	}
}
