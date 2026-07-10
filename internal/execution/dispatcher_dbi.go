package execution

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

const defaultDBIRunner = "drrun"

// applyDBISecurityConfig optionally wraps a native file/RPC worker command with
// DynamoRIO. DBI is a transparent security layer, not an IO interface, so this
// function never changes cfg.IO.Interface or its transport configuration.
func applyDBISecurityConfig(cfg supervisor.Config) (supervisor.Config, error) {
	enabledValue := strings.TrimSpace(os.Getenv("FUNCTION_DBI_SECURITY_ENABLED"))
	if enabledValue == "" {
		return cfg, nil
	}

	enabled, err := strconv.ParseBool(enabledValue)
	if err != nil {
		return supervisor.Config{}, fmt.Errorf("dbi: invalid FUNCTION_DBI_SECURITY_ENABLED %q: %w", enabledValue, err)
	}
	if !enabled {
		return cfg, nil
	}

	switch cfg.IO.Interface {
	case supervisor.FileIO, supervisor.RpcIO:
		// Native process-backed worker interfaces are supported.
	default:
		return supervisor.Config{}, fmt.Errorf(
			"dbi: security wrapper is only supported for file and rpc interfaces; got %q",
			cfg.IO.Interface,
		)
	}

	origCmd := strings.TrimSpace(cfg.StartParams.Cmd)
	if origCmd == "" {
		return supervisor.Config{}, fmt.Errorf("dbi: FUNCTION_COMMAND must name the native worker command")
	}

	if configPath := strings.TrimSpace(os.Getenv("FUNCTION_DBI_CONFIG_PATH")); configPath != "" {
		file, openErr := os.Open(configPath)
		if openErr != nil {
			return supervisor.Config{}, fmt.Errorf("dbi: FUNCTION_DBI_CONFIG_PATH %q is not readable: %w", configPath, openErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return supervisor.Config{}, fmt.Errorf("dbi: close FUNCTION_DBI_CONFIG_PATH %q: %w", configPath, closeErr)
		}
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
	return cfg, nil
}

func splitShellish(s string) []string {
	// Keep the current whitespace-separated option contract. Upgrade to a real
	// argv/config format if DBI options need embedded spaces.
	return strings.Fields(s)
}
