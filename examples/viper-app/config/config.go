// Package config loads the viper-app's typed configuration via Viper.
package config

import (
	"fmt"
	"reflect"

	"github.com/spf13/viper"
)

// Config is the typed application config. Production code reads it the
// same way demo/test code does — Viper handles the source.
type Config struct {
	AppPort     int    `mapstructure:"APP_PORT"`
	DatabaseURL string `mapstructure:"DATABASE_URL"`
	RemoteURL   string `mapstructure:"REMOTE_URL"`
}

// Load builds a Config using Viper. Environment variables are read first;
// `overrides` (typically env.Properties()) layer on top via v.Set, which
// is Viper's highest-precedence source.
func Load(overrides map[string]string) (*Config, error) {
	v := viper.New()
	v.AutomaticEnv()
	bindEnvs(v, Config{})

	for k, val := range overrides {
		v.Set(k, val)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
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

func bindEnvs(v *viper.Viper, iface any) {
	t := reflect.TypeOf(iface)
	for i := 0; i < t.NumField(); i++ {
		envName := t.Field(i).Tag.Get("mapstructure")
		if envName != "" {
			_ = v.BindEnv(envName)
		}
	}
}
