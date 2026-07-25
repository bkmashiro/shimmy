package supervisor

import (
	"bytes"
	"context"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"github.com/lambda-feedback/shimmy/util"
)

type rwc struct {
	*bytes.Buffer
}

func (rwc *rwc) Close() error {
	return nil
}

func newRwc() io.ReadWriteCloser {
	return &rwc{Buffer: new(bytes.Buffer)}
}

func createRpcAdapter(t *testing.T) (*rpcAdapter, *worker.MockWorker) {
	w := worker.NewMockWorker(t)

	workerFactory := func(worker.StartConfig) (worker.Worker, error) {
		return w, nil
	}

	adapter := &rpcAdapter{
		workerFactory: workerFactory,
		log:           zap.NewNop(),
		config:        RpcConfig{Transport: StdioTransport},
	}

	return adapter, w
}

func TestStdioAdapter_Start(t *testing.T) {
	a, w := createRpcAdapter(t)

	ctx := context.Background()
	params := worker.StartConfig{}

	w.EXPECT().DuplexPipe().Return(newRwc(), nil)
	w.EXPECT().Start(ctx).Return(nil)

	err := a.Start(ctx, params)
	assert.NoError(t, err)
}

func TestStdioAdapter_Start_PassesError(t *testing.T) {
	a, w := createRpcAdapter(t)

	ctx := context.Background()
	params := worker.StartConfig{}

	w.EXPECT().DuplexPipe().Return(newRwc(), nil)
	w.EXPECT().Start(ctx).Return(assert.AnError)

	err := a.Start(ctx, params)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestRpcAdapter_Start_RollsBackWorkerWhenDialFails(t *testing.T) {
	a, w := createRpcAdapter(t)
	a.config = RpcConfig{
		Transport: TcpTransport,
		Tcp:       TcpTransportConfig{Address: "not-a-valid-address"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	w.EXPECT().Start(ctx).Return(nil)
	w.EXPECT().Stop().Return(nil)
	w.EXPECT().WaitFor(mock.Anything, mock.Anything).Return(worker.ExitEvent{}, nil)

	err := a.Start(ctx, worker.StartConfig{})
	assert.ErrorContains(t, err, "error dialing rpc")
	assert.NotContains(t, err.Error(), "%!w(<nil>)")
	assert.Nil(t, a.worker)
	assert.Nil(t, a.rpcClient)
	assert.Nil(t, a.stdioPipe)
}

func TestRpcAdapter_Start_ReapsRealWorkerWhenDialFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep process fixture is Unix-only")
	}

	var process *worker.ProcessWorker
	factory := func(params worker.StartConfig) (worker.Worker, error) {
		process = worker.NewProcessWorker(context.Background(), params, zap.NewNop())
		return process, nil
	}
	a := newRpcAdapter(factory, RpcConfig{
		Transport: TcpTransport,
		Tcp:       TcpTransportConfig{Address: "not-a-valid-address"},
	}, zap.NewNop())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := a.Start(ctx, worker.StartConfig{Cmd: "sleep", Args: []string{"60"}})
	assert.ErrorContains(t, err, "error dialing rpc")
	require.NotNil(t, process)
	assert.False(t, util.IsProcessAlive(process.Pid()))
}

func TestStdioAdapter_Stop(t *testing.T) {
	a, w := createRpcAdapter(t)

	w.EXPECT().DuplexPipe().Return(newRwc(), nil)
	w.EXPECT().Start(mock.Anything).Return(nil)
	w.EXPECT().Stop().Return(nil)

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.NoError(t, err)

	_, err = a.Stop()
	assert.NoError(t, err)
}

func TestStdioAdapter_Stop_FailsIfNotStarted(t *testing.T) {
	a, _ := createRpcAdapter(t)

	_, err := a.Stop()
	assert.Error(t, err)
}

func TestStdioAdapter_Stop_PassesError(t *testing.T) {
	a, w := createRpcAdapter(t)

	w.EXPECT().DuplexPipe().Return(newRwc(), nil)
	w.EXPECT().Start(mock.Anything).Return(nil)
	w.EXPECT().Stop().Return(assert.AnError)

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.NoError(t, err)

	_, err = a.Stop()
	assert.ErrorIs(t, err, assert.AnError)
}

func TestStdioAdapter_Stop_WaitFor(t *testing.T) {
	a, w := createRpcAdapter(t)

	ctx := context.Background()

	w.EXPECT().DuplexPipe().Return(newRwc(), nil)
	w.EXPECT().Start(mock.Anything).Return(nil)
	w.EXPECT().Stop().Return(nil)
	w.EXPECT().Wait(ctx).Return(worker.ExitEvent{}, nil)

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.NoError(t, err)

	wait, err := a.Stop()
	assert.NoError(t, err)

	err = wait(ctx)
	assert.NoError(t, err)
}

func TestStdioAdapter_Stop_WaitForError(t *testing.T) {
	a, w := createRpcAdapter(t)

	ctx := context.Background()

	w.EXPECT().DuplexPipe().Return(newRwc(), nil)
	w.EXPECT().Start(mock.Anything).Return(nil)
	w.EXPECT().Stop().Return(nil)
	w.EXPECT().Wait(ctx).Return(worker.ExitEvent{}, assert.AnError)

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.NoError(t, err)

	wait, err := a.Stop()
	assert.NoError(t, err)

	err = wait(ctx)
	assert.ErrorIs(t, err, assert.AnError)
}

// func TestStdioAdapter_Send(t *testing.T) {
// 	a, w := createStdioAdapter(t)

// 	ctx := context.Background()
// 	data := "test"

// 	w.EXPECT().Send(ctx, data, 0).Return("result", nil)

// 	res, err := a.Send(ctx, data, 0)
// 	assert.NoError(t, err)
// 	assert.Equal(t, "result", res)
// }

// func TestStdioAdapter_Send_PassesError(t *testing.T) {
// 	a, w := createStdioAdapter(t)

// 	ctx := context.Background()
// 	data := "test"

// 	w.EXPECT().Send(ctx, data, 0).Return("result", assert.AnError)

// 	_, err := a.Send(ctx, data, 0)
// 	assert.ErrorIs(t, err, assert.AnError)
// }
