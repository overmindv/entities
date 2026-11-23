package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
)

func TestNormalizeGoalTemplate(t *testing.T) {
	t.Parallel()

	const topic = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	valid := domain.GoalTemplateTarget{TopicID: topic, TargetScoreBPS: 1}
	input := domain.GoalTemplateInput{
		Title: "  Подготовка к экзамену ", Description: " Сортировки\nПоиск  ",
		TargetTopics: []domain.GoalTemplateTarget{{TopicID: strings.ToUpper(topic), TargetScoreBPS: domain.MaxGoalTemplateScore}},
	}
	result, err := normalizeGoalTemplate(input)
	if err != nil || result.Title != "Подготовка к экзамену" || result.Description != "Сортировки\nПоиск" ||
		len(result.TargetTopics) != 1 || result.TargetTopics[0].TopicID != topic || input.TargetTopics[0].TopicID == topic {
		t.Fatal("template not normalized without mutating the input:", result, err)
	}

	tests := map[string]domain.GoalTemplateInput{
		"blank title":       {Title: " ", TargetTopics: []domain.GoalTemplateTarget{valid}},
		"oversized title":   {Title: strings.Repeat("я", domain.MaxGoalTemplateTitle/2+1), TargetTopics: []domain.GoalTemplateTarget{valid}},
		"invalid UTF-8":     {Title: string([]byte{0xff}), TargetTopics: []domain.GoalTemplateTarget{valid}},
		"invalid title":     {Title: "one\ntwo", TargetTopics: []domain.GoalTemplateTarget{valid}},
		"oversized details": {Title: "Example", Description: strings.Repeat("a", domain.MaxGoalTemplateDetails+1), TargetTopics: []domain.GoalTemplateTarget{valid}},
		"empty targets":     {Title: "Example"},
		"too many targets":  {Title: "Example", TargetTopics: make([]domain.GoalTemplateTarget, domain.MaxGoalTemplateTargets+1)},
		"zero score":        {Title: "Example", TargetTopics: []domain.GoalTemplateTarget{{TopicID: topic}}},
		"score overflow":    {Title: "Example", TargetTopics: []domain.GoalTemplateTarget{{TopicID: topic, TargetScoreBPS: domain.MaxGoalTemplateScore + 1}}},
		"duplicate topics":  {Title: "Example", TargetTopics: []domain.GoalTemplateTarget{valid, {TopicID: strings.ToUpper(topic), TargetScoreBPS: 2}}},
		"malformed UUID":    {Title: "Example", TargetTopics: []domain.GoalTemplateTarget{{TopicID: "not-an-id", TargetScoreBPS: 1}}},
	}
	for name, template := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, normalizeErr := normalizeGoalTemplate(template)
			var appErr *apperror.Error
			if !errors.As(normalizeErr, &appErr) || appErr.Code != apperror.ValidationError {
				t.Fatal("invalid template accepted:", normalizeErr)
			}
		})
	}
}
