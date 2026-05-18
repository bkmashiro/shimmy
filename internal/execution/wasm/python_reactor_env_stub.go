//go:build !linux

package wasm

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
)

// instantiateEnvModule is not supported on non-Linux platforms.
// python-reactor.wasm is a Linux-only binary.
func instantiateEnvModule(ctx context.Context, rt wazero.Runtime) error {
	return fmt.Errorf("python-reactor env module is only supported on Linux")
}
