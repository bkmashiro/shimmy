package qemuguest

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/qemurun"
)

func TestServeRPCStdioBridgesRealEvaluatorProcess(t *testing.T) {
	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	guestErr := make(chan error, 1)
	go func() {
		guestErr <- Serve(ctx, guest, ServerConfig{WorkRoot: t.TempDir(), MaxFrameBytes: 1 << 20})
	}()

	var output bytes.Buffer
	err := qemurun.RunRPCStdio(ctx, host, qemurun.StartMessage{
		Mode:      qemurun.ModeRPC,
		Command:   "/bin/cat",
		Cwd:       t.TempDir(),
		Env:       os.Environ(),
		Transport: "stdio",
	}, bytes.NewBufferString("first\nsecond\n"), &output, 1<<20)
	if err != nil {
		t.Fatalf("RunRPCStdio: %v", err)
	}
	if output.String() != "first\nsecond\n" {
		t.Fatalf("output = %q", output.String())
	}
	if err := <-guestErr; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestServeRPCStdioReportsEvaluatorExit(t *testing.T) {
	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	guestErr := make(chan error, 1)
	go func() {
		guestErr <- Serve(ctx, guest, ServerConfig{WorkRoot: t.TempDir(), MaxFrameBytes: 1 << 20})
	}()

	err := qemurun.RunRPCStdio(ctx, host, qemurun.StartMessage{
		Mode:      qemurun.ModeRPC,
		Command:   "/bin/sh",
		Args:      []string{"-c", "printf intentional >&2; exit 31"},
		Cwd:       t.TempDir(),
		Env:       os.Environ(),
		Transport: "stdio",
	}, bytes.NewReader(nil), &bytes.Buffer{}, 1<<20)
	remote, ok := err.(*qemurun.RemoteExitError)
	if !ok || remote.Code != 31 || remote.Detail == "" {
		t.Fatalf("error = %#v, want remote exit 31 with detail", err)
	}
	if err := <-guestErr; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestCopyRPCOutputTreatsClosedProcessPipeAsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wire bytes.Buffer
	codec := qemurun.NewCodec(1024)
	writer := qemurun.NewSerializedFrameWriter(ctx, &wire, codec, 1)
	if err := copyRPCOutput(ctx, writer, closedProcessPipe{}); err != nil {
		t.Fatalf("copyRPCOutput: %v", err)
	}
	frame, err := codec.ReadFrame(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Type != qemurun.FrameHalfClose {
		t.Fatalf("frame type = %s", frame.Type)
	}
}

type closedProcessPipe struct{}

func (closedProcessPipe) Read([]byte) (int, error) { return 0, os.ErrClosed }

var _ io.Reader = closedProcessPipe{}
