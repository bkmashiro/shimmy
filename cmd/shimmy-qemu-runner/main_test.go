package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lambda-feedback/shimmy/internal/execution/qemuguest"
	"github.com/lambda-feedback/shimmy/internal/execution/qemurun"
)

type pipeVMSession struct {
	conn net.Conn
}

func (s *pipeVMSession) Connection() net.Conn { return s.conn }
func (s *pipeVMSession) Close(context.Context) error {
	return s.conn.Close()
}

func TestRunRunnerBridgesFileInvocationEndToEnd(t *testing.T) {
	root := t.TempDir()
	evaluator := filepath.Join(root, "evaluator.sh")
	require.NoError(t, os.WriteFile(evaluator, []byte(`#!/bin/sh
set -eu
printf '{"wrapped":' > "$2"
cat "$1" >> "$2"
printf '}' >> "$2"
printf 'guest-stdout'
`), 0o700))
	request := filepath.Join(root, "request.json")
	response := filepath.Join(root, "response.json")
	require.NoError(t, os.WriteFile(request, []byte(`{"value":7}`), 0o600))
	require.NoError(t, os.WriteFile(response, nil, 0o600))

	serverErr := make(chan error, 1)
	var guestConn net.Conn
	deps := runnerDependencies{
		loadRuntime: func() (qemurun.RuntimeConfig, error) {
			return qemurun.RuntimeConfig{
				MaxFrameBytes:   1 << 20,
				BootTimeout:     time.Second,
				ShutdownTimeout: time.Second,
			}, nil
		},
		startVM: func(context.Context, qemurun.RuntimeConfig) (vmSession, error) {
			host, guest := net.Pipe()
			guestConn = guest
			go func() {
				serverErr <- qemuguest.Serve(context.Background(), guest, qemuguest.ServerConfig{
					WorkRoot:      filepath.Join(root, "guest-work"),
					MaxFrameBytes: 1 << 20,
				})
			}()
			return &pipeVMSession{conn: host}, nil
		},
		stdout: new(testBuffer),
	}
	env := append(os.Environ(),
		"EVAL_IO=FILE",
		"EVAL_FILE_NAME_REQUEST="+request,
		"EVAL_FILE_NAME_RESPONSE="+response,
		"FUNCTION_QEMU_BINARY=must-not-enter-guest",
	)
	err := runRunner(context.Background(), []string{"--", evaluator, request, response}, env, deps)
	require.NoError(t, err)
	assert.JSONEq(t, `{"wrapped":{"value":7}}`, string(mustReadFile(t, response)))
	assert.Equal(t, "guest-stdout", deps.stdout.(*testBuffer).String())
	require.NoError(t, <-serverErr)
	if guestConn != nil {
		_ = guestConn.Close()
	}
}

func TestRunRunnerBridgesRPCStdioEndToEnd(t *testing.T) {
	serverErr := make(chan error, 1)
	var guestConn net.Conn
	input := []byte("qemu-rpc-stdio\n")
	var output bytes.Buffer
	deps := runnerDependencies{
		loadRuntime: func() (qemurun.RuntimeConfig, error) {
			return qemurun.RuntimeConfig{
				MaxFrameBytes:   1 << 20,
				BootTimeout:     time.Second,
				ShutdownTimeout: time.Second,
			}, nil
		},
		startVM: func(context.Context, qemurun.RuntimeConfig) (vmSession, error) {
			host, guest := net.Pipe()
			guestConn = guest
			go func() {
				serverErr <- qemuguest.Serve(context.Background(), guest, qemuguest.ServerConfig{
					WorkRoot:      t.TempDir(),
					MaxFrameBytes: 1 << 20,
				})
			}()
			return &pipeVMSession{conn: host}, nil
		},
		stdin:  bytes.NewReader(input),
		stdout: &output,
	}
	env := append(os.Environ(),
		"EVAL_IO=rpc",
		"EVAL_RPC_TRANSPORT=stdio",
		"FUNCTION_QEMU_BINARY=must-not-enter-guest",
	)
	err := runRunner(context.Background(), []string{"--", "/bin/cat"}, env, deps)
	require.NoError(t, err)
	assert.Equal(t, input, output.Bytes())
	require.NoError(t, <-serverErr)
	if guestConn != nil {
		_ = guestConn.Close()
	}
}

type testBuffer struct{ data []byte }

func (b *testBuffer) Write(data []byte) (int, error) {
	b.data = append(b.data, data...)
	return len(data), nil
}
func (b *testBuffer) String() string { return string(b.data) }

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}
