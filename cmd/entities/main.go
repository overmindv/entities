package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/overmindv/entities/internal/config"
	"github.com/overmindv/entities/internal/httpapi"
	"github.com/overmindv/entities/internal/repository"
	"github.com/overmindv/entities/internal/service"
)

// main загружает конфигурацию, открывает PostgreSQL pool и запускает HTTP API Entities.
func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("не удалось загрузить конфигурацию", "error", err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)}))
	requestLog, closeRequestLog, err := requestLogger(cfg.RequestLogPath, logger)
	if err != nil {
		logger.Error("не удалось создать request logger", "error", err)
		os.Exit(1)
	}
	defer closeRequestLog()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("не удалось создать пул PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	store := repository.New(pool)
	catalog := service.New(store)
	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           httpapi.New(catalog, logger, requestLog),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		logger.Info("Entities запущен", "address", cfg.HTTPAddress, "environment", cfg.Environment)
		if listenErr := server.ListenAndServe(); listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			logger.Error("HTTP-сервер завершился с ошибкой", "error", listenErr)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("не удалось корректно остановить HTTP-сервер", "error", err)
	}
}

// requestLogger создаёт отдельный JSON-логгер для HTTP-запросов Entities.
func requestLogger(path string, fallback *slog.Logger) (*slog.Logger, func(), error) {
	if path == "" {
		return fallback, func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, fmt.Errorf("создать директорию request log: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("открыть request log: %w", err)
	}

	return slog.New(slog.NewJSONHandler(file, nil)), func() { _ = file.Close() }, nil
}

// logLevel преобразует строковое значение конфигурации в slog level.
func logLevel(value string) slog.Level {
	switch value {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
