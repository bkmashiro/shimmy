//go:build linux

package wasm

// TestReactorPythonRunner_Numpy verifies that numpy can be imported and used
// inside the CPython-WASI sandbox.
//
// # Prerequisites
//
// Set PYTHON_REACTOR_WASM to a python-reactor.wasm binary that has numpy
// available (either statically linked, or accessible via PYTHONPATH).
//
// Optionally set PYTHON_NUMPY_PATH to a directory containing a pre-built
// wasi-wheels numpy installation (site-packages directory).  When set, the
// path is mounted read-only into the sandbox and added to PYTHONPATH so that
// CPython can import numpy.
//
//	# Example (wasi-wheels numpy installed to /tmp/numpy-wasi):
//	PYTHON_REACTOR_WASM=testdata/python-reactor.wasm \
//	PYTHON_NUMPY_PATH=/tmp/numpy-wasi/site-packages \
//	  go test -v -run TestReactorPythonRunner_Numpy -timeout 300s ./internal/execution/wasm/

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newReactorRunnerWithNumpy creates a ReactorPythonRunner configured with an
// optional extra site-packages path.  The binary path comes from
// PYTHON_REACTOR_WASM; if unset the test is skipped.
//
// If PYTHON_NUMPY_PATH is set, that directory is added to AllowedPaths so
// that wazero mounts it read-only inside the sandbox and PYTHONPATH is set
// accordingly.  If it is empty the runner uses whatever packages are already
// packed inside the binary.
func newReactorRunnerWithNumpy(t testing.TB) *ReactorPythonRunner {
	t.Helper()
	wasmPath := os.Getenv("PYTHON_REACTOR_WASM")
	if wasmPath == "" {
		t.Skip("PYTHON_REACTOR_WASM not set — skipping numpy tests")
	}

	log, err := zap.NewDevelopment()
	require.NoError(t, err)

	cfg := Config{
		Timeout:        120 * time.Second,
		MaxMemoryPages: 8192, // 512 MiB — numpy needs headroom
	}

	// Mount an extra site-packages directory when supplied.
	if numpyPath := os.Getenv("PYTHON_NUMPY_PATH"); numpyPath != "" {
		cfg.AllowedPaths = []string{numpyPath}
		t.Logf("mounting numpy path: %s", numpyPath)
	}

	runner := NewReactorPythonRunner(wasmPath, cfg, log)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	t.Cleanup(cancel)

	require.NoError(t, runner.Init(ctx), "ReactorPythonRunner.Init")
	t.Cleanup(func() {
		shutCtx, sc := context.WithTimeout(context.Background(), 15*time.Second)
		defer sc()
		_ = runner.Shutdown(shutCtx)
	})
	return runner
}

// numpyEvalScript returns the content of examples/eval-numpy/eval.py.
func numpyEvalScript(t testing.TB) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(filename), "..", "..", "..")
	b, err := os.ReadFile(filepath.Join(root, "examples", "eval-numpy", "eval.py"))
	require.NoError(t, err, "read eval-numpy/eval.py")
	return string(b)
}

// probeNumpy sends a minimal script that does nothing but import numpy.
// Returns (true, "") if the import succeeds; (false, errMsg) otherwise.
// This lets us skip individual sub-tests gracefully rather than failing the
// whole suite when the binary does not include numpy.
func probeNumpy(t testing.TB, runner *ReactorPythonRunner) (available bool, reason string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	probeScript := `
import numpy as np

def evaluation_function(response, answer, params=None):
    return {"is_correct": True, "feedback": "numpy " + np.__version__}
`
	result, err := runner.SendRequest(ctx, probeScript, "eval", `{"response":"x","answer":"x"}`)
	if err != nil {
		return false, err.Error()
	}
	feedback, _ := result["feedback"].(string)
	t.Logf("numpy probe: %s", feedback)
	return true, feedback
}

