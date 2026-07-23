package qemuguest

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/qemurun"
)

func TestServeFileRoundTripOverProtocol(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- Serve(ctx, guest, ServerConfig{WorkRoot: t.TempDir(), MaxFrameBytes: 1 << 20})
	}()

	codec := qemurun.NewCodec(1 << 20)
	writeJSONFrame(t, codec, host, qemurun.FrameHello, qemurun.HelloMessage{Version: qemurun.ProtocolVersion})
	ready := readFrame(t, codec, host, qemurun.FrameReady)
	var readyMessage qemurun.ReadyMessage
	if err := json.Unmarshal(ready.Payload, &readyMessage); err != nil {
		t.Fatalf("decode ready: %v", err)
	}
	if readyMessage.Version != qemurun.ProtocolVersion || readyMessage.BootID == "" {
		t.Fatalf("invalid ready message: %#v", readyMessage)
	}

	cwd := t.TempDir()
	start := qemurun.StartMessage{
		Mode:    qemurun.ModeFile,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestGuestHelperProcess", "--", "copy", "same arg"},
		Cwd:     cwd,
		Env: append(os.Environ(),
			helperModeEnv+"=1",
			"EXPECTED_ARG=same arg",
			"EXPECTED_CWD="+cwd,
			"CUSTOM_VALUE=through-protocol",
		),
	}
	writeJSONFrame(t, codec, host, qemurun.FrameStart, start)

	request := []byte(`{"command":"preview","params":{"response":"x"}}`)
	if err := codec.WriteFrame(host, qemurun.Frame{Type: qemurun.FrameFileRequest, Payload: request}); err != nil {
		t.Fatalf("write file request: %v", err)
	}
	resultFrame := readFrame(t, codec, host, qemurun.FrameFileResult)
	var result qemurun.FileResultMessage
	if err := json.Unmarshal(resultFrame.Payload, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Error != "" || result.ExitCode != 0 || !bytes.Equal(result.Response, request) {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Stdout != "through-protocol\n" {
		t.Fatalf("stdout = %q", result.Stdout)
	}

	exitFrame := readFrame(t, codec, host, qemurun.FrameExit)
	var exit qemurun.ExitMessage
	if err := json.Unmarshal(exitFrame.Payload, &exit); err != nil {
		t.Fatalf("decode exit: %v", err)
	}
	if exit.Code != 0 {
		t.Fatalf("exit = %#v", exit)
	}
	if err := <-serveErr; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestServeRejectsUnsupportedProtocolVersion(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, guest, ServerConfig{WorkRoot: t.TempDir(), MaxFrameBytes: 1024})
	}()

	codec := qemurun.NewCodec(1024)
	writeJSONFrame(t, codec, host, qemurun.FrameHello, qemurun.HelloMessage{Version: qemurun.ProtocolVersion + 1})
	if err := <-errCh; err == nil {
		t.Fatal("Serve accepted unsupported protocol version")
	}
}

func writeJSONFrame(t *testing.T, codec qemurun.Codec, conn net.Conn, typ qemurun.FrameType, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %v: %v", typ, err)
	}
	if err := codec.WriteFrame(conn, qemurun.Frame{Type: typ, Payload: payload}); err != nil {
		t.Fatalf("write %v: %v", typ, err)
	}
}

func readFrame(t *testing.T, codec qemurun.Codec, conn net.Conn, want qemurun.FrameType) qemurun.Frame {
	t.Helper()
	frame, err := codec.ReadFrame(conn)
	if err != nil {
		t.Fatalf("read %v: %v", want, err)
	}
	if frame.Type != want {
		t.Fatalf("frame type = %v, want %v", frame.Type, want)
	}
	return frame
}

func replaceEnv(env []string, key, value string) []string {
	return append(env, key+"="+value)
}
