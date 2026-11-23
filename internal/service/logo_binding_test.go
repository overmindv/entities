package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
	"github.com/overmindv/entities/internal/media"
	"github.com/samber/lo"
)

type logoCheckerFunc func(context.Context, string, string) error

func (check logoCheckerFunc) CheckLogo(ctx context.Context, actorID, fileID string) error {
	return check(ctx, actorID, fileID)
}

type universityLogoStore struct {
	Store
	university domain.University
	writes     int
}

func (s *universityLogoStore) CreateUniversity(_ context.Context, item domain.University) (domain.University, error) {
	s.writes++

	return item, nil
}

func (s *universityLogoStore) UniversityLogoFile(context.Context, string) (*string, error) {
	return s.university.LogoFileID, nil
}

func (s *universityLogoStore) UpdateUniversity(_ context.Context, item domain.University, expected *string) (domain.University, error) {
	if lo.FromPtr(expected) != lo.FromPtr(s.university.LogoFileID) {
		return domain.University{}, apperror.New(apperror.UniversityLogoChanged, "conflict", http.StatusConflict)
	}
	s.writes++

	return item, nil
}

func TestChecksNewLogoBeforeWrite(t *testing.T) {
	t.Parallel()

	id, file, actor := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "ready"},
		{name: "unavailable", err: media.ErrUnavailable, code: apperror.MediaUnavailable},
		{name: "not ready", err: media.ErrLogoNotReady, code: apperror.UniversityLogoNotReady},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := &universityLogoStore{}
			checks := 0
			catalog := NewWithLogoChecker(store, logoCheckerFunc(func(_ context.Context, actorID, fileID string) error {
				checks++
				if actorID != actor || fileID != file {
					t.Error("unexpected logo authorization input")
				}

				return test.err
			}))
			item := domain.University{
				ID:         id,
				Name:       "University",
				LogoFileID: &file,
			}
			_, err := catalog.CreateUniversity(t.Context(), item, actor)
			assertLogoMutation(t, err, test.code)
			_, err = catalog.UpdateUniversity(t.Context(), item, actor)
			assertLogoMutation(t, err, test.code)
			if checks != 2 || (test.code != "" && store.writes != 0) ||
				(test.code == "" && store.writes != 2) {
				t.Error("unexpected checks or catalog writes", checks, store.writes)
			}
		})
	}
}

func TestRetainsOrRemovesLogoWhenMediaIsDown(t *testing.T) {
	t.Parallel()

	current, actor := uuid.NewString(), uuid.NewString()
	upper := strings.ToUpper(current)
	store := &universityLogoStore{
		university: domain.University{LogoFileID: &current},
	}
	catalog := NewWithLogoChecker(store, logoCheckerFunc(func(context.Context, string, string) error {
		t.Error("keeping or removing a logo must not call Media")

		return media.ErrUnavailable
	}))
	item := domain.University{
		ID:         uuid.NewString(),
		Name:       "Updated name",
		LogoFileID: &upper,
	}
	saved, err := catalog.UpdateUniversity(t.Context(), item, actor)
	if err != nil || saved.LogoFileID == nil || *saved.LogoFileID != current {
		t.Fatal("could not keep the current logo", err)
	}
	item.LogoFileID = nil
	saved, err = catalog.UpdateUniversity(t.Context(), item, actor)
	if err != nil || saved.LogoFileID != nil || store.writes != 2 {
		t.Fatal("could not remove the current logo", err, store.writes)
	}
}

func assertLogoMutation(t *testing.T, err error, code string) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatal(err)
		}

		return
	}
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code != code {
		t.Fatal("unexpected logo error", err)
	}
}
