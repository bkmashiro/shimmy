package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/qemuguest"
	"github.com/lambda-feedback/shimmy/internal/execution/qemurun"
)

type fakeVMSession struct {
	connection net.Conn
}

func (v *fakeVMSession) Connection() net.Conn { return v.connection }
func (v *fakeVMSession) Close(context.Context) error {
	return v.connection.Close()
}

func TestRunRunnerFilePathExchangesAndWritesHostResponse(t *testing.T) {
	root := t.TempDir()
	requestPath := filepath.Join(root, "request.json")
	responsePath := filepath.Join(root, "response.json")
	request := []byte(`{"command":"eval","params":{"response":"same"}}`)
	if err := os.WriteFile(requestPath, request, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(responsePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	guestErr := make(chan error, 1)
	deps := runnerDependencies{
		loadRuntime: func() (qemurun.RuntimeConfig, error) {
			return qemurun.RuntimeConfig{MaxFrameBytes: 1 << 20, ShutdownTimeout: time.Second}, nil
		},
		startVM: func(context.Context, qemurun.RuntimeConfig) (vmSession, error) {
			host, guest := net.Pipe()
			go func() {
				guestErr <- qemuguest.Serve(ctx, guest, qemuguest.ServerConfig{WorkRoot: t.TempDir(), MaxFrameBytes: 1 << 20})
			}()
			return &fakeVMSession{connection: host}, nil
		},
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"EVAL_IO=FILE",
		"EVAL_FILE_NAME_REQUEST=" + requestPath,
		"EVAL_FILE_NAME_RESPONSE=" + responsePath,
		"FUNCTION_QEMU_ENABLED=true",
	}
	args := []string{
		"--", "/bin/sh", "-c", `cp "$1" "$2"`, "shimmy-evaluator",
		requestPath, responsePath,
	}

	if err := runRunner(ctx, args, env, deps); err != nil {
		t.Fatalf("runRunner: %v", err)
	}
	got, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, request) {
		t.Fatalf("response = %q, want %q", got, request)
	}
	if err := <-guestErr; err != nil {
		t.Fatalf("guest: %v", err)
	}
}

func TestRunRunnerRPCStdioPreservesOuterPipes(t *testing.T) {
	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	guestErr := make(chan error, 1)
	go func() {
		guestErr <- qemuguest.Serve(ctx, guest, qemuguest.ServerConfig{WorkRoot: t.TempDir(), MaxFrameBytes: 1 << 20})
	}()
	var output bytes.Buffer
	err := runRunner(ctx, []string{"--", "/bin/cat"}, []string{
		"PATH=/usr/bin:/bin",
		"EVAL_IO=rpc",
		"EVAL_RPC_TRANSPORT=stdio",
		"FUNCTION_QEMU_ENABLED=true",
	}, runnerDependencies{
		loadRuntime: func() (qemurun.RuntimeConfig, error) {
			return qemurun.RuntimeConfig{MaxFrameBytes: 1 << 20, ShutdownTimeout: time.Second}, nil
		},
		startVM: func(context.Context, qemurun.RuntimeConfig) (vmSession, error) {
			return &fakeVMSession{connection: host}, nil
		},
		stdin:  bytes.NewBufferString("rpc request\n"),
		stdout: &output,
	})
	if err != nil {
		t.Fatalf("runRunner: %v", err)
	}
	if output.String() != "rpc request\n" {
		t.Fatalf("stdout = %q", output.String())
	}
	if err := <-guestErr; err != nil {
		t.Fatalf("guest: %v", err)
	}
}

func TestRunRunnerRejectsFileResponseSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	root := t.TempDir()
	requestPath := filepath.Join(root, "request.json")
	targetPath := filepath.Join(root, "target.json")
	responsePath := filepath.Join(root, "response.json")
	if err := os.WriteFile(requestPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("do not overwrite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetPath, responsePath); err != nil {
		t.Fatal(err)
	}

	err := writeHostResponse(responsePath, []byte(`{"result":1}`))
	if err == nil {
		t.Fatal("writeHostResponse followed a symlink")
	}
	got, readErr := os.ReadFile(targetPath)
	if readErr != nil || string(got) != "do not overwrite" {
		t.Fatalf("target changed: %q, err=%v", got, readErr)
	}
}
