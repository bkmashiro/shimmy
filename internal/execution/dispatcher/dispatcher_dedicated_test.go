package dispatcher

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

func TestDedicatedDispatcherSendReleasesWorkerAfterSendFailure(t *testing.T) {
	ctx := context.Background()
	released := false
	s := supervisor.NewMockSupervisor(t)
	s.EXPECT().Send(mock.Anything, "evaluate", map[string]any{"input": "bad"}).Return(
		&supervisor.Result{
			Release: func(context.Context) error {
				released = true
				return nil
			},
		},
		assert.AnError,
	)

	d, err := NewDedicatedDispatcher(DedicatedDispatcherParams{
		Context: ctx,
		Log:     zap.NewNop(),
		SupervisorFactory: func(supervisor.Params) (supervisor.Supervisor, error) {
			return s, nil
		},
	})
	require.NoError(t, err)

	_, err = d.Send(ctx, "evaluate", map[string]any{"input": "bad"})
	require.ErrorIs(t, err, assert.AnError)
	assert.True(t, released)
}
