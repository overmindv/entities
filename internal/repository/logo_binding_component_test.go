package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
)

//nolint:paralleltest // Доставщик забирает запись из общей очереди тестовой БД.
func TestLogoBindingQueue(t *testing.T) {
	ctx := t.Context()
	pool := testCatalogPool(t, "")
	store := New(pool)
	actor, file := uuid.NewString(), uuid.NewString()
	university, createErr := store.CreateUniversity(ctx, domain.University{
		Name:      "Logo fixture " + uuid.NewString(),
		Status:    domain.StatusDraft,
		CreatedBy: actor,
	})
	if createErr != nil {
		t.Fatal(createErr)
	}
	assertEmpty := func() {
		t.Helper()
		if _, found, claimErr := store.ClaimLogoBinding(ctx); claimErr != nil || found {
			t.Fatal("expected no ready binding", found, claimErr)
		}
	}
	rollback := errors.New("rollback catalog transaction")
	if txErr := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if putErr := putLogoBinding(ctx, tx, university.ID, &file, actor); putErr != nil {
			return putErr
		}

		return rollback
	}); !errors.Is(txErr, rollback) {
		t.Fatal("logo intent transaction did not roll back", txErr)
	}
	assertEmpty()

	put := func(fileID *string) {
		t.Helper()
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			return putLogoBinding(ctx, tx, university.ID, fileID, actor)
		}); err != nil {
			t.Fatal(err)
		}
	}
	put(&file)
	first, found, firstErr := store.ClaimLogoBinding(ctx)
	if firstErr != nil || !found || first.UniversityID != university.ID || first.Version != 1 ||
		first.FileID == nil || *first.FileID != file || first.ActorID != actor || first.ClaimToken == "" {
		t.Fatal("unexpected first intent", first, found, firstErr)
	}
	assertEmpty()
	if ok, retryErr := store.RetryLogoBinding(ctx, first, time.Minute); retryErr != nil || !ok {
		t.Fatal("failed to reschedule binding", ok, retryErr)
	}
	assertEmpty()
	if _, execErr := pool.Exec(ctx, `
		UPDATE university_logo_intents SET next_attempt_at = statement_timestamp() - interval '1 second'
		WHERE id = $1`, university.ID); execErr != nil {
		t.Fatal(execErr)
	}
	retried, found, retryClaimErr := store.ClaimLogoBinding(ctx)
	if retryClaimErr != nil || !found || retried.Version != first.Version || retried.ClaimToken == first.ClaimToken || retried.Attempts != 1 {
		t.Fatal("retry must keep the version and use a different claim", retried, found, retryClaimErr)
	}
	if _, execErr := pool.Exec(ctx, `
		UPDATE university_logo_intents SET lease_until = statement_timestamp() - interval '1 second'
		WHERE id = $1`, university.ID); execErr != nil {
		t.Fatal(execErr)
	}
	recovered, found, recoverErr := store.ClaimLogoBinding(ctx)
	if recoverErr != nil || !found || recovered.Version != retried.Version || recovered.ClaimToken == retried.ClaimToken {
		t.Fatal("expired lease did not become available", recovered, found, recoverErr)
	}
	if ok, err := store.CompleteLogoBinding(ctx, retried); err != nil || ok {
		t.Fatal("expired worker acknowledged another lease", ok, err)
	}

	put(nil)
	if ok, err := store.CompleteLogoBinding(ctx, recovered); err != nil || ok {
		t.Fatal("old worker acknowledged a newer binding", ok, err)
	}
	if ok, err := store.RetryLogoBinding(ctx, recovered, time.Hour); err != nil || ok {
		t.Fatal("old worker rescheduled a newer binding", ok, err)
	}
	detach, found, detachErr := store.ClaimLogoBinding(ctx)
	if detachErr != nil || !found || detach.UniversityID != university.ID || detach.Version != 2 ||
		detach.FileID != nil || detach.Attempts != 0 {
		t.Fatal("unexpected detach intent", detach, found, detachErr)
	}
	if ok, err := store.CompleteLogoBinding(ctx, detach); err != nil || !ok {
		t.Fatal("failed to acknowledge detach", ok, err)
	}
	assertEmpty()
}

