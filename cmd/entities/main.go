package main

import (
	"os"

	"github.com/overmindv/entities/internal/httpapi"
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

// run открывает PostgreSQL, собирает service и регистрирует REST-роуты на parker.
func run(app *parker.App) error {
	pool, err := app.Postgres() // добавляет health-чек "postgres" в /ready
	if err != nil {
		return err
	}

	store := repository.New(pool)
	catalog := service.New(store)
	httpapi.Register(app.HTTP(), catalog, app.Logger())
	return nil
}
