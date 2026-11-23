package domain

import "time"

const (
	MaxGoalTemplateTargets = 16
	MaxGoalTemplateTitle   = 160
	MaxGoalTemplateDetails = 2000
	MaxGoalTemplateScore   = 10000
)

type GoalTemplateTarget struct {
	TopicID        string `json:"topic_id"`
	TargetScoreBPS int    `json:"target_score_bps"`
}

type GoalTemplateInput struct {
	Title        string               `json:"title"`
	Description  string               `json:"description"`
	TargetTopics []GoalTemplateTarget `json:"target_topics"`
}

type GoalTemplateVersion struct {
	ID              string    `json:"id"`
	ProgramID       string    `json:"program_id"`
	Version         int64     `json:"version"`
	CatalogRevision int64     `json:"catalog_revision"`
	PublishedAt     time.Time `json:"published_at"`
	GoalTemplateInput
}

type GoalTemplatePage struct {
	Items      []GoalTemplateVersion `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}
