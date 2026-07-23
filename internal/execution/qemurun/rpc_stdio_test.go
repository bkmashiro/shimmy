package qemurun

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"
)

func TestRunRPCStdioBridgesRawBytesAndHalfClose(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	defer guest.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	peerErr := make(chan error, 1)
	go func() {
		codec := NewCodec(1 << 20)
		if _, err := readExpectedFrame(codec, guest, FrameHello); err != nil {
			peerErr <- err
			return
		}
		if err := writeJSONFrame(codec, guest, FrameReady, ReadyMessage{Version: ProtocolVersion, BootID: "test-boot"}); err != nil {
			peerErr <- err
			return
		}
		startFrame, err := readExpectedFrame(codec, guest, FrameStart)
		if err != nil {
			peerErr <- err
			return
		}
		var start StartMessage
		if err := json.Unmarshal(startFrame.Payload, &start); err != nil || start.Mode != ModeRPC || start.Transport != "stdio" {
			peerErr <- err
			return
		}
		open, err := readExpectedFrame(codec, guest, FrameOpen)
		if err != nil || open.StreamID != StdioStreamID {
			peerErr <- err
			return
		}
		data, err := readExpectedFrame(codec, guest, FrameData)
		if err != nil || data.StreamID != StdioStreamID || string(data.Payload) != "raw request" {
			peerErr <- err
			return
		}
		if err := codec.WriteFrame(guest, Frame{Type: FrameData, StreamID: StdioStreamID, Payload: []byte("raw response")}); err != nil {
			peerErr <- err
			return
		}
		halfClose, err := readExpectedFrame(codec, guest, FrameHalfClose)
		if err != nil || halfClose.StreamID != StdioStreamID {
			peerErr <- err
			return
		}
		if err := codec.WriteFrame(guest, Frame{Type: FrameHalfClose, StreamID: StdioStreamID}); err != nil {
			peerErr <- err
			return
		}
		if err := writeJSONFrame(codec, guest, FrameExit, ExitMessage{Code: 0}); err != nil {
			peerErr <- err
			return
		}
		peerErr <- nil
	}()

	var stdout bytes.Buffer
	err := RunRPCStdio(ctx, host, StartMessage{
		Mode:      ModeRPC,
		Command:   "/opt/evaluator/rpc",
		Args:      []string{"--serve"},
		Cwd:       "/opt/evaluator",
		Env:       os.Environ(),
		Transport: "stdio",
	}, bytes.NewBufferString("raw request"), &stdout, 1<<20)
	if err != nil {
		t.Fatalf("RunRPCStdio: %v", err)
	}
	if stdout.String() != "raw response" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if err := <-peerErr; err != nil {
		t.Fatalf("peer: %v", err)
	}
}

func TestRunRPCStdioReturnsRemoteExit(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	defer guest.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		codec := NewCodec(1 << 20)
		_, _ = readExpectedFrame(codec, guest, FrameHello)
		_ = writeJSONFrame(codec, guest, FrameReady, ReadyMessage{Version: ProtocolVersion, BootID: "test"})
		_, _ = readExpectedFrame(codec, guest, FrameStart)
		_, _ = readExpectedFrame(codec, guest, FrameOpen)
		_, _ = readExpectedFrame(codec, guest, FrameHalfClose)
		_ = writeJSONFrame(codec, guest, FrameExit, ExitMessage{Code: 29, Error: "intentional"})
	}()

	err := RunRPCStdio(ctx, host, StartMessage{Mode: ModeRPC, Transport: "stdio", Command: "worker"}, bytes.NewReader(nil), &bytes.Buffer{}, 1<<20)
	remote, ok := err.(*RemoteExitError)
	if !ok || remote.Code != 29 {
		t.Fatalf("error = %#v, want RemoteExitError code 29", err)
	}
}
