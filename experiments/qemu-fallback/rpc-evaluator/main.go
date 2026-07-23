package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
)

const maxRPCFixtureFrameBytes = 4 << 20

type rpcRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      json.RawMessage   `json:"id"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type evaluatorResult struct {
	IsCorrect bool `json:"is_correct"`
	Echo      any  `json:"echo"`
}

func serveOne(input *bufio.Reader, output io.Writer) error {
	payload, err := readLSPFrame(input)
	if err != nil {
		return err
	}
	var request rpcRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return writeRPCError(output, nil, -32700, "parse error")
	}
	encoded, err := json.Marshal(evaluateRequest(request))
	if err != nil {
		return err
	}
	return writeLSPFrame(output, encoded)
}

func evaluateRequest(request rpcRequest) rpcResponse {
	response := rpcResponse{JSONRPC: "2.0", ID: request.ID}
	if request.JSONRPC != "2.0" || len(request.ID) == 0 {
		response.Error = &rpcError{Code: -32600, Message: "invalid request"}
	} else if request.Method != "eval" {
		response.Error = &rpcError{Code: -32601, Message: "method not found"}
	} else {
		echo := json.RawMessage(`{}`)
		if len(request.Params) > 0 && len(request.Params[0]) > 0 {
			echo = request.Params[0]
		}
		response.Result = evaluatorResult{IsCorrect: true, Echo: echo}
	}
	return response
}

func writeRPCError(output io.Writer, id json.RawMessage, code int, message string) error {
	encoded, err := json.Marshal(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	})
	if err != nil {
		return err
	}
	return writeLSPFrame(output, encoded)
}

func readLSPFrame(input *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := input.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			break
		}
		key, value, found := strings.Cut(line, ":")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			continue
		}
		if contentLength >= 0 {
			return nil, errors.New("duplicate Content-Length")
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 0 || parsed > maxRPCFixtureFrameBytes {
			return nil, errors.New("invalid Content-Length")
		}
		contentLength = parsed
	}
	if contentLength < 0 {
		return nil, errors.New("missing Content-Length")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(input, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeLSPFrame(output io.Writer, payload []byte) error {
	if len(payload) > maxRPCFixtureFrameBytes {
		return errors.New("RPC fixture response exceeds frame limit")
	}
	if _, err := fmt.Fprintf(output, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	for len(payload) > 0 {
		count, err := output.Write(payload)
		if count > 0 {
			payload = payload[count:]
		}
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func run(input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	for {
		if err := serveOne(reader, output); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func serveRaw(network, address string) error {
	if network == "unix" {
		if info, err := os.Lstat(address); err == nil {
			if info.Mode()&os.ModeSocket == 0 {
				return fmt.Errorf("RPC fixture IPC endpoint exists and is not a socket")
			}
			if err := os.Remove(address); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(address), 0o700); err != nil {
			return err
		}
	}
	listener, err := net.Listen(network, address)
	if err != nil {
		return err
	}
	defer listener.Close()
	for {
		connection, err := listener.Accept()
		if err != nil {
			return err
		}
		go func() { _ = serveRawConnection(connection) }()
	}
}

func serveRawConnection(connection io.ReadWriteCloser) error {
	defer connection.Close()
	decoder := json.NewDecoder(connection)
	encoder := json.NewEncoder(connection)
	for {
		var request rpcRequest
		if err := decoder.Decode(&request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := encoder.Encode(evaluateRequest(request)); err != nil {
			return err
		}
	}
}

func serveHTTP(endpoint string, websocketMode bool) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("invalid RPC fixture URL %q", endpoint)
	}
	handler := http.Handler(http.HandlerFunc(serveHTTPRequest))
	if websocketMode {
		handler = http.HandlerFunc(serveWebsocket)
	}
	return http.ListenAndServe(parsed.Host, handler)
}

func serveHTTPRequest(response http.ResponseWriter, request *http.Request) {
	defer request.Body.Close()
	request.Body = http.MaxBytesReader(response, request.Body, maxRPCFixtureFrameBytes)
	var message rpcRequest
	if err := json.NewDecoder(request.Body).Decode(&message); err != nil {
		http.Error(response, "invalid JSON-RPC request", http.StatusBadRequest)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(evaluateRequest(message))
}

var fixtureUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func serveWebsocket(response http.ResponseWriter, request *http.Request) {
	connection, err := fixtureUpgrader.Upgrade(response, request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	for {
		var message rpcRequest
		if err := connection.ReadJSON(&message); err != nil {
			return
		}
		if err := connection.WriteJSON(evaluateRequest(message)); err != nil {
			return
		}
	}
}

func runConfigured() error {
	switch os.Getenv("EVAL_RPC_TRANSPORT") {
	case "", "stdio":
		return run(os.Stdin, os.Stdout)
	case "ipc":
		return serveRaw("unix", os.Getenv("EVAL_RPC_IPC_ENDPOINT"))
	case "tcp":
		return serveRaw("tcp", os.Getenv("EVAL_RPC_TCP_ADDRESS"))
	case "http":
		return serveHTTP(os.Getenv("EVAL_RPC_HTTP_URL"), false)
	case "ws":
		return serveHTTP(os.Getenv("EVAL_RPC_WS_URL"), true)
	default:
		return fmt.Errorf("unsupported RPC fixture transport %q", os.Getenv("EVAL_RPC_TRANSPORT"))
	}
}

func main() {
	if err := runConfigured(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
