package wasm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"go.uber.org/zap"
)

type agentPythonSnapshotSelectionTestStrategy struct {
	SnapshotStrategy
	selected string
}

func (s *agentPythonSnapshotSelectionTestStrategy) selectedSnapshotMode() string {
	return s.selected
}

func writeAgentPythonManifestFixture(t *testing.T, customModule, customName string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "agent-python-runtime.wasm")
	wasmBytes := []byte("\x00asm\x01\x00\x00\x00fixture")
	require.NoError(t, os.WriteFile(wasmPath, wasmBytes, 0o644))
	digest := sha256.Sum256(wasmBytes)
	manifest := map[string]any{
		"schema_version":   2,
		"abi_version":      "v1",
		"artifact_profile": "base",
		"target":           "wasm32-wasip1",
		"artifact": map[string]any{
			"filename": filepath.Base(wasmPath),
			"size":     len(wasmBytes),
			"sha256":   hex.EncodeToString(digest[:]),
		},
		"build": map[string]any{
			"repository_commit": "a3b7c9d1e5f80123456789abcdef0123456789ab",
			"source_date_epoch": "1784781655",
			"compiler_target":   "wasm32-wasip1",
			"execution_model":   "reactor",
		},
		"wasm": map[string]any{
			"exports": []string{
				"memory", "runtime_init", "runtime_prepare", "alloc", "dealloc", "execute", "_initialize",
			},
			"imports": []map[string]string{
				{"module": customModule, "name": customName},
				{"module": "wasi_snapshot_preview1", "name": "random_get"},
			},
		},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	require.NoError(t, err)
	manifestPath := filepath.Join(dir, "manifest.json")
	require.NoError(t, os.WriteFile(manifestPath, append(encoded, '\n'), 0o644))
	return wasmPath, manifestPath
}

func TestVerifyAgentPythonArtifactAcceptsPinnedV1Contract(t *testing.T) {
	wasmPath, manifestPath := writeAgentPythonManifestFixture(t, "agent_runtime_v1", "host_call")

	artifact, err := verifyAgentPythonArtifact(wasmPath, manifestPath)

	require.NoError(t, err)
	assert.Equal(t, "base", artifact.Profile)
	assert.Equal(t, "a3b7c9d1e5f80123456789abcdef0123456789ab", artifact.ProducerCommit)
	assert.Len(t, artifact.WasmBytes, 15)
}

func TestVerifyAgentPythonArtifactRejectsUnexpectedCustomImport(t *testing.T) {
	wasmPath, manifestPath := writeAgentPythonManifestFixture(t, "legacy_env", "stub")

	_, err := verifyAgentPythonArtifact(wasmPath, manifestPath)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `unexpected custom import "legacy_env"."stub"`)
}

func TestVerifyAgentPythonArtifactRejectsDigestDrift(t *testing.T) {
	wasmPath, manifestPath := writeAgentPythonManifestFixture(t, "agent_runtime_v1", "host_call")
	require.NoError(t, os.WriteFile(wasmPath, []byte("\x00asm\x01\x00\x00\x00changed"), 0o644))

	_, err := verifyAgentPythonArtifact(wasmPath, manifestPath)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "artifact SHA-256")
}

func TestBuildAgentPythonRunRequestPreservesShimmyMethodAndParams(t *testing.T) {
	params := map[string]any{
		"response": "42",
		"answer":   "42",
		"params":   map[string]any{"tolerance": 1e-9},
	}

	request, err := buildAgentPythonRunRequest("shimmy-run-1", "preview", params, "")

	require.NoError(t, err)
	var envelope struct {
		RunID  string         `json:"run_id"`
		Code   string         `json:"code"`
		Inputs map[string]any `json:"inputs"`
	}
	require.NoError(t, json.Unmarshal(request, &envelope))
	assert.Equal(t, "shimmy-run-1", envelope.RunID)
	assert.Equal(t, agentPythonPreparedCall, envelope.Code)
	assert.Equal(t, "preview", envelope.Inputs["method"])
	assert.Equal(t, "42", envelope.Inputs["params"].(map[string]any)["response"])
	assert.NotContains(t, envelope.Code, "shimmy-run-1")
}

