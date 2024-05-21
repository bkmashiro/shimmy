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

	cases := []IOInterface{FileIO, StdIO}
	for _, mode := range cases {
		_, err := defaultAdapterFactory[any, any](workerFactory, mode, zap.NewNop())

		assert.NoError(t, err)
	}
}

func TestDefaultAdapterFactory_Fails(t *testing.T) {
	w := worker.NewMockWorker(t)

	workerFactory := func(context.Context, worker.StartConfig) (worker.Worker, error) {
		return w, nil
	}

	_, err := defaultAdapterFactory[any, any](workerFactory, "", zap.NewNop())

	assert.ErrorIs(t, err, ErrUnsupportedIOMode)
}
