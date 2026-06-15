//go:build linux

package worker_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
)

func requireNsjail(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not available on this host")
	}
}

func TestApplySandbox_CommandPlacement(t *testing.T) {
	cfg := worker.SandboxConfig{NsjailPath: "/usr/sbin/nsjail"}
	config := worker.StartConfig{Cmd: "/bin/echo", Args: []string{"hello"}}

	out, err := worker.ApplySandboxForTest(config, cfg)
	require.NoError(t, err)

	assert.Equal(t, "/usr/sbin/nsjail", out.Cmd)

	sepIdx := indexOf(out.Args, "--")
	require.NotEqual(t, -1, sepIdx, "'--' separator must be present")
	assert.Equal(t, "/bin/echo", out.Args[sepIdx+1])
	assert.Equal(t, "hello", out.Args[sepIdx+2])
}

func TestApplySandbox_EmptyCmd_ReturnsError(t *testing.T) {
	_, err := worker.ApplySandboxForTest(worker.StartConfig{}, worker.SandboxConfig{})
	assert.Error(t, err)
}

func TestApplySandbox_DefaultUser(t *testing.T) {
	out, err := worker.ApplySandboxForTest(
		worker.StartConfig{Cmd: "/bin/sh"},
		worker.SandboxConfig{},
	)
	require.NoError(t, err)
	assert.Contains(t, out.Args, "65534:65534")
}

func TestApplySandbox_CustomUser(t *testing.T) {
	out, err := worker.ApplySandboxForTest(
		worker.StartConfig{Cmd: "/bin/sh"},
		worker.SandboxConfig{User: "1000:1000"},
	)
	require.NoError(t, err)
	assert.Contains(t, out.Args, "1000:1000")
}

func TestApplySandbox_ResourceLimits(t *testing.T) {
	out, err := worker.ApplySandboxForTest(
		worker.StartConfig{Cmd: "/bin/sh"},
		worker.SandboxConfig{CpuTimeLimit: 30, MemoryLimit: 256, MaxFds: 64},
	)
	require.NoError(t, err)

	assert.True(t, containsPair(out.Args, "--rlimit_cpu", "30"), "missing --rlimit_cpu 30")
	assert.True(t, containsPair(out.Args, "--rlimit_as", "256"), "missing --rlimit_as 256")
	assert.True(t, containsPair(out.Args, "--rlimit_nofile", "64"), "missing --rlimit_nofile 64")
}

func TestApplySandbox_ZeroLimits_NoFlags(t *testing.T) {
	out, err := worker.ApplySandboxForTest(
		worker.StartConfig{Cmd: "/bin/sh"},
		worker.SandboxConfig{},
	)
	require.NoError(t, err)
	assert.NotContains(t, out.Args, "--time_limit")
	assert.NotContains(t, out.Args, "--rlimit_as")
	assert.NotContains(t, out.Args, "--rlimit_nofile")
}

func TestApplySandbox_NetworkEnabled(t *testing.T) {
	out, err := worker.ApplySandboxForTest(
		worker.StartConfig{Cmd: "/bin/sh"},
		worker.SandboxConfig{DisableNetwork: false},
	)
	require.NoError(t, err)
	assert.Contains(t, out.Args, "--disable_clone_newnet")
	assert.NotContains(t, out.Args, "--iface_no_lo")
}

func TestApplySandbox_NetworkDisabled(t *testing.T) {
	out, err := worker.ApplySandboxForTest(
		worker.StartConfig{Cmd: "/bin/sh"},
		worker.SandboxConfig{DisableNetwork: true},
	)
	require.NoError(t, err)
	assert.Contains(t, out.Args, "--iface_no_lo")
	assert.NotContains(t, out.Args, "--disable_clone_newnet")
}

