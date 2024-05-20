package supervisor

import (
	"testing"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestDefaultAdapterFactory(t *testing.T) {
	worker := worker.NewMockWorker(t)
	cases := []IOInterface{FileIO, StdIO}
	for _, mode := range cases {
		_, err := defaultAdapterFactory[any, any](worker, mode, zap.NewNop())

		assert.NoError(t, err)
	}
}

func TestDefaultAdapterFactory_Fails(t *testing.T) {
	worker := worker.NewMockWorker(t)

	_, err := defaultAdapterFactory[any, any](worker, "", zap.NewNop())

	assert.ErrorIs(t, err, ErrUnsupportedIOMode)
}
