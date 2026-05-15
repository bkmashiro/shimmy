package wasm

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the configuration for the WASM execution backend.
//
// Configuration is read from environment variables via koanf (the same
// mechanism used by the rest of shimmy). The "conf" struct tags map to
// the koanf key names derived from the FUNCTION_* env-var prefix.
type Config struct {
	// ModulePath is the path to the .wasm file to load.
	// Populated from FUNCTION_COMMAND (the command field re-used as the
	// .wasm file path when FUNCTION_INTERFACE=wasm).
	ModulePath string `conf:"cmd"`

	// MaxInstances is the maximum number of concurrently active module
	// instances. When the pool is exhausted requests block until a slot is
	// available. Defaults to runtime.NumCPU() when <= 0.
	// Populated from FUNCTION_MAX_PROCS / max_workers.
	MaxInstances int `conf:"max_workers"`

	// Timeout is the per-request deadline passed to the WASM call.
	// Populated from FUNCTION_TIMEOUT / send.timeout.
	Timeout time.Duration `conf:"timeout"`

	// --- Sandbox limits ---

	// MaxMemoryPages limits WASM linear memory (1 page = 64KB).
	// Default: 256 pages = 16MB. 0 means use module's own max.
	MaxMemoryPages uint32 `conf:"wasm_max_memory_pages"`

	// AllowedPaths is a list of host paths the module may read (read-only).
	// Empty means no filesystem access at all.
	AllowedPaths []string `conf:"wasm_allowed_paths"`

	// AllowedEnv is a list of env var names the module may read.
	// Empty means no env vars exposed.
	AllowedEnv []string `conf:"wasm_allowed_env"`
}

// applyDefaults fills in zero-value fields with sensible defaults.
func (c *Config) applyDefaults() {
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Second
	}
	if c.MaxMemoryPages == 0 {
		c.MaxMemoryPages = 256 // 16 MB
	}
}

// applyEnv reads sandbox fields from FUNCTION_WASM_* environment variables.
// This allows operators to configure sandbox limits without threading them
// through the full koanf config chain.
func (c *Config) applyEnv() {
	if v := os.Getenv("FUNCTION_WASM_MAX_MEMORY_PAGES"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 32); err == nil {
			c.MaxMemoryPages = uint32(n)
		}
	}
	if v := os.Getenv("FUNCTION_WASM_ALLOWED_PATHS"); v != "" {
		c.AllowedPaths = splitNonEmpty(v, ",")
	}
	if v := os.Getenv("FUNCTION_WASM_ALLOWED_ENV"); v != "" {
		c.AllowedEnv = splitNonEmpty(v, ",")
	}
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
