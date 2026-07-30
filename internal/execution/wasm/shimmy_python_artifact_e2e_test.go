package wasm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const shimmyPythonIdentityV1 = uint64(0x53505231)

func TestShimmyPythonArtifactE2E(t *testing.T) {
	artifactPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_ARTIFACT")
	if artifactPath == "" {
		t.Skip("SHIMMY_PYTHON_RUNTIME_ARTIFACT is not set")
	}
	manifestPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_MANIFEST")
	expectedCommit := os.Getenv("SHIMMY_PYTHON_EXPECTED_COMMIT")
	require.NotEmpty(t, manifestPath)
	require.NotEmpty(t, expectedCommit)
	verified, err := verifyShimmyPythonArtifact(artifactPath, manifestPath, expectedCommit)
	require.NoError(t, err)
	artifact := verified.WasmBytes

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	rt := wazero.NewRuntime(ctx)
	t.Cleanup(func() { require.NoError(t, rt.Close(ctx)) })
	_, err = wasi_snapshot_preview1.Instantiate(ctx, rt)
	require.NoError(t, err)

	compiled, err := rt.CompileModule(ctx, artifact)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, compiled.Close(ctx)) })
	require.NoError(t, verifyShimmyPythonCompiledModule(compiled))

	var guestStderr bytes.Buffer
	moduleConfig := wazero.NewModuleConfig().
		WithName("").
		WithStderr(&guestStderr).
		WithRandSource(rand.Reader).
		WithSysWalltime().
		WithSysNanotime().
		WithSysNanosleep().
		WithStartFunctions()
	mod, err := rt.InstantiateModule(ctx, compiled, moduleConfig)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mod.Close(ctx)) })

	_, err = mod.ExportedFunction("_initialize").Call(ctx)
	require.NoError(t, err)
	identity, err := mod.ExportedFunction("shimmy_python_runtime_identity").Call(ctx)
	require.NoError(t, err)
	require.Equal(t, []uint64{shimmyPythonIdentityV1}, identity)
	initialized, err := mod.ExportedFunction("shimmy_python_init").Call(ctx)
	require.NoError(t, err)
	require.Equal(t, []uint64{0}, initialized)

	evaluator := []byte(`counter = 0

def evaluation_function(response, answer, params):
    global counter
    counter += 1
    return {"correct": response == answer, "counter": counter, "params": params}
`)
	if verified.Profile == "numpy-core" {
		evaluator = []byte(`import numpy as np
counter = 0

def evaluation_function(response, answer, params):
    global counter
    counter += 1
    matrix = np.arange(6).reshape(2, 3)
    vector = np.array([1, 2, 3])
    return {
        "correct": response == answer,
        "counter": counter,
        "numpy_version": np.__version__,
        "matmul": (matrix @ vector).tolist(),
        "sum": int(matrix.sum()),
    }
`)
	}
	prepareStatus := callShimmyPythonE2EWithBytes(t, ctx, mod, "shimmy_python_prepare", evaluator)
	require.Equalf(t, uint64(0), prepareStatus, "guest stderr:\n%s", guestStderr.String())

	memory := mod.Memory()
	baselineSize := memory.Size()
	baselineView, ok := memory.Read(0, baselineSize)
	require.True(t, ok)
	baseline := append([]byte(nil), baselineView...)

	request := []byte(`{"method":"eval","params":{"response":4,"answer":4,"params":{"tag":"base"}}}`)
	first := callShimmyPythonEvaluate(t, ctx, mod, request)
	require.Equal(t, "ok", first.Status)
	require.Equal(t, float64(1), first.Result["counter"])
	require.Equal(t, true, first.Result["correct"])
	if verified.Profile == "numpy-core" {
		require.Equal(t, "2.2.6", first.Result["numpy_version"])
		require.Equal(t, []any{float64(8), float64(26)}, first.Result["matmul"])
		require.Equal(t, float64(15), first.Result["sum"])
	}

	require.Equal(t, baselineSize, memory.Size(), "request grew linear memory")
	require.True(t, memory.Write(0, baseline))
	second := callShimmyPythonEvaluate(t, ctx, mod, request)
	require.Equal(t, "ok", second.Status)
	require.Equal(t, float64(1), second.Result["counter"], "snapshot restore did not reset Python state")

	invalid := callShimmyPythonEvaluate(
		t,
		ctx,
		mod,
		[]byte(`{"method":"eval","params":{},"script":"forbidden"}`),
	)
	require.Equal(t, "error", invalid.Status)
	require.Equal(t, "ValueError", invalid.Error.Type)
}

type shimmyPythonE2EResponse struct {
	Status string         `json:"status"`
	Result map[string]any `json:"result"`
	Error  struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func callShimmyPythonE2EWithBytes(
	t *testing.T,
	ctx context.Context,
	mod api.Module,
	function string,
	payload []byte,
) uint64 {
	t.Helper()
	allocated, err := mod.ExportedFunction("alloc").Call(ctx, uint64(len(payload)))
	require.NoError(t, err)
	require.Len(t, allocated, 1)
	pointer := allocated[0]
	require.NotZero(t, pointer)
	require.True(t, mod.Memory().Write(uint32(pointer), payload))
	result, err := mod.ExportedFunction(function).Call(ctx, pointer, uint64(len(payload)))
	require.NoError(t, err)
	_, deallocErr := mod.ExportedFunction("dealloc").Call(ctx, pointer)
	require.NoError(t, deallocErr)
	require.Len(t, result, 1)
	return result[0]
}

func callShimmyPythonEvaluate(
	t *testing.T,
	ctx context.Context,
	mod api.Module,
	request []byte,
) shimmyPythonE2EResponse {
	t.Helper()
	responsePointer := callShimmyPythonE2EWithBytes(t, ctx, mod, "evaluate", request)
	prefix, ok := mod.Memory().Read(uint32(responsePointer), 4)
	require.True(t, ok)
	responseLength := binary.LittleEndian.Uint32(prefix)
	require.LessOrEqual(t, responseLength, uint32(1<<20))
	body, ok := mod.Memory().Read(uint32(responsePointer)+4, responseLength)
	require.True(t, ok)
	var response shimmyPythonE2EResponse
	require.NoError(t, json.Unmarshal(append([]byte(nil), body...), &response))
	return response
}
