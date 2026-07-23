//go:build linux

package wasm

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCowDispatcherUsesOneCanonicalImageAcrossWazeroInstances(t *testing.T) {
	cfg := Config{
		ModulePath:     echoModulePath(t),
		MaxInstances:   2,
		Timeout:        5 * time.Second,
		SnapshotMode:   "cow",
		MaxMemoryPages: 256,
	}
	dispatcher := NewDispatcher(cfg, newTestLogger(t))
	require.NoError(t, dispatcher.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, dispatcher.Shutdown(context.Background())) })

	require.NotNil(t, dispatcher.cowCoordinator)
	require.NotEmpty(t, dispatcher.cowCoordinator.ImageID())

	supervisors := make([]*wasmSupervisor, 0, cap(dispatcher.pool))
	for range cap(dispatcher.pool) {
		supervisor := <-dispatcher.pool
		supervisors = append(supervisors, supervisor)
		strategy, ok := supervisor.strategy.(*CowSnapshotStrategy)
		require.True(t, ok, "explicit cow mode must construct CowSnapshotStrategy, got %T", supervisor.strategy)
		require.True(t, strategy.UsingCow(), "matching prepared instances must attach to the canonical image")
		require.Equal(t, dispatcher.cowCoordinator.ImageID(), strategy.ImageID())
	}
	for _, supervisor := range supervisors {
		dispatcher.pool <- supervisor
	}

	for i := range 20 {
		result, err := dispatcher.Send(context.Background(), "eval", map[string]any{"iteration": i})
		require.NoError(t, err)
		require.Equal(t, true, result["ok"])
	}
}

func TestCowWazeroMemoryGrowthFailsAfterPreparedImageAttach(t *testing.T) {
	cfg := Config{
		ModulePath:     echoModulePath(t),
		MaxInstances:   1,
		Timeout:        5 * time.Second,
		SnapshotMode:   "cow",
		MaxMemoryPages: 256,
	}
	dispatcher := NewDispatcher(cfg, newTestLogger(t))
	require.NoError(t, dispatcher.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, dispatcher.Shutdown(context.Background())) })

	supervisor := <-dispatcher.pool
	defer func() { dispatcher.pool <- supervisor }()
	strategy, ok := supervisor.strategy.(*CowSnapshotStrategy)
	require.True(t, ok)
	require.True(t, strategy.UsingCow())

	before := supervisor.mod.Memory().Size()
	previousPages, grew := supervisor.mod.Memory().Grow(1)
	require.False(t, grew, "COW V1 must reject memory.grow after image attachment")
	require.Zero(t, previousPages)
	require.Equal(t, before, supervisor.mod.Memory().Size())
	require.True(t, supervisor.IsHealthy(), "a rejected grow leaves the fixed mapping valid")
}

func TestCowSupervisorRestoreFailureMarksInstanceUnhealthy(t *testing.T) {
	cfg := Config{
		ModulePath:     echoModulePath(t),
		MaxInstances:   1,
		Timeout:        5 * time.Second,
		SnapshotMode:   "cow",
		MaxMemoryPages: 256,
	}
	dispatcher := NewDispatcher(cfg, newTestLogger(t))
	require.NoError(t, dispatcher.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, dispatcher.Shutdown(context.Background())) })

	supervisor := <-dispatcher.pool
	defer func() { dispatcher.pool <- supervisor }()
	require.NoError(t, dispatcher.cowCoordinator.Close(), "inject canonical-image loss")

	_, err := supervisor.Send(context.Background(), "eval", map[string]any{"restore": "must-fail"})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrCowImageClosed)
	require.False(t, supervisor.IsHealthy(), "restore failure must prevent pool reuse")
}
