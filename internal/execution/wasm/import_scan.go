package wasm

import (
	"os"
	"regexp"
)

// heavyDeps is the set of Python packages that require Pyodide/Emscripten
// because they depend on Fortran runtimes (LAPACK, ARPACK, f2c) or other
// native extensions that cannot be compiled to WASM32-WASI.
//
// CPython-WASI + wazero can handle: pure Python, NumPy (static BLAS),
// SymPy, mpmath, and most stdlib-only packages.
//
// Packages that trigger Pyodide fallback:
//   - scipy      — LAPACK/ARPACK/f2c; unresolvable env imports in WASI
//   - pandas     — depends on scipy internals for some paths; C extensions
//   - statsmodels — depends on scipy
//   - sklearn / scikit-learn — scipy dependency
//   - matplotlib  — C extensions; optional but route to pyodide for safety
var heavyDeps = []string{
	"scipy",
	"pandas",
	"statsmodels",
	"sklearn",
	"scikit_learn",
	"matplotlib",
	"seaborn", // depends on matplotlib + statsmodels
}

// buildHeavyDepPattern returns a compiled regexp that matches any
// "import <pkg>" or "from <pkg>" statement (including submodule imports).
//
// The pattern is case-sensitive and matches at word boundaries so that
// e.g. "import numpyscalar" does NOT trigger on "numpy".
func buildHeavyDepPattern() *regexp.Regexp {
	// Build alternation: scipy|pandas|...
	alt := ""
	for i, pkg := range heavyDeps {
		if i > 0 {
			alt += "|"
		}
		alt += regexp.QuoteMeta(pkg)
	}
	// Match:  import scipy[.submod]
	//         from scipy[.submod] import ...
	// ^\s* anchors to line start (with optional indentation) so that comment
	// lines such as "# import scipy" are not matched.
	return regexp.MustCompile(`(?m)^\s*(?:import|from)\s+(` + alt + `)(?:\s|\.|\r?\n|$)`)
}

var heavyDepRE = buildHeavyDepPattern()

// ScriptNeedsHeavyRuntime returns true if the Python script source contains
// an import of a package that requires the Pyodide (Emscripten) runtime.
// It uses a simple static regexp scan — no AST parsing.
//
// Known false negatives (not detected by the regexp):
//   - Dynamic imports: __import__("scipy"), importlib.import_module("scipy")
//   - Parenthesised from-imports: from (\n    scipy\n) import stats
//
// This is intentional: the overhead of false negatives (wrong backend) is low
// because reactor-python will simply fail and the error message will guide the
// operator to set the right IO interface manually via FUNCTION_INTERFACE.
func ScriptNeedsHeavyRuntime(src string) bool {
	return heavyDepRE.MatchString(src)
}

// ScriptFileNeedsHeavyRuntime reads the Python script at path and calls
// ScriptNeedsHeavyRuntime on its contents. Returns (false, nil) if the file
// cannot be read (callers should fall back to the default backend).
func ScriptFileNeedsHeavyRuntime(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return ScriptNeedsHeavyRuntime(string(b)), nil
}
