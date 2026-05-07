// Package config loads the koanf-app's typed configuration via koanf.
package config

import (
	"fmt"
	"strings"

	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/v2"
)

// Config is the typed application config.
type Config struct {
	AppPort     int    `koanf:"app_port"`
	DatabaseURL string `koanf:"database_url"`
	RemoteURL   string `koanf:"remote_url"`
}

// Load builds a Config using koanf. Environment variables load first;
// `overrides` (typically env.Properties()) layer on top via the confmap
// provider so they win.
func Load(overrides map[string]string) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(env.Provider("", ".", strings.ToLower), nil); err != nil {
		return nil, fmt.Errorf("load env: %w", err)
	}

	if len(overrides) > 0 {
		m := make(map[string]any, len(overrides))
		for key, val := range overrides {
			m[strings.ToLower(key)] = val
		}
		if err := k.Load(confmap.Provider(m, "."), nil); err != nil {
			return nil, fmt.Errorf("load overrides: %w", err)
		}
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if cfg.AppPort == 0 {
		return nil, fmt.Errorf("APP_PORT is required")
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.RemoteURL == "" {
		return nil, fmt.Errorf("REMOTE_URL is required")
	}
	return &cfg, nil
}
