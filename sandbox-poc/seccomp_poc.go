//go:build linux
// +build linux

package main

/*
Seccomp-BPF PoC for shimmy sandbox

Two approaches:
1. libseccomp-golang (cgo, needs libseccomp)
2. Pure Go via golang.org/x/sys/unix (no cgo)

This PoC uses pure Go approach for portability.
*/

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// SeccompFilter represents a simple seccomp allowlist filter
type SeccompFilter struct {
	allowedSyscalls map[int]bool
}

// NewSeccompFilter creates a new filter with default allowlist
func NewSeccompFilter() *SeccompFilter {
	return &SeccompFilter{
		allowedSyscalls: map[int]bool{
			// Essential syscalls for Python/Node evaluation
			unix.SYS_READ:           true,
			unix.SYS_WRITE:          true,
			unix.SYS_CLOSE:          true,
			unix.SYS_FSTAT:          true,
			unix.SYS_LSEEK:          true,
			unix.SYS_MMAP:           true,
			unix.SYS_MPROTECT:       true,
			unix.SYS_MUNMAP:         true,
			unix.SYS_BRK:            true,
			unix.SYS_EXIT:           true,
			unix.SYS_EXIT_GROUP:     true,
			unix.SYS_ARCH_PRCTL:     true,
			unix.SYS_GETTID:         true,
			unix.SYS_GETPID:         true,
			unix.SYS_FUTEX:          true,
			unix.SYS_CLOCK_GETTIME:  true,
			unix.SYS_RT_SIGACTION:   true,
			unix.SYS_RT_SIGPROCMASK: true,
			unix.SYS_RT_SIGRETURN:   true,
			
			// File operations (restricted)
			unix.SYS_OPENAT:         true,
			unix.SYS_NEWFSTATAT:     true,
			unix.SYS_ACCESS:         true,
			unix.SYS_GETCWD:         true,
			unix.SYS_READLINK:       true,
			unix.SYS_GETDENTS64:     true,
			
			// Process (limited)
			unix.SYS_GETUID:         true,
			unix.SYS_GETGID:         true,
			unix.SYS_GETEUID:        true,
			unix.SYS_GETEGID:        true,
			
			// Memory
			unix.SYS_MREMAP:         true,
			unix.SYS_MSYNC:          true,
			
			// Pipes/sockets (for stdio communication)
			unix.SYS_PIPE2:          true,
			unix.SYS_DUP:            true,
			unix.SYS_DUP2:           true,
			unix.SYS_FCNTL:          true,
			unix.SYS_IOCTL:          true,
			unix.SYS_POLL:           true,
			unix.SYS_SELECT:         true,
			
			// Time
			unix.SYS_NANOSLEEP:      true,
			unix.SYS_CLOCK_NANOSLEEP: true,
		},
	}
}

// Deny marks a syscall as blocked
func (f *SeccompFilter) Deny(syscallNr int) {
	delete(f.allowedSyscalls, syscallNr)
}

// Allow marks a syscall as allowed
func (f *SeccompFilter) Allow(syscallNr int) {
	f.allowedSyscalls[syscallNr] = true
}

// DenyNetwork removes network-related syscalls
func (f *SeccompFilter) DenyNetwork() {
	networkSyscalls := []int{
		unix.SYS_SOCKET,
		unix.SYS_CONNECT,
		unix.SYS_ACCEPT,
		unix.SYS_BIND,
		unix.SYS_LISTEN,
		unix.SYS_SENDTO,
		unix.SYS_RECVFROM,
		unix.SYS_SENDMSG,
		unix.SYS_RECVMSG,
	}
	for _, nr := range networkSyscalls {
		f.Deny(nr)
	}
}

// DenyProcessCreation removes process creation syscalls
func (f *SeccompFilter) DenyProcessCreation() {
	f.Deny(unix.SYS_FORK)
	f.Deny(unix.SYS_VFORK)
	f.Deny(unix.SYS_CLONE)
	f.Deny(unix.SYS_CLONE3)
	f.Deny(unix.SYS_EXECVE)
	f.Deny(unix.SYS_EXECVEAT)
}

// Apply loads the seccomp filter into the kernel
// NOTE: This is a simplified version. Real implementation needs BPF bytecode.
func (f *SeccompFilter) Apply() error {
	// Step 1: Set NO_NEW_PRIVS (required for unprivileged seccomp)
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl(NO_NEW_PRIVS): %w", err)
	}
	
	// Step 2: Generate BPF filter
	// This is where we'd build the actual BPF bytecode
	// For production, use libseccomp-golang or pre-compiled BPF
	
	// Placeholder: In real implementation, we'd call:
	// unix.SyscallNoError(unix.SYS_SECCOMP, SECCOMP_SET_MODE_FILTER, 0, uintptr(unsafe.Pointer(&bpfProg)))
	
	fmt.Println("Seccomp filter would be applied here")
	fmt.Printf("Allowed syscalls: %d\n", len(f.allowedSyscalls))
	
	return nil
}

// ApplySysProcAttr returns SysProcAttr for use with exec.Cmd
// This applies the filter to a child process
func ApplySysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true,
		// Cloneflags would go here for namespace isolation
		// Credential for user isolation
	}
}

// ApplyRlimits sets resource limits on the current process
func ApplyRlimits(maxCPU, maxMem uint64) error {
	// CPU time limit (seconds)
	if err := unix.Setrlimit(unix.RLIMIT_CPU, &unix.Rlimit{
		Cur: maxCPU,
		Max: maxCPU,
	}); err != nil {
		return fmt.Errorf("setrlimit(CPU): %w", err)
	}
	
	// Memory limit (bytes)
	if err := unix.Setrlimit(unix.RLIMIT_AS, &unix.Rlimit{
		Cur: maxMem,
		Max: maxMem,
	}); err != nil {
		return fmt.Errorf("setrlimit(AS): %w", err)
	}
	
	// No core dumps
	if err := unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{
		Cur: 0,
		Max: 0,
	}); err != nil {
		return fmt.Errorf("setrlimit(CORE): %w", err)
	}
	
	// Limit number of processes (prevent fork bomb)
	if err := unix.Setrlimit(unix.RLIMIT_NPROC, &unix.Rlimit{
		Cur: 10,
		Max: 10,
	}); err != nil {
		return fmt.Errorf("setrlimit(NPROC): %w", err)
	}
	
	return nil
}

// SandboxedExec runs a command with sandbox restrictions
func SandboxedExec(command string, args []string, workdir string) error {
	cmd := exec.Command(command, args...)
	cmd.Dir = workdir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	
	cmd.SysProcAttr = ApplySysProcAttr()
	
	// Note: seccomp filter should be applied in the child process
	// after fork but before exec. This requires using Pdeathsig
	// or a wrapper script approach.
	
	return cmd.Run()
}

func main() {
	fmt.Println("Seccomp PoC for shimmy sandbox")
	fmt.Println("===============================")
	
	filter := NewSeccompFilter()
	filter.DenyNetwork()
	filter.DenyProcessCreation()
	
	fmt.Printf("Filter config:\n")
	fmt.Printf("- Allowed syscalls: %d\n", len(filter.allowedSyscalls))
	fmt.Printf("- Network: DENIED\n")
	fmt.Printf("- Process creation: DENIED\n")
	
	// Test apply (will only print on Linux)
	if err := filter.Apply(); err != nil {
		fmt.Printf("Apply error: %v\n", err)
	}
	
	fmt.Println("\nNote: Full implementation requires BPF bytecode generation")
	fmt.Println("Consider using libseccomp-golang for production")
}
