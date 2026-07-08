package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

func TestBuildDBISupervisorConfigWrapsRPCWorkerWithDynamoRIO(t *testing.T) {
	t.Setenv("FUNCTION_DBI_DRRUN", "/opt/dr/bin64/drrun")
	t.Setenv("FUNCTION_DBI_CLIENT", "/opt/dr/open_log.so")
	t.Setenv("FUNCTION_DBI_OPTIONS", "-logdir /tmp/drlogs")

	cfg, err := buildDBISupervisorConfig(supervisor.Config{
		IO: supervisor.IOConfig{Interface: supervisor.DbiIO},
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

func TestBuildDBISupervisorConfigSupportsFileWorkers(t *testing.T) {
	t.Setenv("FUNCTION_DBI_TARGET_INTERFACE", "file")

	cfg, err := buildDBISupervisorConfig(supervisor.Config{
		IO:          supervisor.IOConfig{Interface: supervisor.DbiIO},
		StartParams: supervisor.StartConfig{Cmd: "python3", Args: []string{"eval.py"}},
	})
	require.NoError(t, err)

	assert.Equal(t, supervisor.FileIO, cfg.IO.Interface)
	assert.Equal(t, defaultDBIRunner, cfg.StartParams.Cmd)
	assert.Equal(t, []string{"--", "python3", "eval.py"}, cfg.StartParams.Args)
}

func TestBuildDBISupervisorConfigRejectsMissingCommand(t *testing.T) {
	_, err := buildDBISupervisorConfig(supervisor.Config{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FUNCTION_COMMAND")
}
