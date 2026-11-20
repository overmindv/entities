package main

import (
	"errors"
	"os"
	"time"

	"github.com/overmindv/entities/internal/catalogsnapshot"
	"github.com/overmindv/entities/internal/httpapi"
	"github.com/overmindv/entities/internal/outbox"
	"github.com/overmindv/entities/internal/repository"
	"github.com/overmindv/entities/internal/service"
	"github.com/overmindv/parker"
)

// main запускает Entities на каркасе parker: вся инфраструктура (конфиг, HTTP,
// postgres+миграции, логирование, метрики, graceful shutdown) — внутри parker,
// здесь только бизнес-логика (см. run).
func main() {
	os.Exit(parker.Main(run, parker.WithAppName("entities")))
}

// run открывает PostgreSQL, собирает service, регистрирует REST-роуты и
// запускает dispatcher outbox-событий каталога в Kafka (topic catalog.activity.v1).
func run(app *parker.App) error {
	pool, err := app.Postgres() // добавляет health-чек "postgres" в /ready
	if err != nil {
		return err
	}

	store := repository.New(pool)
	catalog := service.New(store)
	gatewayToken := os.Getenv("ENTITIES_GATEWAY_TOKEN")
	snapshotToken := os.Getenv("ENTITIES_SNAPSHOT_TOKEN")
	if gatewayToken != "" && gatewayToken == snapshotToken {
		return errors.New("ENTITIES_GATEWAY_TOKEN must differ from ENTITIES_SNAPSHOT_TOKEN")
	}
	if registerErr := httpapi.Register(app.HTTP(), catalog, service.New(store.PublicReads()), gatewayToken, app.Logger()); registerErr != nil {
		return registerErr
	}
	if registerErr := httpapi.RegisterCatalogSnapshot(app.HTTP(), catalogsnapshot.New(pool), snapshotToken); registerErr != nil {
		return registerErr
	}

	producer, err := app.NewProducer() // из parker opts (KAFKA_BOOTSTRAP_SERVERS)
	if err != nil {
		return err
	}
	app.AddHealthCheck("kafka", parker.HealthCheckFunc(producer.Ping))

	dispatcher := parker.NewOutboxDispatcher(
		outbox.NewCatalogOutbox(pool, env("KAFKA_CATALOG_ACTIVITY_TOPIC", "catalog.activity.v1")),
		producer, time.Second, app.Logger())
	app.AddRunnable("catalog-events-outbox", dispatcher.Run)

	return nil
}

// env возвращает переменную окружения или fallback.
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
