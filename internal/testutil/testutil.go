package testutil

import (
	"log/slog"
	"time"

	"github.com/sha1n/testrig"
)

// MockEnvContext is a mock implementation of testrig.EnvContext for testing purposes.
type MockEnvContext struct {
	Props testrig.Properties
}

// Get returns the value for the given key if it exists in the mock properties.
func (m *MockEnvContext) Get(key string) (string, bool) {
	if m.Props == nil {
		return "", false
	}
	val, ok := m.Props[key]
	return val, ok
}

// Properties returns a copy of all properties in the mock.
func (m *MockEnvContext) Properties() testrig.Properties {
	if m.Props == nil {
		return make(testrig.Properties)
	}
	p := make(testrig.Properties)
	for k, v := range m.Props {
		p[k] = v
	}
	return p
}

// Int returns the value for the given key as an int.
func (m *MockEnvContext) Int(key string) (int, error) {
	return m.Properties().Int(key)
}

// Bool returns the value for the given key as a bool.
func (m *MockEnvContext) Bool(key string) (bool, error) {
	return m.Properties().Bool(key)
}

// Duration returns the value for the given key as a time.Duration.
func (m *MockEnvContext) Duration(key string) (time.Duration, error) {
	return m.Properties().Duration(key)
}

// Logger returns a default logger.
func (m *MockEnvContext) Logger() *slog.Logger {
	return slog.Default()
}
