package execution

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

func TestNewDispatcher_RequiresCommandForProcessInterfaces(t *testing.T) {
	tests := []struct {
		name string
		io   supervisor.IOInterface
	}{
		{name: "rpc", io: supervisor.RpcIO},
		{name: "file", io: supervisor.FileIO},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewDispatcher(Params{
				Context: context.Background(),
				Config: Config{
					MaxWorkers: 1,
					Supervisor: supervisor.Config{
						IO: supervisor.IOConfig{Interface: tt.io},
					},
				},
				Log: zap.NewNop(),
			})
			if err == nil {
				t.Fatal("expected missing command error")
			}
			if got, want := err.Error(), "FUNCTION_COMMAND is required"; !strings.Contains(got, want) {
				t.Fatalf("expected error to contain %q, got %q", want, got)
			}
		})
	}
}

func TestNewDispatcher_PyodideRequiresScriptOrPackageConfig(t *testing.T) {
	t.Setenv("FUNCTION_PYODIDE_RUNNER", "/tmp/runner.js")
	t.Setenv("FUNCTION_PYODIDE_SCRIPT", "")
	t.Setenv("FUNCTION_PYODIDE_ROOT", "")
	t.Setenv("FUNCTION_PYODIDE_EVAL_ENTRYPOINT", "")

	_, err := NewDispatcher(Params{
		Context: context.Background(),
		Config: Config{
			MaxWorkers: 1,
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{Interface: supervisor.PyodideIO},
			},
		},
		Log: zap.NewNop(),
	})
	if err == nil {
		t.Fatal("expected pyodide config error")
	}
	if got, want := err.Error(), "FUNCTION_PYODIDE_SCRIPT"; !strings.Contains(got, want) {
		t.Fatalf("expected error to contain %q, got %q", want, got)
	}
}

func TestNewDispatcher_PyodideDoesNotRequireFunctionCommand(t *testing.T) {
	t.Setenv("FUNCTION_PYODIDE_RUNNER", "/tmp/runner.js")
	t.Setenv("FUNCTION_PYODIDE_SCRIPT", "/tmp/eval.py")
	t.Setenv("FUNCTION_PYODIDE_ROOT", "")
	t.Setenv("FUNCTION_PYODIDE_EVAL_ENTRYPOINT", "")

	d, err := NewDispatcher(Params{
		Context: context.Background(),
		Config: Config{
			MaxWorkers: 1,
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{Interface: supervisor.PyodideIO},
			},
		},
		Log: zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("expected pyodide dispatcher without FUNCTION_COMMAND, got %v", err)
	}
	if d == nil {
		t.Fatal("expected dispatcher")
	}
}

func TestNewDispatcher_PyodidePackageModeDoesNotRequireScript(t *testing.T) {
	t.Setenv("FUNCTION_PYODIDE_RUNNER", "/tmp/runner.js")
	t.Setenv("FUNCTION_PYODIDE_SCRIPT", "")
	t.Setenv("FUNCTION_PYODIDE_ROOT", "/tmp/evaluator-root")
	t.Setenv("FUNCTION_PYODIDE_EVAL_ENTRYPOINT", "evaluation_function.evaluation:evaluation_function")

	d, err := NewDispatcher(Params{
		Context: context.Background(),
		Config: Config{
			MaxWorkers: 1,
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{Interface: supervisor.PyodideIO},
			},
		},
		Log: zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("expected pyodide package-mode dispatcher without script, got %v", err)
	}
	if d == nil {
		t.Fatal("expected dispatcher")
	}
}
