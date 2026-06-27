//go:build !linux

package wasm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
)

func TestInstantiateEnvModuleReportsUnsupportedPlatform(t *testing.T) {
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	t.Cleanup(func() { require.NoError(t, rt.Close(ctx)) })

	err := instantiateEnvModule(ctx, rt)
	require.ErrorContains(t, err, "python-reactor env module is only supported on Linux")
}
