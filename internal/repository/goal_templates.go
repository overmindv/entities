package repository

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
)

const goalTemplateSelect = `
	SELECT t.id::text, t.program_id::text, v.version, v.catalog_revision, v.published_at, v.title, v.description,
	       (SELECT jsonb_agg(jsonb_build_object('topic_id', g.topic_id, 'target_score_bps', g.target_score_bps)
	                         ORDER BY g.position)
	        FROM program_goal_template_targets g WHERE g.template_id=v.template_id AND g.version=v.version)
	FROM program_goal_templates t JOIN program_goal_template_versions v ON v.template_id=t.id
`

func (r *Postgres) PublishGoalTemplate(ctx context.Context, programID, templateID, actorID string, version int64, input domain.GoalTemplateInput) (domain.GoalTemplateVersion, bool, error) {
	var result domain.GoalTemplateVersion
	var created bool
	err := pgx.BeginTxFunc(ctx, r.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		var publishErr error
		result, created, publishErr = publishGoalTemplate(ctx, tx, programID, templateID, actorID, version, input)

		return publishErr
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.ConstraintName == "program_goal_templates_pkey") {
			err = goalTemplateConflict()
		}

		return domain.GoalTemplateVersion{}, false, err
	}

	return result, created, nil
}

func publishGoalTemplate(ctx context.Context, tx pgx.Tx, programID, templateID, actorID string, version int64, input domain.GoalTemplateInput) (domain.GoalTemplateVersion, bool, error) {
	var owner string
	var latest int64
	err := tx.QueryRow(ctx, `
		SELECT program_id::text, latest_version FROM program_goal_templates WHERE id=$1 FOR UPDATE`,
		templateID).Scan(&owner, &latest)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return domain.GoalTemplateVersion{}, false, err
	case owner != programID:
		return domain.GoalTemplateVersion{}, false, goalTemplateConflict()
	case version <= latest:
		saved, readErr := readGoalTemplate(ctx, tx, templateID, version)
		if readErr != nil {
			return domain.GoalTemplateVersion{}, false, readErr
		}
		if saved.Title != input.Title || saved.Description != input.Description || !slices.Equal(saved.TargetTopics, input.TargetTopics) {
			return domain.GoalTemplateVersion{}, false, goalTemplateConflict()
		}

		return saved, false, nil
	}
	if version-1 != latest {
		return domain.GoalTemplateVersion{}, false, goalTemplateConflict()
	}

	topicIDs := make([]string, len(input.TargetTopics))
	scores := make([]int, len(input.TargetTopics))
	for i, target := range input.TargetTopics {
		topicIDs[i] = target.TopicID
		scores[i] = target.TargetScoreBPS
	}
	if validateErr := validateGoalTemplateGraph(ctx, tx, programID, topicIDs); validateErr != nil {
		return domain.GoalTemplateVersion{}, false, validateErr
	}
	revision, err := lockGoalTemplateRevision(ctx, tx)
	if err != nil {
		return domain.GoalTemplateVersion{}, false, err
	}

	if latest == 0 {
		_, err = tx.Exec(ctx, `INSERT INTO program_goal_templates (id, program_id, latest_version) VALUES ($1,$2,$3)`, templateID, programID, version)
	} else {
		_, err = tx.Exec(ctx, `UPDATE program_goal_templates SET latest_version=$2 WHERE id=$1`, templateID, version)
	}
	if err != nil {
		return domain.GoalTemplateVersion{}, false, err
	}

	result := domain.GoalTemplateVersion{
		ID: templateID, ProgramID: programID, Version: version, CatalogRevision: revision, GoalTemplateInput: input,
	}
	if insertErr := tx.QueryRow(ctx, `
		INSERT INTO program_goal_template_versions (template_id, version, title, description, catalog_revision, published_by)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING published_at`,
		templateID, version, input.Title, input.Description, revision, actorID).Scan(&result.PublishedAt); insertErr != nil {
		return domain.GoalTemplateVersion{}, false, insertErr
	}
	if _, insertErr := tx.Exec(ctx, `
		INSERT INTO program_goal_template_targets (template_id, version, topic_id, position, target_score_bps)
		SELECT $1, $2, x.topic_id, x.ordinality-1, x.score
		FROM unnest($3::text[]::uuid[], $4::integer[]) WITH ORDINALITY AS x(topic_id, score, ordinality)`,
		templateID, version, topicIDs, scores); insertErr != nil {
		return domain.GoalTemplateVersion{}, false, insertErr
	}

	return result, true, nil
}

func lockGoalTemplateRevision(ctx context.Context, tx pgx.Tx) (int64, error) {
	var revision int64
	// Каталог обновляет ревизию перед коммитом. Repeatable Read не пропустит смену ревизии.
	err := tx.QueryRow(ctx, `SELECT revision FROM catalog_clock WHERE singleton FOR SHARE`).Scan(&revision)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "40001" {
		return 0, apperror.New(apperror.GoalTemplateCatalogMoved, "каталог изменился, повторите публикацию", http.StatusConflict)
	}

	return revision, err
}

func goalTemplateConflict() error {
	return apperror.New(apperror.GoalTemplateConflict, "конфликт версии шаблона цели", http.StatusConflict)
}

func (r *Postgres) GoalTemplateVersion(ctx context.Context, id string, version int64) (domain.GoalTemplateVersion, error) {
	return readGoalTemplate(ctx, r.pool, id, version)
}

type goalTemplateQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readGoalTemplate(ctx context.Context, db goalTemplateQuerier, id string, version int64) (domain.GoalTemplateVersion, error) {
	result, err := scanGoalTemplate(db.QueryRow(ctx, goalTemplateSelect+`WHERE t.id=$1 AND v.version=$2`, id, version))

	return result, translateNotFound(err, apperror.GoalTemplateNotFound)
}

func (r *Postgres) ListGoalTemplates(ctx context.Context, programID, after string, limit int) (domain.GoalTemplatePage, error) {
	rows, err := r.pool.Query(ctx, goalTemplateSelect+`
		WHERE t.program_id=$1 AND t.id>$2 AND v.version=t.latest_version
		  AND EXISTS (SELECT 1 FROM catalog_public_programs p WHERE p.id=$1)
		ORDER BY t.id LIMIT $3`, programID, after, limit+1)
	if err != nil {
		return domain.GoalTemplatePage{}, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.GoalTemplateVersion, error) {
		return scanGoalTemplate(row)
	})
	if err != nil {
		return domain.GoalTemplatePage{}, fmt.Errorf("read goal templates: %w", err)
	}
	page := domain.GoalTemplatePage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = items[limit-1].ID
	}

	return page, nil
}

func scanGoalTemplate(row scanner) (domain.GoalTemplateVersion, error) {
	var item domain.GoalTemplateVersion
	err := row.Scan(&item.ID, &item.ProgramID, &item.Version, &item.CatalogRevision, &item.PublishedAt,
		&item.Title, &item.Description, &item.TargetTopics)

	return item, err
}
