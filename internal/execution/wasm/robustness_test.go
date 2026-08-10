package wasm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wasmULEB(v uint32) []byte {
	var buf []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if v == 0 {
			break
		}
	}
	return buf
}

func wasmSection(id byte, payload []byte) []byte {
	out := []byte{id}
	out = append(out, wasmULEB(uint32(len(payload)))...)
	out = append(out, payload...)
	return out
}

func wasmName(s string) []byte {
	out := wasmULEB(uint32(len(s)))
	out = append(out, []byte(s)...)
	return out
}

func malformedABIWasm(allocReturnsValue, dispatchReturnsValue bool) []byte {
	module := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

	// Types: alloc(i32) [-> i32], dispatch(i32, i32) [-> i32].
	var types []byte
	types = append(types, 0x02)
	types = append(types, 0x60, 0x01, 0x7f)
	if allocReturnsValue {
		types = append(types, 0x01, 0x7f)
	} else {
		types = append(types, 0x00)
	}
	types = append(types, 0x60, 0x02, 0x7f, 0x7f)
	if dispatchReturnsValue {
		types = append(types, 0x01, 0x7f)
	} else {
		types = append(types, 0x00)
	}
	module = append(module, wasmSection(1, types)...)

	// Two functions: alloc uses type 0; dispatch uses type 1.
	module = append(module, wasmSection(3, []byte{0x02, 0x00, 0x01})...)

	// One memory page.
	module = append(module, wasmSection(5, []byte{0x01, 0x00, 0x01})...)

	// Export memory, alloc, dispatch.
	var exports []byte
	exports = append(exports, 0x03)
	exports = append(exports, wasmName("memory")...)
	exports = append(exports, 0x02, 0x00)
	exports = append(exports, wasmName("alloc")...)
	exports = append(exports, 0x00, 0x00)
	exports = append(exports, wasmName("dispatch")...)
	exports = append(exports, 0x00, 0x01)
	module = append(module, wasmSection(7, exports)...)

	// Code bodies.
	var code []byte
	code = append(code, 0x02)
	allocBody := []byte{0x00}
	if allocReturnsValue {
		allocBody = append(allocBody, 0x41, 0x08) // i32.const 8
	}
	allocBody = append(allocBody, 0x0b) // end
	code = append(code, wasmULEB(uint32(len(allocBody)))...)
	code = append(code, allocBody...)

	dispatchBody := []byte{0x00}
	if dispatchReturnsValue {
		dispatchBody = append(dispatchBody, 0x41, 0x08) // i32.const 8
	}
	dispatchBody = append(dispatchBody, 0x0b) // end
	code = append(code, wasmULEB(uint32(len(dispatchBody)))...)
	code = append(code, dispatchBody...)
	module = append(module, wasmSection(10, code)...)

	return module
}

func writeTempWasm(t *testing.T, bytes []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "eval.wasm")
	require.NoError(t, os.WriteFile(path, bytes, 0o644))
	return path
}

func TestDispatcher_Send_ReturnsErrorForAllocWithoutReturnValue(t *testing.T) {
	path := writeTempWasm(t, malformedABIWasm(false, true))
	d := NewDispatcher(Config{ModulePath: path, MaxInstances: 1, Timeout: time.Second}, newTestLogger(t))
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	_, err := d.Send(context.Background(), "eval", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alloc returned 0 values")
}

func TestDispatcher_Send_ReturnsErrorForDispatchWithoutReturnValue(t *testing.T) {
	path := writeTempWasm(t, malformedABIWasm(true, false))
	d := NewDispatcher(Config{ModulePath: path, MaxInstances: 1, Timeout: time.Second}, newTestLogger(t))
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	_, err := d.Send(context.Background(), "eval", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dispatch returned 0 values")
}
