package qemuguest

import (
	"context"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/qemurun"
)

func TestServeRPCNetworkBridgesRealTCPProcess(t *testing.T) {
	hostListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	guestEndpoint := probe.Addr().String()
	_ = probe.Close()

	host, guest := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	guestErr := make(chan error, 1)
	go func() {
		guestErr <- Serve(ctx, guest, ServerConfig{WorkRoot: t.TempDir(), MaxFrameBytes: 1 << 20})
	}()
	bridgeErr := make(chan error, 1)
	go func() {
		bridgeErr <- qemurun.RunRPCNetwork(ctx, host, qemurun.StartMessage{
			Mode:          qemurun.ModeRPC,
			Command:       os.Args[0],
			Args:          []string{"-test.run=TestRPCNetworkHelperProcess"},
			Cwd:           t.TempDir(),
			Env:           append(os.Environ(), "SHIMMY_QEMU_RPC_HELPER=1", "EVAL_IO=rpc", "EVAL_RPC_TRANSPORT=tcp", "EVAL_RPC_TCP_ADDRESS=wrong:1"),
			Transport:     "tcp",
			Endpoint:      hostListener.Addr().String(),
			GuestEndpoint: guestEndpoint,
		}, hostListener, 1<<20, 8)
	}()

	client, err := net.Dial("tcp", hostListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("network ping")); err != nil {
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
	if string(response) != "network ping" {
		t.Fatalf("response = %q", response)
	}
	if err := <-bridgeErr; err != nil {
		t.Fatalf("RunRPCNetwork: %v", err)
	}
	if err := <-guestErr; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestRPCNetworkHelperProcess(t *testing.T) {
	if os.Getenv("SHIMMY_QEMU_RPC_HELPER") != "1" {
		return
	}
	listener, err := net.Listen("tcp", os.Getenv("EVAL_RPC_TCP_ADDRESS"))
	if err != nil {
		os.Exit(81)
	}
	connection, err := listener.Accept()
	if err != nil {
		os.Exit(82)
	}
	data, err := io.ReadAll(connection)
	if err != nil {
		os.Exit(83)
	}
	if _, err := connection.Write(data); err != nil {
		os.Exit(84)
	}
	_ = connection.Close()
	_ = listener.Close()
	os.Exit(0)
}
