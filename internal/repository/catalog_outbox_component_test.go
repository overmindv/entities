package repository

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/overmindv/entities/internal/domain"
	"github.com/overmindv/entities/internal/outbox"
	"github.com/overmindv/parker"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

//nolint:paralleltest // Проверяет отправку из выделенной БД с отдельным брокером.
func TestCatalogOutboxKafkaRecovery(t *testing.T) {
	broker := os.Getenv("ENTITIES_TEST_KAFKA_ADDR")
	if broker == "" {
		t.Skip("ENTITIES_TEST_KAFKA_ADDR is not set")
	}
	pool := testCatalogPool(t, "")
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	topic := "catalog.recovery." + uuid.NewString()
	reader, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
		topic: {0: kgo.NewOffset().AtStart()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	create := kmsg.NewPtrCreateTopicsRequest()
	create.Topics = []kmsg.CreateTopicsRequestTopic{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}}
	response, err := create.RequestWith(ctx, reader)
	if err != nil || len(response.Topics) != 1 || response.Topics[0].ErrorCode != 0 {
		t.Fatal("could not create test topic:", err)
	}
	var before int64
	if readErr := pool.QueryRow(ctx, "SELECT revision FROM catalog_clock WHERE singleton").Scan(&before); readErr != nil {
		t.Fatal(readErr)
	}
	item, err := New(pool).CreateTopic(ctx, domain.Topic{
		Title:      "Recovery fixture",
		Slug:       uuid.NewString(),
		Status:     domain.StatusDraft,
		Difficulty: "basic",
		CreatedBy:  uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var eventID string
	if readErr := pool.QueryRow(ctx, "SELECT id::text FROM outbox_events WHERE catalog_revision=$1", before+1).Scan(&eventID); readErr != nil {
		t.Fatal(readErr)
	}
	events := outbox.NewCatalogOutbox(pool, topic)
	assertReport := func(ready, delayed, sent int) {
		t.Helper()
		report, reportErr := outbox.Reconcile(ctx, pool, before, 1)
		if reportErr != nil || report.FirstGap != 0 || report.Through != before+1 ||
			report.Ready != ready || report.Delayed != delayed || report.Sent != sent {
			t.Fatal("unexpected outbox report:", report, reportErr)
		}
	}
	assertReport(1, 0, 0)
	lc := net.ListenConfig{}
	listener, err := lc.Listen(ctx, "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unavailable := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	stopFailure := startTestDispatcher(t, ctx, events, unavailable)
	waitOutbox(t, ctx, func() bool {
		var delayed bool
		if err := pool.QueryRow(ctx, `
			SELECT attempts>0 AND published_at IS NULL AND claim_token IS NULL AND next_attempt_at>now()
			FROM outbox_events WHERE id=$1`, eventID).Scan(&delayed); err != nil {
			t.Fatal(err)
		}

		return delayed
	})
	stopFailure()
	assertReport(0, 1, 0)

	stop := startTestDispatcher(t, ctx, events, broker)
	waitOutbox(t, ctx, func() bool {
		var sent bool
		if err := pool.QueryRow(ctx, "SELECT published_at IS NOT NULL AND attempts>=2 FROM outbox_events WHERE id=$1",
			eventID).Scan(&sent); err != nil {
			t.Fatal(err)
		}

		return sent
	})
	stop()
	assertReport(0, 0, 1)
	fetch := reader.PollRecords(ctx, 1)
	if fetch.Err() != nil || fetch.NumRecords() != 1 {
		t.Fatal("expected the recovered Kafka event:", fetch.Err())
	}
	record := fetch.Records()[0]
	var envelope struct {
		ID       string `json:"event_id"`
		Revision int64  `json:"catalog_revision"`
	}
	if err := json.Unmarshal(record.Value, &envelope); err != nil || envelope.ID != eventID ||
		envelope.Revision != before+1 || string(record.Key) != item.ID {
		t.Fatal("recovered event differs from catalog commit:", envelope, err)
	}
}

func startTestDispatcher(t *testing.T, ctx context.Context, events *outbox.CatalogOutbox, broker string) func() {
	t.Helper()
	p, err := parker.NewProducer([]string{broker})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := parker.NewOutboxDispatcher(events, p, 30*time.Millisecond, log)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(runCtx) }()
	stop := sync.OnceFunc(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(stop)

	return stop
}

func waitOutbox(t *testing.T, ctx context.Context, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatal("catalog dispatcher did not reach the expected state:", ctx.Err())
		case <-ticker.C:
		}
	}
}

func BenchmarkCatalogOutboxPage(b *testing.B) {
	ctx := b.Context()
	pool := testCatalogPool(b, "")
	actor := uuid.NewString()
	_, err := pool.Exec(ctx, `
		INSERT INTO outbox_events (event_type,actor_user_id,aggregate_id,aggregate_type,payload)
		SELECT 'catalog.fixture.created',$1,gen_random_uuid(),'fixture','{}'
		FROM generate_series(1,4096)`, actor)
	if err != nil {
		b.Fatal(err)
	}
	var leases bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT FROM pg_attribute WHERE attrelid='outbox_events'::regclass
		    AND attname='claim_token' AND NOT attisdropped)`).Scan(&leases); err != nil {
		b.Fatal(err)
	}
	store := outbox.NewCatalogOutbox(pool, "fixture.catalog")
	for b.Loop() {
		events, err := store.FetchPending(ctx, 100)
		if err != nil || len(events) != 100 {
			b.Fatal("unexpected outbox page:", len(events), err)
		}
		b.StopTimer()
		if leases {
			ids := make([]string, len(events))
			for i, event := range events {
				ids[i] = event.ID
			}
			if _, err := pool.Exec(ctx, `
				UPDATE outbox_events SET claim_token=NULL, lease_until=NULL WHERE id = ANY($1::uuid[])`, ids); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
	}
}
