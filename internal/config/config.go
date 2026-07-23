package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config объединяет runtime-настройки Ironhide.
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

// Load читает конфигурацию Ironhide из environment и валидирует обязательные значения.
func Load() (Config, error) {
	cfg := Config{
		ServiceName:    env("IRONHIDE_SERVICE_NAME", "ironhide"),
		HTTPAddress:    env("IRONHIDE_HTTP_ADDR", ":8080"),
		DatabaseURL:    env("IRONHIDE_DATABASE_URL", "postgres://ironhide:ironhide@localhost:5432/ironhide?sslmode=disable"),
		LogLevel:       env("IRONHIDE_LOG_LEVEL", "info"),
		Environment:    env("IRONHIDE_ENV", "local"),
		RequestLogPath: env("IRONHIDE_REQUEST_LOG_PATH", ""),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   20 * time.Second,
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return Config{}, fmt.Errorf("IRONHIDE_DATABASE_URL не задан")
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
