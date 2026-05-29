package tools_test

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"
)

// TestCIMatrixMatchesGoWork enforces that .github/workflows/ci.yml stays in
// sync with go.work. go.work is the single source of truth for the multi-module
// workspace; every module listed there (except tools/, which has no testable
// code) must appear in the `module` matrix of both the lint and build-test jobs.
//
// The test fails fast with a clear message if any list drifts, preventing the
// silent "added a module to go.work but CI doesn't run it" failure mode.
func TestCIMatrixMatchesGoWork(t *testing.T) {
	workModules := parseGoWorkModules(t, "../go.work")
	// Expected matrix modules: workspace modules minus tools/, with "." for the
	// root (matching how the workflow encodes it).
	var expected []string
	for _, m := range workModules {
		if m == "tools" {
			continue
		}
		if m == "" {
			expected = append(expected, ".")
			continue
		}
		expected = append(expected, m)
	}
	slices.Sort(expected)

	ci := readFile(t, filepath.Join("..", ".github", "workflows", "ci.yml"))

	matrices := extractMatrixModules(ci)
	require.NotEmpty(t, matrices, "expected at least one module matrix in ci.yml")

	// Every module matrix must match — the count is not pinned so adding a
	// new job (e.g., integration) extends coverage instead of breaking the test.
	for i, got := range matrices {
		slices.Sort(got)
		assert.Equal(t, expected, got, "matrix #%d does not match go.work modules", i+1)
	}
}

// parseGoWorkModules returns each module path from the `use` directives of a
// go.work file, normalized: root `.` becomes "", `./foo/bar` becomes "foo/bar".
// Parsing is delegated to golang.org/x/mod/modfile — the same package the Go
// toolchain itself uses — so the grammar (comments, multi-line use blocks,
// indentation) is handled correctly rather than via textual coincidence.
func parseGoWorkModules(t *testing.T, pathStr string) []string {
	t.Helper()
	src := readFile(t, pathStr)
	wf, err := modfile.ParseWork(pathStr, []byte(src), nil)
	require.NoError(t, err, "parse %s failed", pathStr)

	var modules []string
	for _, u := range wf.Use {
		p := path.Clean(u.Path)
		if p == "." {
			p = ""
		}
		modules = append(modules, p)
	}
	return modules
}

// extractMatrixModules returns the `module:` list under each `matrix:` block
// in the workflow. Parsing is indent-anchored: a `module:` key at a deeper
// indent than the nearest enclosing `matrix:` is treated as a matrix axis;
// `module:` keys at the same or shallower indent (e.g., an action input
// elsewhere in the file) are ignored.
func extractMatrixModules(yaml string) [][]string {
	yaml = strings.ReplaceAll(yaml, "\r\n", "\n")
	lines := strings.Split(yaml, "\n")
	var lists [][]string
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, "matrix:") {
			continue
		}
		matrixIndent := len(line) - len(trimmed)
		// Scan inside the matrix block for a `module:` key, then collect its
		// list entries. The block ends when indent returns to matrixIndent
		// or shallower.
		moduleIndent := -1
		var items []string
		for j := i + 1; j < len(lines); j++ {
			l := lines[j]
			t := strings.TrimLeft(l, " \t")
			if t == "" {
				continue
			}
			indent := len(l) - len(t)
			if indent <= matrixIndent {
				break
			}
			if moduleIndent < 0 {
				if strings.HasPrefix(t, "module:") {
					moduleIndent = indent
				}
				continue
			}
			// Inside the module list: entries are at indent > moduleIndent
			// and begin with `-`. Anything at moduleIndent or shallower (but
			// still inside the matrix) ends the list.
			if indent <= moduleIndent {
				break
			}
			if !strings.HasPrefix(t, "-") {
				continue
			}
			items = append(items, strings.TrimSpace(strings.TrimPrefix(t, "-")))
		}
		if moduleIndent >= 0 {
			lists = append(lists, items)
		}
	}
	return lists
}

// TestParseGoWorkModules_IgnoresCommentLookalikes verifies the parser is not
// fooled by a `use (` substring appearing inside a line comment before the
// real directive. A naive substring split on "use (" would match the comment
// and return garbage. The parser is load-bearing for CI/go.work drift
// detection — it must rely on the go.work grammar, not textual coincidence.
func TestParseGoWorkModules_IgnoresCommentLookalikes(t *testing.T) {
	fixture := `go 1.25.0

// example syntax: use ( foo ) — must not be parsed as the directive

use (
	.
	./services/a
	./services/b
)
`
	dir := t.TempDir()
	pathStr := filepath.Join(dir, "go.work")
	err := os.WriteFile(pathStr, []byte(fixture), 0o644)
	require.NoError(t, err, "write fixture failed")

	got := parseGoWorkModules(t, pathStr)
	want := []string{"", "services/a", "services/b"}
	assert.Equal(t, want, got)
}

// TestExtractMatrixModules_OnlyMatchesUnderMatrix anchors parsing to
// strategy.matrix contexts. An unrelated `module:` key elsewhere (e.g., an
// action input or a step name) must not be mistaken for a matrix axis.
func TestExtractMatrixModules_OnlyMatchesUnderMatrix(t *testing.T) {
	fixture := `jobs:
  build:
    strategy:
      matrix:
        module:
          - .
          - services/a
    steps:
      - name: Configure custom action
        with:
          module:
            - bogus
            - notreal
`
	got := extractMatrixModules(fixture)
	require.Len(t, got, 1)
	want := []string{".", "services/a"}
	assert.Equal(t, want, got[0])
}

