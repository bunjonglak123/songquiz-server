package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != "8080" || c.DeckSecret != DevDeckSecret || c.DatabaseURL != "" {
		t.Fatalf("%+v", c)
	}
	if strings.Join(c.AllowedOrigins, ",") != "localhost:3000,127.0.0.1:3200,localhost:3200" {
		t.Fatalf("origins %v", c.AllowedOrigins)
	}
}

func TestProductionNeedsSecret(t *testing.T) {
	if _, err := Load(env(map[string]string{"APP_ENV": "production"})); err == nil {
		t.Fatal("default secret accepted in production")
	}
	if _, err := Load(env(map[string]string{"APP_ENV": "production", "DECK_SECRET": "short"})); err == nil {
		t.Fatal("short secret accepted in production")
	}
	if _, err := Load(env(map[string]string{"APP_ENV": "production", "DECK_SECRET": strings.Repeat("k", 32)})); err != nil {
		t.Fatal(err)
	}
}

func TestOriginsSplitTrim(t *testing.T) {
	c, _ := Load(env(map[string]string{"ALLOWED_ORIGINS": " a.vercel.app , ,localhost:3000 ", "PORT": "9000"}))
	if strings.Join(c.AllowedOrigins, "|") != "a.vercel.app|localhost:3000" || c.Port != "9000" {
		t.Fatalf("%+v", c)
	}
}
