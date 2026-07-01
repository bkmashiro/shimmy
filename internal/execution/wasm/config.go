package wasm

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the configuration for the opt-in generic WASM execution
// backend. Shimmy consumes an already-built WASI module; source-language
// compilation remains a deployment/build concern outside this package.
type Config struct {
	// ModulePath is the path to the .wasm module. It is normally populated from
	// FUNCTION_COMMAND for compatibility with the rest of Shimmy, or overridden
	// by FUNCTION_WASM_MODULE when set.
	ModulePath string

	// MaxInstances is the number of warm module instances in the pool.
	MaxInstances int

	// Timeout is the per-request deadline passed to the guest evaluate call.
	Timeout time.Duration

	// MaxMemoryPages limits WASM linear memory (1 page = 64 KiB). The default is
	// intentionally small and can be raised by FUNCTION_WASM_MAX_MEMORY_PAGES.
	MaxMemoryPages uint32

	// AllowedPaths is a comma-separated allowlist of read-only host directories
	// exposed to WASI. Empty means no filesystem access.
	AllowedPaths []string

	// AllowedEnv is a comma-separated allowlist of host environment variable
	// names exposed to WASI. Empty means no environment variables.
	AllowedEnv []string

	// CompileCacheDir enables wazero's on-disk compilation cache when set via
	// FUNCTION_WASM_COMPILE_CACHE.
	CompileCacheDir string

	// SnapshotStrategy selects how warm WASM instances reset linear memory after
	// each request. "full" is the production default; "off" is a benchmark-only
	// comparison mode that intentionally allows guest state to leak.
	SnapshotStrategy string

	// PythonScriptPath points at the Python source loaded by the python-reactor
	// profile. It is intentionally only used by that compatibility lane.
	PythonScriptPath string
}

const (
	SnapshotStrategyFull = "full"
	SnapshotStrategyOff  = "off"
)

func (c *Config) applyDefaults() {
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Second
	}
	if c.MaxMemoryPages == 0 {
		c.MaxMemoryPages = 256 // 16 MiB
	}
	if c.SnapshotStrategy == "" {
		c.SnapshotStrategy = SnapshotStrategyFull
	}
}

// applyEnv reads FUNCTION_WASM_* overrides. These settings are intentionally
// limited to generic WASM runtime concerns; Python/reactor/package bundling is
// out of scope for this backend.
func (c *Config) applyEnv() error {
	if v := os.Getenv("FUNCTION_WASM_MODULE"); v != "" {
		c.ModulePath = v
	}
	if v := os.Getenv("FUNCTION_WASM_MAX_MEMORY_PAGES"); v != "" {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return fmt.Errorf("FUNCTION_WASM_MAX_MEMORY_PAGES must be an unsigned 32-bit integer: %w", err)
		}
		c.MaxMemoryPages = uint32(n)
	}
	if v := os.Getenv("FUNCTION_WASM_ALLOWED_PATHS"); v != "" {
		c.AllowedPaths = splitNonEmpty(v, ",")
	}
	if v := os.Getenv("FUNCTION_WASM_ALLOWED_ENV"); v != "" {
		c.AllowedEnv = splitNonEmpty(v, ",")
	}
	if v := os.Getenv("FUNCTION_WASM_COMPILE_CACHE"); v != "" {
		c.CompileCacheDir = v
	}
	if v := os.Getenv("FUNCTION_WASM_PYTHON_SCRIPT"); v != "" {
		c.PythonScriptPath = v
	}
	if v := os.Getenv("FUNCTION_WASM_SNAPSHOT_STRATEGY"); v != "" {
		switch v {
		case SnapshotStrategyFull, SnapshotStrategyOff:
			c.SnapshotStrategy = v
		default:
			return fmt.Errorf("FUNCTION_WASM_SNAPSHOT_STRATEGY must be one of %q or %q", SnapshotStrategyFull, SnapshotStrategyOff)
		}
	}
	return nil
}

func splitNonEmpty(s, sep string) []string {
	var out []string
	for _, p := range strings.Split(s, sep) {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
