package qemurun

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const (
	MaxMemoryMB = 32 * 1024
	MaxVCPUs    = 64
)

var ErrInvalidVMConfig = errors.New("qemu runner: invalid vm config")

type Accelerator string

const (
	AcceleratorAuto Accelerator = "auto"
	AcceleratorTCG  Accelerator = "tcg"
	AcceleratorKVM  Accelerator = "kvm"
)

type NetworkProfile string

const (
	NetworkNone NetworkProfile = "none"
	NetworkUser NetworkProfile = "user"
)

type VMConfig struct {
	Binary        string
	Kernel        string
	Initrd        string
	RootFS        string
	ControlSocket string
	Accelerator   Accelerator
	MemoryMB      int
	VCPUs         int
	Network       NetworkProfile
}

func BuildQEMUCommand(config VMConfig) (*exec.Cmd, error) {
	if err := validateVMConfig(config); err != nil {
		return nil, err
	}

	args := []string{
		"-no-user-config",
		"-nodefaults",
		"-nographic",
		"-display", "none",
		"-monitor", "none",
		"-serial", "none",
		"-no-reboot",
		"-machine", "accel=" + string(config.Accelerator),
		"-smp", strconv.Itoa(config.VCPUs),
		"-m", strconv.Itoa(config.MemoryMB) + "M",
		"-kernel", config.Kernel,
		"-initrd", config.Initrd,
		"-append", "root=/dev/vda ro rootfstype=ext4 init=/init panic=-1 reboot=t",
		"-drive", "file=" + config.RootFS + ",format=qcow2,if=virtio,readonly=on",
		"-device", "virtio-serial-pci",
		"-chardev", "socket,id=shimmy,path=" + config.ControlSocket + ",server=on,wait=off",
		"-device", "virtserialport,chardev=shimmy,name=org.shimmy.control",
	}
	if config.Network == NetworkUser {
		args = append(args, "-nic", "user,model=virtio-net-pci")
	} else {
		args = append(args, "-nic", "none")
	}
	return exec.Command(config.Binary, args...), nil
}

func validateVMConfig(config VMConfig) error {
	required := []struct {
		name  string
		value string
	}{
		{"binary", config.Binary},
		{"kernel", config.Kernel},
		{"initrd", config.Initrd},
		{"rootfs", config.RootFS},
		{"control socket", config.ControlSocket},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%w: missing %s", ErrInvalidVMConfig, field.name)
		}
	}
	if config.MemoryMB <= 0 || config.MemoryMB > MaxMemoryMB {
		return fmt.Errorf("%w: memory must be in [1,%d] MiB", ErrInvalidVMConfig, MaxMemoryMB)
	}
	if config.VCPUs <= 0 || config.VCPUs > MaxVCPUs {
		return fmt.Errorf("%w: vcpus must be in [1,%d]", ErrInvalidVMConfig, MaxVCPUs)
	}
	switch config.Accelerator {
	case AcceleratorTCG, AcceleratorKVM:
	default:
		return fmt.Errorf("%w: accelerator %q must be resolved to tcg or kvm", ErrInvalidVMConfig, config.Accelerator)
	}
	switch config.Network {
	case NetworkNone, NetworkUser:
	default:
		return fmt.Errorf("%w: unsupported network profile %q", ErrInvalidVMConfig, config.Network)
	}
	return nil
}