//nolint:paralleltest // Доставщик забирает запись из общей очереди тестовой БД.
func TestUniversityLogoMutations(t *testing.T) {
	ctx := t.Context()
	store := New(testCatalogPool(t, ""))
	actor, firstLogo := uuid.NewString(), uuid.NewString()
	created, createErr := store.CreateUniversity(ctx, domain.University{
		Name:       "Logo mutation " + uuid.NewString(),
		Status:     domain.StatusDraft,
		CreatedBy:  actor,
		LogoFileID: &firstLogo,
	})
	if createErr != nil {
		t.Fatal(createErr)
	}
	first, found, err := store.ClaimLogoBinding(ctx)
	if err != nil || !found || first.UniversityID != created.ID || first.Version != 1 ||
		first.FileID == nil || *first.FileID != firstLogo {
		t.Fatal("creation did not queue the logo", first, found, err)
	}
	rename := created
	rename.Name += " renamed"
	rename.UpdatedBy = uuid.NewString()
	if _, updateErr := store.UpdateUniversity(ctx, rename, &firstLogo); updateErr != nil {
		t.Fatal("failed to rename university", updateErr)
	}
	if ok, completeErr := store.CompleteLogoBinding(ctx, first); completeErr != nil || !ok {
		t.Fatal("rename must not change the claimed binding", ok, completeErr)
	}

	type mutation struct {
		file  string
		actor string
		err   error
	}
	results := make(chan mutation, 2)
	for range 2 {
		go func() {
			updated := rename
			file, updatedBy := uuid.NewString(), uuid.NewString()
			updated.LogoFileID = &file
			updated.UpdatedBy = updatedBy
			_, updateErr := store.UpdateUniversity(ctx, updated, &firstLogo)
			results <- mutation{file: file, actor: updatedBy, err: updateErr}
		}()
	}
	var winner mutation
	conflicts := 0
	for range 2 {
		result := <-results
		if result.err == nil {
			winner = result

			continue
		}
		var app *apperror.Error
		if !errors.As(result.err, &app) || app.Code != apperror.UniversityLogoChanged {
			t.Fatal("unexpected concurrent update error", result.err)
		}
		conflicts++
	}
	if conflicts != 1 || winner.file == "" {
		t.Fatal("only one update based on the same logo should succeed", conflicts)
	}
	current, found, err := store.ClaimLogoBinding(ctx)
	if err != nil || !found || current.Version != 2 || current.FileID == nil ||
		*current.FileID != winner.file || current.ActorID != winner.actor {
		t.Fatal("queued binding does not match the winning update", current, found, err)
	}
	if deleteErr := store.DeleteUniversity(ctx, created.ID, actor); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	if ok, lateErr := store.CompleteLogoBinding(ctx, current); lateErr != nil || ok {
		t.Fatal("late response must not overwrite detach", ok, lateErr)
	}
	detach, found, err := store.ClaimLogoBinding(ctx)
	if err != nil || !found || detach.Version != 3 || detach.FileID != nil || detach.ActorID != actor {
		t.Fatal("deletion did not queue detach", detach, found, err)
	}
	if ok, err := store.CompleteLogoBinding(ctx, detach); err != nil || !ok {
		t.Fatal("failed to acknowledge detach", ok, err)
	}
}

//nolint:paralleltest // Reconciliation забирает запись из общей очереди тестовой БД.
func TestUniversityLogoRepair(t *testing.T) {
	ctx := t.Context()
	pool := testCatalogPool(t, "")
	store := New(pool)
	actor, file, replacement := uuid.NewString(), uuid.NewString(), uuid.NewString()
	item, createErr := store.CreateUniversity(ctx, domain.University{
		Name: "Logo repair " + uuid.NewString(), Status: domain.StatusDraft, CreatedBy: actor, LogoFileID: &file,
	})
	if createErr != nil {
		t.Fatal(createErr)
	}
	applied := func(want int64) {
		t.Helper()
		intent, found, err := store.ClaimLogoBinding(ctx)
		if err != nil || !found || intent.UniversityID != item.ID || intent.Version != want {
			t.Fatal("unexpected delivery intent", intent, found, err)
		}
		if ok, err := store.CompleteLogoBinding(ctx, intent); err != nil || !ok {
			t.Fatal("failed to acknowledge delivery", ok, err)
		}
	}
	claimRecheck := func(want int64) domain.LogoBindingIntent {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			UPDATE university_logo_intents SET next_attempt_at = statement_timestamp() - interval '1 second'
			WHERE id = $1`, item.ID); err != nil {
			t.Fatal(err)
		}
		intent, found, err := store.ClaimLogoReconciliation(ctx)
		if err != nil || !found || intent.UniversityID != item.ID || intent.Version != want {
			t.Fatal("unexpected reconciliation intent", intent, found, err)
		}

		return intent
	}
	applied(1)
	if _, found, err := store.ClaimLogoReconciliation(ctx); err != nil || found {
		t.Fatal("reconciliation should wait until due", found, err)
	}
	old := claimRecheck(1)
	item.LogoFileID = &replacement
	item.UpdatedBy = actor
	if _, err := store.UpdateUniversity(ctx, item, &file); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.RepairLogoBinding(ctx, old, 9); err != nil || ok {
		t.Fatal("old reconciliation replaced a newer edit", ok, err)
	}
	applied(2)
	current := claimRecheck(2)
	if ok, err := store.RepairLogoBinding(ctx, current, 9); err != nil || !ok {
		t.Fatal("could not queue repair above remote version", ok, err)
	}
	repaired, found, err := store.ClaimLogoBinding(ctx)
	if err != nil || !found || repaired.Version != 10 || repaired.FileID == nil || *repaired.FileID != replacement {
		t.Fatal("repair should preserve desired logo and advance the version", repaired, found, err)
	}
	if ok, err := store.CompleteLogoBinding(ctx, repaired); err != nil || !ok {
		t.Fatal("repair could not be acknowledged", ok, err)
	}
}
