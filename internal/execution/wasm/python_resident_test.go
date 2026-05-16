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

// newResidentRunner creates a started ResidentPythonRunner for tests.
// It accepts PYTHON_ASYNC_WASM (asyncified binary) or PYTHON_WASM (regular).
// For the goroutine-based resident runner, either binary works; it does NOT
// require the asyncify transform.
func newResidentRunner(t testing.TB) *ResidentPythonRunner {
	t.Helper()
	wasmPath := os.Getenv("PYTHON_ASYNC_WASM")
	if wasmPath == "" {
		wasmPath = os.Getenv("PYTHON_WASM")
	}
	if wasmPath == "" {
		t.Skip("PYTHON_ASYNC_WASM or PYTHON_WASM not set")
	}

	log, err := zap.NewDevelopment()
	require.NoError(t, err)

	cfg := Config{
		Timeout:        60 * time.Second,
		MaxMemoryPages: 8192, // 512 MiB
	}

	runner := NewResidentPythonRunner(wasmPath, cfg, log)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)

	require.NoError(t, runner.Init(ctx), "ResidentPythonRunner.Init")
	t.Cleanup(func() {
		shutCtx, sc := context.WithTimeout(context.Background(), 15*time.Second)
		defer sc()
		_ = runner.Shutdown(shutCtx)
	})
	return runner
}

// evalPyScriptStr returns the eval.py script content as a string.
func evalPyScriptStr(t testing.TB) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/execution/wasm/ -> ../../.. -> project root
	root := filepath.Join(filepath.Dir(filename), "..", "..", "..")
	p := filepath.Join(root, "examples", "eval-python", "eval.py")
	b, err := os.ReadFile(p)
	require.NoError(t, err, "read eval.py")
	return string(b)
}

// TestResidentPythonRunner_Basic verifies that the resident runner handles
// sequential requests correctly.
func TestResidentPythonRunner_Basic(t *testing.T) {
	runner := newResidentRunner(t)
	script := evalPyScriptStr(t)
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
			reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			result, err := runner.SendRequest(reqCtx, script, tc.input)
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

// TestResidentPythonRunner_StateIsolation verifies that Python global state
// does NOT leak between requests (since exec() creates a fresh namespace).
func TestResidentPythonRunner_StateIsolation(t *testing.T) {
	runner := newResidentRunner(t)
	ctx := context.Background()

	// Script with a module-level counter. Each exec() call creates a fresh
	// namespace so _counter should always start at 0.
	counterScript := `
import sys, json

_counter = 0

def evaluation_function(response, answer, params=None):
    global _counter
    _counter += 1
    return {"is_correct": True, "feedback": f"counter={_counter}"}
`

	for i := 1; i <= 3; i++ {
		result, err := runner.SendRequest(ctx, counterScript, `{"response": "x", "answer": "x"}`)
		require.NoError(t, err, "SendRequest #%d", i)
		t.Logf("result[%d]: %v", i, result)

		feedback, _ := result["feedback"].(string)
		// Each exec() creates a fresh namespace, so counter starts at 1 each time.
		assert.Contains(t, feedback, "counter=1",
			"exec() should isolate state — got: %s", feedback)
	}
}

// BenchmarkResidentPythonRunner_SendRequest measures per-request cost with
// the resident runner. Compare against BenchmarkPythonRunner_RunScript.
func BenchmarkResidentPythonRunner_SendRequest(b *testing.B) {
	wasmPath := os.Getenv("PYTHON_ASYNC_WASM")
	if wasmPath == "" {
		wasmPath = os.Getenv("PYTHON_WASM")
	}
	if wasmPath == "" {
		b.Skip("PYTHON_ASYNC_WASM or PYTHON_WASM not set")
	}

	log := zap.NewNop()
	cfg := Config{
		Timeout:        60 * time.Second,
		MaxMemoryPages: 8192,
	}

	runner := NewResidentPythonRunner(wasmPath, cfg, log)

	initCtx, initCancel := context.WithTimeout(context.Background(), 180*time.Second)
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
		reqCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := runner.SendRequest(reqCtx, script, input)
		cancel()
		if err != nil {
			b.Fatalf("SendRequest failed at iteration %d: %v", i, err)
		}
	}
}
