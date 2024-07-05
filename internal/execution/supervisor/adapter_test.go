package supervisor

import (
	"context"
	"testing"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestDefaultAdapterFactory(t *testing.T) {
	w := worker.NewMockWorker(t)

	workerFactory := func(context.Context, worker.StartConfig) (worker.Worker, error) {
		return w, nil
	}

	cases := []IOConfig{{Interface: FileIO}, {Interface: RpcIO}}
	for _, mode := range cases {
		_, err := defaultAdapterFactory(workerFactory, mode, zap.NewNop())

		assert.NoError(t, err)
	}
}

func TestDefaultAdapterFactory_Fails(t *testing.T) {
	w := worker.NewMockWorker(t)

	workerFactory := func(context.Context, worker.StartConfig) (worker.Worker, error) {
		return w, nil
	}

	_, err := defaultAdapterFactory(workerFactory, IOConfig{Interface: ""}, zap.NewNop())

	assert.ErrorIs(t, err, ErrUnsupportedIOInterface)
}
