// Package config loads the application settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const minJWTSecretLen = 32

type Config struct {
	Port            string        // PORT, default 8080
	DatabaseURL     string        // DATABASE_URL, required
	CORSOrigins     []string      // CORS_ORIGINS, comma-separated, default http://localhost:3000
	JWTSecret       []byte        // JWT_SECRET, required, at least 32 bytes
	AccessTokenTTL  time.Duration // ACCESS_TOKEN_TTL, default 15m
	RefreshTokenTTL time.Duration // REFRESH_TOKEN_TTL, default 720h (30 days)
	CookieSecure    bool          // COOKIE_SECURE, default true
}

func Load() (Config, error) {
	var errs []error

	cfg := Config{
		Port:        getenv("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		CORSOrigins: splitList(getenv("CORS_ORIGINS", "http://localhost:3000")),
		JWTSecret:   []byte(os.Getenv("JWT_SECRET")),
	}
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if len(cfg.JWTSecret) < minJWTSecretLen {
		errs = append(errs, fmt.Errorf("JWT_SECRET is required and must be at least %d bytes", minJWTSecretLen))
	}

	var err error
	if cfg.AccessTokenTTL, err = getDuration("ACCESS_TOKEN_TTL", 15*time.Minute); err != nil {
		errs = append(errs, err)
	}
	if cfg.RefreshTokenTTL, err = getDuration("REFRESH_TOKEN_TTL", 30*24*time.Hour); err != nil {
		errs = append(errs, err)
	}
	if cfg.CookieSecure, err = getBool("COOKIE_SECURE", true); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration like 15m or 720h", key)
	}
	return d, nil
}

func getBool(key string, fallback bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return b, nil
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