func TestBuildAgentPythonRunRequestSupportsExplicitPreloadOff(t *testing.T) {
	request, err := buildAgentPythonRunRequest(
		"shimmy-run-2",
		"eval",
		map[string]any{"response": "1", "answer": "1"},
		"def evaluation_function(response, answer, params=None): return {'is_correct': True}",
	)

	require.NoError(t, err)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(request, &envelope))
	inputs := envelope["inputs"].(map[string]any)
	assert.Contains(t, envelope["code"], `inputs["script"]`)
	assert.Contains(t, inputs["script"], "def evaluation_function")
}

func TestDecodeAgentPythonResponseMapsSuccessToLegacyResult(t *testing.T) {
	payload := []byte(`{"status":"ok","result":{"is_correct":true},"receipts":[],"metrics":{"capability_calls":0,"result_bytes":19},"error":null}`)

	result, err := decodeAgentPythonResponse(payload)

	require.NoError(t, err)
	assert.Equal(t, true, result["is_correct"])
}

func TestDecodeAgentPythonResponseMapsPythonExceptionToLegacyStructuredResult(t *testing.T) {
	payload := []byte(`{"status":"error","result":null,"receipts":[],"metrics":{"capability_calls":0,"result_bytes":0},"error":{"code":"python_exception","message":"bad value","error_type":"ValueError","traceback":"trace"}}`)

	result, err := decodeAgentPythonResponse(payload)

	require.NoError(t, err)
	assert.Equal(t, "bad value", result["error"])
	assert.Equal(t, "ValueError", result["error_type"])
	assert.Equal(t, "trace", result["traceback"])
	assert.Equal(t, "python_exception", result["error_code"])
}

func TestAgentPythonRejectsHostFilesystemPaths(t *testing.T) {
	t.Setenv("FUNCTION_WASM_ALLOWED_PATHS", "/tmp")
	dispatcher := NewAgentPythonDispatcher(Config{}, zap.NewNop())
	err := dispatcher.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not expose Host filesystem paths")
}

