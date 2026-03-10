package sandbox

import (
	"context"
	"os/exec"
)

// DirectBackend runs commands without sandboxing.
type DirectBackend struct{}

func (d *DirectBackend) WrapCommand(ctx context.Context, name string, args []string, _ Config) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, name, args...), nil
}

func (d *DirectBackend) Available() bool { return true }

func (d *DirectBackend) Name() string { return "direct" }
