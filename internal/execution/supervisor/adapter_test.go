package supervisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestDefaultAdapterFactory(t *testing.T) {
	cases := []IOInterface{FileIO, StdIO}
	for _, mode := range cases {
		_, err := defaultAdapterFactory[any, any](mode, zap.NewNop())

		assert.NoError(t, err)
	}
}

func TestDefaultAdapterFactory_Fails(t *testing.T) {
	_, err := defaultAdapterFactory[any, any]("", zap.NewNop())

	assert.ErrorIs(t, err, ErrUnsupportedIOMode)
}
