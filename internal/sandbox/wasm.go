package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const defaultWasmtimeBinary = "wasmtime"
const wasmtimePathEnv = "SHIMMY_WASMTIME_PATH"

// WasmBackend runs precompiled wasm binaries with wasmtime.
type WasmBackend struct {
	wasmtimePath string
}

func (b *WasmBackend) WrapCommand(ctx context.Context, name string, args []string, cfg Config) (*exec.Cmd, error) {
	wasmtime, err := b.resolveBinary()
	if err != nil {
		return nil, err
	}

	program, err := b.resolveProgram(name, cfg.WorkDir)
	if err != nil {
		return nil, err
	}

	wrappedArgs := []string{"run"}
	if cfg.WorkDir != "" {
		wrappedArgs = append(wrappedArgs, "--dir", cfg.WorkDir)
	} else {
		wrappedArgs = append(wrappedArgs, "--dir", ".")
	}
	if cfg.CPUTimeSecs > 0 {
		wrappedArgs = append(wrappedArgs, "--fuel", strconv.Itoa(cfg.CPUTimeSecs*1_000_000))
	}
	wrappedArgs = append(wrappedArgs, "--")
	wrappedArgs = append(wrappedArgs, program)
	wrappedArgs = append(wrappedArgs, args...)

	return exec.CommandContext(ctx, wasmtime, wrappedArgs...), nil
}

func (b *WasmBackend) Available() bool {
	_, err := b.resolveBinary()
	return err == nil
}

func (b *WasmBackend) Name() string { return "wasm" }

func (b *WasmBackend) resolveBinary() (string, error) {
	if b.wasmtimePath != "" {
		return exec.LookPath(b.wasmtimePath)
	}
	if customPath := os.Getenv(wasmtimePathEnv); customPath != "" {
		return exec.LookPath(customPath)
	}
	return exec.LookPath(defaultWasmtimeBinary)
}

func (b *WasmBackend) resolveProgram(name string, workDir string) (string, error) {
	if strings.HasSuffix(name, ".wasm") {
		return name, nil
	}

	candidate := name + ".wasm"
	if workDir != "" && !filepath.IsAbs(candidate) {
		candidate = filepath.Join(workDir, candidate)
	}
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}

	return "", fmt.Errorf("wasm program not found: %s", candidate)
}