// TestReactorPythonRunner_Numpy is the top-level numpy integration test.
// It first probes whether numpy is importable; if not it skips gracefully so
// the suite still reports a useful message rather than a test failure.
func TestReactorPythonRunner_Numpy(t *testing.T) {
	runner := newReactorRunnerWithNumpy(t)

	// ── probe ────────────────────────────────────────────────────────────────
	available, reason := probeNumpy(t, runner)
	if !available {
		t.Skipf("numpy not available in this binary (%s); rebuild python-reactor.wasm with wasi-wheels numpy", reason)
	}
	t.Logf("numpy available: %s", reason)

	script := numpyEvalScript(t)
	ctx := context.Background()

	// ── correctness ──────────────────────────────────────────────────────────
	t.Run("correct_array", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		result, err := runner.SendRequest(reqCtx, script, "eval",
			`{"response":"1.0,2.0,3.0","answer":"1.0,2.0,3.0"}`)
		require.NoError(t, err)

		isCorrect, _ := result["is_correct"].(bool)
		assert.True(t, isCorrect, "identical arrays should be correct: %v", result)
		t.Logf("result: %v", result)
	})

	t.Run("incorrect_array", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		result, err := runner.SendRequest(reqCtx, script, "eval",
			`{"response":"1.0,2.0,9.9","answer":"1.0,2.0,3.0"}`)
		require.NoError(t, err)

		isCorrect, _ := result["is_correct"].(bool)
		assert.False(t, isCorrect, "array with wrong element should be incorrect: %v", result)

		feedback, _ := result["feedback"].(string)
		assert.Contains(t, feedback, "index 2", "feedback should identify worst index: %s", feedback)
		t.Logf("result: %v", result)
	})

	t.Run("within_tolerance", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		// 1e-6 deviation, default rtol=1e-5 → should be correct.
		result, err := runner.SendRequest(reqCtx, script, "eval",
			`{"response":"1.000001","answer":"1.0","params":{"rtol":1e-4}}`)
		require.NoError(t, err)

		isCorrect, _ := result["is_correct"].(bool)
		assert.True(t, isCorrect, "value within tolerance should be correct: %v", result)
		t.Logf("result: %v", result)
	})

	t.Run("shape_mismatch", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		result, err := runner.SendRequest(reqCtx, script, "eval",
			`{"response":"1.0,2.0","answer":"1.0,2.0,3.0"}`)
		require.NoError(t, err)

		isCorrect, _ := result["is_correct"].(bool)
		assert.False(t, isCorrect, "shape mismatch should be incorrect")

		feedback, _ := result["feedback"].(string)
		assert.Contains(t, feedback, "Shape mismatch", "feedback should mention shape: %s", feedback)
		t.Logf("result: %v", result)
	})

	t.Run("parse_error", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		result, err := runner.SendRequest(reqCtx, script, "eval",
			`{"response":"not,a,number","answer":"1.0,2.0,3.0"}`)
		require.NoError(t, err)

		isCorrect, _ := result["is_correct"].(bool)
		assert.False(t, isCorrect, "bad input should be incorrect")
		t.Logf("result: %v", result)
	})

	t.Run("preview", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		result, err := runner.SendRequest(reqCtx, script, "preview",
			`{"response":"1.0,2.0,3.0","answer":""}`)
		require.NoError(t, err)

		preview, ok := result["preview"]
		require.True(t, ok, "preview result must contain 'preview' key: %v", result)
		previewStr, _ := preview.(string)
		assert.Contains(t, previewStr, "1.0", "preview should mention submitted values: %s", previewStr)
		t.Logf("preview: %s", previewStr)
	})

	// ── state isolation: numpy's global RNG must reset between requests ──────
	t.Run("numpy_rng_isolation", func(t *testing.T) {
		reqCtx1, c1 := context.WithTimeout(ctx, 60*time.Second)
		reqCtx2, c2 := context.WithTimeout(ctx, 60*time.Second)
		defer c1()
		defer c2()

		// Seed numpy RNG and record the first random draw.
		rngScript := `
import numpy as np

def evaluation_function(response, answer, params=None):
    np.random.seed(42)
    val = float(np.random.rand())
    return {"is_correct": True, "feedback": f"{val:.15f}"}
`
		r1, err := runner.SendRequest(reqCtx1, rngScript, "eval", `{"response":"x","answer":"x"}`)
		require.NoError(t, err)
		r2, err := runner.SendRequest(reqCtx2, rngScript, "eval", `{"response":"x","answer":"x"}`)
		require.NoError(t, err)

		// With snapshot/restore the heap (including numpy's RNG state) resets
		// before every request, so seeding with 42 must give the same draw.
		f1, _ := r1["feedback"].(string)
		f2, _ := r2["feedback"].(string)
		assert.Equal(t, f1, f2,
			"numpy RNG state must reset with heap snapshot; got %q then %q", f1, f2)
		t.Logf("numpy RNG draw (both requests): %s", f1)
	})
}
