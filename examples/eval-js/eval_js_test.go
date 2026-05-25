// Smoke test for the eval-js javy-compiled WASM runner.
//
// Requires runner.wasm to be present in this directory (built by build-runner.sh)
// and the wazero CLI on PATH (or WAZERO_PATH set).
//
// Run as part of:
//
//	go test ./examples/... -v -timeout 120s
package evaljs_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func wasmPath(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filename), "runner.wasm")
}

func wazeroPath(t *testing.T) (string, bool) {
	t.Helper()
	if p := os.Getenv("WAZERO_PATH"); p != "" {
		return p, true
	}
	p, err := exec.LookPath("wazero")
	if err != nil {
		return "", false
	}
	return p, true
}

type rpcProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

func startRunner(t *testing.T, wazeroBin, wasm string) *rpcProc {
	t.Helper()
	cmd := exec.Command(wazeroBin, "run", wasm)
	cmd.Stderr = os.Stderr

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start wazero: %v", err)
	}

	p := &rpcProc{cmd: cmd, stdin: stdinPipe, stdout: bufio.NewReader(stdoutPipe)}
	t.Cleanup(func() {
		_ = stdinPipe.Close()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	return p
}

func (p *rpcProc) send(t *testing.T, id int, method string, params any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  []any{params},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := fmt.Fprint(p.stdin, header); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := p.stdin.Write(body); err != nil {
		t.Fatalf("write body: %v", err)
	}
}

func (p *rpcProc) recv(t *testing.T) map[string]any {
	t.Helper()
	var contentLength int
	for {
		line, err := p.stdout.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "Content-Length:") {
			parts := strings.SplitN(line, ":", 2)
			n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				t.Fatalf("parse Content-Length: %v", err)
			}
			contentLength = n
			break
		}
	}
	for {
		line, err := p.stdout.ReadString('\n')
		if err != nil {
			t.Fatalf("read separator: %v", err)
		}
		if strings.TrimRight(line, "\r\n") == "" {
			break
		}
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(p.stdout, body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	var rpc struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &rpc); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: code=%d message=%s", rpc.Error.Code, rpc.Error.Message)
	}
	return rpc.Result
}

func TestEvalJS(t *testing.T) {
	wazeroBin, ok := wazeroPath(t)
	if !ok {
		t.Skip("wazero not found on PATH (set WAZERO_PATH or install wazero)")
	}

	wasm := wasmPath(t)
	if _, err := os.Stat(wasm); err != nil {
		t.Skipf("runner.wasm not found at %s (run build-runner.sh first)", wasm)
	}

	proc := startRunner(t, wazeroBin, wasm)

	t.Run("healthcheck", func(t *testing.T) {
		proc.send(t, 1, "healthcheck", map[string]any{})
		result := proc.recv(t)
		if result["status"] != "ok" {
			t.Fatalf("expected status=ok, got %v", result["status"])
		}
	})

	t.Run("eval_correct", func(t *testing.T) {
		proc.send(t, 2, "eval", map[string]any{"response": "42", "answer": "42"})
		result := proc.recv(t)
		isCorrect, ok := result["is_correct"].(bool)
		if !ok || !isCorrect {
			t.Fatalf("expected is_correct=true, got %v", result["is_correct"])
		}
	})

	t.Run("eval_incorrect", func(t *testing.T) {
		proc.send(t, 3, "eval", map[string]any{"response": "41", "answer": "42"})
		result := proc.recv(t)
		isCorrect, ok := result["is_correct"].(bool)
		if !ok || isCorrect {
			t.Fatalf("expected is_correct=false, got %v", result["is_correct"])
		}
	})

	t.Run("preview", func(t *testing.T) {
		proc.send(t, 4, "preview", map[string]any{"response": "3.14"})
		result := proc.recv(t)
		if _, ok := result["preview"].(string); !ok {
			t.Fatalf("expected preview string, got %v", result)
		}
	})
}
