//go:build linux

package wasm

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestReactorPythonRunner_LegacyPyExecFallback verifies that old reactor
// artifacts without the newer evaluate export still execute through the
// historical py_exec + resp_buf/resp_len ABI. The fixture is tiny and not
// CPython; it only models the host-facing legacy ABI, so this test runs on all
// developer platforms instead of requiring the large Linux-only reactor binary.
func TestReactorPythonRunner_LegacyPyExecFallback(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	wasmPath := filepath.Join(filepath.Dir(filename), "testdata", "reactor-legacy-abi.wasm")

	log, err := zap.NewDevelopment()
	require.NoError(t, err)
	runner := NewReactorPythonRunner(wasmPath, Config{
		Timeout:        5 * time.Second,
		MaxMemoryPages: 16,
	}, log)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, runner.Init(ctx))
	t.Cleanup(func() {
		shutCtx, sc := context.WithTimeout(context.Background(), 5*time.Second)
		defer sc()
		_ = runner.Shutdown(shutCtx)
	})

	require.Nil(t, runner.fnEvaluate, "fixture intentionally has no evaluate export")
	result, err := runner.SendRequest(ctx, `ignored by fixture`, "eval", `{"response":"x","answer":"x"}`)
	require.NoError(t, err)
	assert.Equal(t, true, result["is_correct"])
	assert.Equal(t, "legacy py_exec", result["feedback"])
}
