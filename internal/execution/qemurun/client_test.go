package qemurun_test

import (
	"bytes"
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/qemuguest"
	"github.com/lambda-feedback/shimmy/internal/execution/qemurun"
)

func TestRunFileExchangesOneEvaluatorInvocation(t *testing.T) {
	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	guestErr := make(chan error, 1)
	go func() {
		guestErr <- qemuguest.Serve(ctx, guest, qemuguest.ServerConfig{
			WorkRoot:      t.TempDir(),
			MaxFrameBytes: 1 << 20,
		})
	}()

	request := []byte(`{"command":"eval","params":{"response":"x","answer":"x"}}`)
	result, ready, err := qemurun.RunFile(ctx, host, qemurun.StartMessage{
		Mode:    qemurun.ModeFile,
		Command: "/bin/sh",
		Args:    []string{"-c", `cp "$1" "$2"; printf host-guest`, "shimmy-evaluator"},
		Cwd:     t.TempDir(),
		Env:     os.Environ(),
	}, request, 1<<20)
	if err != nil {
		t.Fatalf("RunFile: %v", err)
	}
	if ready.BootID == "" || ready.Version != qemurun.ProtocolVersion {
		t.Fatalf("invalid ready: %#v", ready)
	}
	if !bytes.Equal(result.Response, request) || result.ExitCode != 0 || result.Error != "" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Stdout != "host-guest" {
		t.Fatalf("stdout = %q", result.Stdout)
	}
	if err := <-guestErr; err != nil {
		t.Fatalf("guest Serve: %v", err)
	}
}

func TestRunFileReturnsEvaluatorFailureWithoutProtocolLoss(t *testing.T) {
	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	guestErr := make(chan error, 1)
	go func() {
		guestErr <- qemuguest.Serve(ctx, guest, qemuguest.ServerConfig{WorkRoot: t.TempDir(), MaxFrameBytes: 1 << 20})
	}()

	result, _, err := qemurun.RunFile(ctx, host, qemurun.StartMessage{
		Mode:    qemurun.ModeFile,
		Command: "/bin/sh",
		Args:    []string{"-c", `echo intentional >&2; exit 19`, "shimmy-evaluator"},
		Env:     os.Environ(),
	}, []byte(`{}`), 1<<20)
	if err == nil {
		t.Fatal("RunFile returned nil error for evaluator exit 19")
	}
	if result.ExitCode != 19 || result.Error == "" || result.Stderr != "intentional\n" {
		t.Fatalf("failure metadata lost: %#v, err=%v", result, err)
	}
	if err := <-guestErr; err != nil {
		t.Fatalf("guest Serve: %v", err)
	}
}
