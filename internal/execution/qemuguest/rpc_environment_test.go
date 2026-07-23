package qemuguest

import (
	"strings"
	"testing"

	"github.com/lambda-feedback/shimmy/internal/execution/qemurun"
)

func TestRPCNetworkEnvironmentOverridesOnlySelectedTransportEndpoint(t *testing.T) {
	tests := []struct {
		transport string
		key       string
		endpoint  string
	}{
		{transport: "ipc", key: "EVAL_RPC_IPC_ENDPOINT", endpoint: "/run/shimmy/evaluator.sock"},
		{transport: "tcp", key: "EVAL_RPC_TCP_ADDRESS", endpoint: "127.0.0.1:7001"},
		{transport: "http", key: "EVAL_RPC_HTTP_URL", endpoint: "http://127.0.0.1:7002/rpc"},
		{transport: "ws", key: "EVAL_RPC_WS_URL", endpoint: "ws://127.0.0.1:7003/ws"},
	}
	for _, test := range tests {
		t.Run(test.transport, func(t *testing.T) {
			environment := rpcNetworkEnvironment(qemurun.StartMessage{
				Transport:     test.transport,
				GuestEndpoint: test.endpoint,
				Env: []string{
					"PATH=/usr/bin",
					"CUSTOM_VALUE=preserved",
					test.key + "=host-value",
				},
			})
			if lastEnvironmentValue(environment, "CUSTOM_VALUE") != "preserved" {
				t.Fatalf("custom env lost: %#v", environment)
			}
			if lastEnvironmentValue(environment, "EVAL_RPC_TRANSPORT") != test.transport {
				t.Fatalf("transport env = %#v", environment)
			}
			if lastEnvironmentValue(environment, test.key) != test.endpoint {
				t.Fatalf("endpoint env = %#v", environment)
			}
		})
	}
}

func lastEnvironmentValue(environment []string, key string) string {
	prefix := key + "="
	for index := len(environment) - 1; index >= 0; index-- {
		if strings.HasPrefix(environment[index], prefix) {
			return strings.TrimPrefix(environment[index], prefix)
		}
	}
	return ""
}
