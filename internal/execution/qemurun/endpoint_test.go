package qemurun

import "testing"

func TestPrepareGuestEndpointPreservesProtocolShape(t *testing.T) {
	tests := []struct {
		transport string
		endpoint  string
		workRoot  string
		want      string
	}{
		{transport: "ipc", endpoint: "/tmp/host-eval.sock", workRoot: "/run/shimmy", want: "/run/shimmy/evaluator.sock"},
		{transport: "tcp", endpoint: "0.0.0.0:7321", want: "127.0.0.1:7321"},
		{transport: "http", endpoint: "http://0.0.0.0:7321/rpc?x=1", want: "http://127.0.0.1:7321/rpc?x=1"},
		{transport: "ws", endpoint: "ws://localhost:7321/ws", want: "ws://127.0.0.1:7321/ws"},
	}
	for _, test := range tests {
		t.Run(test.transport, func(t *testing.T) {
			got, err := PrepareGuestEndpoint(test.transport, test.endpoint, test.workRoot)
			if err != nil {
				t.Fatalf("PrepareGuestEndpoint: %v", err)
			}
			if got != test.want {
				t.Fatalf("endpoint = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNetworkListenAddressParsesTransportEndpoint(t *testing.T) {
	tests := []struct {
		transport string
		endpoint  string
		network   string
		address   string
	}{
		{transport: "ipc", endpoint: "/tmp/eval.sock", network: "unix", address: "/tmp/eval.sock"},
		{transport: "tcp", endpoint: "127.0.0.1:7001", network: "tcp", address: "127.0.0.1:7001"},
		{transport: "http", endpoint: "http://127.0.0.1:7002/rpc", network: "tcp", address: "127.0.0.1:7002"},
		{transport: "ws", endpoint: "ws://127.0.0.1:7003/ws", network: "tcp", address: "127.0.0.1:7003"},
	}
	for _, test := range tests {
		t.Run(test.transport, func(t *testing.T) {
			network, address, err := NetworkListenAddress(test.transport, test.endpoint)
			if err != nil {
				t.Fatalf("NetworkListenAddress: %v", err)
			}
			if network != test.network || address != test.address {
				t.Fatalf("got (%q, %q), want (%q, %q)", network, address, test.network, test.address)
			}
		})
	}
}

func TestNetworkEndpointRejectsUnsupportedOrMalformedValues(t *testing.T) {
	for _, test := range []struct{ transport, endpoint string }{
		{transport: "stdio", endpoint: ""},
		{transport: "tcp", endpoint: "missing-port"},
		{transport: "http", endpoint: "ftp://127.0.0.1:1"},
		{transport: "ws", endpoint: "://bad"},
		{transport: "ipc", endpoint: "relative.sock"},
	} {
		if _, _, err := NetworkListenAddress(test.transport, test.endpoint); err == nil {
			t.Fatalf("NetworkListenAddress(%q, %q) succeeded", test.transport, test.endpoint)
		}
	}
}
