//go:build linux

// Package execution_test provides end-to-end benchmarks comparing the WASM
// dispatcher against the existing subprocess RPC/stdio backend.
package execution_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// --------------------------------------------------------------------------
// Inline echo-server source
//
// Implements a minimal subset of the go-ethereum JSON-RPC protocol over stdio.
// Frames are delimited by LSP-style Content-Length headers — exactly what
// shimmy's rpcAdapter + headerPrefixPipe expect.
//
// Request:  {"jsonrpc":"2.0","id":1,"method":"eval","params":[{...}]}
// Response: {"jsonrpc":"2.0","id":1,"result":{"ok":true}}
// --------------------------------------------------------------------------

const echoServerSrc = `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type request struct {
	JSONRPC string          ` + "`" + `json:"jsonrpc"` + "`" + `
	ID      json.RawMessage ` + "`" + `json:"id"` + "`" + `
	Method  string          ` + "`" + `json:"method"` + "`" + `
	Params  json.RawMessage ` + "`" + `json:"params"` + "`" + `
}

type response struct {
	JSONRPC string          ` + "`" + `json:"jsonrpc"` + "`" + `
	ID      json.RawMessage ` + "`" + `json:"id"` + "`" + `
	Result  map[string]any  ` + "`" + `json:"result"` + "`" + `
}

func writeFrame(w io.Writer, payload []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload))
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	var contentLength int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "Content-Length:") {
			parts := strings.SplitN(line, ":", 2)
			v, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
			contentLength = v
		}
		if line == "" {
			break
		}
	}
	buf := make([]byte, contentLength)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func main() {
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	for {
		frame, err := readFrame(reader)
		if err != nil {
			return
		}
		var req request
		if err := json.Unmarshal(frame, &req); err != nil {
			return
		}
		resp := response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"ok": true},
		}
		payload, _ := json.Marshal(resp)
		if err := writeFrame(writer, payload); err != nil {
			return
		}
		writer.Flush()
	}
}
`

// buildEchoServer compiles the inline echo-server source into a temp binary
// and returns its path. The binary is removed when the benchmark finishes.
func buildEchoServer(b *testing.B) string {
	b.Helper()

	tmpDir := b.TempDir()
	srcPath := filepath.Join(tmpDir, "main.go")
	binPath := filepath.Join(tmpDir, "echoserver")

	require.NoError(b, os.WriteFile(srcPath, []byte(echoServerSrc), 0644))

	// Honour PATH=/tmp/go/bin:$PATH used by CI.
	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = "go"
	}

	out, buildErr := exec.Command(goBin, "build", "-o", binPath, srcPath).CombinedOutput()
	require.NoError(b, buildErr, "build echo server: %s", string(out))

	return binPath
}

// --------------------------------------------------------------------------
// headerPipe — LSP Content-Length framing over a subprocess's stdio pipes.
// --------------------------------------------------------------------------

type headerPipe struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	reader *bufio.Reader
}

func (h *headerPipe) writeFrame(payload []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload))
	if _, err := io.WriteString(h.stdin, header); err != nil {
		return err
	}
	_, err := h.stdin.Write(payload)
	return err
}

func (h *headerPipe) readFrame() ([]byte, error) {
	var contentLength int
	for {
		line, err := h.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "Content-Length:") {
			parts := strings.SplitN(line, ":", 2)
			v, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
			contentLength = v
		}
		if line == "" {
			break
		}
	}
	buf := make([]byte, contentLength)
	_, err := io.ReadFull(h.reader, buf)
	return buf, err
}

// --------------------------------------------------------------------------
// rpcReq is a minimal JSON-RPC 2.0 request envelope.
// --------------------------------------------------------------------------

type rpcReq struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      int              `json:"id"`
	Method  string           `json:"method"`
	Params  []map[string]any `json:"params"`
}

