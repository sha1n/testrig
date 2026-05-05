package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"reflect"

	"github.com/spf13/viper"
)

// Indirected for tests so main can be exercised end-to-end without binding a
// fixed port, blocking forever, or terminating the test process.
var (
	listenAndServe = http.Serve
	listen         = net.Listen
	exit           = os.Exit
)

type Config struct {
	AppPort     int    `mapstructure:"APP_PORT"`
	DatabaseURL string `mapstructure:"DATABASE_URL"`
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

	if cfg.AppPort == 0 {
		return nil, fmt.Errorf("APP_PORT is required")
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
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

// newHandler returns the HTTP handler the example serves. Extracted so tests
// can exercise it without binding a real port.
func newHandler(cfg *Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK - " + cfg.DatabaseURL))
	})
	return mux
}

// run encapsulates the bootstrap so main is a thin shim and tests can drive
// the full path with overridden listen/serve/exit hooks.
func run() int {
	cfg, err := LoadConfig(nil)
	if err != nil {
		log.Printf("Config error: %v", err)
		return 1
	}
	ln, err := listen("tcp", fmt.Sprintf(":%d", cfg.AppPort))
	if err != nil {
		log.Printf("listen error: %v", err)
		return 1
	}
	log.Printf("Starting server on port %d...", cfg.AppPort)
	if err := listenAndServe(ln, newHandler(cfg)); err != nil && err != http.ErrServerClosed {
		log.Printf("serve error: %v", err)
		return 1
	}
	return 0
}

func main() { exit(run()) }