// TestExtractMatrixModules_MultipleJobs verifies one entry is returned per job.
func TestExtractMatrixModules_MultipleJobs(t *testing.T) {
	fixture := `jobs:
  lint:
    strategy:
      matrix:
        module:
          - .
          - services/a
  build:
    strategy:
      matrix:
        module:
          - .
          - services/a
          - services/b
`
	got := extractMatrixModules(fixture)
	assert.Len(t, got, 2)
}

func readFile(t *testing.T, pathStr string) string {
	t.Helper()
	b, err := os.ReadFile(pathStr)
	require.NoError(t, err, "read %s failed", pathStr)
	return string(b)
}

type Step struct {
	Name string                 `yaml:"name"`
	Uses string                 `yaml:"uses"`
	With map[string]interface{} `yaml:"with"`
}

type Job struct {
	Strategy struct {
		Matrix map[string]interface{} `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []Step `yaml:"steps"`
}

type Workflow struct {
	Jobs map[string]Job `yaml:"jobs"`
}

// TestCIGoCacheConfiguration ensures that every job in .github/workflows/ci.yml
// that sets up Go:
// 1. Explicitly disables setup-go's internal cache to avoid conflict.
// 2. Implements separate custom cache steps for Go modules and Go build cache (actions/cache).
// 3. Verifies that the module cache does NOT include github.sha (to prevent redundant uploads).
// 4. Verifies that the build cache DOES include github.sha (for incremental updates).
// 5. Ensures correct dependency hashes and matrix parameters are included in cache keys.
func TestCIGoCacheConfiguration(t *testing.T) {
	ciPath := filepath.Join("..", ".github", "workflows", "ci.yml")
	ciBytes, err := os.ReadFile(ciPath)
	require.NoError(t, err)

	var wf Workflow
	err = yaml.Unmarshal(ciBytes, &wf)
	require.NoError(t, err)

	for jobName, job := range wf.Jobs {
		var setupGoStep *Step
		var cacheSteps []*Step
		for i := range job.Steps {
			step := &job.Steps[i]
			if strings.HasPrefix(step.Uses, "actions/setup-go@") {
				setupGoStep = step
			} else if strings.HasPrefix(step.Uses, "actions/cache@") {
				cacheSteps = append(cacheSteps, step)
			}
		}

		if setupGoStep == nil {
			continue
		}

		t.Run(jobName, func(t *testing.T) {
			// Verify setup-go explicitly sets cache: false
			cacheVal, hasCache := setupGoStep.With["cache"]
			assert.True(t, hasCache, "setup-go step must explicitly set 'cache' field")
			if cacheBool, ok := cacheVal.(bool); ok {
				assert.False(t, cacheBool, "setup-go step 'cache' must be false to avoid conflict with custom cache")
			} else if cacheStr, ok := cacheVal.(string); ok {
				assert.Equal(t, "false", cacheStr, "setup-go step 'cache' must be false to avoid conflict with custom cache")
			} else {
				t.Errorf("setup-go step 'cache' value must be boolean or string: got %T", cacheVal)
			}

			// Verify we have exactly two cache steps
			require.Len(t, cacheSteps, 2, "job uses setup-go but does not have exactly two custom cache steps (modules and build cache)")

			var modCacheStep *Step
			var buildCacheStep *Step

			for _, cs := range cacheSteps {
				pathVal, hasPath := cs.With["path"]
				require.True(t, hasPath, "cache step must define 'path'")
				pathStr, ok := pathVal.(string)
				require.True(t, ok, "cache step 'path' must be a string")

				if strings.Contains(pathStr, "~/go/pkg/mod") {
					modCacheStep = cs
				} else if strings.Contains(pathStr, "~/.cache/go-build") {
					buildCacheStep = cs
				}
			}

			require.NotNil(t, modCacheStep, "missing Go module cache step targeting ~/go/pkg/mod")
			require.NotNil(t, buildCacheStep, "missing Go build cache step targeting ~/.cache/go-build")

			// Check Module Cache Key
			modKeyVal, hasModKey := modCacheStep.With["key"]
			require.True(t, hasModKey, "module cache step must define 'key'")
			modKeyStr, ok := modKeyVal.(string)
			require.True(t, ok, "module cache step 'key' must be a string")
			assert.Contains(t, modKeyStr, "hashFiles(", "module cache step key must include dependency hash")
			assert.NotContains(t, modKeyStr, "github.sha", "module cache step key must NOT include github.sha to avoid redundant module cache uploads")

			// Check Build Cache Key
			buildKeyVal, hasBuildKey := buildCacheStep.With["key"]
			require.True(t, hasBuildKey, "build cache step must define 'key'")
			buildKeyStr, ok := buildKeyVal.(string)
			require.True(t, ok, "build cache step 'key' must be a string")
			assert.Contains(t, buildKeyStr, "hashFiles(", "build cache step key must include dependency hash")
			assert.Contains(t, buildKeyStr, "github.sha", "build cache step key must include github.sha for incremental compiler caching")

			// Verify matrix setups to prevent stampedes
			if job.Strategy.Matrix != nil {
				if _, hasModule := job.Strategy.Matrix["module"]; hasModule {
					assert.Contains(t, modKeyStr, "matrix.module", "matrix job module cache key must include matrix.module to avoid collisions")
					assert.Contains(t, buildKeyStr, "matrix.module", "matrix job build cache key must include matrix.module to avoid collisions")
				}
			}
		})
	}
}
