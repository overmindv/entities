package service

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/overmindv/entities/internal/domain"
)

const (
	maxStudyMinutes  = 1200
	maxStudyOutcomes = 8
	maxOutcomeBytes  = 256
)

func normalizeLearningMetadata(input *domain.TopicLearningMetadata) (*domain.TopicLearningMetadata, error) {
	if input == nil {
		return nil, nil
	}
	if len(input.Outcomes) > maxStudyOutcomes {
		return nil, validation("нельзя указать больше восьми результатов обучения")
	}
	result := &domain.TopicLearningMetadata{
		Outcomes: make([]string, 0, len(input.Outcomes)),
	}
	if input.EstimatedMinutes != nil {
		minutes := *input.EstimatedMinutes
		if minutes < 1 || minutes > maxStudyMinutes {
			return nil, validation("estimated_minutes должен быть от 1 до 1200")
		}
		result.EstimatedMinutes = &minutes
	}
	seen := make(map[string]struct{}, len(input.Outcomes))
	for _, text := range input.Outcomes {
		outcome := strings.TrimSpace(text)
		if len(outcome) < 1 || len(outcome) > maxOutcomeBytes || !utf8.ValidString(outcome) ||
			strings.ContainsFunc(outcome, unicode.IsControl) {
			return nil, validation("результат обучения должен содержать от 1 до 256 байт текста без управляющих символов")
		}
		key := strings.ToLower(outcome)
		if _, exists := seen[key]; exists {
			return nil, validation("результаты обучения не должны повторяться")
		}
		seen[key] = struct{}{}
		result.Outcomes = append(result.Outcomes, outcome)
	}

	return result, nil
}
