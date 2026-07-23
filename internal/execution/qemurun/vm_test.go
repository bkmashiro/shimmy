package qemurun

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func lifecycleRuntimeConfig(t *testing.T) RuntimeConfig {
	t.Helper()
	return RuntimeConfig{
		VM: VMConfig{
			Binary:      "/unused/qemu",
			Kernel:      "/unused/kernel",
			Initrd:      "/unused/initrd",
			RootFS:      "/unused/rootfs",
			Accelerator: AcceleratorTCG,
			MemoryMB:    128,
			VCPUs:       1,
			Network:     NetworkNone,
		},
		MaxFrameBytes:   1024,
		BootTimeout:     time.Second,
		ShutdownTimeout: 200 * time.Millisecond,
		WorkRoot:        t.TempDir(),
	}
}

func TestStartVMConnectsAndCleansPrivateWorkDirectory(t *testing.T) {
	config := lifecycleRuntimeConfig(t)
	var socketPath string
	var dialAttempts atomic.Int32
	peerCh := make(chan net.Conn, 1)
	deps := vmDependencies{
		buildCommand: func(vm VMConfig) (*exec.Cmd, error) {
			socketPath = vm.ControlSocket
			return exec.Command("/bin/sh", "-c", "sleep 30"), nil
		},
		dial: func(context.Context, string) (net.Conn, error) {
			if dialAttempts.Add(1) < 3 {
				return nil, errors.New("not ready")
			}
			host, guest := net.Pipe()
			peerCh <- guest
			return host, nil
		},
	}

	vm, err := startVM(context.Background(), config, deps)
	if err != nil {
		t.Fatalf("startVM: %v", err)
	}
	peer := <-peerCh
	defer peer.Close()
	if vm.Connection() == nil {
		t.Fatal("VM has no control connection")
	}
	workDir := filepath.Dir(socketPath)
	if filepath.Dir(workDir) != filepath.Clean(config.WorkRoot) {
		t.Fatalf("work dir %q is not under %q", workDir, config.WorkRoot)
	}
	info, err := os.Stat(workDir)
	if err != nil {
		t.Fatalf("stat work dir: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("work dir mode = %o", info.Mode().Perm())
	}

	if err := vm.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Fatalf("work dir retained after Close: %v", err)
	}
}

func TestStartVMReturnsEarlyProcessFailureAndBoundedStderr(t *testing.T) {
	config := lifecycleRuntimeConfig(t)
	deps := vmDependencies{
		buildCommand: func(VMConfig) (*exec.Cmd, error) {
			return exec.Command("/bin/sh", "-c", "printf 'boot failed marker' >&2; exit 17"), nil
		},
		dial: func(context.Context, string) (net.Conn, error) {
			return nil, errors.New("not ready")
		},
	}

	_, err := startVM(context.Background(), config, deps)
	if err == nil || !strings.Contains(err.Error(), "boot failed marker") || !strings.Contains(err.Error(), "17") {
		t.Fatalf("early exit error = %v", err)
	}
	entries, readErr := os.ReadDir(config.WorkRoot)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("early failure retained %d work entries", len(entries))
	}
}

func TestStartVMTimeoutKillsProcessAndCleans(t *testing.T) {
	config := lifecycleRuntimeConfig(t)
	config.BootTimeout = 75 * time.Millisecond
	deps := vmDependencies{
		buildCommand: func(VMConfig) (*exec.Cmd, error) {
			return exec.Command("/bin/sh", "-c", "sleep 30"), nil
		},
		dial: func(context.Context, string) (net.Conn, error) {
			return nil, errors.New("not ready")
		},
	}

	started := time.Now()
	_, err := startVM(context.Background(), config, deps)
	if !errors.Is(err, ErrVMBootTimeout) {
		t.Fatalf("error = %v, want ErrVMBootTimeout", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("boot timeout cleanup took %s", time.Since(started))
	}
	entries, readErr := os.ReadDir(config.WorkRoot)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("timeout retained %d work entries", len(entries))
	}
}
