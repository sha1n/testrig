package testrig

import (
	"sort"
	"testing"
)

// InjectIntoEnv sets the properties as OS environment variables and automatically
// restores the original values when the test ends. Keys are processed in sorted
// order for deterministic behavior.
//
// This function uses t.Setenv internally, which means it will panic if the test
// has already called t.Parallel(). For parallel-safe tests, pass env.Properties()
// directly to your config library's native API (e.g., viper.Set, koanf confmap.Provider)
// instead of mutating OS environment variables.
func InjectIntoEnv(t *testing.T, props Properties) {
	t.Helper()

	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		t.Setenv(k, props[k])
	}
}
