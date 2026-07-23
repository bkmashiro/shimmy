package qemurun

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"
)

func TestRunRPCNetworkMultiplexesAcceptedRawStream(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	peerErr := make(chan error, 1)
	go func() {
		codec := NewCodec(1 << 20)
		if _, err := readExpectedFrame(codec, guest, FrameHello); err != nil {
			peerErr <- err
			return
		}
		if err := writeJSONFrame(codec, guest, FrameReady, ReadyMessage{Version: ProtocolVersion, BootID: "network-test"}); err != nil {
			peerErr <- err
			return
		}
		startFrame, err := readExpectedFrame(codec, guest, FrameStart)
		if err != nil {
			peerErr <- err
			return
		}
		var start StartMessage
		if err := json.Unmarshal(startFrame.Payload, &start); err != nil || start.Transport != "tcp" {
			peerErr <- err
			return
		}
		open, err := readExpectedFrame(codec, guest, FrameOpen)
		if err != nil || open.StreamID == 0 {
			peerErr <- err
			return
		}
		data, err := readExpectedFrame(codec, guest, FrameData)
		if err != nil || data.StreamID != open.StreamID || string(data.Payload) != "ping" {
			peerErr <- err
			return
		}
		if err := codec.WriteFrame(guest, Frame{Type: FrameData, StreamID: open.StreamID, Payload: []byte("pong")}); err != nil {
			peerErr <- err
			return
		}
		halfClose, err := readExpectedFrame(codec, guest, FrameHalfClose)
		if err != nil || halfClose.StreamID != open.StreamID {
			peerErr <- err
			return
		}
		if err := codec.WriteFrame(guest, Frame{Type: FrameHalfClose, StreamID: open.StreamID}); err != nil {
			peerErr <- err
			return
		}
		if err := codec.WriteFrame(guest, Frame{Type: FrameClose, StreamID: open.StreamID}); err != nil {
			peerErr <- err
			return
		}
		if err := writeJSONFrame(codec, guest, FrameExit, ExitMessage{Code: 0}); err != nil {
			peerErr <- err
			return
		}
		peerErr <- nil
	}()

	bridgeErr := make(chan error, 1)
	go func() {
		bridgeErr <- RunRPCNetwork(ctx, host, StartMessage{
			Mode:          ModeRPC,
			Command:       "/opt/evaluator/rpc",
			Transport:     "tcp",
			Endpoint:      listener.Addr().String(),
			GuestEndpoint: "127.0.0.1:17321",
		}, listener, 1<<20, 8)
	}()

	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if tcp, ok := client.(*net.TCPConn); ok {
		if err := tcp.CloseWrite(); err != nil {
			t.Fatal(err)
		}
	}
	response, err := io.ReadAll(client)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	if !bytes.Equal(response, []byte("pong")) {
		t.Fatalf("response = %q", response)
	}
	if err := <-bridgeErr; err != nil {
		t.Fatalf("RunRPCNetwork: %v", err)
	}
	if err := <-peerErr; err != nil {
		t.Fatalf("peer: %v", err)
	}
}
