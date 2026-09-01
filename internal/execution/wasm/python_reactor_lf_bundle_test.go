package wasm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func lambdaFeedbackReactorRepoRoot(t testing.TB) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller")
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

func buildLambdaFeedbackBundle(t testing.TB, root, evalEntry, previewEntry string) string {
	t.Helper()
	repo := lambdaFeedbackReactorRepoRoot(t)
	output := filepath.Join(t.TempDir(), "evaluator.bundle.py")
	args := []string{
		filepath.Join(repo, "tools", "lf-bundle-python", "lf_bundle_python.py"),
		"--root", filepath.Join(repo, root),
		"--adapter-root", filepath.Join(repo, "tools", "lf-bundle-python", "adapter"),
		"--eval-entrypoint", evalEntry,
		"--out", output,
	}
	if previewEntry != "" {
		args = append(args, "--preview-entrypoint", previewEntry)
	}
	command := exec.Command("python3", args...)
	command.Dir = repo
	outputBytes, err := command.CombinedOutput()
	require.NoError(t, err, "bundle command failed: %s", outputBytes)
	return output
}

func startLambdaFeedbackReactor(t *testing.T, bundlePath string) *PythonReactorDispatcher {
	t.Helper()
	wasmPath := os.Getenv("SHIMMY_PYTHON_REACTOR_WASM")
	manifestPath := os.Getenv("SHIMMY_PYTHON_REACTOR_MANIFEST")
	if wasmPath == "" || manifestPath == "" {
		t.Skip("SHIMMY_PYTHON_REACTOR_WASM and SHIMMY_PYTHON_REACTOR_MANIFEST are required")
	}

	// These are the same FUNCTION_WASM_* inputs used by the staging E2E script.
	t.Setenv("FUNCTION_WASM_PROFILE", "python-reactor")
	t.Setenv("FUNCTION_WASM_MODULE", wasmPath)
	t.Setenv("FUNCTION_WASM_MANIFEST", manifestPath)
	t.Setenv("FUNCTION_WASM_PYTHON_SCRIPT", bundlePath)
	t.Setenv("FUNCTION_WASM_PYTHON_LIFECYCLE", "snapshot")
	t.Setenv("FUNCTION_WASM_ALLOWED_PATHS", "")

	dispatcher := NewPythonReactorDispatcher(Config{
		MaxInstances:   1,
		MaxMemoryPages: 8192,
		Timeout:        2 * time.Minute,
	}, zap.NewNop())
	startContext, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	require.NoError(t, dispatcher.Start(startContext))
	t.Cleanup(func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), time.Minute)
		defer shutdownCancel()
		_ = dispatcher.Shutdown(shutdownContext)
	})
	return dispatcher
}

func lambdaFeedbackResult(t testing.TB, response map[string]any) map[string]any {
	t.Helper()
	if errValue, ok := response["error"]; ok {
		t.Fatalf("reactor returned error: %#v", errValue)
	}
	result, ok := response["result"].(map[string]any)
	require.True(t, ok, "response result: %#v", response)
	return result
}

func TestPythonReactorDispatcherLambdaFeedbackCompatibility(t *testing.T) {
	wasmPath := os.Getenv("SHIMMY_PYTHON_REACTOR_WASM")
	manifestPath := os.Getenv("SHIMMY_PYTHON_REACTOR_MANIFEST")
	if wasmPath == "" || manifestPath == "" {
		t.Skip("SHIMMY_PYTHON_REACTOR_WASM and SHIMMY_PYTHON_REACTOR_MANIFEST are required")
	}

	t.Run("boilerplate-python", func(t *testing.T) {
		bundle := buildLambdaFeedbackBundle(
			t,
			"examples/lambda-feedback-fixtures/boilerplate-python",
			"evaluation_function.evaluation:evaluation_function",
			"evaluation_function.preview:preview_function",
		)
		dispatcher := startLambdaFeedbackReactor(t, bundle)

		health, err := dispatcher.Send(context.Background(), "healthcheck", nil)
		require.NoError(t, err)
		healthResult := lambdaFeedbackResult(t, health)
		require.Equal(t, "numpy-core", healthResult["profile"])

		eval, err := dispatcher.Send(context.Background(), "eval", map[string]any{
			"response": "2", "answer": "2", "params": map[string]any{},
		})
		require.NoError(t, err)
		require.Equal(t, map[string]any{"is_correct": true}, lambdaFeedbackResult(t, eval))

		preview, err := dispatcher.Send(context.Background(), "preview", map[string]any{
			"response": "x + 1", "params": map[string]any{},
		})
		require.NoError(t, err)
		require.Equal(t, map[string]any{"preview": map[string]any{"sympy": "x + 1"}}, lambdaFeedbackResult(t, preview))
	})

	t.Run("ArrayEqual", func(t *testing.T) {
		bundle := buildLambdaFeedbackBundle(
			t,
			"examples/lambda-feedback-fixtures/array-equal",
			"evaluation_function.evaluation:evaluation_function",
			"",
		)
		dispatcher := startLambdaFeedbackReactor(t, bundle)

		correct, err := dispatcher.Send(context.Background(), "eval", map[string]any{
			"response": [][]any{{1, 2}, {3, 4}},
			"answer":   [][]any{{1, 2}, {3, 4}},
			"params":   map[string]any{"rtol": 0, "atol": 0},
		})
		require.NoError(t, err)
		require.Equal(t, true, lambdaFeedbackResult(t, correct)["is_correct"])

		incorrect, err := dispatcher.Send(context.Background(), "eval", map[string]any{
			"response": [][]any{{1, 2}, {3, 5}},
			"answer":   [][]any{{1, 2}, {3, 4}},
			"params": map[string]any{
				"rtol": 0, "atol": 0, "feedback_for_incorrect_response": "not equal",
			},
		})
		require.NoError(t, err)
		incorrectResult := lambdaFeedbackResult(t, incorrect)
		require.Equal(t, false, incorrectResult["is_correct"])
		require.Equal(t, "not equal", incorrectResult["feedback"])
	})

	t.Run("IsSimilar", func(t *testing.T) {
		bundle := buildLambdaFeedbackBundle(
			t,
			"examples/lambda-feedback-fixtures/is-similar",
			"evaluation_function.evaluation:evaluation_function",
			"",
		)
		dispatcher := startLambdaFeedbackReactor(t, bundle)

		result, err := dispatcher.Send(context.Background(), "eval", map[string]any{
			"response": 1.0,
			"answer":   1.0,
			"params":   map[string]any{"rtol": 0, "atol": 0},
		})
		require.NoError(t, err)
		isSimilar := lambdaFeedbackResult(t, result)
		require.Equal(t, true, isSimilar["is_correct"])
		require.Equal(t, float64(0), isSimilar["real_diff"])
		allowedDiff, ok := isSimilar["allowed_diff"].(float64)
		require.True(t, ok, "allowed_diff should be normalized to JSON number: %#v", isSimilar)
		require.Greater(t, allowedDiff, float64(0))
	})
}
