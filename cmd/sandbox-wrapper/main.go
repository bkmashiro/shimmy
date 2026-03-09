//go:build linux
// +build linux

package main

/*
#cgo pkg-config: libseccomp
#include <seccomp.h>
#include <sys/prctl.h>

int apply_network_filter() {
    prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0);
    scmp_filter_ctx ctx = seccomp_init(SCMP_ACT_ALLOW);
    if (!ctx) return -1;
    seccomp_attr_set(ctx, SCMP_FLTATR_CTL_TSYNC, 1);
    seccomp_rule_add(ctx, SCMP_ACT_ERRNO(1), SCMP_SYS(socket), 0);
    seccomp_rule_add(ctx, SCMP_ACT_ERRNO(1), SCMP_SYS(connect), 0);
    seccomp_rule_add(ctx, SCMP_ACT_ERRNO(1), SCMP_SYS(bind), 0);
    seccomp_rule_add(ctx, SCMP_ACT_ERRNO(1), SCMP_SYS(listen), 0);
    seccomp_rule_add(ctx, SCMP_ACT_ERRNO(1), SCMP_SYS(accept), 0);
    seccomp_rule_add(ctx, SCMP_ACT_ERRNO(1), SCMP_SYS(accept4), 0);
    seccomp_rule_add(ctx, SCMP_ACT_ERRNO(1), SCMP_SYS(sendto), 0);
    seccomp_rule_add(ctx, SCMP_ACT_ERRNO(1), SCMP_SYS(recvfrom), 0);
    seccomp_rule_add(ctx, SCMP_ACT_ERRNO(1), SCMP_SYS(sendmsg), 0);
    seccomp_rule_add(ctx, SCMP_ACT_ERRNO(1), SCMP_SYS(recvmsg), 0);
    int rc = seccomp_load(ctx);
    seccomp_release(ctx);
    return rc;
}
*/
import "C"

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"golang.org/x/sys/unix"
)

func init() { runtime.LockOSThread() }

func main() {
	cpu := flag.Uint64("cpu", 5, "CPU seconds")
	mem := flag.Uint64("mem", 256, "Memory MB")
	nproc := flag.Uint64("nproc", 10, "Max procs")
	fsize := flag.Uint64("fsize", 10, "Max file MB")
	nofile := flag.Uint64("nofile", 100, "Max FDs")
	timeout := flag.Duration("timeout", 30*time.Second, "Timeout")
	noNet := flag.Bool("no-network", false, "Block network")
	noFork := flag.Bool("no-fork", false, "Block fork via LD_PRELOAD")
	cleanEnv := flag.Bool("clean-env", false, "Clean env")
	allowEnv := flag.String("allow-env", "", "Allowed env vars")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: sandbox-wrapper [opts] -- cmd [args]")
		os.Exit(1)
	}

	// rlimits
	for _, l := range []struct{r int; v uint64}{
		{unix.RLIMIT_CPU, *cpu}, {unix.RLIMIT_AS, *mem*1024*1024},
		{unix.RLIMIT_NPROC, *nproc}, {unix.RLIMIT_FSIZE, *fsize*1024*1024},
		{unix.RLIMIT_NOFILE, *nofile}, {unix.RLIMIT_CORE, 0},
	} { unix.Setrlimit(l.r, &unix.Rlimit{Cur: l.v, Max: l.v}) }

	// Network blocking via seccomp (applied to wrapper, inherited by child)
	if *noNet {
		if rc := C.apply_network_filter(); rc != 0 {
			fmt.Fprintf(os.Stderr, "seccomp: %d\n", rc)
			os.Exit(1)
		}
	}

	// Build environment
	var env []string
	if *cleanEnv {
		env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp", "USER=sandbox", "LANG=C.UTF-8"}
		for _, n := range strings.Split(*allowEnv, ",") {
			if v := os.Getenv(strings.TrimSpace(n)); v != "" { env = append(env, n+"="+v) }
		}
	} else {
		env = os.Environ()
	}

	// Fork blocking via LD_PRELOAD (injected into child)
	if *noFork {
		// Look for sandbox_preload.so next to the wrapper
		exe, _ := os.Executable()
		preloadPath := filepath.Join(filepath.Dir(exe), "sandbox_preload.so")
		if _, err := os.Stat(preloadPath); err == nil {
			env = append(env, "LD_PRELOAD="+preloadPath, "SANDBOX_NO_FORK=1")
		}
	}

	// exec
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil { fmt.Fprintf(os.Stderr, "start: %v\n", err); os.Exit(1) }
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if e, ok := err.(*exec.ExitError); ok { os.Exit(e.ExitCode()) }
		if err != nil { os.Exit(1) }
	case <-time.After(*timeout):
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		fmt.Fprintln(os.Stderr, "timeout")
		os.Exit(124)
	}
}
