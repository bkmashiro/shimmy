//go:build linux

package wasm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
)

func TestInstantiateEnvModuleProvidesPythonReactorImports(t *testing.T) {
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	t.Cleanup(func() { require.NoError(t, rt.Close(ctx)) })

	require.NoError(t, instantiateEnvModule(ctx, rt))

	compiled, err := rt.CompileModule(ctx, buildEnvImportProbeModule(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, compiled.Close(ctx)) })

	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mod.Close(ctx)) })
}

func buildEnvImportProbeModule(t *testing.T) []byte {
	t.Helper()

	types := [][]byte{
		wasmFuncType([]byte{0x7f, 0x7f}, []byte{0x7f}),             // (i32, i32) -> i32
		wasmFuncType([]byte{0x7d}, []byte{0x7d}),                   // (f32) -> f32
		wasmFuncType([]byte{0x7c}, []byte{0x7c}),                   // (f64) -> f64
		wasmFuncType([]byte{0x7f, 0x7f}, []byte{0x7f}),             // (i32, i32) -> i32
		wasmFuncType([]byte{0x7f, 0x7e, 0x7e, 0x7e}, []byte{0x7e}), // (i32, i64, i64, i64) -> i64
	}
	imports := []struct {
		name      string
		typeIndex uint32
	}{
		{name: "dlopen", typeIndex: 0},
		{name: "npy_expf", typeIndex: 1},
		{name: "npy_spacing", typeIndex: 2},
		{name: "npy_half_eq", typeIndex: 3},
		{name: "random_hypergeometric", typeIndex: 4},
	}

	module := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	module = append(module, wasmSection(1, wasmVec(types...))...)

	importPayload := leb128Encode(uint32(len(imports)))
	for _, imp := range imports {
		importPayload = append(importPayload, wasmName("env")...)
		importPayload = append(importPayload, wasmName(imp.name)...)
		importPayload = append(importPayload, 0x00) // function import
		importPayload = append(importPayload, leb128Encode(imp.typeIndex)...)
	}
	module = append(module, wasmSection(2, importPayload)...)
	return module
}

func wasmFuncType(params, results []byte) []byte {
	out := []byte{0x60}
	out = append(out, leb128Encode(uint32(len(params)))...)
	out = append(out, params...)
	out = append(out, leb128Encode(uint32(len(results)))...)
	out = append(out, results...)
	return out
}

func wasmVec(entries ...[]byte) []byte {
	out := leb128Encode(uint32(len(entries)))
	for _, entry := range entries {
		out = append(out, entry...)
	}
	return out
}

func wasmSection(id byte, payload []byte) []byte {
	out := []byte{id}
	out = append(out, leb128Encode(uint32(len(payload)))...)
	out = append(out, payload...)
	return out
}

func wasmName(s string) []byte {
	out := leb128Encode(uint32(len(s)))
	out = append(out, []byte(s)...)
	return out
}
