package repository

import (
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/overmindv/entities/internal/domain"
)

func BenchmarkTopicWrite(b *testing.B) {
	benchmarkTopicWrite(b, nil)
}

func BenchmarkTopicWriteFullMetadata(b *testing.B) {
	minutes := 180
	metadata := &domain.TopicLearningMetadata{
		EstimatedMinutes: &minutes,
		Outcomes:         make([]string, 8),
	}
	for i := range metadata.Outcomes {
		metadata.Outcomes[i] = strconv.Itoa(i) + strings.Repeat("x", 255)
	}
	benchmarkTopicWrite(b, metadata)
}

func benchmarkTopicWrite(b *testing.B, metadata *domain.TopicLearningMetadata) {
	store := New(testCatalogPool(b, ""))
	actor := uuid.NewString()
	topic := domain.Topic{
		Title:            "benchmark",
		Slug:             actor + "-0",
		Difficulty:       "basic",
		Status:           domain.StatusActive,
		CreatedBy:        actor,
		LearningMetadata: metadata,
	}
	if _, err := store.CreateTopic(b.Context(), topic); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for n := 1; b.Loop(); n++ {
		topic.Slug = actor + "-" + strconv.Itoa(n)
		if _, err := store.CreateTopic(b.Context(), topic); err != nil {
			b.Fatal(err)
		}
	}
}
