package outbox

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/overmindv/entities/internal/catalogsnapshot"
)

type feedRevisions struct {
	pgx.Rows
	revisions []int64
	next      int
}

func (r *feedRevisions) Next() bool {
	if r.next == len(r.revisions) {
		return false
	}
	r.next++

	return true
}

func (r *feedRevisions) Scan(fields ...any) error {
	*fields[0].(*int64) = r.revisions[r.next-1]
	*fields[3].(*string) = catalogPrerequisiteAdded
	*fields[6].(*string) = catalogTopicKind

	return nil
}

func (r *feedRevisions) Err() error { return nil }

func TestFeedReplayRejectsRevisionGaps(t *testing.T) {
	t.Parallel()

	req := catalogsnapshot.ChangeRequest{After: 4, Limit: 2}
	for _, revisions := range [][]int64{{6, 7}, {5}, {5, 6, 8}, nil} {
		page, err := readFeedChanges(&feedRevisions{revisions: revisions}, req, 9)
		if !errors.Is(err, ErrFeedHistoryGap) || page.Through != 0 {
			t.Fatal("partial catalog history was returned", revisions, page, err)
		}
	}
}

func TestFeedReplayAdvancesPastPrerequisites(t *testing.T) {
	t.Parallel()

	req := catalogsnapshot.ChangeRequest{After: 4, Limit: 2}
	page, err := readFeedChanges(&feedRevisions{revisions: []int64{5, 6, 7}}, req, 7)
	if err != nil || page.Through != 6 || page.Watermark != 7 || !page.More || len(page.Events) != 0 {
		t.Fatal("graph-only events stalled the cursor", page, err)
	}
	req.After = page.Through
	page, err = readFeedChanges(&feedRevisions{revisions: []int64{7}}, req, 7)
	if err != nil || page.Through != 7 || page.More || len(page.Events) != 0 {
		t.Fatal("last graph-only page did not finish", page, err)
	}
}
