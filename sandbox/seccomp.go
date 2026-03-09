//go:build linux
// +build linux

// sandbox/seccomp.go - Seccomp-BPF filter for syscall restriction
//
// Uses libseccomp-golang for production.
// This is a simplified example using pure Go.

package sandbox

import (
	"encoding/binary"
	"unsafe"

	"golang.org/x/sys/unix"
)

// SeccompFilter configures syscall filtering
type SeccompFilter struct {
	DefaultAction Action
	Rules         []Rule
}

// Action defines what to do when a rule matches
type Action int

const (
	ActionAllow Action = iota
	ActionKill
	ActionErrno
	ActionLog
)

// Rule defines a syscall rule
type Rule struct {
	Syscall int
	Action  Action
}

// DefaultAllowlistFilter returns a restrictive filter
// that only allows essential syscalls
func DefaultAllowlistFilter() *SeccompFilter {
	// Essential syscalls for Python/Node execution
	allowed := []int{
		unix.SYS_READ,
		unix.SYS_WRITE,
		unix.SYS_CLOSE,
		unix.SYS_FSTAT,
		unix.SYS_LSEEK,
		unix.SYS_MMAP,
		unix.SYS_MPROTECT,
		unix.SYS_MUNMAP,
		unix.SYS_BRK,
		unix.SYS_EXIT,
		unix.SYS_EXIT_GROUP,
		unix.SYS_ARCH_PRCTL,
		unix.SYS_GETTID,
		unix.SYS_GETPID,
		unix.SYS_FUTEX,
		unix.SYS_CLOCK_GETTIME,
		unix.SYS_RT_SIGACTION,
		unix.SYS_RT_SIGPROCMASK,
		unix.SYS_RT_SIGRETURN,
		unix.SYS_OPENAT,
		unix.SYS_NEWFSTATAT,
		unix.SYS_ACCESS,
		unix.SYS_GETCWD,
		unix.SYS_READLINK,
		unix.SYS_GETDENTS64,
		unix.SYS_GETUID,
		unix.SYS_GETGID,
		unix.SYS_GETEUID,
		unix.SYS_GETEGID,
		unix.SYS_MREMAP,
		unix.SYS_PIPE2,
		unix.SYS_DUP,
		unix.SYS_DUP2,
		unix.SYS_FCNTL,
		unix.SYS_IOCTL,
		unix.SYS_POLL,
		unix.SYS_NANOSLEEP,
		unix.SYS_CLOCK_NANOSLEEP,
	}

	rules := make([]Rule, len(allowed))
	for i, syscall := range allowed {
		rules[i] = Rule{Syscall: syscall, Action: ActionAllow}
	}

	return &SeccompFilter{
		DefaultAction: ActionKill,
		Rules:         rules,
	}
}

// NetworkDenyFilter returns a filter that blocks network syscalls
func NetworkDenyFilter() *SeccompFilter {
	denied := []int{
		unix.SYS_SOCKET,
		unix.SYS_CONNECT,
		unix.SYS_ACCEPT,
		unix.SYS_ACCEPT4,
		unix.SYS_BIND,
		unix.SYS_LISTEN,
		unix.SYS_SENDTO,
		unix.SYS_RECVFROM,
		unix.SYS_SENDMSG,
		unix.SYS_RECVMSG,
	}

	rules := make([]Rule, len(denied))
	for i, syscall := range denied {
		rules[i] = Rule{Syscall: syscall, Action: ActionKill}
	}

	return &SeccompFilter{
		DefaultAction: ActionAllow,
		Rules:         rules,
	}
}

// Apply loads the seccomp filter into the kernel
func (f *SeccompFilter) Apply() error {
	// Set NO_NEW_PRIVS (required for unprivileged seccomp)
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}

	// Build BPF program
	prog, err := f.buildBPF()
	if err != nil {
		return err
	}

	// Load filter
	return unix.SyscallNoError(
		unix.SYS_SECCOMP,
		uintptr(2), // SECCOMP_SET_MODE_FILTER
		0,
		uintptr(unsafe.Pointer(prog)),
	)
}

// buildBPF constructs the BPF bytecode
func (f *SeccompFilter) buildBPF() (*unix.SockFprog, error) {
	// Simplified BPF generation
	// Production should use libseccomp-golang

	var insns []unix.SockFilter

	// BPF constants
	const (
		BPF_LD  = 0x00
		BPF_W   = 0x00
		BPF_ABS = 0x20
		BPF_JMP = 0x05
		BPF_JEQ = 0x10
		BPF_K   = 0x00
		BPF_RET = 0x06
	)

	// Action values
	const (
		SECCOMP_RET_ALLOW = 0x7fff0000
		SECCOMP_RET_KILL  = 0x00000000
	)

	// Load syscall number
	// BPF_STMT(BPF_LD | BPF_W | BPF_ABS, 0)
	insns = append(insns, unix.SockFilter{
		Code: BPF_LD | BPF_W | BPF_ABS,
		K:    0, // offsetof(seccomp_data, nr)
	})

	// For each rule, add a jump instruction
	for _, rule := range f.Rules {
		action := SECCOMP_RET_KILL
		if rule.Action == ActionAllow {
			action = SECCOMP_RET_ALLOW
		}

		// Jump if equal
		insns = append(insns, unix.SockFilter{
			Code: BPF_JMP | BPF_JEQ | BPF_K,
			Jt:   0, // If true, execute next
			Jf:   1, // If false, skip
			K:    uint32(rule.Syscall),
		})

		// Return action
		insns = append(insns, unix.SockFilter{
			Code: BPF_RET | BPF_K,
			K:    uint32(action),
		})
	}

	// Default action
	defaultAction := SECCOMP_RET_ALLOW
	if f.DefaultAction == ActionKill {
		defaultAction = SECCOMP_RET_KILL
	}
	insns = append(insns, unix.SockFilter{
		Code: BPF_RET | BPF_K,
		K:    uint32(defaultAction),
	})

	// Create sock_fprog
	prog := &unix.SockFprog{
		Len:    uint16(len(insns)),
		Filter: &insns[0],
	}

	return prog, nil
}

// dummy function to suppress unused import warning
func init() {
	_ = binary.LittleEndian
}
