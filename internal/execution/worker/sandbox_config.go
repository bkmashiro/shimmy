package worker

// SandboxConfig holds the configuration for nsjail-based process sandboxing.
// Zero values mean "disabled" / "unlimited". Sandboxing is Linux-only.
type SandboxConfig struct {
	// Enabled activates nsjail wrapping for worker processes.
	Enabled bool `conf:"enabled"`

	// NsjailPath is the path to the nsjail binary. Default: /usr/sbin/nsjail.
	NsjailPath string `conf:"nsjail_path"`

	// User is the uid:gid the worker runs as inside the sandbox.
	// Default: "65534:65534" (nobody:nogroup).
	User string `conf:"user"`

	// ReadOnlyBinds are host paths bind-mounted read-only at the same path
	// inside the sandbox. E.g. ["/usr", "/lib", "/lib64"].
	ReadOnlyBinds []string `conf:"ro_binds"`

	// WritableBinds are host paths bind-mounted read-write at the same path
	// inside the sandbox. Required for file-mode: include "/tmp/shimmy".
	WritableBinds []string `conf:"rw_binds"`

	// TmpfsMounts are paths inside the sandbox to mount as tmpfs.
	TmpfsMounts []string `conf:"tmpfs"`

	// CpuTimeLimit is the CPU time limit in seconds. 0 = unlimited.
	CpuTimeLimit int `conf:"cpu_time_limit"`

	// MemoryLimit is the address-space limit in megabytes. 0 = unlimited.
	MemoryLimit int `conf:"memory_limit"`

	// MaxFds is the maximum number of open file descriptors. 0 = nsjail default.
	MaxFds int `conf:"max_fds"`

	// DisableNetwork removes network access inside the sandbox.
	DisableNetwork bool `conf:"disable_network"`

	// Seccomp enables syscall filtering via seccomp-bpf using nsjail's
	// built-in default policy. Requires kernel seccomp support.
	Seccomp bool `conf:"seccomp"`
}
