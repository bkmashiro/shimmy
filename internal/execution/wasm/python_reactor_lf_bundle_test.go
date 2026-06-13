//go:build linux

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

func repoRootForLFBundleTest(t testing.TB) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller")
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

func buildLFBundleForReactorTest(t testing.TB, root, evalEntry, previewEntry string, includeRoots ...string) string {
	t.Helper()
	repo := repoRootForLFBundleTest(t)
	out := filepath.Join(t.TempDir(), filepath.Base(root)+".bundle.py")
	args := []string{
		filepath.Join(repo, "tools", "lf-bundle-python", "lf_bundle_python.py"),
		"--root", filepath.Join(repo, root),
		"--adapter-root", filepath.Join(repo, "examples", "lambda-feedback-adapter"),
		"--eval-entrypoint", evalEntry,
		"--out", out,
	}
	if previewEntry != "" {
		args = append(args, "--preview-entrypoint", previewEntry)
	}
	for _, includeRoot := range includeRoots {
		if includeRoot != "" {
			args = append(args, "--include-root", includeRoot)
		}
	}
	cmd := exec.Command("python3", args...)
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "bundle command failed: %s", output)
	return out
}

func newReactorRunnerForLFBundleTest(t testing.TB) *ReactorPythonRunner {
	t.Helper()
	wasmPath := os.Getenv("PYTHON_REACTOR_WASM")
	if wasmPath == "" {
		t.Skip("PYTHON_REACTOR_WASM not set — skipping Lambda Feedback reactor bundle matrix")
	}

	log, err := zap.NewDevelopment()
	require.NoError(t, err)
	runner := NewReactorPythonRunner(wasmPath, Config{
		Timeout:        120 * time.Second,
		MaxMemoryPages: 8192,
	}, log)
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

func TestReactorPythonRunner_LambdaFeedbackBundleMatrix(t *testing.T) {
	cases := []struct {
		name         string
		root         string
		evalEntry    string
		previewEntry string
		input        string
		method       string
		wantKeys     []string
		pureDeps     bool
	}{
		{
			name:         "boilerplate package",
			root:         "examples/lambda-feedback-fixtures/boilerplate-python",
			evalEntry:    "evaluation_function.evaluation:evaluation_function",
			previewEntry: "evaluation_function.preview:preview_function",
			input:        `{"response":"2","answer":"2","params":{}}`,
			wantKeys:     []string{"is_correct"},
		},
		{
			name:         "compare boolean sympy package",
			root:         "examples/lambda-feedback-fixtures/compare-boolean",
			evalEntry:    "evaluation_function.evaluation:evaluation_function",
			previewEntry: "evaluation_function.preview:preview_function",
			input:        `{"response":"A","answer":"A","params":{"disallowed":[]}}`,
			wantKeys:     []string{"is_correct"},
			pureDeps:     true,
		},
		{
			name:      "array equal numpy package",
			root:      "examples/lambda-feedback-fixtures/array-equal",
			evalEntry: "evaluation_function.evaluation:evaluation_function",
			input:     `{"response":[[1,2],[3,4]],"answer":[[1,2],[3,4]],"params":{"rtol":0,"atol":0}}`,
			wantKeys:  []string{"is_correct"},
		},
		{
			name:      "is similar numpy package",
			root:      "examples/lambda-feedback-fixtures/is-similar",
			evalEntry: "evaluation_function.evaluation:evaluation_function",
			input:     `{"response":1.0,"answer":1.0,"params":{"rtol":0,"atol":0}}`,
			wantKeys:  []string{"is_correct", "allowed_diff"},
		},
		{
			name:         "symbolic equal package",
			root:         "examples/lambda-feedback-fixtures/symbolic-equal",
			evalEntry:    "evaluation_function.evaluation:evaluation_function",
			previewEntry: "evaluation_function.preview:preview_function",
			input:        `{"response":"x + 1","answer":"1 + x","params":{"strict_syntax":false}}`,
			wantKeys:     []string{"is_correct"},
			pureDeps:     true,
		},
		{
			name:         "symbolic equal latex preview package",
			root:         "examples/lambda-feedback-fixtures/symbolic-equal",
			evalEntry:    "evaluation_function.evaluation:evaluation_function",
			previewEntry: "evaluation_function.preview:preview_function",
			input:        `{"response":"\\frac{1}{2}","answer":"","params":{"is_latex":true}}`,
			method:       "preview",
			wantKeys:     []string{"preview"},
			pureDeps:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var includeRoots []string
			if tc.pureDeps {
				pureDeps := os.Getenv("SHIMMY_LF_PURE_PY_DEPS")
				if pureDeps == "" {
					t.Skip("SHIMMY_LF_PURE_PY_DEPS not set — skipping fixture that needs bundled pure-Python deps")
				}
				includeRoots = append(includeRoots, pureDeps)
				if polyfills := os.Getenv("SHIMMY_LF_REACTOR_POLYFILLS"); polyfills != "" {
					includeRoots = append(includeRoots, polyfills)
				}
			}
			bundlePath := buildLFBundleForReactorTest(t, tc.root, tc.evalEntry, tc.previewEntry, includeRoots...)
			scriptBytes, err := os.ReadFile(bundlePath)
			require.NoError(t, err)

			runner := newReactorRunnerForLFBundleTest(t)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			method := tc.method
			if method == "" {
				method = "eval"
			}
			result, err := runner.SendRequest(ctx, string(scriptBytes), method, tc.input)
			require.NoError(t, err)
			for _, key := range tc.wantKeys {
				require.Contains(t, result, key)
			}
			require.NotContains(t, result, "error", "reactor bundle returned Python error: %#v", result)
		})
	}
}
