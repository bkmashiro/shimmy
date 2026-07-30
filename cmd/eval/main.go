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
		fmt.Fprintf(os.Stderr, "Usage: shimmy-eval [flags] <script.py>\n\nFlags:\n")
		fs.PrintDefaults()
	}

	wasmPath := fs.String("wasm", firstEnvironment("SHIMMY_PYTHON_RUNTIME_WASM", "FUNCTION_WASM_MODULE"), "Path to Agent Python Runtime Wasm")
	manifestPath := fs.String("manifest", firstEnvironment("SHIMMY_PYTHON_RUNTIME_MANIFEST", "FUNCTION_WASM_MANIFEST"), "Path to the producer manifest (defaults to manifest.json next to Wasm)")
	inputJSON := fs.String("input", `{"response":"","answer":""}`, `JSON input object, e.g. '{"response":"1","answer":"1"}'`)
	method := fs.String("method", "eval", "eval or preview")
	timeoutSeconds := fs.Int("timeout", 120, "Per-request timeout in seconds")
	pretty := fs.Bool("pretty", true, "Pretty-print JSON output")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if len(fs.Args()) != 1 || *wasmPath == "" {
		fmt.Fprintln(os.Stderr, "error: exactly one <script.py> and --wasm are required")
		fs.Usage()
		os.Exit(2)
	}

	var params map[string]any
	if err := json.Unmarshal([]byte(*inputJSON), &params); err != nil || params == nil {
		fmt.Fprintf(os.Stderr, "error: --input must be a JSON object: %v\n", err)
		os.Exit(2)
	}
	for name, value := range map[string]string{
		"FUNCTION_WASM_MODULE":        *wasmPath,
		"FUNCTION_WASM_MANIFEST":      *manifestPath,
		"FUNCTION_WASM_PYTHON_SCRIPT": fs.Args()[0],
	} {
		if err := os.Setenv(name, value); err != nil {
			fmt.Fprintf(os.Stderr, "error: setting %s: %v\n", name, err)
			os.Exit(1)
		}
	}

	log, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: creating logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = log.Sync() }()

	dispatcher := wasm.NewShimmyPythonDispatcher(wasm.Config{
		ModulePath:               *wasmPath,
		ShimmyPythonManifestPath: *manifestPath,
		PythonScriptPath:         fs.Args()[0],
		MaxInstances:             1,
		MaxMemoryPages:           8192,
		Timeout:                  time.Duration(*timeoutSeconds) * time.Second,
	}, log)
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	if err := dispatcher.Start(startContext); err != nil {
		startCancel()
		fmt.Fprintf(os.Stderr, "error: starting Agent Python Runtime: %v\n", err)
		os.Exit(1)
	}
	startCancel()
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := dispatcher.Shutdown(shutdownContext); err != nil {
			fmt.Fprintf(os.Stderr, "warning: shutdown: %v\n", err)
		}
	}()

	result, err := dispatcher.Send(context.Background(), *method, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: evaluation failed: %v\n", err)
		os.Exit(1)
	}

	var output []byte
	if *pretty {
		output, err = json.MarshalIndent(result, "", "  ")
	} else {
		output, err = json.Marshal(result)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: encoding result: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(output))

	if value, ok := result["result"].(map[string]any); ok {
		if _, failed := value["error"]; failed {
			os.Exit(1)
		}
	}
}

func firstEnvironment(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}
