package qemurun

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildQEMUCommandIsHeadlessReadOnlyAndNetworkOff(t *testing.T) {
	config := representativeVMConfig(t)
	command, err := BuildQEMUCommand(config)
	require.NoError(t, err)
	args := strings.Join(command.Args[1:], " ")
	assert.Contains(t, args, "-no-user-config")
	assert.Contains(t, args, "-nodefaults")
	assert.Contains(t, args, "-nographic")
	assert.Contains(t, args, "readonly=on")
	assert.Contains(t, args, "-nic none")
	assert.Contains(t, args, "virtserialport")
	assert.NotContains(t, args, "accel=auto")
}

func TestBuildQEMUCommandRejectsUnresolvedAccelerator(t *testing.T) {
	config := representativeVMConfig(t)
	config.Accelerator = AcceleratorAuto
	_, err := BuildQEMUCommand(config)
	assert.ErrorIs(t, err, ErrInvalidVMConfig)
}

func TestLoadRuntimeConfigRequiresExplicitAccelerator(t *testing.T) {
	manifest, rootfs := writeImageFixture(t)
	env := map[string]string{
		"FUNCTION_QEMU_IMAGE_MANIFEST": manifest,
		"FUNCTION_QEMU_ROOTFS":         rootfs,
		"FUNCTION_QEMU_BINARY":         "/usr/bin/qemu-system-x86_64",
	}
	_, err := LoadRuntimeConfig(func(name string) string { return env[name] }, false)
	assert.ErrorIs(t, err, ErrInvalidRuntimeConfig)
}

func TestLoadRuntimeConfigRejectsUnavailableKVM(t *testing.T) {
	manifest, rootfs := writeImageFixture(t)
	env := map[string]string{
		"FUNCTION_QEMU_IMAGE_MANIFEST": manifest,
		"FUNCTION_QEMU_ROOTFS":         rootfs,
		"FUNCTION_QEMU_BINARY":         "/usr/bin/qemu-system-x86_64",
		"FUNCTION_QEMU_ACCELERATOR":    "kvm",
	}
	_, err := LoadRuntimeConfig(func(name string) string { return env[name] }, false)
	assert.True(t, errors.Is(err, ErrKVMUnavailable))
}

func TestLoadRuntimeConfigDefaultsToNoNetwork(t *testing.T) {
	manifest, rootfs := writeImageFixture(t)
	env := map[string]string{
		"FUNCTION_QEMU_IMAGE_MANIFEST": manifest,
		"FUNCTION_QEMU_ROOTFS":         rootfs,
		"FUNCTION_QEMU_BINARY":         "/usr/bin/qemu-system-x86_64",
		"FUNCTION_QEMU_ACCELERATOR":    "tcg",
	}
	config, err := LoadRuntimeConfig(func(name string) string { return env[name] }, false)
	require.NoError(t, err)
	assert.Equal(t, NetworkNone, config.VM.Network)
	assert.Equal(t, 512, config.VM.MemoryMB)
	assert.Equal(t, 1, config.VM.VCPUs)
	assert.Equal(t, 60*time.Second, config.BootTimeout)
}

func TestParseInvocationFiltersQEMUControlEnvironment(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	request := filepath.Join(t.TempDir(), "request.json")
	response := filepath.Join(t.TempDir(), "response.json")
	env := []string{
		"VISIBLE=value",
		"FUNCTION_QEMU_BINARY=secret-control",
		"SHIMMY_QEMU_INTERNAL=secret-control",
		"EVAL_IO=FILE",
		"EVAL_FILE_NAME_REQUEST=" + request,
		"EVAL_FILE_NAME_RESPONSE=" + response,
	}
	invocation, err := ParseInvocation([]string{"--", "/evaluator", "--flag", request, response}, env)
	require.NoError(t, err)
	assert.Equal(t, cwd, invocation.Start.Cwd)
	assert.Equal(t, []string{"--flag"}, invocation.Start.Args)
	assert.Contains(t, invocation.Start.Env, "VISIBLE=value")
	assert.NotContains(t, invocation.Start.Env, "FUNCTION_QEMU_BINARY=secret-control")
	assert.NotContains(t, invocation.Start.Env, "SHIMMY_QEMU_INTERNAL=secret-control")
}

func representativeVMConfig(t *testing.T) VMConfig {
	t.Helper()
	root := t.TempDir()
	path := func(name string) string {
		value := filepath.Join(root, name)
		require.NoError(t, os.WriteFile(value, []byte(name), 0o600))
		return value
	}
	return VMConfig{
		Binary:        "/usr/bin/qemu-system-x86_64",
		Kernel:        path("vmlinuz"),
		Initrd:        path("initrd"),
		RootFS:        path("rootfs"),
		RootFSFormat:  "raw",
		ControlSocket: filepath.Join(root, "control.sock"),
		Accelerator:   AcceleratorTCG,
		MemoryMB:      256,
		VCPUs:         1,
		Network:       NetworkNone,
	}
}
