package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/overmindv/entities/internal/domain"
)

func putLogoBinding(ctx context.Context, tx pgx.Tx, id string, fileID *string, actorID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO university_logo_intents (id, file_id, actor_user_id, version)
		VALUES ($1, $2, $3, 1)
		ON CONFLICT (id) DO UPDATE SET
		    file_id = EXCLUDED.file_id,
		    actor_user_id = EXCLUDED.actor_user_id,
		    version = university_logo_intents.version + 1,
		    attempts = 0,
		    next_attempt_at = statement_timestamp(),
		    claim_token = NULL,
		    lease_until = NULL,
		    applied_at = NULL,
		    updated_at = statement_timestamp()`, id, fileID, actorID)
	if err != nil {
		return fmt.Errorf("queue university logo binding: %w", err)
	}

	return nil
}

func (r *Postgres) ClaimLogoBinding(ctx context.Context) (domain.LogoBindingIntent, bool, error) {
	return r.claimLogoBinding(ctx, false)
}

func (r *Postgres) ClaimLogoReconciliation(ctx context.Context) (domain.LogoBindingIntent, bool, error) {
	return r.claimLogoBinding(ctx, true)
}

func (r *Postgres) claimLogoBinding(ctx context.Context, applied bool) (domain.LogoBindingIntent, bool, error) {
	var intent domain.LogoBindingIntent
	err := r.pool.QueryRow(ctx, `
		WITH ready AS (
		    SELECT id FROM university_logo_intents
		    WHERE (applied_at IS NOT NULL) = $1
		      AND COALESCE(lease_until, next_attempt_at) <= statement_timestamp()
		    ORDER BY COALESCE(lease_until, next_attempt_at), id
		    LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		UPDATE university_logo_intents AS i SET
		    claim_token = gen_random_uuid(),
		    lease_until = statement_timestamp() + interval '30 seconds',
		    updated_at = statement_timestamp()
		FROM ready WHERE i.id = ready.id
		RETURNING i.id::text, i.file_id::text, i.actor_user_id::text,
		          i.version, i.claim_token::text, i.attempts`, applied).Scan(
		&intent.UniversityID, &intent.FileID, &intent.ActorID,
		&intent.Version, &intent.ClaimToken, &intent.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.LogoBindingIntent{}, false, nil
	}
	if err != nil {
		return domain.LogoBindingIntent{}, false, fmt.Errorf("claim university logo binding: %w", err)
	}

	return intent, true, nil
}

func (r *Postgres) CompleteLogoBinding(ctx context.Context, intent domain.LogoBindingIntent) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE university_logo_intents SET
		    applied_at = statement_timestamp(),
		    next_attempt_at = statement_timestamp() + CASE
		        WHEN applied_at IS NULL THEN interval '1 minute'
		        ELSE interval '24 hours'
		    END,
		    attempts = 0,
		    claim_token = NULL,
		    lease_until = NULL,
		    updated_at = statement_timestamp()
		WHERE id = $1 AND version = $2 AND claim_token = $3`, intent.UniversityID, intent.Version, intent.ClaimToken)
	if err != nil {
		return false, fmt.Errorf("complete university logo binding: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

func (r *Postgres) RepairLogoBinding(ctx context.Context, intent domain.LogoBindingIntent, observedVersion int64) (bool, error) {
	if observedVersion < 0 || max(intent.Version, observedVersion) == math.MaxInt64 {
		return false, errors.New("invalid media logo binding version")
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE university_logo_intents SET
		    version = GREATEST(version, $4::bigint) + 1,
		    attempts = 0,
		    next_attempt_at = statement_timestamp(),
		    claim_token = NULL,
		    lease_until = NULL,
		    applied_at = NULL,
		    updated_at = statement_timestamp()
		WHERE id = $1 AND version = $2 AND claim_token = $3`,
		intent.UniversityID, intent.Version, intent.ClaimToken, observedVersion)
	if err != nil {
		return false, fmt.Errorf("queue repaired university logo binding: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

func (r *Postgres) RetryLogoBinding(ctx context.Context, intent domain.LogoBindingIntent, delay time.Duration) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE university_logo_intents SET
		    attempts = LEAST(attempts + 1, 30),
		    next_attempt_at = statement_timestamp() + ($4::bigint * interval '1 millisecond'),
		    claim_token = NULL,
		    lease_until = NULL,
		    updated_at = statement_timestamp()
		WHERE id = $1 AND version = $2 AND claim_token = $3`,
		intent.UniversityID, intent.Version, intent.ClaimToken, delay.Milliseconds())
	if err != nil {
		return false, fmt.Errorf("retry university logo binding: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}
