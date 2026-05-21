//go:build linux

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/wasm"
)

func main() {
	fs := flag.NewFlagSet("shimmy-eval", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: shimmy-eval [flags] <script.py>\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
	}

	wasmPath := fs.String("wasm", "", "Path to python-reactor.wasm (required, or PYTHON_REACTOR_WASM env)")
	inputJSON := fs.String("input", `{"response":"","answer":""}`, `JSON input object, e.g. '{"response":"1","answer":"1"}'`)
	method := fs.String("method", "eval", "eval or preview")
	timeoutSecs := fs.Int("timeout", 120, "Timeout in seconds")
	pretty := fs.Bool("pretty", true, "Pretty-print JSON output")

	if err := fs.Parse(os.Args[1:]); err != nil {
		// ExitOnError handles this, but be explicit.
		os.Exit(2)
	}

	args := fs.Args()
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "error: exactly one positional argument required: <script.py>")
		fs.Usage()
		os.Exit(2)
	}
	scriptPath := args[0]

	// Resolve wasm path: flag takes priority, then env var.
	if *wasmPath == "" {
		*wasmPath = os.Getenv("PYTHON_REACTOR_WASM")
	}
	if *wasmPath == "" {
		fmt.Fprintln(os.Stderr, "error: --wasm flag or PYTHON_REACTOR_WASM environment variable is required")
		fs.Usage()
		os.Exit(2)
	}

	// Read the Python script.
	scriptBytes, err := os.ReadFile(scriptPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading script %q: %v\n", scriptPath, err)
		os.Exit(1)
	}
	script := string(scriptBytes)

	// Build a production-grade logger (no-op on success, errors on stderr).
	log, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: creating logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Sync() //nolint:errcheck

	cfg := wasm.Config{
		Timeout: time.Duration(*timeoutSecs) * time.Second,
	}

	runner := wasm.NewReactorPythonRunner(*wasmPath, cfg, log)

	ctx := context.Background()

	if err := runner.Init(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: initialising runner: %v\n", err)
		os.Exit(1)
	}

	result, err := runner.SendRequest(ctx, script, *method, *inputJSON)
	if err != nil {
		// Shutdown best-effort before exiting.
		_ = runner.Shutdown(ctx)
		fmt.Fprintf(os.Stderr, "error: sending request: %v\n", err)
		os.Exit(1)
	}

	if err := runner.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "warning: shutdown: %v\n", err)
	}

	// Encode result to JSON.
	var out []byte
	if *pretty {
		out, err = json.MarshalIndent(result, "", "  ")
	} else {
		out, err = json.Marshal(result)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: marshalling result: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(out))

	// Exit 1 if the result contains an "error" key.
	if _, hasErr := result["error"]; hasErr {
		fmt.Fprintf(os.Stderr, "error: evaluator returned error: %v\n", result["error"])
		os.Exit(1)
	}
}
