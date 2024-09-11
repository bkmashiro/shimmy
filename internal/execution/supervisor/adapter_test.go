package supervisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
)

func TestDefaultAdapterFactory(t *testing.T) {
	w := worker.NewMockWorker(t)

	workerFactory := func(worker.StartConfig) (worker.Worker, error) {
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

	workerFactory := func(worker.StartConfig) (worker.Worker, error) {
		return w, nil
	}

	_, err := defaultAdapterFactory(workerFactory, IOConfig{Interface: ""}, zap.NewNop())

	assert.ErrorIs(t, err, ErrUnsupportedIOInterface)
}