func TestAgentPythonDispatcherRealNumPyArtifactCompatibility(t *testing.T) {
	wasmPath := os.Getenv("AGENT_PYTHON_RUNTIME_WASM")
	manifestPath := os.Getenv("AGENT_PYTHON_RUNTIME_MANIFEST")
	if wasmPath == "" || manifestPath == "" {
		t.Skip("AGENT_PYTHON_RUNTIME_WASM and AGENT_PYTHON_RUNTIME_MANIFEST are required")
	}

	scriptPath := filepath.Join(t.TempDir(), "eval.py")
	script := `
import numpy as np
_counter = 0

def evaluation_function(response, answer, params=None):
    global _counter
    _counter += 1
    if response == "explode":
        raise ValueError("expected explosion")
    if response == "host_call":
        from agent_runtime.tools import fetch_many
        return fetch_many([{"request_id": "r1", "target": "fixture", "path": "/ok"}])
    if response == "float128":
        one = np.longdouble("1")
        wide = np.longdouble("1.0000000000000000000000000000000002")
        return {
            "longdouble_itemsize": int(np.dtype(np.longdouble).itemsize),
            "longdouble_nmant": int(np.finfo(np.longdouble).nmant),
            "double_nmant": int(np.finfo(np.double).nmant),
            "preserves_extra_precision": bool(wide > one),
            "narrows_to_double_one": bool(float(wide) == 1.0),
            "epsilon_is_narrower": bool(np.finfo(np.longdouble).eps < np.finfo(np.double).eps),
            "counter": _counter,
        }
    return {"is_correct": response == answer, "counter": _counter}

def preview_function(response, answer, params=None):
    return {"preview": f"response={response}"}
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o644))

	dispatcher := NewAgentPythonDispatcher(Config{
		ModulePath:              wasmPath,
		AgentPythonManifestPath: manifestPath,
		PythonScriptPath:        scriptPath,
		PythonLifecycle:         "snapshot",
		SnapshotMode:            "memcpy",
		MaxInstances:            1,
		MaxMemoryPages:          8192,
		Timeout:                 120 * time.Second,
	}, zap.NewNop())
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer startCancel()
	require.NoError(t, dispatcher.Start(startContext))
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dispatcher.Shutdown(shutdownContext)
	})

	health, err := dispatcher.Send(context.Background(), "healthcheck", nil)
	require.NoError(t, err)
	assert.Equal(t, "snapshot", health["result"].(map[string]any)["lifecycle"])
	assert.Equal(t, "memcpy", health["result"].(map[string]any)["snapshot_mode"])
	assert.Equal(t, "linear-memory-memcpy", health["result"].(map[string]any)["reset_mode"])

	first, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, true, first["result"].(map[string]any)["is_correct"])
	assert.Equal(t, float64(1), first["result"].(map[string]any)["counter"])

	second, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), second["result"].(map[string]any)["counter"], "snapshot restore must not retain globals")

	preview, err := dispatcher.Send(context.Background(), "preview", map[string]any{"response": "3.14", "answer": "3.14"})
	require.NoError(t, err)
	assert.Equal(t, "response=3.14", preview["result"].(map[string]any)["preview"])

	failure, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "explode", "answer": "x"})
	require.NoError(t, err)
	assert.Equal(t, "ValueError", failure["result"].(map[string]any)["error_type"])

	denied, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "host_call", "answer": "x"})
	require.NoError(t, err)
	assert.Equal(t, "RuntimeError", denied["result"].(map[string]any)["error_type"])
	assert.Contains(t, denied["result"].(map[string]any)["error"], "Host capability bridge rejected")

	binary128, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "float128", "answer": "x"})
	require.NoError(t, err)
	value := binary128["result"].(map[string]any)
	assert.Equal(t, float64(16), value["longdouble_itemsize"])
	assert.GreaterOrEqual(t, value["longdouble_nmant"].(float64), float64(112))
	assert.Equal(t, true, value["preserves_extra_precision"])
	assert.Equal(t, true, value["narrows_to_double_one"])
	assert.Equal(t, true, value["epsilon_is_narrower"])
}

func TestAgentPythonSnapshotStrategyName(t *testing.T) {
	assert.Equal(t, "memcpy", agentPythonSnapshotStrategyName(NewFullMemcpyStrategy()))
	assert.Equal(t, "cow", agentPythonSnapshotStrategyName(&agentPythonSnapshotSelectionTestStrategy{
		SnapshotStrategy: NewFullMemcpyStrategy(),
		selected:         "cow",
	}))
	assert.Equal(t, "memcpy", agentPythonSnapshotStrategyName(&agentPythonSnapshotSelectionTestStrategy{
		SnapshotStrategy: NewFullMemcpyStrategy(),
		selected:         "memcpy",
	}))
}

func TestAcquireAgentPythonSnapshotSlotReplenishesMissingSlot(t *testing.T) {
	prepared := make(chan *agentPythonModuleSlot, 1)
	closed := make(chan struct{})
	want := &agentPythonModuleSlot{snapshotSelected: "memcpy"}
	calls := 0

	got, err := acquireAgentPythonSnapshotSlot(
		context.Background(),
		prepared,
		closed,
		func(context.Context) (*agentPythonModuleSlot, error) {
			calls++
			return want, nil
		},
	)

	require.NoError(t, err)
	assert.Same(t, want, got)
	assert.Equal(t, 1, calls)
}

func TestAcquireAgentPythonSnapshotSlotReturnsReplenishFailure(t *testing.T) {
	prepared := make(chan *agentPythonModuleSlot, 1)
	closed := make(chan struct{})
	wantErr := errors.New("replacement unavailable")

	_, err := acquireAgentPythonSnapshotSlot(
		context.Background(),
		prepared,
		closed,
		func(context.Context) (*agentPythonModuleSlot, error) {
			return nil, wantErr
		},
	)

	require.ErrorIs(t, err, wantErr)
}

func TestRestoreAgentPythonSnapshotRejectsMemoryGrowth(t *testing.T) {
	ctx := context.Background()
	rt, compiled := compileEchoModule(t, ctx, echoWasmBytes(t))
	t.Cleanup(func() { require.NoError(t, rt.Close(ctx)) })
	module, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	require.NoError(t, err)

	strategy := NewFullMemcpyStrategy()
	require.NoError(t, strategy.Take(module.Memory()))
	slot := &agentPythonModuleSlot{
		module:       module,
		strategy:     strategy,
		baselineSize: module.Memory().Size(),
	}
	_, grew := module.Memory().Grow(1)
	require.True(t, grew)

	err = restoreAgentPythonSnapshot(slot)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory size drift")
}

func TestAgentPythonDispatcherRealNumPyCOWRestoresState(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("COW memory allocator is Linux-only")
	}
	wasmPath := os.Getenv("AGENT_PYTHON_RUNTIME_WASM")
	manifestPath := os.Getenv("AGENT_PYTHON_RUNTIME_MANIFEST")
	if wasmPath == "" || manifestPath == "" {
		t.Skip("AGENT_PYTHON_RUNTIME_WASM and AGENT_PYTHON_RUNTIME_MANIFEST are required")
	}

	scriptPath := filepath.Join(t.TempDir(), "cow.py")
	script := `
_counter = 0

def evaluation_function(response, answer, params=None):
    global _counter
    _counter += 1
    return {"counter": _counter, "is_correct": response == answer}
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o644))

	dispatcher := NewAgentPythonDispatcher(Config{
		ModulePath:              wasmPath,
		AgentPythonManifestPath: manifestPath,
		PythonScriptPath:        scriptPath,
		PythonLifecycle:         "snapshot",
		SnapshotMode:            "cow",
		MaxInstances:            2,
		MaxMemoryPages:          8192,
		Timeout:                 120 * time.Second,
	}, zap.NewNop())
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer startCancel()
	require.NoError(t, dispatcher.Start(startContext))
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dispatcher.Shutdown(shutdownContext)
	})

	health, err := dispatcher.Send(context.Background(), "healthcheck", nil)
	require.NoError(t, err)
	assert.Equal(t, "cow", health["result"].(map[string]any)["snapshot_selected"])
	assert.Equal(t, "linear-memory-cow", health["result"].(map[string]any)["reset_mode"])
	assert.Equal(t, 2, health["result"].(map[string]any)["prepared_ready"])

	for i := 0; i < 2; i++ {
		result, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
		require.NoError(t, err)
		assert.Equal(t, float64(1), result["result"].(map[string]any)["counter"])
	}
}

