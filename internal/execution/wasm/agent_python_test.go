package wasm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

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

	first, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, true, first["result"].(map[string]any)["is_correct"])
	assert.Equal(t, float64(1), first["result"].(map[string]any)["counter"])

	second, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), second["result"].(map[string]any)["counter"], "fresh instance must not retain globals")

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
