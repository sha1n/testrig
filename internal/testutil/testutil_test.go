package testutil

import (
	"testing"
	"time"

	"github.com/sha1n/testrig"
	"github.com/stretchr/testify/assert"
)

func TestMockEnvContext_Get(t *testing.T) {
	m := &MockEnvContext{
		Props: testrig.Properties{
			"key1": "value1",
		},
	}

	val, ok := m.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", val)

	val, ok = m.Get("key2")
	assert.False(t, ok)
	assert.Equal(t, "", val)
}

func TestMockEnvContext_Get_NilProps(t *testing.T) {
	m := &MockEnvContext{}

	val, ok := m.Get("key1")
	assert.False(t, ok)
	assert.Equal(t, "", val)
}

func TestMockEnvContext_Properties(t *testing.T) {
	m := &MockEnvContext{
		Props: testrig.Properties{
			"key1": "value1",
			"key2": "value2",
		},
	}

	props := m.Properties()
	assert.Equal(t, m.Props, props)

	// Ensure it's a copy
	props["key3"] = "value3"
	_, ok := m.Props["key3"]
	assert.False(t, ok)
}

func TestMockEnvContext_Properties_NilProps(t *testing.T) {
	m := &MockEnvContext{}

	props := m.Properties()
	assert.NotNil(t, props)
	assert.Empty(t, props)
}

func TestMockEnvContext_Logger(t *testing.T) {
	m := &MockEnvContext{}
	assert.NotNil(t, m.Logger())
}

func TestMockEnvContext_TypedHelpers(t *testing.T) {
	m := &MockEnvContext{
		Props: testrig.Properties{
			"int":      "42",
			"bool":     "true",
			"duration": "1s",
		},
	}

	intVal, err := m.Int("int")
	assert.NoError(t, err)
	assert.Equal(t, 42, intVal)

	_, err = m.Int("missing")
	assert.Error(t, err)

	boolVal, err := m.Bool("bool")
	assert.NoError(t, err)
	assert.True(t, boolVal)

	_, err = m.Bool("missing")
	assert.Error(t, err)

	durVal, err := m.Duration("duration")
	assert.NoError(t, err)
	assert.Equal(t, time.Second, durVal)

	_, err = m.Duration("missing")
	assert.Error(t, err)
}
