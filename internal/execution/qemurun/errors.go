package qemurun

import "errors"

// ErrVMBootTimeout reports that the host did not complete the guest control
// handshake before the configured boot deadline.
var ErrVMBootTimeout = errors.New("qemu runner: vm boot timeout")
