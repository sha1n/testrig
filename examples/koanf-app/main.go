package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/v2"
)

// Indirected for tests so main can be exercised end-to-end without binding a
// fixed port, blocking forever, or terminating the test process.
var (
	listenAndServe = http.Serve
	listen         = net.Listen
	exit           = os.Exit
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
