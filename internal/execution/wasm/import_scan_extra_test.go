//go:build !plan9

package wasm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// import_scan_extra_test.go contains additional boundary / edge-case tests for
// ScriptNeedsHeavyRuntime that complement the table in import_scan_test.go.

// TestScriptNeedsHeavyRuntime_FromStatement verifies that a "from X import Y"
// statement is correctly identified as requiring the heavy runtime.
func TestScriptNeedsHeavyRuntime_FromStatement(t *testing.T) {
	src := "from scipy import stats\n"
	assert.True(t, ScriptNeedsHeavyRuntime(src),
		"'from scipy import stats' should require heavy runtime")
}

// TestScriptNeedsHeavyRuntime_InlineComment verifies that a trailing inline
// comment does not prevent detection of the import on the same line.
// The import itself is syntactically valid; only the package name matters.
func TestScriptNeedsHeavyRuntime_InlineComment(t *testing.T) {
	src := "import scipy  # heavy\n"
	assert.True(t, ScriptNeedsHeavyRuntime(src),
		"'import scipy  # heavy' (import with trailing comment) should require heavy runtime")
}

// TestScriptNeedsHeavyRuntime_MultilineImport ensures that Python code
// containing a parenthesised multi-symbol import (valid Python syntax) does
// not cause false positives for packages that are NOT in the heavy-dep list.
func TestScriptNeedsHeavyRuntime_MultilineImport(t *testing.T) {
	src := "from os.path import (\n    join,\n    exists,\n)\n"
	assert.False(t, ScriptNeedsHeavyRuntime(src),
		"multi-line import of stdlib symbols should not require heavy runtime")
}

// TestScriptNeedsHeavyRuntime_ScikitLearn verifies that both the common
// top-level import and a submodule from-import trigger the heavy runtime.
func TestScriptNeedsHeavyRuntime_ScikitLearn(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		heavy bool
	}{
		{"import sklearn", "import sklearn\n", true},
		{"from sklearn submodule", "from sklearn.model_selection import train_test_split\n", true},
		{"import scikit_learn underscore", "import scikit_learn\n", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.heavy, ScriptNeedsHeavyRuntime(tc.src),
				"ScriptNeedsHeavyRuntime(%q)", tc.src)
		})
	}
}

// TestScriptNeedsHeavyRuntime_Seaborn verifies that seaborn imports (which
// depend on matplotlib + statsmodels) are correctly flagged.
func TestScriptNeedsHeavyRuntime_Seaborn(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"import seaborn as sns", "import seaborn as sns\n"},
		{"import seaborn bare", "import seaborn\n"},
		{"from seaborn import", "from seaborn import heatmap\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.True(t, ScriptNeedsHeavyRuntime(tc.src),
				"seaborn import %q should require heavy runtime", tc.src)
		})
	}
}

// TestScriptNeedsHeavyRuntime_EmptyString checks that an empty source string
// is handled gracefully and returns false.
func TestScriptNeedsHeavyRuntime_EmptyString(t *testing.T) {
	assert.False(t, ScriptNeedsHeavyRuntime(""),
		"empty string should not require heavy runtime")
}

// TestScriptNeedsHeavyRuntime_OnlyComments verifies that a script consisting
// entirely of comment lines (including ones that mention heavy packages) is
// not flagged — only actual import statements count.
func TestScriptNeedsHeavyRuntime_OnlyComments(t *testing.T) {
	src := "# import scipy\n# from pandas import DataFrame\n# TODO: add seaborn\n"
	assert.False(t, ScriptNeedsHeavyRuntime(src),
		"a script with only comment lines should not require heavy runtime")
}

// TestScriptNeedsHeavyRuntime_IndentedFromImport verifies that an indented
// "from <heavy> import ..." (e.g. inside a try block) is still detected.
func TestScriptNeedsHeavyRuntime_IndentedFromImport(t *testing.T) {
	src := "try:\n    from scipy.linalg import solve\nexcept ImportError:\n    pass\n"
	assert.True(t, ScriptNeedsHeavyRuntime(src),
		"indented 'from scipy.linalg import solve' should require heavy runtime")
}

// TestScriptNeedsHeavyRuntime_HeavyPackageInString ensures that a heavy
// package name appearing only inside a string literal does NOT trigger.
func TestScriptNeedsHeavyRuntime_HeavyPackageInString(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"scipy in double-quoted string", `pkg = "scipy"` + "\n"},
		{"pandas in single-quoted string", `pkg = 'pandas'` + "\n"},
		{"matplotlib in docstring", `"""use matplotlib for plots"""` + "\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.False(t, ScriptNeedsHeavyRuntime(tc.src),
				"heavy package name in string literal %q must not trigger", tc.src)
		})
	}
}
