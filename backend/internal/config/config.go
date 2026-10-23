// Package config loads the application settings from environment variables.
package config

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	Port        string   // PORT, default 8080
	DatabaseURL string   // DATABASE_URL, required
	CORSOrigins []string // CORS_ORIGINS, comma-separated, default http://localhost:3000
}

func Load() (Config, error) {
	cfg := Config{
		Port:        getenv("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		CORSOrigins: splitList(getenv("CORS_ORIGINS", "http://localhost:3000")),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitList(v string) []string {
	var items []string
	for item := range strings.SplitSeq(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}
