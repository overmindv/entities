package repository

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/overmindv/entities/internal/apperror"
)

const (
	maxGoalTemplateGraphTopics = 4096
	maxGoalTemplateGraphEdges  = 32768
)

func validateGoalTemplateGraph(ctx context.Context, tx pgx.Tx, programID string, targets []string) error {
	topics, err := goalTemplateGraphTopics(ctx, tx, programID, targets)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT t.id::text, p.prerequisite_topic_id::text
		FROM unnest($1::text[]::uuid[]) AS t(id)
		CROSS JOIN LATERAL (
		    SELECT prerequisite_topic_id FROM topic_prerequisites WHERE topic_id=t.id OFFSET 0
		) p
		LIMIT $2`, topics, maxGoalTemplateGraphEdges+1)
	if err != nil {
		return err
	}
	defer rows.Close()

	// Убираем темы без пререквизитов. Если остались вершины, в графе есть цикл.
	remaining := make(map[string]int, len(topics))
	dependents := make(map[string][]string, len(topics))
	for _, id := range topics {
		remaining[id] = 0
	}
	edges := 0
	for rows.Next() {
		var topicID, prerequisiteID string
		if scanErr := rows.Scan(&topicID, &prerequisiteID); scanErr != nil {
			return scanErr
		}
		edges++
		if edges > maxGoalTemplateGraphEdges {
			return invalidGoalTemplateGraph()
		}
		remaining[topicID]++
		dependents[prerequisiteID] = append(dependents[prerequisiteID], topicID)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return rowsErr
	}
	resolved := make([]string, 0, len(topics))
	for _, id := range topics {
		if remaining[id] == 0 {
			resolved = append(resolved, id)
		}
	}
	for i := 0; i < len(resolved); i++ {
		for _, id := range dependents[resolved[i]] {
			remaining[id]--
			if remaining[id] == 0 {
				resolved = append(resolved, id)
			}
		}
	}
	if len(resolved) != len(topics) {
		return invalidGoalTemplateGraph()
	}

	return nil
}

func goalTemplateGraphTopics(ctx context.Context, tx pgx.Tx, programID string, targets []string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE reachable(id) AS (
		    SELECT unnest($2::text[]::uuid[])
		    UNION
		    SELECT p.prerequisite_topic_id FROM reachable r
		    CROSS JOIN LATERAL (
		        SELECT prerequisite_topic_id FROM topic_prerequisites WHERE topic_id=r.id OFFSET 0
		    ) p
		), bounded AS MATERIALIZED (
		    SELECT id FROM reachable LIMIT $3
		)
		SELECT b.id::text, COALESCE(v.active, false)
		FROM bounded b
		LEFT JOIN LATERAL (
		    SELECT true AS active FROM catalog_public_topics t JOIN courses c ON c.id=t.course_id
		    WHERE t.id=b.id AND c.program_id=$1
		    LIMIT 1
		) v ON true`, programID, targets, maxGoalTemplateGraphTopics+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	topics := make([]string, 0, len(targets))
	for rows.Next() {
		var id string
		var active bool
		if scanErr := rows.Scan(&id, &active); scanErr != nil {
			return nil, scanErr
		}
		if !active || len(topics) >= maxGoalTemplateGraphTopics {
			return nil, invalidGoalTemplateGraph()
		}
		topics = append(topics, id)
	}

	return topics, rows.Err()
}

func invalidGoalTemplateGraph() error {
	return apperror.New(apperror.GoalTemplateInvalidGraph, "темы недоступны в программе или граф пререквизитов некорректен", http.StatusUnprocessableEntity)
}
