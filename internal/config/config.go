// Package config reads settings from environment variables.
package config

import (
	"errors"
	"strings"
)

// DevDeckSecret matches the web app's local fallback so dev works out of the box.
const DevDeckSecret = "tsq-local-dev-only"

type Config struct {
	Port, DeckSecret, DatabaseURL, AppEnv string
	AllowedOrigins                        []string
}

func Load(getenv func(string) string) (Config, error) {
	get := func(k, def string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return def
	}
	c := Config{
		Port:        get("PORT", "8080"),
		DeckSecret:  get("DECK_SECRET", DevDeckSecret),
		DatabaseURL: get("DATABASE_URL", ""),
		AppEnv:      get("APP_ENV", "development"),
	}
	for _, o := range strings.Split(get("ALLOWED_ORIGINS", "localhost:3000,127.0.0.1:3200,localhost:3200"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.AllowedOrigins = append(c.AllowedOrigins, o)
		}
	}
	if c.AppEnv == "production" && (c.DeckSecret == DevDeckSecret || len(c.DeckSecret) < 32) {
		return c, errors.New("DECK_SECRET must be set (32+ chars) in production")
	}
	return c, nil
}
