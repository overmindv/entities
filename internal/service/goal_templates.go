package service

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/overmindv/entities/internal/domain"
)

func (s *Service) PublishGoalTemplate(ctx context.Context, programID, templateID, actorID string, version int64, input domain.GoalTemplateInput) (domain.GoalTemplateVersion, bool, error) {
	program, programOK := goalTemplateUUID(programID)
	template, templateOK := goalTemplateUUID(templateID)
	actor, actorOK := goalTemplateUUID(actorID)
	if !programOK || !templateOK || !actorOK || version < 1 {
		return domain.GoalTemplateVersion{}, false, validation("некорректный ID программы, шаблона, редактора или номер версии")
	}
	normalized, err := normalizeGoalTemplate(input)
	if err != nil {
		return domain.GoalTemplateVersion{}, false, err
	}

	return s.store.PublishGoalTemplate(ctx, program, template, actor, version, normalized)
}

func goalTemplateUUID(s string) (string, bool) {
	id, err := uuid.Parse(s)

	return id.String(), len(s) == 36 && err == nil && id != uuid.Nil
}

func normalizeGoalTemplate(input domain.GoalTemplateInput) (domain.GoalTemplateInput, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	if input.Title == "" || !templateText(input.Title, domain.MaxGoalTemplateTitle, false) ||
		!templateText(input.Description, domain.MaxGoalTemplateDetails, true) {
		return domain.GoalTemplateInput{}, validation("некорректный заголовок или описание шаблона")
	}
	if len(input.TargetTopics) < 1 || len(input.TargetTopics) > domain.MaxGoalTemplateTargets {
		return domain.GoalTemplateInput{}, validation("в шаблоне должно быть от 1 до 16 целевых тем")
	}
	targets := make([]domain.GoalTemplateTarget, 0, len(input.TargetTopics))
	seen := make(map[string]struct{}, len(input.TargetTopics))
	for _, target := range input.TargetTopics {
		id, ok := goalTemplateUUID(target.TopicID)
		if !ok || target.TargetScoreBPS < 1 || target.TargetScoreBPS > domain.MaxGoalTemplateScore {
			return domain.GoalTemplateInput{}, validation("некорректная целевая тема или уровень")
		}
		if _, duplicate := seen[id]; duplicate {
			return domain.GoalTemplateInput{}, validation("целевая тема повторяется")
		}
		seen[id] = struct{}{}
		target.TopicID = id
		targets = append(targets, target)
	}
	input.TargetTopics = targets

	return input, nil
}

func templateText(s string, limit int, multiline bool) bool {
	return len(s) <= limit && utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) && (!multiline || (r != '\n' && r != '\t'))
	})
}
