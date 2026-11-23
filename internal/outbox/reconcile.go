package outbox

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const MaxReconcilePage = 1000

var ErrReconcileBounds = errors.New("invalid catalog reconciliation bounds")

type Report struct {
	After    int64 `json:"after_revision"`
	Through  int64 `json:"through_revision"`
	Head     int64 `json:"head_revision"`
	FirstGap int64 `json:"first_missing_revision,omitempty"`
	Sent     int   `json:"published"`
	Ready    int   `json:"ready"`
	Leased   int   `json:"leased"`
	Delayed  int   `json:"delayed"`
}

// Reconcile сверяет одну страницу журнала с ревизией каталога в одном снимке.
func Reconcile(ctx context.Context, pool *pgxpool.Pool, after int64, limit int) (Report, error) {
	if after < 0 || limit < 1 || limit > MaxReconcilePage {
		return Report{}, ErrReconcileBounds
	}
	report := Report{After: after, Through: after}
	err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT revision FROM catalog_clock WHERE singleton").Scan(&report.Head); err != nil {
			return err
		}
		if report.After > report.Head {
			return ErrReconcileBounds
		}
		rows, err := tx.Query(ctx, `
			SELECT catalog_revision, published_at IS NOT NULL,
			       COALESCE(lease_until>statement_timestamp(),false), next_attempt_at>statement_timestamp()
			FROM outbox_events WHERE catalog_revision>$1
			ORDER BY catalog_revision LIMIT $2`, after, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var revision int64
			var sent, leased, delayed bool
			if scanErr := rows.Scan(&revision, &sent, &leased, &delayed); scanErr != nil {
				return scanErr
			}
			if report.FirstGap == 0 && revision-report.Through > 1 {
				report.FirstGap = report.Through + 1
			}
			report.Through = revision
			count++
			switch {
			case sent:
				report.Sent++
			case leased:
				report.Leased++
			case delayed:
				report.Delayed++
			default:
				report.Ready++
			}
		}
		if count < limit && report.Through < report.Head && report.FirstGap == 0 {
			report.FirstGap = report.Through + 1
		}

		return rows.Err()
	})
	if err != nil {
		return Report{}, err
	}

	return report, nil
}