func TestApplySandbox_BindMounts(t *testing.T) {
	out, err := worker.ApplySandboxForTest(
		worker.StartConfig{Cmd: "/bin/sh"},
		worker.SandboxConfig{
			ReadOnlyBinds: []string{"/usr", "/lib"},
			WritableBinds: []string{"/tmp/shimmy"},
			TmpfsMounts:   []string{"/tmp"},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, 2, countFlag(out.Args, "--bindmount_ro"))
	assert.Equal(t, 1, countFlag(out.Args, "--bindmount"))
	assert.Equal(t, 1, countFlag(out.Args, "--tmpfsmount"))
	assert.Contains(t, out.Args, "/usr")
	assert.Contains(t, out.Args, "/lib")
	assert.Contains(t, out.Args, "/tmp/shimmy")
}

func TestApplySandbox_CwdPreserved(t *testing.T) {
	out, err := worker.ApplySandboxForTest(
		worker.StartConfig{Cmd: "/bin/sh", Cwd: "/app"},
		worker.SandboxConfig{},
	)
	require.NoError(t, err)

	cwdIdx := indexOf(out.Args, "--cwd")
	require.NotEqual(t, -1, cwdIdx, "--cwd flag must be present")
	assert.Equal(t, "/app", out.Args[cwdIdx+1])
	assert.Empty(t, out.Cwd, "exec.Cmd CWD must be empty; nsjail manages it")
}

func TestApplySandbox_EnvPreserved(t *testing.T) {
	env := []string{"FOO=bar", "BAZ=qux"}
	out, err := worker.ApplySandboxForTest(
		worker.StartConfig{Cmd: "/bin/sh", Env: env},
		worker.SandboxConfig{},
	)
	require.NoError(t, err)
	assert.Equal(t, env, out.Env)
}

func TestNewSandboxedWorkerFactory_MissingBinary(t *testing.T) {
	_, err := worker.NewSandboxedWorkerFactory(worker.SandboxConfig{
		NsjailPath: "/nonexistent/nsjail",
	})
	assert.Error(t, err)
}

// Integration test: requires nsjail binary on the host.
func TestSandboxedWorker_ExitsSuccessfully(t *testing.T) {
	requireNsjail(t)

	factory, err := worker.NewSandboxedWorkerFactory(worker.SandboxConfig{
		NsjailPath:    "/usr/sbin/nsjail",
		ReadOnlyBinds: []string{"/usr", "/bin", "/lib", "/lib64"},
	})
	require.NoError(t, err)

	w, err := factory(context.Background(), worker.StartConfig{Cmd: "/bin/true"}, zap.NewNop())
	require.NoError(t, err)

	require.NoError(t, w.Start(context.Background()))

	exit, err := w.Wait(context.Background())
	require.NoError(t, err)
	assert.True(t, exit.Success(), "expected exit 0, got: %s", exit.String())
}

// TestSandboxedWorker_FilesystemIsolation verifies that the worker cannot
// access filesystem paths that were not explicitly bind-mounted.
func TestSandboxedWorker_FilesystemIsolation(t *testing.T) {
	requireNsjail(t)

	factory, err := worker.NewSandboxedWorkerFactory(worker.SandboxConfig{
		NsjailPath:    "/usr/sbin/nsjail",
		ReadOnlyBinds: []string{"/usr", "/bin", "/lib", "/lib64"},
		// /etc is deliberately NOT mounted
	})
	require.NoError(t, err)

	// Try to read /etc/shadow — must not be accessible inside the sandbox.
	w, err := factory(context.Background(), worker.StartConfig{
		Cmd:  "/bin/cat",
		Args: []string{"/etc/shadow"},
	}, zap.NewNop())
	require.NoError(t, err)
	require.NoError(t, w.Start(context.Background()))

	exit, err := w.Wait(context.Background())
	require.NoError(t, err)
	assert.False(t, exit.Success(), "worker should not be able to read /etc/shadow: %s", exit.String())
}

// TestSandboxedWorker_CanReadBoundPath verifies that a worker can read a file
// whose parent directory is explicitly bind-mounted.
func TestSandboxedWorker_CanReadBoundPath(t *testing.T) {
	requireNsjail(t)

	// Write a sentinel file on the host. Use os.MkdirTemp so the directory lives
	// directly under /tmp (mode 1777) — t.TempDir nests under a 0700 parent that
	// nobody (uid 65534) cannot traverse even after chmoding the leaf.
	dir, err := os.MkdirTemp("", "sandbox-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	require.NoError(t, os.Chmod(dir, 0755))
	sentinelPath := dir + "/sentinel.txt"
	require.NoError(t, os.WriteFile(sentinelPath, []byte("sandbox-ok\n"), 0644))

	factory, err := worker.NewSandboxedWorkerFactory(worker.SandboxConfig{
		NsjailPath:    "/usr/sbin/nsjail",
		ReadOnlyBinds: []string{"/usr", "/bin", "/lib", "/lib64", dir},
	})
	require.NoError(t, err)

	w, err := factory(context.Background(), worker.StartConfig{
		Cmd:  "/bin/cat",
		Args: []string{sentinelPath},
	}, zap.NewNop())
	require.NoError(t, err)

	stdout, err := w.ReadPipe()
	require.NoError(t, err)

	require.NoError(t, w.Start(context.Background()))

	var out bytes.Buffer
	io.Copy(&out, stdout) //nolint:errcheck

	exit, err := w.Wait(context.Background())
	require.NoError(t, err)
	assert.True(t, exit.Success(), "expected exit 0: %s", exit.String())
	assert.Equal(t, "sandbox-ok\n", out.String())
}

// TestSandboxedWorker_CpuTimeLimit verifies that nsjail kills a CPU-spinning
// worker once the CPU time limit is reached.
func TestSandboxedWorker_CpuTimeLimit(t *testing.T) {
	requireNsjail(t)

	factory, err := worker.NewSandboxedWorkerFactory(worker.SandboxConfig{
		NsjailPath:    "/usr/sbin/nsjail",
		ReadOnlyBinds: []string{"/usr", "/bin", "/lib", "/lib64"},
		CpuTimeLimit:  1, // 1 CPU-second
	})
	require.NoError(t, err)

	w, err := factory(context.Background(), worker.StartConfig{
		Cmd:  "/bin/sh",
		Args: []string{"-c", "while true; do :; done"},
	}, zap.NewNop())
	require.NoError(t, err)

	start := time.Now()
	require.NoError(t, w.Start(context.Background()))

	exit, err := w.Wait(context.Background())
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.False(t, exit.Success(), "CPU-bound worker should have been killed by nsjail")
	assert.Less(t, elapsed, 10*time.Second, "worker should have been killed well before 10s wall time")
}

// TestSandboxedWorker_NetworkIsolation verifies that a worker with
// DisableNetwork set cannot make outbound network connections.
func TestSandboxedWorker_NetworkIsolation(t *testing.T) {
	requireNsjail(t)

	factory, err := worker.NewSandboxedWorkerFactory(worker.SandboxConfig{
		NsjailPath:     "/usr/sbin/nsjail",
		ReadOnlyBinds:  []string{"/usr", "/bin", "/lib", "/lib64"},
		DisableNetwork: true,
	})
	require.NoError(t, err)

	// Attempt any TCP connection; nc exits non-zero if the interface is gone.
	w, err := factory(context.Background(), worker.StartConfig{
		Cmd:  "/bin/sh",
		Args: []string{"-c", "nc -z -w1 8.8.8.8 53; exit $?"},
	}, zap.NewNop())
	require.NoError(t, err)
	require.NoError(t, w.Start(context.Background()))

	exit, err := w.Wait(context.Background())
	require.NoError(t, err)
	assert.False(t, exit.Success(), "worker should not be able to reach the network")
}

// TestSandboxedWorker_StdioPassthrough verifies that stdin/stdout pipes work
// correctly through the nsjail layer — critical for RPC/stdio transport mode.
func TestSandboxedWorker_StdioPassthrough(t *testing.T) {
	requireNsjail(t)

	factory, err := worker.NewSandboxedWorkerFactory(worker.SandboxConfig{
		NsjailPath:    "/usr/sbin/nsjail",
		ReadOnlyBinds: []string{"/usr", "/bin", "/lib", "/lib64"},
	})
	require.NoError(t, err)

	// /bin/cat echoes stdin to stdout — simplest stdio round-trip.
	w, err := factory(context.Background(), worker.StartConfig{Cmd: "/bin/cat"}, zap.NewNop())
	require.NoError(t, err)

	duplex, err := w.DuplexPipe()
	require.NoError(t, err)

	require.NoError(t, w.Start(context.Background()))

	msg := "hello nsjail\n"
	_, err = io.WriteString(duplex, msg)
	require.NoError(t, err)
	require.NoError(t, duplex.Close()) // send EOF so cat exits

	var out bytes.Buffer
	_, err = io.Copy(&out, duplex)
	// EOF after close is expected
	if err != nil && err != io.EOF && err != io.ErrClosedPipe {
		require.NoError(t, err)
	}

	exit, err := w.Wait(context.Background())
	require.NoError(t, err)
	assert.True(t, exit.Success(), "expected exit 0: %s", exit.String())
	assert.Equal(t, msg, out.String())
}

// helpers

func indexOf(slice []string, val string) int {
	for i, s := range slice {
		if s == val {
			return i
		}
	}
	return -1
}

func countFlag(slice []string, flag string) int {
	n := 0
	for _, s := range slice {
		if s == flag {
			n++
		}
	}
	return n
}

// containsPair returns true if flag and value appear consecutively in slice.
func containsPair(slice []string, flag, value string) bool {
	for i := 0; i < len(slice)-1; i++ {
		if slice[i] == flag && slice[i+1] == value {
			return true
		}
	}
	return false
}
