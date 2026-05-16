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

// evalPyScript returns the contents of examples/eval-python/eval.py,
// resolved relative to this source file.
func evalPyScript(t testing.TB) string {
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

// newPythonRunner creates a started PythonRunner for tests.
// It is skipped if PYTHON_WASM is not set.
func newPythonRunner(t testing.TB) *PythonRunner {
	t.Helper()
	wasmPath := os.Getenv("PYTHON_WASM")
	if wasmPath == "" {
		t.Skip("PYTHON_WASM not set")
	}

	log, err := zap.NewDevelopment()
	require.NoError(t, err)

	cfg := Config{
		Timeout:        30 * time.Second,
		MaxMemoryPages: 8192, // 512 MiB — CPython needs ~100-200 MiB
	}

	runner := NewPythonRunner(wasmPath, cfg, log)
	require.NoError(t, runner.Start(context.Background()), "runner.Start")
	t.Cleanup(func() { _ = runner.Shutdown(context.Background()) })
	return runner
}

// --------------------------------------------------------------------------
// Functional tests
// --------------------------------------------------------------------------

// TestPythonRunner_BasicEval verifies correct and incorrect evaluations.
func TestPythonRunner_BasicEval(t *testing.T) {
	runner := newPythonRunner(t)
	script := evalPyScript(t)

	tests := []struct {
		name      string
		input     string
		wantOK    bool
		wantFrag  string // substring expected in feedback
	}{
		{
			name:     "correct integer match",
			input:    `{"response": "42", "answer": "42"}`,
			wantOK:   true,
			wantFrag: "Correct",
		},
		{
			name:     "correct float match",
			input:    `{"response": "3.14", "answer": "3.14"}`,
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
			name:     "incorrect — off by 1",
			input:    `{"response": "41", "answer": "42"}`,
			wantOK:   false,
			wantFrag: "Incorrect",
		},
		{
			name:     "non-numeric input",
			input:    `{"response": "abc", "answer": "42"}`,
			wantOK:   false,
			wantFrag: "Error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			result, err := runner.RunScript(ctx, script, tc.input)
			require.NoError(t, err, "RunScript should not return an error")
			require.NotNil(t, result)

			t.Logf("result: %v", result)

			isCorrect, ok := result["is_correct"]
			require.True(t, ok, "result must contain 'is_correct'")
			assert.Equal(t, tc.wantOK, isCorrect, "is_correct mismatch")

			feedback, ok := result["feedback"]
			require.True(t, ok, "result must contain 'feedback'")
			feedbackStr, ok := feedback.(string)
			require.True(t, ok, "feedback must be a string")
			assert.Contains(t, feedbackStr, tc.wantFrag, "feedback content mismatch")
		})
	}
}

// TestPythonRunner_NoFilesystemAccess confirms that the sandbox blocks reads
// from the host filesystem. The script tries to open /etc/passwd; this should
// either raise an OSError (sandbox denies the syscall) or fail in some other
// way — but must NOT return the file's contents.
func TestPythonRunner_NoFilesystemAccess(t *testing.T) {
	runner := newPythonRunner(t)

	script := `
import sys, json
try:
    data = open('/etc/passwd').read()
    print(json.dumps({"leaked": True, "data": data[:50]}))
except Exception as e:
    print(json.dumps({"leaked": False, "error": str(e)}))
`

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := runner.RunScript(ctx, script, "{}")
	// We accept either a clean result with leaked=false, or an error from the
	// runner (e.g., if CPython crashes on the denied syscall).
	if err != nil {
		t.Logf("RunScript returned error (acceptable — sandbox blocked exec): %v", err)
		return
	}

	t.Logf("sandbox result: %v", result)

	leaked, ok := result["leaked"]
	if !ok {
		// No 'leaked' key — script likely crashed. That's also fine.
		t.Log("result has no 'leaked' key — script likely crashed (sandbox)")
		return
	}

	assert.Equal(t, false, leaked,
		"sandbox must prevent filesystem access — 'leaked' must be false")
}

// --------------------------------------------------------------------------
// Benchmark
// --------------------------------------------------------------------------

// BenchmarkPythonRunner_RunScript measures per-request instantiation cost.
// CPython WASI startup is ~2-5 s per call; set -benchtime=3x to limit runs.
func BenchmarkPythonRunner_RunScript(b *testing.B) {
	wasmPath := os.Getenv("PYTHON_WASM")
	if wasmPath == "" {
		b.Skip("PYTHON_WASM not set")
	}

	log := zap.NewNop()
	cfg := Config{
		Timeout:        60 * time.Second,
		MaxMemoryPages: 8192,
	}

	runner := NewPythonRunner(wasmPath, cfg, log)
	if err := runner.Start(context.Background()); err != nil {
		b.Fatalf("runner.Start: %v", err)
	}
	b.Cleanup(func() { _ = runner.Shutdown(context.Background()) })

	// Read script once.
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
	b.SetBytes(int64(len(script)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, err := runner.RunScript(ctx, script, input)
		cancel()
		if err != nil {
			b.Fatalf("RunScript failed at iteration %d: %v", i, err)
		}
	}
}
