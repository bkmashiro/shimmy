package execution

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

func TestApplyDBISecurityConfigWrapsRPCWorkerWithoutChangingInterface(t *testing.T) {
	t.Setenv("FUNCTION_DBI_SECURITY_ENABLED", "true")
	t.Setenv("FUNCTION_DBI_DRRUN", "/opt/dr/bin64/drrun")
	t.Setenv("FUNCTION_DBI_CLIENT", "/opt/dr/open_log.so")
	t.Setenv("FUNCTION_DBI_OPTIONS", "-logdir /tmp/drlogs")

	cfg, err := applyDBISecurityConfig(supervisor.Config{
		IO: supervisor.IOConfig{
			Interface: supervisor.RpcIO,
			Rpc:       supervisor.RpcConfig{Transport: supervisor.StdioTransport},
		},
		StartParams: supervisor.StartConfig{
			Cmd:  "python3",
			Args: []string{"worker.py", "--stdio"},
		},
		SendParams: supervisor.SendConfig{Timeout: time.Second},
	})
	require.NoError(t, err)

	assert.Equal(t, supervisor.RpcIO, cfg.IO.Interface)
	assert.Equal(t, supervisor.StdioTransport, cfg.IO.Rpc.Transport)
	assert.Equal(t, "/opt/dr/bin64/drrun", cfg.StartParams.Cmd)
	assert.Equal(t, []string{
		"-logdir", "/tmp/drlogs",
		"-c", "/opt/dr/open_log.so",
		"--", "python3", "worker.py", "--stdio",
	}, cfg.StartParams.Args)
}

func TestApplyDBISecurityConfigWrapsFileWorkerWithoutChangingInterface(t *testing.T) {
	t.Setenv("FUNCTION_DBI_SECURITY_ENABLED", "1")

	cfg, err := applyDBISecurityConfig(supervisor.Config{
		IO:          supervisor.IOConfig{Interface: supervisor.FileIO},
		StartParams: supervisor.StartConfig{Cmd: "python3", Args: []string{"eval.py"}},
	})
	require.NoError(t, err)

	assert.Equal(t, supervisor.FileIO, cfg.IO.Interface)
	assert.Equal(t, defaultDBIRunner, cfg.StartParams.Cmd)
	assert.Equal(t, []string{"--", "python3", "eval.py"}, cfg.StartParams.Args)
}

func TestApplyDBISecurityConfigLeavesDisabledWorkerUnchanged(t *testing.T) {
	original := supervisor.Config{
		IO:          supervisor.IOConfig{Interface: supervisor.WasmIO},
		StartParams: supervisor.StartConfig{Cmd: "eval.wasm"},
	}

	cfg, err := applyDBISecurityConfig(original)
	require.NoError(t, err)
	assert.Equal(t, original, cfg)
}

func TestApplyDBISecurityConfigRejectsWASM(t *testing.T) {
	t.Setenv("FUNCTION_DBI_SECURITY_ENABLED", "true")

	_, err := applyDBISecurityConfig(supervisor.Config{
		IO:          supervisor.IOConfig{Interface: supervisor.WasmIO},
		StartParams: supervisor.StartConfig{Cmd: "eval.wasm"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only supported for file and rpc")
	assert.Contains(t, err.Error(), "wasm")
}

func TestApplyDBISecurityConfigRejectsOtherProcessBackends(t *testing.T) {
	t.Setenv("FUNCTION_DBI_SECURITY_ENABLED", "true")

	_, err := applyDBISecurityConfig(supervisor.Config{
		IO:          supervisor.IOConfig{Interface: supervisor.PyodideIO},
		StartParams: supervisor.StartConfig{Cmd: "node"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only supported for file and rpc")
	assert.Contains(t, err.Error(), "pyodide")
}

func TestApplyDBISecurityConfigRejectsMissingCommand(t *testing.T) {
	t.Setenv("FUNCTION_DBI_SECURITY_ENABLED", "true")

	_, err := applyDBISecurityConfig(supervisor.Config{
		IO: supervisor.IOConfig{Interface: supervisor.FileIO},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FUNCTION_COMMAND")
}

func TestApplyDBISecurityConfigAcceptsReadablePolicyConfig(t *testing.T) {
	t.Setenv("FUNCTION_DBI_SECURITY_ENABLED", "true")
	policyPath := filepath.Join(t.TempDir(), "policy.yaml")
	require.NoError(t, os.WriteFile(policyPath, []byte("version: 1\n"), 0o600))
	t.Setenv("FUNCTION_DBI_CONFIG_PATH", policyPath)

	cfg, err := applyDBISecurityConfig(supervisor.Config{
		IO:          supervisor.IOConfig{Interface: supervisor.FileIO},
		StartParams: supervisor.StartConfig{Cmd: "python3"},
	})
	require.NoError(t, err)
	assert.Equal(t, defaultDBIRunner, cfg.StartParams.Cmd)
}

func TestApplyDBISecurityConfigRejectsMissingPolicyConfig(t *testing.T) {
	t.Setenv("FUNCTION_DBI_SECURITY_ENABLED", "true")
	t.Setenv("FUNCTION_DBI_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))

	_, err := applyDBISecurityConfig(supervisor.Config{
		IO:          supervisor.IOConfig{Interface: supervisor.FileIO},
		StartParams: supervisor.StartConfig{Cmd: "python3"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FUNCTION_DBI_CONFIG_PATH")
}

func TestApplyDBISecurityConfigRejectsInvalidEnabledValue(t *testing.T) {
	t.Setenv("FUNCTION_DBI_SECURITY_ENABLED", "sometimes")

	_, err := applyDBISecurityConfig(supervisor.Config{
		IO:          supervisor.IOConfig{Interface: supervisor.FileIO},
		StartParams: supervisor.StartConfig{Cmd: "python3"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FUNCTION_DBI_SECURITY_ENABLED")
}
