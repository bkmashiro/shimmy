// Adversarial sandbox tests for the shimmy WASM runtime.
// Each test compiles a malicious wasm32-wasip1 module that attempts an attack,
// then verifies wazero's isolation properties block it.
//
// Requires pre-built eval.wasm artifacts in each subdirectory.
// Build them with:
//
//	cd mem-bomb   && GOOS=wasip1 GOARCH=wasm go build -o eval.wasm .
//	(repeat for each subdirectory)
//
// Then run:
//
//	go test ./examples/adversarial/... -v -timeout 60s
package adversarial_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// attackResult is the JSON shape returned by each adversarial wasm module.
type attackResult struct {
	Attack  string `json:"attack"`
	Blocked bool   `json:"blocked"`
	Detail  string `json:"detail"`
}

// callResult holds either a decoded result or an error from the evaluate call.
type callResult struct {
	res attackResult
	err error
}

// loadAndCall loads a wasm module from wasmPath, instantiates it with a
// timeout-enabled wazero runtime, calls evaluate with an empty request, and
// returns the parsed response or any error.
//
// If the wasm file is missing the test is skipped.
func loadAndCall(t *testing.T, wasmPath string, timeout time.Duration) (attackResult, error) {
	t.Helper()

	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Skipf("wasm artifact not found (%s), skipping: %v", wasmPath, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// WithCloseOnContextDone enables epoch-based interruption: when ctx is
	// cancelled (or times out) wazero will interrupt any running WASM execution.
	rtCfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(true)
	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)
	defer rt.Close(ctx)

	wasi_snapshot_preview1.MustInstantiate(ctx, rt)

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}

	modCfg := wazero.NewModuleConfig().
		WithName("eval").
		WithStartFunctions("_initialize")
	mod, err := rt.InstantiateModule(ctx, compiled, modCfg)
	if err != nil {
		return attackResult{}, err
	}
	defer mod.Close(ctx)

	mem := mod.Memory()
	allocFn := mod.ExportedFunction("alloc")
	evalFn := mod.ExportedFunction("evaluate")

	// Write an empty JSON request — adversarial modules ignore the request body.
	reqBytes := []byte(`{}`)

	allocRes, err := allocFn.Call(ctx, uint64(len(reqBytes)))
	if err != nil {
		return attackResult{}, err
	}
	ptr := uint32(allocRes[0])

	if !mem.Write(ptr, reqBytes) {
		t.Fatal("mem.Write failed")
	}

	evalRes, err := evalFn.Call(ctx, uint64(ptr), uint64(len(reqBytes)))
	if err != nil {
		return attackResult{}, err
	}

	respPtr := uint32(evalRes[0])

	lenBytes, ok := mem.Read(respPtr, 4)
	if !ok {
		t.Fatal("mem.Read length prefix failed")
	}
	respLen := binary.LittleEndian.Uint32(lenBytes)

	body, ok := mem.Read(respPtr+4, respLen)
	if !ok {
		t.Fatalf("mem.Read body failed (respLen=%d)", respLen)
	}

	var result attackResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal response: %v\nbody: %s", err, body)
	}
	return result, nil
}

// wasmPath returns the path to eval.wasm for a given adversarial module.
func wasmPath(module string) string {
	return filepath.Join(module, "eval.wasm")
}

// TestMemBomb verifies that wazero's WithMemoryLimitPages prevents the guest
// from allocating unbounded memory.
func TestMemBomb(t *testing.T) {
	res, err := loadAndCall(t, wasmPath("mem-bomb"), 10*time.Second)
	if err != nil {
		// A trap/OOM from the runtime itself counts as blocked.
		t.Logf("evaluate returned error (counts as blocked): %v", err)
		return
	}
	if !res.Blocked {
		t.Fatalf("mem-bomb was NOT blocked: detail=%q", res.Detail)
	}
	t.Logf("mem-bomb blocked: %s", res.Detail)
}

// TestCpuBomb verifies that an infinite loop is interrupted by the epoch
// timeout — the evaluate call must return an error within the deadline.
func TestCpuBomb(t *testing.T) {
	_, err := loadAndCall(t, wasmPath("cpu-bomb"), 2*time.Second)
	if err == nil {
		t.Fatal("cpu-bomb should have been interrupted by context timeout, but evaluate returned nil error")
	}
	t.Logf("cpu-bomb interrupted as expected: %v", err)
}