func TestAgentPythonDispatcherSingleUsePreparedRefillsNeverServedCandidates(t *testing.T) {
	wasmPath := os.Getenv("AGENT_PYTHON_RUNTIME_WASM")
	manifestPath := os.Getenv("AGENT_PYTHON_RUNTIME_MANIFEST")
	if wasmPath == "" || manifestPath == "" {
		t.Skip("AGENT_PYTHON_RUNTIME_WASM and AGENT_PYTHON_RUNTIME_MANIFEST are required")
	}

	scriptPath := filepath.Join(t.TempDir(), "single-use.py")
	script := `
_counter = 0

def evaluation_function(response, answer, params=None):
    global _counter
    _counter += 1
    return {"counter": _counter, "is_correct": response == answer}
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o644))

	dispatcher := NewAgentPythonDispatcher(Config{
		ModulePath:              wasmPath,
		AgentPythonManifestPath: manifestPath,
		PythonScriptPath:        scriptPath,
		PythonLifecycle:         "single-use",
		PythonPreparedCapacity:  1,
		MaxInstances:            1,
		MaxMemoryPages:          8192,
		Timeout:                 120 * time.Second,
	}, zap.NewNop())
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer startCancel()
	require.NoError(t, dispatcher.Start(startContext))
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dispatcher.Shutdown(shutdownContext)
	})

	health, err := dispatcher.Send(context.Background(), "healthcheck", nil)
	require.NoError(t, err)
	assert.Equal(t, "single-use", health["result"].(map[string]any)["lifecycle"])
	assert.Equal(t, 1, health["result"].(map[string]any)["prepared_ready"])
	assert.Equal(t, "single-use-prepared", health["result"].(map[string]any)["reset_mode"])

	first, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), first["result"].(map[string]any)["counter"])

	// The hit starts a slow background refill. An immediate next request must not
	// wait for it; it initializes one fresh single-use fallback synchronously.
	second, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), second["result"].(map[string]any)["counter"])

	require.Eventually(t, func() bool {
		health, err = dispatcher.Send(context.Background(), "healthcheck", nil)
		if err != nil {
			return false
		}
		state := health["result"].(map[string]any)
		return state["prepared_ready"] == 1 && state["prepared_refills"] == uint64(1)
	}, 2*time.Minute, 100*time.Millisecond)

	health, err = dispatcher.Send(context.Background(), "healthcheck", nil)
	require.NoError(t, err)
	state := health["result"].(map[string]any)
	assert.Equal(t, uint64(1), state["prepared_hits"])
	assert.Equal(t, uint64(1), state["prepared_misses"])

	third, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), third["result"].(map[string]any)["counter"])

	health, err = dispatcher.Send(context.Background(), "healthcheck", nil)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), health["result"].(map[string]any)["prepared_hits"])
}

func TestAgentPythonDispatcherTimeoutDoesNotPoisonRuntime(t *testing.T) {
	wasmPath := os.Getenv("AGENT_PYTHON_RUNTIME_WASM")
	manifestPath := os.Getenv("AGENT_PYTHON_RUNTIME_MANIFEST")
	if wasmPath == "" || manifestPath == "" {
		t.Skip("AGENT_PYTHON_RUNTIME_WASM and AGENT_PYTHON_RUNTIME_MANIFEST are required")
	}

	scriptPath := filepath.Join(t.TempDir(), "timeout.py")
	script := `
def evaluation_function(response, answer, params=None):
    if response == "loop":
        while True:
            pass
    return {"is_correct": response == answer}
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o644))

	dispatcher := NewAgentPythonDispatcher(Config{
		ModulePath:              wasmPath,
		AgentPythonManifestPath: manifestPath,
		PythonScriptPath:        scriptPath,
		MaxInstances:            1,
		MaxMemoryPages:          8192,
		Timeout:                 12 * time.Second,
	}, zap.NewNop())
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer startCancel()
	require.NoError(t, dispatcher.Start(startContext))
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dispatcher.Shutdown(shutdownContext)
	})

	_, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "loop", "answer": "x"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	after, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, true, after["result"].(map[string]any)["is_correct"])
}

