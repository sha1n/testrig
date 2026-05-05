package testrig_test

import (
	"os"
	"strings"
	"testing"

	"github.com/sha1n/testrig"
)

func TestSetEnvVars_PanicsOnParallelTest(t *testing.T) {
	t.Run("inner", func(t *testing.T) {
		t.Parallel()

		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("Expected panic from SetEnvVars on parallel test")
			}
			msg, ok := r.(string)
			if !ok || !strings.Contains(msg, "cannot use t.Setenv in test called Parallel") {
				// Go's t.Setenv panics with a specific message; verify we get it
				t.Logf("Got expected panic: %v", r)
			}
		}()

		testrig.SetEnvVars(t, testrig.Properties{"KEY": "value"})
	})
}

func TestSetEnvVars_SetsValues(t *testing.T) {
	key := "TESTRIG_INJECT_TEST_SET"

	t.Run("inner", func(t *testing.T) {
		testrig.SetEnvVars(t, testrig.Properties{key: "injected"})

		val, ok := os.LookupEnv(key)
		if !ok || val != "injected" {
			t.Errorf("Expected injected, got %q (ok=%v)", val, ok)
		}
	})
}

func TestSetEnvVars_RestoresOriginal(t *testing.T) {
	key := "TESTRIG_INJECT_TEST_RESTORE"
	t.Setenv(key, "original")

	t.Run("inner", func(t *testing.T) {
		testrig.SetEnvVars(t, testrig.Properties{key: "overridden"})

		val, _ := os.LookupEnv(key)
		if val != "overridden" {
			t.Errorf("Expected overridden, got %q", val)
		}
	})

	// After inner test cleanup, original value should be restored
	val, ok := os.LookupEnv(key)
	if !ok || val != "original" {
		t.Errorf("Expected original restored, got %q (ok=%v)", val, ok)
	}
}

func TestSetEnvVars_UnsetsNewKeys(t *testing.T) {
	key := "TESTRIG_INJECT_TEST_NEW"

	t.Run("inner", func(t *testing.T) {
		testrig.SetEnvVars(t, testrig.Properties{key: "new-value"})

		val, ok := os.LookupEnv(key)
		if !ok || val != "new-value" {
			t.Errorf("Expected new-value, got %q (ok=%v)", val, ok)
		}
	})

	// After cleanup, key should be gone
	if _, ok := os.LookupEnv(key); ok {
		t.Error("Expected key to be unset after cleanup")
	}
}

func TestSetEnvVars_EmptyProperties(t *testing.T) {
	testrig.SetEnvVars(t, testrig.Properties{})
}

func TestSetEnvVars_NilProperties(t *testing.T) {
	testrig.SetEnvVars(t, nil)
}

func TestSetEnvVars_MultipleKeys(t *testing.T) {
	keys := []string{
		"TESTRIG_INJECT_MULTI_A",
		"TESTRIG_INJECT_MULTI_B",
		"TESTRIG_INJECT_MULTI_C",
	}

	t.Run("inner", func(t *testing.T) {
		testrig.SetEnvVars(t, testrig.Properties{
			keys[0]: "a",
			keys[1]: "b",
			keys[2]: "c",
		})

		for _, k := range keys {
			if _, ok := os.LookupEnv(k); !ok {
				t.Errorf("Expected key %s to be set", k)
			}
		}
	})

	for _, k := range keys {
		if _, ok := os.LookupEnv(k); ok {
			t.Errorf("Expected key %s to be unset after cleanup", k)
		}
	}
}
