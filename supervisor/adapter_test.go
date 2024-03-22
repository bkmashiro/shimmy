package supervisor

import (
	"context"
	"time"

	"github.com/lambda-feedback/shimmy/worker"
	"github.com/stretchr/testify/mock"
)

type mockWorker struct {
	mock.Mock
}

var _ = worker.Worker[any, any](&mockWorker{})

func (w *mockWorker) Start(ctx context.Context, params worker.StartParams) error {
	args := w.Called(ctx, params)
	return args.Error(0)
}

func (w *mockWorker) Terminate() error {
	args := w.Called()
	return args.Error(0)
}

func (w *mockWorker) Read(ctx context.Context, params worker.ReadParams) (any, error) {
	args := w.Called(ctx, params)
	return args.Get(0), args.Error(1)
}

func (w *mockWorker) Write(ctx context.Context, data any) error {
	args := w.Called(ctx, data)
	return args.Error(0)
}

func (w *mockWorker) Send(ctx context.Context, data any, params worker.SendParams) (any, error) {
	args := w.Called(ctx, data, params)
	return args.Get(0), args.Error(1)
}

func (w *mockWorker) Wait(ctx context.Context) (worker.ExitEvent, error) {
	args := w.Called(ctx)
	return args.Get(0).(worker.ExitEvent), args.Error(1)
}

func (w *mockWorker) WaitFor(ctx context.Context, timeout time.Duration) (worker.ExitEvent, error) {
	args := w.Called(ctx, timeout)
	return args.Get(0).(worker.ExitEvent), args.Error(1)
}
