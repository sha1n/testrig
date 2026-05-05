package main

import (
	"fmt"
	"log"
	"net/http"
	"reflect"

	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"
)

type Config struct {
	AppPort     int    `mapstructure:"APP_PORT" validate:"required"`
	DatabaseURL string `mapstructure:"DATABASE_URL" validate:"required"`
}

// LoadConfig creates a Config from environment variables, with optional
// overrides. Overrides take the highest precedence, followed by environment
// variables. Pass nil for production use (reads only from environment).
func LoadConfig(overrides map[string]string) (*Config, error) {
	v := viper.New()
	v.AutomaticEnv()
	bindEnvs(v, Config{})

	for k, val := range overrides {
		v.Set(k, val)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	validate := validator.New()
	if err := validate.Struct(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

func bindEnvs(v *viper.Viper, iface any) {
	for field := range reflect.TypeOf(iface).Fields() {
		envName := field.Tag.Get("mapstructure")
		if envName != "" {
			_ = v.BindEnv(envName)
		}
	}
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
