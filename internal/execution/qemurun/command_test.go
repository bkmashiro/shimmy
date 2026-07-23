package qemurun

import (
	"errors"
	"slices"
	"testing"
)

func validVMConfig() VMConfig {
	return VMConfig{
		Binary:        "/opt/qemu/bin/qemu-system-x86_64",
		Kernel:        "/opt/shimmy qemu/vmlinuz",
		Initrd:        "/opt/shimmy qemu/initramfs.gz",
		RootFS:        "/opt/shimmy qemu/rootfs.qcow2",
		ControlSocket: "/tmp/shimmy qemu/control.sock",
		Accelerator:   AcceleratorTCG,
		MemoryMB:      512,
		VCPUs:         1,
		Network:       NetworkNone,
	}
}

func TestBuildQEMUCommandUsesTypedArgumentBoundaries(t *testing.T) {
	cmd, err := BuildQEMUCommand(validVMConfig())
	if err != nil {
		t.Fatalf("BuildQEMUCommand: %v", err)
	}
	if cmd.Path != "/opt/qemu/bin/qemu-system-x86_64" {
		t.Fatalf("path = %q", cmd.Path)
	}
	for _, want := range []string{
		"/opt/shimmy qemu/vmlinuz",
		"/opt/shimmy qemu/initramfs.gz",
		"socket,id=shimmy,path=/tmp/shimmy qemu/control.sock,server=on,wait=off",
		"virtserialport,chardev=shimmy,name=org.shimmy.control",
		"accel=tcg",
		"512M",
	} {
		if !slices.Contains(cmd.Args, want) {
			t.Fatalf("args missing exact token %q: %#v", want, cmd.Args)
		}
	}
	for _, forbidden := range []string{"sh", "-c", "/bin/sh"} {
		if slices.Contains(cmd.Args, forbidden) {
			t.Fatalf("args contain shell token %q", forbidden)
		}
	}
}

func TestBuildQEMUCommandUsesPrivateVirtioSerialAndReadOnlyRoot(t *testing.T) {
	cmd, err := BuildQEMUCommand(validVMConfig())
	if err != nil {
		t.Fatalf("BuildQEMUCommand: %v", err)
	}
	for _, want := range []string{
		"-no-user-config", "-nodefaults", "-nographic", "-no-reboot",
		"file=/opt/shimmy qemu/rootfs.qcow2,format=qcow2,if=virtio,readonly=on",
		"virtio-serial-pci",
		"-nic", "none",
	} {
		if !slices.Contains(cmd.Args, want) {
			t.Fatalf("args missing %q: %#v", want, cmd.Args)
		}
	}
}

func TestBuildQEMUCommandBootsReadOnlyGuestRoot(t *testing.T) {
	cmd, err := BuildQEMUCommand(validVMConfig())
	if err != nil {
		t.Fatalf("BuildQEMUCommand: %v", err)
	}
	const want = "root=/dev/vda ro rootfstype=squashfs init=/init panic=-1 reboot=t"
	if !adjacentArgs(cmd.Args, "-append", want) {
		t.Fatalf("kernel append args absent: %#v", cmd.Args)
	}
}

func TestBuildQEMUCommandSupportsUserNetworkProfile(t *testing.T) {
	cfg := validVMConfig()
	cfg.Network = NetworkUser
	cmd, err := BuildQEMUCommand(cfg)
	if err != nil {
		t.Fatalf("BuildQEMUCommand: %v", err)
	}
	if !adjacentArgs(cmd.Args, "-nic", "user,model=virtio-net-pci") {
		t.Fatalf("user network args absent: %#v", cmd.Args)
	}
}

func TestBuildQEMUCommandRejectsIncompleteOrUnboundedConfig(t *testing.T) {
	cases := map[string]func(*VMConfig){
		"missing binary":   func(c *VMConfig) { c.Binary = "" },
		"missing kernel":   func(c *VMConfig) { c.Kernel = "" },
		"missing initrd":   func(c *VMConfig) { c.Initrd = "" },
		"missing rootfs":   func(c *VMConfig) { c.RootFS = "" },
		"missing socket":   func(c *VMConfig) { c.ControlSocket = "" },
		"zero memory":      func(c *VMConfig) { c.MemoryMB = 0 },
		"excessive memory": func(c *VMConfig) { c.MemoryMB = MaxMemoryMB + 1 },
		"zero vcpus":       func(c *VMConfig) { c.VCPUs = 0 },
		"excessive vcpus":  func(c *VMConfig) { c.VCPUs = MaxVCPUs + 1 },
		"unknown accel":    func(c *VMConfig) { c.Accelerator = "magic" },
		"auto unresolved":  func(c *VMConfig) { c.Accelerator = AcceleratorAuto },
		"unknown network":  func(c *VMConfig) { c.Network = "bridge-everything" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validVMConfig()
			mutate(&cfg)
			_, err := BuildQEMUCommand(cfg)
			if !errors.Is(err, ErrInvalidVMConfig) {
				t.Fatalf("error = %v, want ErrInvalidVMConfig", err)
			}
		})
	}
}

func adjacentArgs(args []string, first, second string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == first && args[i+1] == second {
			return true
		}
	}
	return false
}
