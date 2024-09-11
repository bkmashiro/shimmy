package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"runtime"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
)

// RpcConfig describes the configuration for the rpc interface.
type RpcConfig struct {
	// Transport describes the transport mechanism used to communicate
	// with the worker. Only valid for rpc-based workers. Default is "stdio".
	//
	// If "stdio", the supervisor will communicate with the worker over
	// stdio. The worker is expected to read incoming messages from stdin
	// and write responses to stdout.
	//
	// If "ipc", the supervisor will communicate with the worker over
	// unix sockets or windows named pipes, depending on the OS. The
	// worker is expected to listen on a unix socket / pipe and handle
	// incoming messages.
	//
	// If "http", the supervisor will communicate with the worker over
	// http. The worker is expected to listen on a specified port and
	// handle incoming http requests.
	//
	// If "ws", the supervisor will communicate with the worker over
	// websockets. The worker is expected to listen on a specified port
	// and handle incoming websocket messages.
	Transport IOTransport `conf:"transport"`

	// HttpTransport is the configuration for the http transport.
	Http HttpTransportConfig `conf:"http"`

	// IPCTransportConfig is the configuration for the Ipc transport.
	Ipc IpcTransportConfig `conf:"ipc"`

	// WsTransport is the configuration for the websocket transport.
	Ws WsTransportConfig `conf:"ws"`

	// TcpTransport is the configuration for the tcp transport.
	Tcp TcpTransportConfig `config:"tcp"`
}

// HttpTransportConfig describes the configuration for http transport.
type HttpTransportConfig struct {
	// Url is the url to send http requests to.
	Url string `conf:"url"`
}

// TcpTransportConfig describes the configuration for tcp transport.
type TcpTransportConfig struct {
	// Address is the address to send tcp requests to.
	Address string `conf:"address"`
}

// IpcTransportConfig describes the configuration for unix socket transport.
type IpcTransportConfig struct {
	// Endpoint is the full path to the unix socket or
	// the name of the windows named pipe.
	Endpoint string `conf:"endpoint"`
}

// WsTransportConfig describes the configuration for websocket transport.
type WsTransportConfig struct {
	// Url is the url to connect to.
	Url string `conf:"url"`
}

type rpcAdapter struct {
	workerFactory AdapterWorkerFactoryFn

	worker worker.Worker

	// stdioPipe is the stdio pipe used to communicate with the worker.
	// It is only set if the transport is "stdio".
	stdioPipe io.ReadWriteCloser

	// rpcClient is the rpc client used to communicate with the worker.
	rpcClient *rpc.Client

	config RpcConfig
	log    *zap.Logger
}

func newRpcAdapter(
	workerFactory AdapterWorkerFactoryFn,
	config RpcConfig,
	log *zap.Logger,
) *rpcAdapter {
	return &rpcAdapter{
		workerFactory: workerFactory,
		config:        config,
		log:           log.Named("adapter_rpc"),
	}
}

func (a *rpcAdapter) Start(
	ctx context.Context,
	params worker.StartConfig,
) error {
	if a.workerFactory == nil {
		return errors.New("no worker factory provided")
	}

	params.Env = buildEnv(params.Env, a.config)

	// create the worker
	worker, err := a.workerFactory(params)
	if err != nil {
		return fmt.Errorf("error creating worker: %w", err)
	}

	a.worker = worker

	// initialize the stdio pipe if the transport is "stdio"
	if a.config.Transport == StdioTransport {
		stdio, err := a.worker.DuplexPipe()
		if err != nil {
			return fmt.Errorf("error creating duplex pipe: %w", err)
		}

		// wrap the pipe in a header stream
		a.stdioPipe = &headerPrefixPipe{stdio: stdio}

		// TODO: close pipe?
	}

	// for rpc, we can already start the worker, as we do not need to pass
	// any additional, message-specific data to the worker via arguments
	if err := worker.Start(ctx); err != nil {
		return fmt.Errorf("error starting worker: %w", err)
	}

	// dial the rpc client
	return a.dialRpcWithRetry(
		ctx,
		100*time.Millisecond, // initial delay
		10*time.Second,       // max delay
	)
}