func TestAgentPythonDispatcherRealLambdaFeedbackBundle(t *testing.T) {
	wasmPath := os.Getenv("AGENT_PYTHON_RUNTIME_WASM")
	manifestPath := os.Getenv("AGENT_PYTHON_RUNTIME_MANIFEST")
	if wasmPath == "" || manifestPath == "" {
		t.Skip("set AGENT_PYTHON_RUNTIME_WASM and AGENT_PYTHON_RUNTIME_MANIFEST")
	}

	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
	bundlePath := filepath.Join(t.TempDir(), "boilerplate.bundle.py")
	command := exec.Command("python3",
		filepath.Join(repoRoot, "tools", "lf-bundle-python", "lf_bundle_python.py"),
		"--root", filepath.Join(repoRoot, "examples", "lambda-feedback-fixtures", "boilerplate-python"),
		"--adapter-root", filepath.Join(repoRoot, "examples", "lambda-feedback-adapter"),
		"--eval-entrypoint", "evaluation_function.evaluation:evaluation_function",
		"--preview-entrypoint", "evaluation_function.preview:preview_function",
		"--out", bundlePath,
	)
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))

	dispatcher := NewAgentPythonDispatcher(Config{
		ModulePath:              wasmPath,
		AgentPythonManifestPath: manifestPath,
		PythonScriptPath:        bundlePath,
		MaxMemoryPages:          8192,
		MaxInstances:            1,
		Timeout:                 2 * time.Minute,
	}, zap.NewNop())
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer startCancel()
	require.NoError(t, dispatcher.Start(startContext))
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dispatcher.Shutdown(shutdownContext)
	})

	evalResult, err := dispatcher.Send(context.Background(), "eval", map[string]any{
		"response": "same",
		"answer":   "same",
		"params":   map[string]any{},
	})
	require.NoError(t, err)
	assert.Equal(t, true, evalResult["result"].(map[string]any)["is_correct"])

	previewResult, err := dispatcher.Send(context.Background(), "preview", map[string]any{
		"response": "x+y",
		"params":   map[string]any{},
	})
	require.NoError(t, err)
	preview := previewResult["result"].(map[string]any)["preview"].(map[string]any)
	assert.Equal(t, "x+y", preview["sympy"])
}
