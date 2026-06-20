// Package config holds process-level configuration. Only bootstrap settings
// (listen address, DB/Redis connection, log level) come from the environment;
// all runtime behavior (providers, models, profiles, routing) lives in the
// database and is managed via the admin web UI. See docs/02-modules.md.
package config

import (
	"os"
	"strings"
)

// Config is the bootstrap configuration loaded from environment variables.
type Config struct {
	HTTPAddr      string
	PostgresDSN   string
	RedisAddr     string
	RedisPassword string
	AdminToken    string // bearer token for /api/admin; empty disables admin
	LogLevel      string
}

// Load reads configuration from the environment with sensible defaults.
func Load() Config {
	return Config{
		HTTPAddr:      getenv("GATEWAY_HTTP_ADDR", ":8080"),
		PostgresDSN:   getenv("GATEWAY_POSTGRES_DSN", "postgres://gateway:gateway@localhost:5432/aihub?sslmode=disable"),
		RedisAddr:     getenv("GATEWAY_REDIS_ADDR", "localhost:6379"),
		RedisPassword: getenv("GATEWAY_REDIS_PASSWORD", ""),
		AdminToken:    getenv("GATEWAY_ADMIN_TOKEN", ""),
		LogLevel:      strings.ToLower(getenv("GATEWAY_LOG_LEVEL", "info")),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
