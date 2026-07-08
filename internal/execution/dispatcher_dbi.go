package execution

import (
	"fmt"
	"os"
	"strings"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

const (
	defaultDBIRunner = "drrun"
)

// buildDBISupervisorConfig rewrites the normal worker command so the existing
// supervisor/adapter stack talks to a worker launched under DynamoRIO.  This is
// intentionally thin: DBI is an execution wrapper, not a new protocol.
func buildDBISupervisorConfig(cfg supervisor.Config) (supervisor.Config, error) {
	origCmd := strings.TrimSpace(cfg.StartParams.Cmd)
	if origCmd == "" {
		return supervisor.Config{}, fmt.Errorf("dbi: FUNCTION_COMMAND must name the native worker command")
	}

	runner := firstNonEmpty(os.Getenv("FUNCTION_DBI_DRRUN"), defaultDBIRunner)
	args := splitShellish(os.Getenv("FUNCTION_DBI_OPTIONS"))
	if client := strings.TrimSpace(os.Getenv("FUNCTION_DBI_CLIENT")); client != "" {
		args = append(args, "-c", client)
	}
	args = append(args, "--", origCmd)
	args = append(args, cfg.StartParams.Args...)

	cfg.StartParams.Cmd = runner
	cfg.StartParams.Args = args

	targetInterface := supervisor.IOInterface(strings.TrimSpace(os.Getenv("FUNCTION_DBI_TARGET_INTERFACE")))
	if targetInterface == "" {
		targetInterface = supervisor.RpcIO
	}
	switch targetInterface {
	case supervisor.RpcIO:
		cfg.IO.Interface = supervisor.RpcIO
		if cfg.IO.Rpc.Transport == "" {
			cfg.IO.Rpc.Transport = supervisor.StdioTransport
		}
	case supervisor.FileIO:
		cfg.IO.Interface = supervisor.FileIO
	default:
		return supervisor.Config{}, fmt.Errorf("dbi: unsupported FUNCTION_DBI_TARGET_INTERFACE %q; supported values: rpc, file", targetInterface)
	}

	return cfg, nil
}

func splitShellish(s string) []string {
	// ponytail: use Fields for the prototype; upgrade to shellquote only if DBI
	// options need embedded spaces.
	return strings.Fields(s)
}
