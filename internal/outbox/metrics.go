package outbox

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const MetricsPath = "/internal/metrics/outbox"

type Monitor struct {
	outbox   *CatalogOutbox
	registry *prometheus.Registry
	lag      prometheus.Gauge
	up       prometheus.Gauge
}

func NewMonitor(store *CatalogOutbox) *Monitor {
	m := &Monitor{
		outbox:   store,
		registry: prometheus.NewRegistry(),
		lag: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "entities_outbox_oldest_pending_seconds",
			Help: "Age of the oldest unpublished catalog event.",
		}),
		up: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "entities_outbox_lag_sample_ok",
			Help: "Whether the last outbox age query succeeded.",
		}),
	}
	m.registry.MustRegister(m.lag, m.up, store.publishFailures)

	return m
}

func (m *Monitor) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{MaxRequestsInFlight: 2})
}

func (m *Monitor) Run(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		m.sample(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}

	return nil
}

func (m *Monitor) sample(ctx context.Context) {
	query, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var seconds float64
	if err := m.outbox.pool.QueryRow(query, `
		SELECT GREATEST(0, EXTRACT(EPOCH FROM statement_timestamp() -
		    (SELECT occurred_at FROM outbox_events WHERE published_at IS NULL ORDER BY occurred_at LIMIT 1)))::float8`).
		Scan(&seconds); err != nil {
		m.up.Set(0)

		return
	}
	m.lag.Set(seconds)
	m.up.Set(1)
}
