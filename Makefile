LOCAL_BIN := $(CURDIR)/bin
GOLANGCI_LINT := $(LOCAL_BIN)/golangci-lint
DATABASE_URL ?= postgres://entities:entities@localhost:5432/entities?sslmode=disable

.PHONY: run build test lint migrate-up migrate-down tidy

# Запуск сервиса на хосте
run:
	go run ./cmd/entities

# Сборка сервиса
build:
	go build ./...

# Запуск unit/API тестов
test:
	go test -race ./...

# Проверка линтером
lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run

# Миграции через parker (goose внутри бинарника entities)
migrate-up:
	go run ./cmd/entities migrate --dir migrations --dsn "$(DATABASE_URL)" up

# Откатить миграцию
migrate-down:
	go run ./cmd/entities migrate --dir migrations --dsn "$(DATABASE_URL)" down

# Обновление go mod
tidy:
	go mod tidy

$(GOLANGCI_LINT):
	GOBIN="$(LOCAL_BIN)" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.1.6
