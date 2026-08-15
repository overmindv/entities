package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config объединяет runtime-настройки Entities.
type Config struct {
	ServiceName    string
	HTTPAddress    string
	DatabaseURL    string
	LogLevel       string
	Environment    string
	RequestLogPath string
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
}

// Load читает конфигурацию Entities из environment и валидирует обязательные значения.
func Load() (Config, error) {
	cfg := Config{
		ServiceName:    env("ENTITIES_SERVICE_NAME", "entities"),
		HTTPAddress:    env("ENTITIES_HTTP_ADDR", ":8080"),
		DatabaseURL:    env("ENTITIES_DATABASE_URL", "postgres://entities:entities@localhost:5432/entities?sslmode=disable"),
		LogLevel:       env("ENTITIES_LOG_LEVEL", "info"),
		Environment:    env("ENTITIES_ENV", "local"),
		RequestLogPath: env("ENTITIES_REQUEST_LOG_PATH", ""),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   20 * time.Second,
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return Config{}, fmt.Errorf("ENTITIES_DATABASE_URL не задан")
	}

	return cfg, nil
}

// env возвращает значение environment variable или fallback.
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
