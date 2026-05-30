package api_test

import (
	"log/slog"
	"testing"

	"github.com/sha1n/testrig/api"
	"github.com/stretchr/testify/assert"
)

func TestStubEnvHandle_ReturnsProvidedValues(t *testing.T) {
	logger := slog.Default()
	props := api.Properties{"k": "v"}

	h := api.StubEnvHandle("env-name", logger, props)

	assert.Equal(t, "env-name", h.Name())
	assert.Equal(t, logger, h.Logger())
	assert.Equal(t, props, h.Properties())
}

func TestStubEnvHandle_NilLoggerFallsBackToDefault(t *testing.T) {
	h := api.StubEnvHandle("env", nil, nil)
	assert.NotNil(t, h.Logger())
}

func TestStubEnvHandle_NilPropertiesYieldsEmpty(t *testing.T) {
	h := api.StubEnvHandle("env", nil, nil)
	assert.NotNil(t, h.Properties())
	assert.Empty(t, h.Properties())
}

func TestStubEnvHandle_PropertiesIsSnapshot(t *testing.T) {
	original := api.Properties{"k": "v"}
	h := api.StubEnvHandle("env", nil, original)

	original["k"] = "mutated"
	assert.Equal(t, "v", h.Properties()["k"])
}

func TestStubEnvHandle_InputMutationDoesNotLeak(t *testing.T) {
	input := api.Properties{"k": "v"}
	h := api.StubEnvHandle("env", nil, input)

	h.Properties()["k"] = "mutated"
	assert.Equal(t, "v", h.Properties()["k"])
}
