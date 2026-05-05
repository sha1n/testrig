package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	AppPort     int    `koanf:"app_port"`
	DatabaseURL string `koanf:"database_url"`
}

// LoadConfig creates a Config from environment variables, with optional
// overrides. Overrides are loaded after environment variables, giving them
// higher precedence. Pass nil for production use (reads only from environment).
func LoadConfig(overrides map[string]string) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(env.Provider("", ".", func(s string) string {
		return strings.ToLower(s)
	}), nil); err != nil {
		return nil, fmt.Errorf("error loading env: %w", err)
	}

	if len(overrides) > 0 {
		m := make(map[string]interface{}, len(overrides))
		for key, val := range overrides {
			m[strings.ToLower(key)] = val
		}
		if err := k.Load(confmap.Provider(m, "."), nil); err != nil {
			return nil, fmt.Errorf("error loading overrides: %w", err)
		}
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling: %w", err)
	}

	if cfg.AppPort == 0 {
		return nil, fmt.Errorf("APP_PORT is required")
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	return &cfg, nil
}

func main() {
	cfg, err := LoadConfig(nil)
	if err != nil {
		log.Fatalf("Config error: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK - " + cfg.DatabaseURL))
	})

	log.Printf("Starting server on port %d...", cfg.AppPort)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.AppPort), mux); err != nil {
		log.Fatal(err)
	}
}