// --------------------------------------------------------------------------
// BenchmarkSubprocessRPC_Echo
//
// Subprocess baseline: one persistent child process per benchmark run,
// sequential requests over Content-Length-framed JSON-RPC stdio.
//
// Compare ns/op with BenchmarkDispatcher_Send_Pool1 (WASM, pool=1) to
// measure the IPC overhead of subprocess execution vs in-process WASM.
// --------------------------------------------------------------------------

func BenchmarkSubprocessRPC_Echo(b *testing.B) {
	binPath := buildEchoServer(b)

	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, binPath)
	stdin, err := cmd.StdinPipe()
	require.NoError(b, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(b, err)
	require.NoError(b, cmd.Start())
	b.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	pipe := &headerPipe{
		stdin:  stdin,
		stdout: stdout,
		reader: bufio.NewReader(stdout),
	}

	data := map[string]any{"response": 42, "answer": 42}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := rpcReq{
			JSONRPC: "2.0",
			ID:      i,
			Method:  "eval",
			Params:  []map[string]any{data},
		}
		payload, _ := json.Marshal(req)

		if err := pipe.writeFrame(payload); err != nil {
			b.Fatalf("write frame: %v", err)
		}
		frame, err := pipe.readFrame()
		if err != nil {
			b.Fatalf("read frame: %v", err)
		}
		_ = frame
	}
}

// --------------------------------------------------------------------------
// BenchmarkSubprocessRPC_Echo_Parallel
//
// N subprocesses (one per goroutine), running concurrently.  Mirrors the
// WASM Pool=N benchmark: each goroutine owns a dedicated subprocess so
// there is no lock contention on a shared pipe.
// --------------------------------------------------------------------------

func BenchmarkSubprocessRPC_Echo_Parallel(b *testing.B) {
	binPath := buildEchoServer(b)

	n := runtime.NumCPU()

	type subproc struct {
		cmd    *exec.Cmd
		pipe   *headerPipe
		cancel context.CancelFunc
		mu     sync.Mutex
	}

	procs := make([]*subproc, n)
	for i := range procs {
		wctx, wcancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(wctx, binPath)
		stdin, err := cmd.StdinPipe()
		require.NoError(b, err)
		stdout, err := cmd.StdoutPipe()
		require.NoError(b, err)
		require.NoError(b, cmd.Start())
		procs[i] = &subproc{
			cmd:    cmd,
			cancel: wcancel,
			pipe: &headerPipe{
				stdin:  stdin,
				stdout: stdout,
				reader: bufio.NewReader(stdout),
			},
		}
	}
	b.Cleanup(func() {
		for _, p := range procs {
			p.cancel()
			_ = p.pipe.stdin.Close()
			_ = p.cmd.Wait()
		}
	})

	data := map[string]any{"response": 42, "answer": 42}

	var globalSeq sync.Mutex
	var seq int
	var assignMu sync.Mutex
	var nextProc int

	b.ResetTimer()
	b.SetParallelism(n)
	b.RunParallel(func(pb *testing.PB) {
		// Assign each goroutine a dedicated subprocess slot.
		assignMu.Lock()
		myProc := procs[nextProc%n]
		nextProc++
		assignMu.Unlock()

		for pb.Next() {
			globalSeq.Lock()
			seq++
			mySeq := seq
			globalSeq.Unlock()

			req := rpcReq{
				JSONRPC: "2.0",
				ID:      mySeq,
				Method:  "eval",
				Params:  []map[string]any{data},
			}
			payload, _ := json.Marshal(req)

			myProc.mu.Lock()
			writeErr := myProc.pipe.writeFrame(payload)
			if writeErr != nil {
				myProc.mu.Unlock()
				b.Errorf("write frame: %v", writeErr)
				return
			}
			frame, readErr := myProc.pipe.readFrame()
			myProc.mu.Unlock()

			if readErr != nil {
				b.Errorf("read frame: %v", readErr)
				return
			}
			_ = frame
		}
	})
}