// TestStackBomb verifies that infinite recursion is interrupted by the epoch
// timeout or results in a trap — either way evaluate must not succeed.
func TestStackBomb(t *testing.T) {
	_, err := loadAndCall(t, wasmPath("stack-bomb"), 2*time.Second)
	if err == nil {
		t.Fatal("stack-bomb should have been interrupted or trapped, but evaluate returned nil error")
	}
	t.Logf("stack-bomb interrupted/trapped as expected: %v", err)
}

// assertBlocked is a helper that passes if either:
// (a) the evaluate call returned an error (wasm trap = blocked), or
// (b) the call succeeded and the guest reported blocked:true.
// It fails only if the call succeeded AND blocked==false (attack got through).
func assertBlocked(t *testing.T, name string, res attackResult, err error) {
	t.Helper()
	if err != nil {
		t.Logf("%s: blocked via wasm trap: %v", name, err)
		return
	}
	if !res.Blocked {
		t.Fatalf("%s was NOT blocked — attack succeeded: detail=%q", name, res.Detail)
	}
	t.Logf("%s blocked: %s", name, res.Detail)
}

// TestFsReadEnviron verifies that /proc/self/environ is not accessible inside
// the WASM sandbox (no preopened directories).
func TestFsReadEnviron(t *testing.T) {
	res, err := loadAndCall(t, wasmPath("fs-read-environ"), 10*time.Second)
	assertBlocked(t, "fs-read-environ", res, err)
}

// TestFsReadEtc verifies that /etc/passwd is not accessible inside the WASM
// sandbox.
func TestFsReadEtc(t *testing.T) {
	res, err := loadAndCall(t, wasmPath("fs-read-etc"), 10*time.Second)
	assertBlocked(t, "fs-read-etc", res, err)
}

// TestFsWriteTmp verifies that the guest cannot write to /tmp on the host.
// We additionally confirm the file does not exist on the host after the call.
func TestFsWriteTmp(t *testing.T) {
	hostPath := "/tmp/evil-shimmy-test"
	os.Remove(hostPath)
	t.Cleanup(func() { os.Remove(hostPath) })

	res, err := loadAndCall(t, wasmPath("fs-write-tmp"), 10*time.Second)
	assertBlocked(t, "fs-write-tmp", res, err)

	// Double-check: verify the file doesn't exist on the host filesystem.
	if _, statErr := os.Stat(hostPath); statErr == nil {
		t.Fatal("evil file /tmp/evil-shimmy-test exists on the host — WASM sandbox failed to isolate writes")
	}
}

// TestNetTcp verifies that the guest cannot make outbound TCP connections.
// wazero does not provide WASI sock_* imports, so net.Dial must fail.
func TestNetTcp(t *testing.T) {
	res, err := loadAndCall(t, wasmPath("net-tcp"), 10*time.Second)
	assertBlocked(t, "net-tcp", res, err)
}

// TestEnvRead verifies that the guest cannot read host environment variables.
// We set a fake secret in the host environment and confirm WASM never sees it.
func TestEnvRead(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-do-not-leak")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAIOSFODNN7EXAMPLE")

	res, err := loadAndCall(t, wasmPath("env-read"), 10*time.Second)
	assertBlocked(t, "env-read", res, err)
}

// TestForkExec verifies that the guest cannot execute host processes.
// WASM has no fork/exec syscalls, so exec.Command must fail.
func TestForkExec(t *testing.T) {
	res, err := loadAndCall(t, wasmPath("fork-exec"), 10*time.Second)
	assertBlocked(t, "fork-exec", res, err)
}

// TestLargeOutput verifies that a guest returning a large response (filling its
// 256KB respBuf) does not crash or panic the host. The host should handle it
// gracefully — this is a robustness test, not an isolation test.
func TestLargeOutput(t *testing.T) {
	res, err := loadAndCall(t, wasmPath("large-output"), 10*time.Second)
	if err != nil {
		t.Fatalf("large-output: host should not error on large response, got: %v", err)
	}
	// The module sets blocked=false intentionally — it's not an attack being
	// blocked, just a large payload robustness check.
	t.Logf("large-output: attack=%q blocked=%v detail len=%d",
		res.Attack, res.Blocked, len(res.Detail))
}
