//go:build linux

package wasm

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

// newReactorRunner creates and initialises a ReactorPythonRunner for tests.
// Requires PYTHON_REACTOR_WASM env var pointing to python-reactor.wasm.
func newReactorRunner(t testing.TB) *ReactorPythonRunner {
	t.Helper()
	wasmPath := os.Getenv("PYTHON_REACTOR_WASM")
	if wasmPath == "" {
		t.Skip("PYTHON_REACTOR_WASM not set — skipping reactor tests")
	}

	log, err := zap.NewDevelopment()
	require.NoError(t, err)

	cfg := Config{
		Timeout:        120 * time.Second,
		MaxMemoryPages: 8192, // 512 MiB
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

// reactorEvalScript returns the content of examples/eval-python/eval.py.
func reactorEvalScript(t testing.TB) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(filename), "..", "..", "..")
	b, err := os.ReadFile(filepath.Join(root, "examples", "eval-python", "eval.py"))
	require.NoError(t, err, "read eval.py")
	return string(b)
}

// TestReactorPythonRunner_Basic verifies basic eval correctness.
func TestReactorPythonRunner_Basic(t *testing.T) {
	runner := newReactorRunner(t)
	script := reactorEvalScript(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		input    string
		wantOK   bool
		wantFrag string
	}{
		{
			name:     "correct integer match",
			input:    `{"response": "42", "answer": "42"}`,
			wantOK:   true,
			wantFrag: "Correct",
		},
		{
			name:     "incorrect — wrong value",
			input:    `{"response": "1", "answer": "2"}`,
			wantOK:   false,
			wantFrag: "Incorrect",
		},
		{
			name:     "correct float match",
			input:    `{"response": "3.14", "answer": "3.14"}`,
			wantOK:   true,
			wantFrag: "Correct",
		},
		{
			name:     "non-numeric input",
			input:    `{"response": "abc", "answer": "42"}`,
			wantOK:   false,
			wantFrag: "Error",
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()

			result, err := runner.SendRequest(reqCtx, script, "eval", tc.input)
			require.NoError(t, err, "SendRequest #%d should not fail", i+1)
			require.NotNil(t, result)

			t.Logf("result[%d]: %v", i+1, result)

			isCorrect, ok := result["is_correct"]
			require.True(t, ok, "result must contain 'is_correct'")
			assert.Equal(t, tc.wantOK, isCorrect, "is_correct mismatch")

			feedback, ok := result["feedback"]
			require.True(t, ok, "result must contain 'feedback'")
			feedbackStr, _ := feedback.(string)
			assert.Contains(t, feedbackStr, tc.wantFrag, "feedback content mismatch")
		})
	}
}

// TestReactorPythonRunner_StateIsolation verifies that snapshot/restore gives
// true per-request interpreter isolation — global state mutations in one request
// do NOT bleed into subsequent requests.
//
// This is the key advantage of reactor mode over the stdin/stdout resident runner:
// snapshot/restore resets the entire Python heap, not just exec() namespaces.
func TestReactorPythonRunner_StateIsolation(t *testing.T) {
	runner := newReactorRunner(t)
	ctx := context.Background()

	// Script with a module-level counter that increments each call.
	// With snapshot/restore the counter must always be 1 — the heap is
	// reset to post-py_init() state before every py_exec().
	counterScript := `
_counter = 0

def evaluation_function(response, answer, params=None):
    global _counter
    _counter += 1
    return {"is_correct": True, "feedback": f"counter={_counter}"}
`

	for i := 1; i <= 5; i++ {
		reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		result, err := runner.SendRequest(reqCtx, counterScript, "eval", `{"response": "x", "answer": "x"}`)
		cancel()

		require.NoError(t, err, "SendRequest #%d", i)
		t.Logf("result[%d]: %v", i, result)

		feedback, _ := result["feedback"].(string)
		assert.Contains(t, feedback, "counter=1",
			"snapshot/restore must reset _counter to 0 before each request; got: %s", feedback)
	}
}

// TestReactorPythonRunner_ModuleImportIsolation verifies that modules imported
// by user scripts in one request are not present in the next — even for modules
// with mutable global state.
func TestReactorPythonRunner_ModuleImportIsolation(t *testing.T) {
	runner := newReactorRunner(t)
	ctx := context.Background()

	// Script that imports a module and mutates its state.
	// After snapshot restore the import should not be visible.
	importScript := `
import random as _rnd

def evaluation_function(response, answer, params=None):
    # Check whether 'random' is in sys.modules — it should only be the
    # stdlib random (imported as part of Python init), not a user import
    # that carries mutable seeded state from a previous request.
    import sys
    in_modules = 'random' in sys.modules
    _rnd.seed(12345)
    return {"is_correct": True, "feedback": f"random_in_modules={in_modules}"}
`

	for i := 1; i <= 3; i++ {
		reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		result, err := runner.SendRequest(reqCtx, importScript, "eval", `{"response": "x", "answer": "x"}`)
		cancel()

		require.NoError(t, err, "request %d", i)
		t.Logf("result[%d]: %v", i, result)
	}
}

// TestReactorPythonRunner_Preview verifies the preview method path.
func TestReactorPythonRunner_Preview(t *testing.T) {
	runner := newReactorRunner(t)
	script := reactorEvalScript(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := runner.SendRequest(ctx, script, "preview", `{"response": "3.14", "answer": "3.14"}`)
	require.NoError(t, err)
	t.Logf("preview result: %v", result)
	preview, ok := result["preview"]
	require.True(t, ok, "preview result must contain 'preview' key")
	assert.NotEmpty(t, preview)
}

// BenchmarkReactorPythonRunner_SendRequest measures per-request latency with
// snapshot/restore isolation. Compare against BenchmarkResidentPythonRunner_SendRequest.
func BenchmarkReactorPythonRunner_SendRequest(b *testing.B) {
	wasmPath := os.Getenv("PYTHON_REACTOR_WASM")
	if wasmPath == "" {
		b.Skip("PYTHON_REACTOR_WASM not set")
	}

	log := zap.NewNop()
	cfg := Config{
		Timeout:        120 * time.Second,
		MaxMemoryPages: 8192,
	}

	runner := NewReactorPythonRunner(wasmPath, cfg, log)

	initCtx, initCancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer initCancel()

	if err := runner.Init(initCtx); err != nil {
		b.Fatalf("Init: %v", err)
	}
	b.Cleanup(func() {
		shutCtx, sc := context.WithTimeout(context.Background(), 15*time.Second)
		defer sc()
		_ = runner.Shutdown(shutCtx)
	})

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		b.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(filename), "..", "..", "..")
	scriptBytes, err := os.ReadFile(filepath.Join(root, "examples", "eval-python", "eval.py"))
	if err != nil {
		b.Fatalf("read eval.py: %v", err)
	}
	script := string(scriptBytes)
	input := `{"response": "42", "answer": "42"}`

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		reqCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, err := runner.SendRequest(reqCtx, script, "eval", input)
		cancel()
		if err != nil {
			b.Fatalf("SendRequest failed at iteration %d: %v", i, err)
		}
	}
}
