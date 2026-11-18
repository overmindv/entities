package catalogsnapshot

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSnapshotCursor(t *testing.T) {
	for _, key := range []graphKey{
		{kind: 1, topic: uuid.New()},
		{kind: 2, topic: uuid.New(), target: uuid.New()},
	} {
		if got, version, err := parseCursor(key.cursor(17)); err != nil || got != key || version != 17 {
			t.Fatal("cursor did not round trip", got, version, err)
		}
	}
	invalidRecord := graphKey{kind: 1, topic: uuid.New(), target: uuid.New()}
	for _, input := range []string{
		"wrong", strings.Repeat("a", 1024), invalidRecord.cursor(1),
		graphKey{kind: 1, topic: uuid.New()}.cursor(-1),
		base64.RawURLEncoding.EncodeToString(make([]byte, cursorSize)),
	} {
		if _, _, err := parseCursor(input); !errors.Is(err, ErrInvalidPage) {
			t.Fatal("malformed cursor accepted")
		}
	}
}

func TestStoreRejectsUnpinnedCursor(t *testing.T) {
	page := SnapshotRequest{Cursor: graphKey{kind: 1, topic: uuid.New()}.cursor(17), Limit: 20}
	if _, err := New(nil).Snapshot(t.Context(), page); !errors.Is(err, ErrInvalidPage) {
		t.Fatal("cursor accepted without a watermark", err)
	}
	other := int64(18)
	page.Watermark = &other
	if _, err := New(nil).Snapshot(t.Context(), page); !errors.Is(err, ErrInvalidPage) {
		t.Fatal("cursor accepted on another watermark", err)
	}
}
