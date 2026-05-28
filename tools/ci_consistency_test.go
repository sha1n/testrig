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
)

// TestCIMatrixMatchesGoWork enforces that .github/workflows/ci.yml stays in
// sync with go.work. go.work is the single source of truth for the multi-module
// workspace; every module listed there (except tools/, which has no testable
// code) must appear in:
//
//   - the `module` matrix of the lint job
//   - the `module` matrix of the build-test job
//   - both jobs' cache-dependency-path lists (as <module>/go.sum)
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

	// Each cache-dependency-path block must independently list go.sum for
	// every workspace module that has one. A presence-only check on the full
	// YAML cannot detect drift between jobs.
	blocks := extractCacheDepPaths(ci)
	require.NotEmpty(t, blocks, "expected at least one cache-dependency-path block in ci.yml")

	for i, paths := range blocks {
		for _, m := range workModules {
			if m == "" {
				continue // root go.sum required, asserted by the "go.sum" check below
			}
			needle := m + "/go.sum"
			assert.Contains(t, paths, needle, "cache-dependency-path block #%d is missing %q", i+1, needle)
		}
		assert.Contains(t, paths, "go.sum", "cache-dependency-path block #%d is missing root \"go.sum\"", i+1)
	}
}

// extractCacheDepPaths returns the entries of every `cache-dependency-path: |`
// block in the workflow, one slice per block. The block grammar in GitHub
// Actions YAML is a literal block scalar: each entry sits on its own line at
// a deeper indent than the key. Parsing terminates when indent returns to
// the key's level or shallower.
func extractCacheDepPaths(yaml string) [][]string {
	yaml = strings.ReplaceAll(yaml, "\r\n", "\n")
	const key = "cache-dependency-path:"
	var blocks [][]string
	lines := strings.Split(yaml, "\n")
	for i := 0; i < len(lines); i++ {
		trimmedLine := strings.TrimLeft(lines[i], " \t")
		if !strings.HasPrefix(trimmedLine, key) {
			continue
		}
		idx := strings.Index(lines[i], key)
		// Block scalar marker `|` must follow the key for this form. Anything
		// else (e.g., a flow-scalar value on the same line) is out of scope.
		if !strings.Contains(lines[i][idx+len(key):], "|") {
			continue
		}
		keyIndent := idx
		var entries []string
		for j := i + 1; j < len(lines); j++ {
			line := lines[j]
			trimmed := strings.TrimLeft(line, " \t")
			if trimmed == "" {
				continue // blank lines don't terminate the block
			}
			entryIndent := len(line) - len(trimmed)
			if entryIndent <= keyIndent {
				break
			}
			entries = append(entries, strings.TrimSpace(trimmed))
			i = j
		}
		blocks = append(blocks, entries)
	}
	return blocks
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

// TestExtractCacheDepPaths_ParsesPerBlock verifies the helper returns one list
// per cache-dependency-path block. A presence-only check on the full YAML
// cannot detect drift between jobs (e.g., a sum file in the lint block but
// missing from the build-test block); per-block parsing can.
func TestExtractCacheDepPaths_ParsesPerBlock(t *testing.T) {
	fixture := `jobs:
  lint:
    steps:
      - uses: actions/setup-go@v6
        with:
          cache-dependency-path: |
            go.sum
            services/a/go.sum
            services/b/go.sum
  build:
    steps:
      - uses: actions/setup-go@v6
        with:
          cache-dependency-path: |
            go.sum
            services/a/go.sum
`
	got := extractCacheDepPaths(fixture)
	require.Len(t, got, 2)
	wantLint := []string{"go.sum", "services/a/go.sum", "services/b/go.sum"}
	wantBuild := []string{"go.sum", "services/a/go.sum"}
	assert.Equal(t, wantLint, got[0])
	assert.Equal(t, wantBuild, got[1])
}

// TestExtractCacheDepPaths_DetectsDriftBetweenBlocks demonstrates the bug the
// old presence-only check missed: a per-block view sees that block 2 is
// missing services/b/go.sum even though the string still appears elsewhere
// in the YAML.
func TestExtractCacheDepPaths_DetectsDriftBetweenBlocks(t *testing.T) {
	fixture := `cache-dependency-path: |
  go.sum
  services/a/go.sum
  services/b/go.sum
---
cache-dependency-path: |
  go.sum
  services/a/go.sum
`
	got := extractCacheDepPaths(fixture)
	require.Len(t, got, 2)
	assert.NotContains(t, got[1], "services/b/go.sum")
	assert.Contains(t, got[0], "services/b/go.sum")
}

func readFile(t *testing.T, pathStr string) string {
	t.Helper()
	b, err := os.ReadFile(pathStr)
	require.NoError(t, err, "read %s failed", pathStr)
	return string(b)
}
