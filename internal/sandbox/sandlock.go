package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
)

const sandlockRelativePath = ".openclaw/workspace/sandlock/sandlock"
const sandlockPathEnv = "SHIMMY_SANDLOCK_PATH"

// SandlockBackend wraps a process with the sandlock binary.
type SandlockBackend struct {
	binaryPath string
}

func (b *SandlockBackend) WrapCommand(ctx context.Context, name string, args []string, cfg Config) (*exec.Cmd, error) {
	if name == "" {
		return nil, fmt.Errorf("sandlock: command name must not be empty")
	}

	binary, err := b.resolveBinary()
	if err != nil {
		return nil, err
	}

	wrappedArgs := make([]string, 0, len(args)+8)
	if cfg.CPUTimeSecs > 0 {
		wrappedArgs = append(wrappedArgs, "--cpu", strconv.Itoa(cfg.CPUTimeSecs))
		wrappedArgs = append(wrappedArgs, "--timeout", strconv.Itoa(cfg.CPUTimeSecs))
	}
	if cfg.MaxMemoryMB > 0 {
		wrappedArgs = append(wrappedArgs, "--mem", strconv.Itoa(cfg.MaxMemoryMB))
	}
	if !cfg.AllowNetwork {
		wrappedArgs = append(wrappedArgs, "--no-network")
	}
	if cfg.WorkDir != "" {
		wrappedArgs = append(wrappedArgs, "--workdir", cfg.WorkDir)
	}
	wrappedArgs = append(wrappedArgs, "--", name)
	wrappedArgs = append(wrappedArgs, args...)

	return exec.CommandContext(ctx, binary, wrappedArgs...), nil
}

func (b *SandlockBackend) Available() bool {
	if runtime.GOOS != "linux" {
		return false
	}

	_, err := b.resolveBinary()
	return err == nil
}

func (b *SandlockBackend) Name() string { return "sandlock" }

func (b *SandlockBackend) resolveBinary() (string, error) {
	if b.binaryPath != "" {
		return exec.LookPath(b.binaryPath)
	}
	if customPath := os.Getenv(sandlockPathEnv); customPath != "" {
		return exec.LookPath(customPath)
	}
	if _, err := exec.LookPath("sandlock"); err == nil {
		return "sandlock", nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		return exec.LookPath(filepath.Join(home, sandlockRelativePath))
	}
	return "", exec.ErrNotFound
}