func (a *rpcAdapter) Send(
	ctx context.Context,
	method string,
	data map[string]any,
	timeout time.Duration,
) (map[string]any, error) {
	if a.worker == nil {
		return nil, errors.New("no worker provided")
	}

	if a.rpcClient == nil {
		return nil, errors.New("rpc client not available")
	}

	var result map[string]any

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := a.rpcClient.CallContext(ctx, &result, method, data); err != nil {
		return nil, fmt.Errorf("error sending rpc request: %w", err)
	}

	return map[string]any{"result": result, "command": method}, nil
}

func (a *rpcAdapter) Stop() (ReleaseFunc, error) {
	if a.worker == nil {
		return nil, errors.New("no worker provided")
	}

	return stopWorker(a.worker)
}

func (a *rpcAdapter) dialRpcWithRetry(
	ctx context.Context,
	baseDelay time.Duration,
	maxDelay time.Duration,
) error {
	var err error
	for i := 0; ; i++ {
		if client, err := a.dialRpc(ctx, a.config); err == nil {
			a.rpcClient = client
			return nil
		}

		// Calculate the backoff delay with a cap at maxDelay
		backoffDelay := baseDelay * time.Duration(math.Pow(2, float64(i)))
		if backoffDelay > maxDelay {
			backoffDelay = maxDelay
		}

		a.log.With(
			zap.Int("retry", i),
			zap.Duration("backoff", backoffDelay),
			zap.Error(err),
		).Debug("error dialing")

		// Wait for the backoff delay or until the context is done
		select {
		case <-time.After(backoffDelay):
			// Continue to the next retry
		case <-ctx.Done():
			// Context canceled or timeout reached
			return fmt.Errorf("error dialing rpc: %w", err)
		}
	}
}

// dialRpc dials the rpc client based on the given configuration.
func (a *rpcAdapter) dialRpc(
	ctx context.Context,
	config RpcConfig,
) (*rpc.Client, error) {
	if a.worker == nil {
		return nil, errors.New("worker not available")
	}

	log := a.log.With(
		zap.String("transport", string(config.Transport)),
	)

	switch config.Transport {
	case StdioTransport:
		if a.stdioPipe == nil {
			return nil, errors.New("stdio pipe not available")
		}

		log.Debug("dialing")

		return rpc.DialIO(ctx, a.stdioPipe, a.stdioPipe)

	case IpcTransport:
		endpoint := getIPCEndpoint(config.Ipc)

		log.Debug("dialing", zap.String("endpoint", endpoint))

		return rpc.DialIPC(ctx, endpoint)

	case HttpTransport:
		log.Debug("dialing", zap.String("url", config.Http.Url))

		// TODO: use custom client
		return rpc.DialHTTP(config.Http.Url)

	case WsTransport:
		log.Debug("dialing", zap.String("url", config.Ws.Url))

		// TODO: use custom dialer
		// TODO: do we need to set custom origin?
		return rpc.DialWebsocket(ctx, config.Ws.Url, "")

	case TcpTransport:
		log.Debug("dialing", zap.String("address", config.Tcp.Address))

		return dialTCP(ctx, config.Tcp.Address)
	}

	return nil, ErrUnsupportedIOTransport
}

func getIPCEndpoint(config IpcTransportConfig) string {
	if config.Endpoint != "" {
		return config.Endpoint
	}

	if runtime.GOOS == "windows" {
		return `\\.\pipe\eval`
	} else {
		return "/tmp/eval.sock"
	}
}

func buildEnv(env []string, config RpcConfig) []string {
	if env == nil {
		env = make([]string, 0)
	}

	env = append(env,
		"EVAL_IO=rpc",
		"EVAL_RPC_TRANSPORT="+string(config.Transport),
	)

	switch config.Transport {
	case IpcTransport:
		env = append(env, "EVAL_RPC_IPC_ENDPOINT="+getIPCEndpoint(config.Ipc))
	case HttpTransport:
		env = append(env, "EVAL_RPC_HTTP_URL="+config.Http.Url)
	case WsTransport:
		env = append(env, "EVAL_RPC_WS_URL="+config.Ws.Url)
	case TcpTransport:
		env = append(env, "EVAL_RPC_TCP_ADDRESS="+config.Tcp.Address)
	}

	return env
}

func dialTCP(ctx context.Context, address string) (*rpc.Client, error) {
	conn, err := newTCPConnection(ctx, address)
	if err != nil {
		return nil, err
	}

	return rpc.DialIO(ctx, conn, conn)
}

func newTCPConnection(ctx context.Context, endpoint string) (net.Conn, error) {
	return new(net.Dialer).DialContext(ctx, "tcp", endpoint)
}
