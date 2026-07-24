package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
)

func TestServeOneReturnsSchemaValidJSONRPCResponse(t *testing.T) {
	request := []byte(`{"jsonrpc":"2.0","id":7,"method":"eval","params":[{"response":"same","answer":"same"}]}`)
	var input bytes.Buffer
	writeFrameForTest(t, &input, request)
	var output bytes.Buffer
	if err := serveOne(bufio.NewReader(&input), &output); err != nil {
		t.Fatalf("serveOne: %v", err)
	}
	payload := readFrameForTest(t, bufio.NewReader(&output))
	var response map[string]any
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response["jsonrpc"] != "2.0" || response["id"].(float64) != 7 {
		t.Fatalf("response envelope = %#v", response)
	}
	result := response["result"].(map[string]any)
	if result["is_correct"] != true || result["echo"].(map[string]any)["answer"] != "same" {
		t.Fatalf("result = %#v", result)
	}
}

func TestNewRPCServerServesEvalOverRawConnection(t *testing.T) {
	serverConnection, clientConnection := net.Pipe()
	deadline := time.Now().Add(2 * time.Second)
	_ = serverConnection.SetDeadline(deadline)
	_ = clientConnection.SetDeadline(deadline)
	serverErr := make(chan error, 1)
	go func() { serverErr <- serveRawConnection(serverConnection) }()
	client, err := rpc.DialIO(context.Background(), clientConnection, clientConnection)
	if err != nil {
		t.Fatalf("DialIO: %v", err)
	}
	defer client.Close()
	var result map[string]any
	if err := client.CallContext(context.Background(), &result, "eval", map[string]any{"answer": "same"}); err != nil {
		select {
		case serverError := <-serverErr:
			t.Fatalf("CallContext: %v; server: %v", err, serverError)
		default:
			t.Fatalf("CallContext: %v", err)
		}
	}
	if result["is_correct"] != true || result["echo"].(map[string]any)["answer"] != "same" {
		t.Fatalf("result = %#v", result)
	}
}

func TestHTTPAndWebsocketHandlersServeOfficialRPCClient(t *testing.T) {
	tests := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		dial    func(context.Context, string) (*rpc.Client, error)
	}{
		{
			name:    "http",
			handler: serveHTTPRequest,
			dial: func(_ context.Context, endpoint string) (*rpc.Client, error) {
				return rpc.DialHTTP(endpoint)
			},
		},
		{
			name:    "ws",
			handler: serveWebsocket,
			dial: func(ctx context.Context, endpoint string) (*rpc.Client, error) {
				return rpc.DialWebsocket(ctx, strings.Replace(endpoint, "http://", "ws://", 1), "")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(test.handler))
			defer server.Close()
			client, err := test.dial(context.Background(), server.URL)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer client.Close()
			assertEvalCall(t, client)
		})
	}
}

func assertEvalCall(t *testing.T, client *rpc.Client) {
	t.Helper()
	var result map[string]any
	if err := client.CallContext(context.Background(), &result, "eval", map[string]any{"answer": "same"}); err != nil {
		t.Fatalf("CallContext: %v", err)
	}
	if result["is_correct"] != true || result["echo"].(map[string]any)["answer"] != "same" {
		t.Fatalf("result = %#v", result)
	}
}

func TestEvaluateHealthReportsProcessAndBoot(t *testing.T) {
	response := evaluateRequest(rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("9"),
		Method:  "healthcheck",
	})
	if response.Error != nil {
		t.Fatalf("healthcheck error = %#v", response.Error)
	}
	health, ok := response.Result.(evaluatorHealth)
	if !ok {
		t.Fatalf("healthcheck result = %#v", response.Result)
	}
	if health.Status != "ok" || health.PID <= 0 || health.BootID == "" {
		t.Fatalf("healthcheck = %#v", health)
	}
}

func TestServeOneRejectsUnknownMethodWithJSONRPCError(t *testing.T) {
	request := []byte(`{"jsonrpc":"2.0","id":8,"method":"unknown","params":[]}`)
	var input bytes.Buffer
	writeFrameForTest(t, &input, request)
	var output bytes.Buffer
	if err := serveOne(bufio.NewReader(&input), &output); err != nil {
		t.Fatalf("serveOne: %v", err)
	}
	payload := readFrameForTest(t, bufio.NewReader(&output))
	var response map[string]any
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response["error"] == nil {
		t.Fatalf("response = %#v", response)
	}
}

func writeFrameForTest(t *testing.T, output *bytes.Buffer, payload []byte) {
	t.Helper()
	if err := writeLSPFrame(output, payload); err != nil {
		t.Fatal(err)
	}
}

func readFrameForTest(t *testing.T, input *bufio.Reader) []byte {
	t.Helper()
	payload, err := readLSPFrame(input)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
