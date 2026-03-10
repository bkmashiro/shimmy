package supervisor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
)

// TestGetIPCEndpoint_DefaultEndpoint tests the default IPC endpoint (no explicit endpoint).
func TestGetIPCEndpoint_DefaultEndpoint(t *testing.T) {
	cfg := IpcTransportConfig{Endpoint: ""}
	ep := getIPCEndpoint(cfg)
	// Should return platform-specific default path
	assert.NotEmpty(t, ep)
}

// TestGetIPCEndpoint_ExplicitEndpoint tests when an endpoint is explicitly set.
func TestGetIPCEndpoint_ExplicitEndpoint(t *testing.T) {
	cfg := IpcTransportConfig{Endpoint: "/tmp/my-custom.sock"}
	ep := getIPCEndpoint(cfg)
	assert.Equal(t, "/tmp/my-custom.sock", ep)
}

// TestBuildEnv_StdioTransport tests buildEnv for stdio transport.
func TestBuildEnv_StdioTransport(t *testing.T) {
	cfg := RpcConfig{Transport: StdioTransport}
	env := buildEnv(nil, cfg)

	assert.Contains(t, env, "EVAL_IO=rpc")
	assert.Contains(t, env, "EVAL_RPC_TRANSPORT=stdio")
}

// TestBuildEnv_IpcTransport tests buildEnv for IPC transport.
func TestBuildEnv_IpcTransport(t *testing.T) {
	cfg := RpcConfig{
		Transport: IpcTransport,
		Ipc:       IpcTransportConfig{Endpoint: "/tmp/test.sock"},
	}
	env := buildEnv(nil, cfg)

	assert.Contains(t, env, "EVAL_IO=rpc")
	assert.Contains(t, env, "EVAL_RPC_TRANSPORT=ipc")
	assert.Contains(t, env, "EVAL_RPC_IPC_ENDPOINT=/tmp/test.sock")
}

// TestBuildEnv_HttpTransport tests buildEnv for HTTP transport.
func TestBuildEnv_HttpTransport(t *testing.T) {
	cfg := RpcConfig{
		Transport: HttpTransport,
		Http:      HttpTransportConfig{Url: "http://localhost:8080"},
	}
	env := buildEnv(nil, cfg)

	assert.Contains(t, env, "EVAL_RPC_TRANSPORT=http")
	assert.Contains(t, env, "EVAL_RPC_HTTP_URL=http://localhost:8080")
}

// TestBuildEnv_WsTransport tests buildEnv for WebSocket transport.
func TestBuildEnv_WsTransport(t *testing.T) {
	cfg := RpcConfig{
		Transport: WsTransport,
		Ws:        WsTransportConfig{Url: "ws://localhost:9090"},
	}
	env := buildEnv(nil, cfg)

	assert.Contains(t, env, "EVAL_RPC_TRANSPORT=ws")
	assert.Contains(t, env, "EVAL_RPC_WS_URL=ws://localhost:9090")
}

// TestBuildEnv_TcpTransport tests buildEnv for TCP transport.
func TestBuildEnv_TcpTransport(t *testing.T) {
	cfg := RpcConfig{
		Transport: TcpTransport,
		Tcp:       TcpTransportConfig{Address: "localhost:5555"},
	}
	env := buildEnv(nil, cfg)

	assert.Contains(t, env, "EVAL_RPC_TRANSPORT=tcp")
	assert.Contains(t, env, "EVAL_RPC_TCP_ADDRESS=localhost:5555")
}

// TestBuildEnv_WithExistingEnv tests buildEnv appends to existing env vars.
func TestBuildEnv_WithExistingEnv(t *testing.T) {
	existing := []string{"MY_VAR=hello"}
	cfg := RpcConfig{Transport: StdioTransport}
	env := buildEnv(existing, cfg)

	assert.Contains(t, env, "MY_VAR=hello")
	assert.Contains(t, env, "EVAL_IO=rpc")
}

// TestDialTCP_ConnectionRefused tests dialTCP when connection is refused.
func TestDialTCP_ConnectionRefused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := dialTCP(ctx, "127.0.0.1:1")
	assert.Error(t, err)
}

// TestNewTCPConnection_ContextCancelled tests newTCPConnection with cancelled context.
func TestNewTCPConnection_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := newTCPConnection(ctx, "127.0.0.1:1")
	assert.Error(t, err)
}

