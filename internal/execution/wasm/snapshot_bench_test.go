//go:build linux

package wasm

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"go.uber.org/zap"
)

// BenchmarkSnapshot_* compares snapshot/restore strategies under realistic
// WASM workloads. Requires PYTHON_REACTOR_WASM env var.

func benchmarkReactorStrategy(b *testing.B, snapshotMode string) {
	b.Helper()
	wasmPath := os.Getenv("PYTHON_REACTOR_WASM")
	if wasmPath == "" {
		b.Skip("PYTHON_REACTOR_WASM not set")
	}

	log := zap.NewNop()
	cfg := Config{
		Timeout:        60 * time.Second,
		MaxMemoryPages: 8192,
		SnapshotMode:   snapshotMode,
	}

	runner := NewReactorPythonRunner(wasmPath, cfg, log)

	initCtx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	if err := runner.Init(initCtx); err != nil {
		b.Fatalf("Init failed: %v", err)
	}
	b.Cleanup(func() {
		shutCtx, sc := context.WithTimeout(context.Background(), 15*time.Second)
		defer sc()
		_ = runner.Shutdown(shutCtx)
	})

	_, filename, _, _ := runtime.Caller(0)
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
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := runner.SendRequest(ctx, script, "eval", input)
		cancel()
		if err != nil {
			b.Fatalf("SendRequest failed at i=%d: %v", i, err)
		}
	}
}

func BenchmarkSnapshot_Memcpy(b *testing.B)    { benchmarkReactorStrategy(b, "memcpy") }
func BenchmarkSnapshot_SoftDirty(b *testing.B) { benchmarkReactorStrategy(b, "soft-dirty") }
func BenchmarkSnapshot_Mprotect(b *testing.B)  { benchmarkReactorStrategy(b, "mprotect") }
func BenchmarkSnapshot_Uffd(b *testing.B)      { benchmarkReactorStrategy(b, "uffd") }
