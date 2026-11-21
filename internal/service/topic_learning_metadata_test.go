package service

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
)

func TestNormalizeLearningMetadata(t *testing.T) {
	t.Parallel()
	for _, minutes := range []int{1, maxStudyMinutes} {
		input := &domain.TopicLearningMetadata{
			EstimatedMinutes: &minutes,
			Outcomes:         []string{"  Сравнить алгоритмы  ", "Объяснить оценку"},
		}
		result, err := normalizeLearningMetadata(input)
		if err != nil || result.EstimatedMinutes == nil || *result.EstimatedMinutes != minutes ||
			!slices.Equal(result.Outcomes, []string{"Сравнить алгоритмы", "Объяснить оценку"}) {
			t.Fatal("metadata not normalized", result, err)
		}
		for i := range input.Outcomes {
			input.Outcomes[i] = ""
		}
		if result.Outcomes[0] == "" || result.EstimatedMinutes == input.EstimatedMinutes {
			t.Fatal("normalized data still references the input")
		}
	}
	omitted, err := normalizeLearningMetadata(nil)
	if omitted != nil || err != nil {
		t.Fatal("omitted metadata must be preserved", omitted, err)
	}
	clear, err := normalizeLearningMetadata(&domain.TopicLearningMetadata{})
	if clear == nil || clear.EstimatedMinutes != nil || len(clear.Outcomes) != 0 || err != nil {
		t.Fatal("explicit empty object must clear metadata", clear, err)
	}
}

func TestRejectsInvalidLearningMetadata(t *testing.T) {
	t.Parallel()
	zero, oversized := 0, maxStudyMinutes+1
	for name, metadata := range map[string]*domain.TopicLearningMetadata{
		"zero minutes":      {EstimatedMinutes: &zero},
		"too many minutes":  {EstimatedMinutes: &oversized},
		"too many outcomes": {Outcomes: make([]string, maxStudyOutcomes+1)},
		"empty outcome":     {Outcomes: []string{" \t "}},
		"duplicate outcome": {Outcomes: []string{"Оценить", " оценить "}},
		"long outcome":      {Outcomes: []string{strings.Repeat("я", maxOutcomeBytes/2+1)}},
		"control character": {Outcomes: []string{"first\nsecond"}},
		"invalid UTF-8":     {Outcomes: []string{string([]byte{0xff})}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var appErr *apperror.Error
			result, err := normalizeLearningMetadata(metadata)
			if !errors.As(err, &appErr) || appErr.Code != apperror.ValidationError || result != nil {
				t.Fatal("invalid learning metadata accepted", result, err)
			}
		})
	}
}