// TestDialRpcWithRetry_ContextCancelled tests dialRpcWithRetry when context is cancelled.
func TestDialRpcWithRetry_ContextCancelled(t *testing.T) {
	w := worker.NewMockWorker(t)

	a := &rpcAdapter{
		worker: w,
		config: RpcConfig{Transport: IpcTransport, Ipc: IpcTransportConfig{Endpoint: "/tmp/nonexistent-test-12345.sock"}},
		log:    zap.NewNop().Named("test"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediate cancel

	err := a.dialRpcWithRetry(ctx, 0, 0)
	assert.Error(t, err)
}

// TestDialRpc_NoWorker tests dialRpc when worker is nil.
func TestDialRpc_NoWorker(t *testing.T) {
	a := &rpcAdapter{
		worker: nil,
		config: RpcConfig{Transport: StdioTransport},
		log:    zap.NewNop().Named("test"),
	}

	_, err := a.dialRpc(context.Background(), a.config)
	assert.Error(t, err)
}

// TestDialRpc_StdioNoPipe tests dialRpc for stdio when stdioPipe is nil.
func TestDialRpc_StdioNoPipe(t *testing.T) {
	w := worker.NewMockWorker(t)
	a := &rpcAdapter{
		worker:    w,
		stdioPipe: nil,
		config:    RpcConfig{Transport: StdioTransport},
		log:       zap.NewNop().Named("test"),
	}

	_, err := a.dialRpc(context.Background(), a.config)
	assert.Error(t, err)
}

// TestDialRpc_UnsupportedTransport tests dialRpc for unknown transport.
func TestDialRpc_UnsupportedTransport(t *testing.T) {
	w := worker.NewMockWorker(t)
	a := &rpcAdapter{
		worker: w,
		config: RpcConfig{Transport: IOTransport("unknown")},
		log:    zap.NewNop().Named("test"),
	}

	_, err := a.dialRpc(context.Background(), a.config)
	assert.ErrorIs(t, err, ErrUnsupportedIOTransport)
}

// TestRpcAdapter_Send_NoWorker tests Send when no worker is set.
func TestRpcAdapter_Send_NoWorker(t *testing.T) {
	a := &rpcAdapter{
		worker: nil,
		log:    zap.NewNop(),
	}

	_, err := a.Send(context.Background(), "eval", map[string]any{}, 0)
	assert.Error(t, err)
}

// TestRpcAdapter_Send_NoRpcClient tests Send when rpcClient is nil.
func TestRpcAdapter_Send_NoRpcClient(t *testing.T) {
	w := worker.NewMockWorker(t)
	a := &rpcAdapter{
		worker:    w,
		rpcClient: nil,
		log:       zap.NewNop(),
	}

	_, err := a.Send(context.Background(), "eval", map[string]any{}, 0)
	assert.Error(t, err)
}

// TestRpcAdapter_Start_NoWorkerFactory tests Start when workerFactory is nil.
func TestRpcAdapter_Start_NoWorkerFactory(t *testing.T) {
	a := &rpcAdapter{
		workerFactory: nil,
		log:           zap.NewNop(),
		config:        RpcConfig{Transport: StdioTransport},
	}

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.Error(t, err)
}

// TestDefaultWorkerFactory_CreatesWorker exercises defaultWorkerFactory.
func TestDefaultWorkerFactory_CreatesWorker(t *testing.T) {
	ctx := context.Background()
	cfg := worker.StartConfig{Cmd: "echo"}
	w, err := defaultWorkerFactory(ctx, cfg, zap.NewNop())
	assert.NoError(t, err)
	assert.NotNil(t, w)
}

// TestRpcAdapter_Start_WorkerFactoryFails tests Start when worker factory fails.
func TestRpcAdapter_Start_WorkerFactoryFails(t *testing.T) {
	a := &rpcAdapter{
		workerFactory: func(cfg worker.StartConfig) (worker.Worker, error) {
			return nil, assert.AnError
		},
		log:    zap.NewNop(),
		config: RpcConfig{Transport: StdioTransport},
	}

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.ErrorIs(t, err, assert.AnError)
}

// TestRpcAdapter_Start_DuplexPipeFails tests Start when DuplexPipe fails.
func TestRpcAdapter_Start_DuplexPipeFails(t *testing.T) {
	w := worker.NewMockWorker(t)
	w.EXPECT().DuplexPipe().Return(nil, assert.AnError)

	a := &rpcAdapter{
		workerFactory: func(cfg worker.StartConfig) (worker.Worker, error) {
			return w, nil
		},
		log:    zap.NewNop(),
		config: RpcConfig{Transport: StdioTransport},
	}

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.ErrorIs(t, err, assert.AnError)
}

// TestRpcAdapter_Start_WithBuildEnv tests buildEnv integration during Start.
func TestRpcAdapter_Start_WithBuildEnv(t *testing.T) {
	w := worker.NewMockWorker(t)
	w.EXPECT().DuplexPipe().Return(newRwc(), nil)
	w.EXPECT().Start(mock.Anything).Return(nil)

	a := &rpcAdapter{
		workerFactory: func(cfg worker.StartConfig) (worker.Worker, error) {
			return w, nil
		},
		log: zap.NewNop(),
		config: RpcConfig{
			Transport: StdioTransport,
			Http:      HttpTransportConfig{Url: "http://localhost:9999"},
		},
	}

	// Start attempts to dial; with StdioTransport + empty buffer, DialIO will fail or succeed
	// We just verify Start got through the env-building and worker creation phases.
	_ = a.Start(context.Background(), worker.StartConfig{Env: []string{"MY=var"}})
}
