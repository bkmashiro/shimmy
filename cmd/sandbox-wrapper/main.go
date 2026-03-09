//go:build linux
// +build linux

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

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

func main() {
	cpuLimit := flag.Uint64("cpu", 5, "CPU time limit (seconds)")
	memLimit := flag.Uint64("mem", 256, "Memory limit (MB)")
	procLimit := flag.Uint64("nproc", 10, "Max processes")
	fileLimit := flag.Uint64("fsize", 10, "Max file size (MB)")
	fdLimit := flag.Uint64("nofile", 100, "Max open files")
	timeout := flag.Duration("timeout", 30*time.Second, "Overall timeout")
	noNetwork := flag.Bool("no-network", false, "Block network syscalls")
	cleanEnv := flag.Bool("clean-env", false, "Clear environment (security)")
	allowEnv := flag.String("allow-env", "", "Comma-separated env vars to keep (with -clean-env)")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: sandbox-wrapper [options] -- command [args...]")
		os.Exit(1)
	}

	// Apply rlimits
	applyRlimits(*cpuLimit, *memLimit*1024*1024, *procLimit, *fileLimit*1024*1024, *fdLimit)

	// Apply seccomp
	if *noNetwork {
		if err := applySeccomp(); err != nil {
			fmt.Fprintf(os.Stderr, "seccomp: %v\n", err)
			os.Exit(1)
		}
	}

	// Build environment
	var env []string
	if *cleanEnv {
		// Start with minimal environment
		env = []string{
			"PATH=/usr/local/bin:/usr/bin:/bin",
			"HOME=/tmp",
			"USER=sandbox",
			"LANG=C.UTF-8",
		}
		// Add allowed env vars
		if *allowEnv != "" {
			for _, name := range strings.Split(*allowEnv, ",") {
				name = strings.TrimSpace(name)
				if val := os.Getenv(name); val != "" {
					env = append(env, name+"="+val)
				}
			}
		}
	} else {
		env = os.Environ()
	}

	// Exec command
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(1)
	}

	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case <-time.After(*timeout):
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		fmt.Fprintln(os.Stderr, "timeout")
		os.Exit(124)
	}
}

func applyRlimits(cpu, mem, nproc, fsize, nofile uint64) {
	limits := []struct{ res int; val uint64 }{
		{unix.RLIMIT_CPU, cpu},
		{unix.RLIMIT_AS, mem},
		{unix.RLIMIT_NPROC, nproc},
		{unix.RLIMIT_FSIZE, fsize},
		{unix.RLIMIT_NOFILE, nofile},
		{unix.RLIMIT_CORE, 0},
	}
	for _, l := range limits {
		unix.Setrlimit(l.res, &unix.Rlimit{Cur: l.val, Max: l.val})
	}
}

func applySeccomp() error {
	unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0)

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
