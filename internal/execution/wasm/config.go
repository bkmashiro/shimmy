package wasm

import (
	"fmt"
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
	// Populated from FUNCTION_WORKER_SEND_TIMEOUT / send.timeout.
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

	// UseUffd enables userfaultfd write-protect dirty-page tracking for memory
	// snapshot/restore. When true, only pages written by the guest since the
	// last snapshot are restored, reducing restore cost for large modules.
	// Requires Linux kernel with UFFD_FEATURE_PAGEFAULT_FLAG_WP support and a
	// permissive seccomp profile (not available under Docker's default profile).
	// Falls back to FullMemcpyStrategy automatically if uffd is unavailable.
	// Populated from FUNCTION_WASM_USE_UFFD=true.
	UseUffd bool `conf:"use_uffd"`

	// SnapshotMode selects the strategy used to snapshot and restore WASM linear
	// memory between requests. Valid values:
	//   "memcpy"     — copy all pages on every restore (default, always available)
	//   "soft-dirty" — use /proc/self/pagemap soft-dirty bits (Linux >= 3.18)
	//   "mprotect"   — use mprotect(PROT_READ) + SIGSEGV dirty tracking
	//                  (Linux + CGO, EXPERIMENTAL — installs a process-wide
	//                  SIGSEGV handler that intercepts every segfault in the Go
	//                  process. Requires the operator to also set
	//                  FUNCTION_WASM_ALLOW_EXPERIMENTAL_MPROTECT=true; otherwise
	//                  it silently falls back to "memcpy" with a warning log.
	//                  Not recommended for production.)
	//   "uffd"       — use userfaultfd write-protect (Linux, requires privilege)
	//
	// Falls back to "memcpy" if the requested strategy is unavailable.
	// FUNCTION_WASM_SNAPSHOT_MODE env var.
	SnapshotMode string `conf:"wasm_snapshot_mode"`

	// PythonScriptPath is the path to the Python eval script (eval.py).
	// Only used when FUNCTION_INTERFACE=python-wasm.
	// The script must define evaluation_function(response, answer, params=None).
	PythonScriptPath string `conf:"wasm_python_script"`

	// PythonPreloadMode controls whether the trusted evaluator script and its
	// imports are loaded before the pristine snapshot. "evaluator" (default)
	// enables post-import snapshots; "off" preserves legacy per-request exec.
	PythonPreloadMode string `conf:"wasm_python_preload"`

	// PythonSnapshotHeadroomBytes grows guest memory before taking a prepared
	// snapshot so normal request allocations do not immediately trigger drift.
	PythonSnapshotHeadroomBytes uint64 `conf:"wasm_python_snapshot_headroom_bytes"`

	// CompileCacheDir, if non-empty, enables wazero's on-disk compilation cache.
	// Set via FUNCTION_WASM_COMPILE_CACHE env var. Shared across all runners and
	// processes that point at the same directory, making cold starts much faster
	// after the first compile.
	CompileCacheDir string `conf:"wasm_compile_cache"`
}

// applyDefaults fills in zero-value fields with sensible defaults.
// It also resolves the deprecated UseUffd bool into SnapshotMode so that
// downstream code only needs to check SnapshotMode.
func (c *Config) applyDefaults() {
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Second
	}
	if c.MaxMemoryPages == 0 {
		c.MaxMemoryPages = 256 // 16 MB
	}
	if c.PythonPreloadMode == "" {
		c.PythonPreloadMode = "evaluator"
	}
	if c.PythonSnapshotHeadroomBytes == 0 {
		c.PythonSnapshotHeadroomBytes = 8 * 1024 * 1024
	}
	// Resolve deprecated UseUffd → SnapshotMode so downstream code never
	// needs to check both fields.
	if c.SnapshotMode == "" && c.UseUffd {
		c.SnapshotMode = "uffd"
	}
}

func (c *Config) validatePythonPreloadMode() error {
	switch c.PythonPreloadMode {
	case "evaluator", "off":
		return nil
	default:
		return fmt.Errorf("python preload mode %q is invalid; use \"evaluator\" or \"off\"", c.PythonPreloadMode)
	}
}

// validateSnapshotMode checks that the configured snapshot mode is compatible
// with the pool size. Modes that use process-wide state (soft-dirty, mprotect)
// are only safe with a single instance.
func (c *Config) validateSnapshotMode(poolSize int) error {
	if poolSize <= 1 {
		return nil
	}
	switch c.SnapshotMode {
	case "soft-dirty":
		return fmt.Errorf("snapshot mode %q is not safe with pool_size=%d > 1 (process-wide dirty bits cannot be attributed to individual instances); use \"memcpy\" or \"uffd\" instead", c.SnapshotMode, poolSize)
	case "mprotect":
		return fmt.Errorf("snapshot mode %q is not safe with pool_size=%d > 1 (global SIGSEGV handler cannot distinguish instances); use \"memcpy\" or \"uffd\" instead", c.SnapshotMode, poolSize)
	}
	return nil
}

// applyEnv reads sandbox fields from FUNCTION_WASM_* environment variables.
// This allows operators to configure sandbox limits without threading them
// through the full koanf config chain.
func (c *Config) applyEnv() {
	// FUNCTION_WASM_MODULE overrides FUNCTION_COMMAND as the .wasm file path.
	if v := os.Getenv("FUNCTION_WASM_MODULE"); v != "" {
		c.ModulePath = v
	}
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
	if v := os.Getenv("FUNCTION_WASM_USE_UFFD"); v == "true" || v == "1" {
		c.UseUffd = true
	}
	if v := os.Getenv("FUNCTION_WASM_SNAPSHOT_MODE"); v != "" {
		c.SnapshotMode = v
	}
	if v := os.Getenv("FUNCTION_WASM_PYTHON_SCRIPT"); v != "" {
		c.PythonScriptPath = v
	}
	if v := os.Getenv("FUNCTION_WASM_PYTHON_PRELOAD"); v != "" {
		c.PythonPreloadMode = v
	}
	if v := os.Getenv("FUNCTION_WASM_PYTHON_SNAPSHOT_HEADROOM_BYTES"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			c.PythonSnapshotHeadroomBytes = n
		}
	}
	if v := os.Getenv("FUNCTION_WASM_COMPILE_CACHE"); v != "" {
		c.CompileCacheDir = v
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
