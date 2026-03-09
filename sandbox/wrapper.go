//go:build linux
// +build linux

// Package sandbox provides userspace sandboxing for code execution.
//
// Features:
// - rlimits: CPU, memory, processes, files, file descriptors
// - seccomp-bpf: Network syscall blocking
// - Timeout: Process termination on timeout
//
// Usage:
//
//	cfg := sandbox.DefaultConfig()
//	cfg.AllowNetwork = false
//	result, err := sandbox.Execute("python3", []string{"student.py"}, cfg)
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Execute runs a command in a sandbox
func Execute(name string, args []string, config Config) (*ExecResult, error) {
	// First, apply rlimits (inherited by child)
	if err := applyRlimits(config); err != nil {
		return nil, fmt.Errorf("rlimits: %w", err)
	}

	// Apply seccomp if network is blocked
	if !config.AllowNetwork {
		if err := applyNetworkSeccomp(); err != nil {
			return nil, fmt.Errorf("seccomp: %w", err)
		}
	}

	// Set up command
	cmd := exec.Command(name, args...)
	cmd.Dir = config.WorkDir
	cmd.Env = config.Env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Start timing
	start := time.Now()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	// Wait with timeout
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	result := &ExecResult{StartTime: start}

	select {
	case err := <-done:
		result.Duration = time.Since(start)
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				result.ExitCode = exitErr.ExitCode()
			} else {
				result.Error = err
			}
		}
	case <-time.After(config.Timeout):
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		result.Timeout = true
		result.Duration = config.Timeout
	}

	result.EndTime = time.Now()
	return result, nil
}

// ExecResult holds execution results
type ExecResult struct {
	ExitCode  int
	Duration  time.Duration
	StartTime time.Time
	EndTime   time.Time
	Timeout   bool
	Error     error
}

func applyRlimits(cfg Config) error {
	limits := []struct {
		res   int
		value uint64
	}{
		{unix.RLIMIT_CPU, cfg.MaxCPUSeconds},
		{unix.RLIMIT_AS, cfg.MaxMemoryBytes},
		{unix.RLIMIT_NPROC, cfg.MaxProcesses},
		{unix.RLIMIT_FSIZE, cfg.MaxFileSize},
		{unix.RLIMIT_NOFILE, cfg.MaxOpenFiles},
		{unix.RLIMIT_CORE, 0},
	}

	for _, l := range limits {
		if err := unix.Setrlimit(l.res, &unix.Rlimit{Cur: l.value, Max: l.value}); err != nil {
			return fmt.Errorf("setrlimit %d: %w", l.res, err)
		}
	}
	return nil
}

func applyNetworkSeccomp() error {
	// Set NO_NEW_PRIVS
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}

	// Network syscalls to block
	blocked := []uint32{
		uint32(unix.SYS_SOCKET),
		uint32(unix.SYS_CONNECT),
		uint32(unix.SYS_ACCEPT),
		uint32(unix.SYS_ACCEPT4),
		uint32(unix.SYS_BIND),
		uint32(unix.SYS_LISTEN),
		uint32(unix.SYS_SENDTO),
		uint32(unix.SYS_RECVFROM),
		uint32(unix.SYS_SENDMSG),
		uint32(unix.SYS_RECVMSG),
	}

	// BPF constants
	const (
		BPF_LD  = 0x00
		BPF_W   = 0x00
		BPF_ABS = 0x20
		BPF_JMP = 0x05
		BPF_JEQ = 0x10
		BPF_K   = 0x00
		BPF_RET = 0x06

		SECCOMP_RET_ALLOW = 0x7fff0000
		SECCOMP_RET_ERRNO = 0x00050000
	)

	var insns []unix.SockFilter
	insns = append(insns, unix.SockFilter{Code: BPF_LD | BPF_W | BPF_ABS, K: 0})

	for _, nr := range blocked {
		insns = append(insns, unix.SockFilter{Code: BPF_JMP | BPF_JEQ | BPF_K, Jt: 0, Jf: 1, K: nr})
		insns = append(insns, unix.SockFilter{Code: BPF_RET | BPF_K, K: SECCOMP_RET_ERRNO | 1})
	}
	insns = append(insns, unix.SockFilter{Code: BPF_RET | BPF_K, K: SECCOMP_RET_ALLOW})

	prog := unix.SockFprog{Len: uint16(len(insns)), Filter: &insns[0]}
	_, _, e := syscall.Syscall(unix.SYS_SECCOMP, 1, 0, uintptr(unsafe.Pointer(&prog)))
	if e != 0 {
		return fmt.Errorf("seccomp: %v", e)
	}
	return nil
}
