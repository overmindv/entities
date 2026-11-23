package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/overmindv/entities/internal/outbox"
)

func runOutboxReconcile(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("outbox-reconcile", flag.ContinueOnError)
	flags.SetOutput(stderr)
	after := flags.Int64("after-revision", 0, "last verified catalog revision")
	limit := flags.Int("limit", outbox.MaxReconcilePage, "maximum number of catalog revisions to read")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}
	if flags.NArg() != 0 || *after < 0 || *limit < 1 || *limit > outbox.MaxReconcilePage {
		_, _ = fmt.Fprintln(stderr, "invalid catalog reconciliation bounds")

		return 2
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		_, _ = fmt.Fprintln(stderr, "DATABASE_URL is required")

		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "invalid PostgreSQL configuration")

		return 1
	}
	defer pool.Close()
	report, err := outbox.Reconcile(ctx, pool, *after, *limit)
	if err != nil {
		if errors.Is(err, outbox.ErrReconcileBounds) {
			_, _ = fmt.Fprintln(stderr, "revision is ahead of the catalog")

			return 2
		}
		_, _ = fmt.Fprintln(stderr, "could not reconcile the catalog outbox")

		return 1
	}
	if err := json.NewEncoder(stdout).Encode(report); err != nil || report.FirstGap != 0 {
		return 1
	}

	return 0
}
