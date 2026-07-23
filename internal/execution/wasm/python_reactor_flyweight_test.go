//go:build linux

package wasm

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"go.uber.org/zap"
)

func TestReactorPythonPoolSizeHonorsConfiguredCapacity(t *testing.T) {
	require.Equal(t, 8, reactorPythonPoolSize(8, 16), "explicit capacity must not be capped at four")
	require.Equal(t, 4, reactorPythonPoolSize(0, 16), "automatic capacity keeps the conservative default")
	require.Equal(t, 2, reactorPythonPoolSize(-1, 2))
}

func TestReactorPythonFlyweightSharesCompiledCodeAcrossDistinctInstances(t *testing.T) {
	ctx := context.Background()
	flyweight, err := newReactorPythonFlyweight(ctx, buildTestMemoryModule(t, 1), Config{
		MaxMemoryPages: 16,
	}, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, flyweight.Close(context.Background())) })

	first, err := flyweight.Instantiate(ctx, wazero.NewModuleConfig().WithName(""))
	require.NoError(t, err)
	second, err := flyweight.Instantiate(ctx, wazero.NewModuleConfig().WithName(""))
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close(context.Background()) })
	t.Cleanup(func() { _ = second.Close(context.Background()) })

	require.NotEqual(t, first, second)
	require.True(t, first.Memory().WriteByte(0, 0x7f))
	got, ok := second.Memory().ReadByte(0)
	require.True(t, ok)
	require.Zero(t, got, "mutable linear memory must remain instance-local")

	require.NoError(t, first.Close(ctx))
	require.True(t, first.IsClosed())
	require.False(t, second.IsClosed(), "closing one child must not close a sibling")
	require.True(t, second.Memory().WriteByte(0, 0x42))
}

func TestReactorPythonFlyweightTimeoutClosesOnlyAffectedInstance(t *testing.T) {
	ctx := context.Background()
	flyweight, err := newReactorPythonFlyweight(ctx, loopAndReturnWasm(), Config{
		MaxMemoryPages: 16,
	}, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, flyweight.Close(context.Background())) })

	timedOut, err := flyweight.Instantiate(ctx, wazero.NewModuleConfig().WithName(""))
	require.NoError(t, err)
	sibling, err := flyweight.Instantiate(ctx, wazero.NewModuleConfig().WithName(""))
	require.NoError(t, err)

	callCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	_, err = timedOut.ExportedFunction("loop").Call(callCtx)
	require.Error(t, err)
	require.True(t, timedOut.IsClosed(), "WithCloseOnContextDone must close the timed-out module")

	_, err = sibling.ExportedFunction("return").Call(ctx)
	require.NoError(t, err, "sibling in the shared Runtime must remain usable")

	replacement, err := flyweight.Instantiate(ctx, wazero.NewModuleConfig().WithName(""))
	require.NoError(t, err, "replacement must reuse the live flyweight")
	_, err = replacement.ExportedFunction("return").Call(ctx)
	require.NoError(t, err)
}

func TestReactorPythonDispatcherPartialStartClosesSharedResources(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	wasmPath := root + "/minimal.wasm"
	scriptPath := root + "/evaluator.py"
	require.NoError(t, os.WriteFile(wasmPath, buildTestMemoryModule(t, 1), 0o600))
	require.NoError(t, os.WriteFile(scriptPath, []byte("def evaluation_function(*args): return {}\n"), 0o600))

	dispatcher := NewReactorPythonDispatcher(Config{
		ModulePath:       wasmPath,
		PythonScriptPath: scriptPath,
		SnapshotMode:     "cow",
		MaxInstances:     2,
		MaxMemoryPages:   16,
	}, zap.NewNop())
	err := dispatcher.Start(ctx)
	require.ErrorContains(t, err, "missing required export")
	require.Nil(t, dispatcher.flyweight, "failed Start must close and clear shared Runtime")
	require.Nil(t, dispatcher.cowCoordinator, "failed Start must close and clear canonical-image owner")
	require.NoError(t, dispatcher.Shutdown(ctx))
}

func TestBorrowedReactorPythonRunnerShutdownDoesNotCloseFlyweight(t *testing.T) {
	ctx := context.Background()
	flyweight, err := newReactorPythonFlyweight(ctx, buildTestMemoryModule(t, 1), Config{
		MaxMemoryPages: 16,
	}, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, flyweight.Close(context.Background())) })

	ownedModule, err := flyweight.Instantiate(ctx, wazero.NewModuleConfig().WithName(""))
	require.NoError(t, err)
	sibling, err := flyweight.Instantiate(ctx, wazero.NewModuleConfig().WithName(""))
	require.NoError(t, err)

	runner := &ReactorPythonRunner{
		rt:        flyweight.runtime,
		mod:       ownedModule,
		flyweight: flyweight,
	}
	require.NoError(t, runner.closeAll(ctx))
	require.True(t, ownedModule.IsClosed())
	require.False(t, sibling.IsClosed())
	require.True(t, sibling.Memory().WriteByte(0, 1), "borrowed runner shutdown must leave shared Runtime alive")
}

func TestReactorPythonFlyweightCloseClosesChildrenAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	flyweight, err := newReactorPythonFlyweight(ctx, buildTestMemoryModule(t, 1), Config{
		MaxMemoryPages: 16,
	}, zap.NewNop())
	require.NoError(t, err)

	first, err := flyweight.Instantiate(ctx, wazero.NewModuleConfig().WithName(""))
	require.NoError(t, err)
	second, err := flyweight.Instantiate(ctx, wazero.NewModuleConfig().WithName(""))
	require.NoError(t, err)

	require.NoError(t, flyweight.Close(ctx))
	require.True(t, first.IsClosed())
	require.True(t, second.IsClosed())
	require.NoError(t, flyweight.Close(ctx))
	_, err = flyweight.Instantiate(ctx, wazero.NewModuleConfig().WithName(""))
	require.Error(t, err)
}

// loopAndReturnWasm exports an infinite `loop` function and an empty `return`
// function. It is intentionally tiny so timeout isolation is exercised without
// the large Python artifact.
func loopAndReturnWasm() []byte {
	module := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	// type: one () -> () signature
	module = append(module, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00)
	// functions: two functions of type 0
	module = append(module, 0x03, 0x03, 0x02, 0x00, 0x00)
	// exports: loop=function 0, return=function 1
	exports := []byte{0x02,
		0x04, 'l', 'o', 'o', 'p', 0x00, 0x00,
		0x06, 'r', 'e', 't', 'u', 'r', 'n', 0x00, 0x01,
	}
	module = append(module, 0x07, byte(len(exports)))
	module = append(module, exports...)
	// code: loop { br 0 }; and an empty function
	code := []byte{0x02,
		0x07, 0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x0b,
		0x02, 0x00, 0x0b,
	}
	module = append(module, 0x0a, byte(len(code)))
	module = append(module, code...)
	return module
}
