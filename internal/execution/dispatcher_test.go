package execution

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

func TestNewDispatcher_RequiresCommandForProcessInterfaces(t *testing.T) {
	tests := []struct {
		name string
		io   supervisor.IOInterface
	}{
		{name: "rpc", io: supervisor.RpcIO},
		{name: "file", io: supervisor.FileIO},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewDispatcher(Params{
				Context: context.Background(),
				Config: Config{
					MaxWorkers: 1,
					Supervisor: supervisor.Config{
						IO: supervisor.IOConfig{Interface: tt.io},
					},
				},
				Log: zap.NewNop(),
			})
			if err == nil {
				t.Fatal("expected missing command error")
			}
			if got, want := err.Error(), "FUNCTION_COMMAND is required"; !strings.Contains(got, want) {
				t.Fatalf("expected error to contain %q, got %q", want, got)
			}
		})
	}
}
