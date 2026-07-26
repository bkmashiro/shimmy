package wasm

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"go.uber.org/zap"
)

const (
	agentPythonDefaultMemoryPages = 8192
	agentPythonMaxMemoryPages     = 16384
	agentPythonDiagnosticMax      = 16 * 1024
)

// AgentPythonDispatcher consumes the clean Agent Python Runtime v1 artifact.
// It compiles once, but every request gets a freshly initialized, exclusively
// owned module instance. Served instances are always closed, never restored or
// returned to a pool.
type AgentPythonDispatcher struct {
	cfg Config
	log *zap.Logger

	mu       sync.Mutex
	started  bool
	closed   bool
	closedCh chan struct{}
	pending  sync.WaitGroup

	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	cache    wazero.CompilationCache
	artifact *AgentPythonArtifact
	script   string
	slots    chan struct{}

	runCounter atomic.Uint64
}

func NewAgentPythonDispatcher(cfg Config, log *zap.Logger) *AgentPythonDispatcher {
	if log == nil {
		log = zap.NewNop()
	}
	return &AgentPythonDispatcher{
		cfg:      cfg,
		log:      log.Named("dispatcher_agent_python"),
		closedCh: make(chan struct{}),
	}
}

func (d *AgentPythonDispatcher) Start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return errors.New("agent-python: dispatcher is shut down")
	}
	if d.started {
		return nil
	}

	d.cfg.applyEnv()
	if d.cfg.Timeout == 0 {
		d.cfg.Timeout = 30 * time.Second
	}
	if d.cfg.MaxMemoryPages == 0 {
		d.cfg.MaxMemoryPages = agentPythonDefaultMemoryPages
	}
	if d.cfg.MaxMemoryPages > agentPythonMaxMemoryPages {
		return fmt.Errorf("agent-python: memory limit %d pages exceeds hard bound %d", d.cfg.MaxMemoryPages, agentPythonMaxMemoryPages)
	}
	if d.cfg.MaxInstances <= 0 {
		d.cfg.MaxInstances = runtime.NumCPU()
		if d.cfg.MaxInstances > 4 {
			d.cfg.MaxInstances = 4
		}
		if d.cfg.MaxInstances < 1 {
			d.cfg.MaxInstances = 1
		}
	}
	if d.cfg.PythonPreloadMode == "" {
		d.cfg.PythonPreloadMode = "evaluator"
	}
	if err := d.cfg.validatePythonPreloadMode(); err != nil {
		return fmt.Errorf("agent-python: %w", err)
	}
	if d.cfg.SnapshotMode != "" || d.cfg.UseUffd {
		return errors.New("agent-python: snapshot modes are unsupported for the fresh-instance runtime; unset FUNCTION_WASM_SNAPSHOT_MODE and FUNCTION_WASM_USE_UFFD")
	}
	if d.cfg.PythonScriptPath == "" {
		return errors.New("agent-python: PythonScriptPath must be set (FUNCTION_WASM_PYTHON_SCRIPT)")
	}
	scriptBytes, err := os.ReadFile(d.cfg.PythonScriptPath)
	if err != nil {
		return fmt.Errorf("agent-python: read script %q: %w", d.cfg.PythonScriptPath, err)
	}
	if len(scriptBytes) == 0 || len(scriptBytes) > agentPythonPayloadMax {
		return fmt.Errorf("agent-python: trusted script size %d is outside the 1 MiB guest bound", len(scriptBytes))
	}

	artifact, err := verifyAgentPythonArtifact(d.cfg.ModulePath, d.cfg.AgentPythonManifestPath)
	if err != nil {
		return err
	}

	runtimeConfig := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(d.cfg.MaxMemoryPages)
	var cache wazero.CompilationCache
	if d.cfg.CompileCacheDir != "" {
		cache, err = wazero.NewCompilationCacheWithDir(d.cfg.CompileCacheDir)
		if err != nil {
			return fmt.Errorf("agent-python: create compilation cache: %w", err)
		}
		runtimeConfig = runtimeConfig.WithCompilationCache(cache)
	}

	wasmRuntime := wazero.NewRuntimeWithConfig(ctx, runtimeConfig)
	closePartial := func() {
		_ = wasmRuntime.Close(context.Background())
		if cache != nil {
			_ = cache.Close(context.Background())
		}
	}
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, wasmRuntime); err != nil {
		closePartial()
		return fmt.Errorf("agent-python: instantiate WASI imports: %w", err)
	}
	if _, err := wasmRuntime.NewHostModuleBuilder("agent_runtime_v1").
		NewFunctionBuilder().
		WithFunc(agentPythonDeniedHostCall).
		Export("host_call").
		Instantiate(ctx); err != nil {
		closePartial()
		return fmt.Errorf("agent-python: instantiate Host imports: %w", err)
	}
	compiled, err := wasmRuntime.CompileModule(ctx, artifact.WasmBytes)
	if err != nil {
		closePartial()
		return fmt.Errorf("agent-python: compile guest: %w", err)
	}

	d.runtime = wasmRuntime
	d.compiled = compiled
	d.cache = cache
	d.artifact = artifact
	d.script = string(scriptBytes)
	d.slots = make(chan struct{}, d.cfg.MaxInstances)

	// Probe the exact artifact and trusted script before reporting readiness.
	probe, diagnostic, err := d.newInitializedModule(ctx, true)
	if probe != nil {
		_ = probe.Close(context.Background())
	}
	if err != nil {
		_ = d.closeRuntime(context.Background())
		return withAgentPythonDiagnostic(err, diagnostic.String())
	}

	d.started = true
	d.log.Info("agent-python dispatcher ready",
		zap.String("artifact_sha256", artifact.SHA256),
		zap.String("producer_commit", artifact.ProducerCommit),
		zap.String("artifact_profile", artifact.Profile),
		zap.Int("max_instances", d.cfg.MaxInstances),
		zap.String("reset_mode", "fresh-instance"),
	)
	return nil
}

func (d *AgentPythonDispatcher) Send(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	if method == "healthcheck" {
		d.mu.Lock()
		ready := d.started && !d.closed
		profile := ""
		if d.artifact != nil {
			profile = d.artifact.Profile
		}
		d.mu.Unlock()
		if !ready {
			return nil, errors.New("agent-python: dispatcher is not ready")
		}
		return map[string]any{
			"command": "healthcheck",
			"result": map[string]any{
				"status":     "ok",
				"profile":    profile,
				"reset_mode": "fresh-instance",
			},
		}, nil
	}
	if !d.tryBeginSend() {
		return nil, errors.New("agent-python: dispatcher is not ready")
	}
	defer d.pending.Done()

	select {
	case d.slots <- struct{}{}:
		defer func() { <-d.slots }()
	case <-d.closedCh:
		return nil, errors.New("agent-python: dispatcher is shut down")
	case <-ctx.Done():
		return nil, fmt.Errorf("agent-python: acquire execution slot: %w", ctx.Err())
	}

	runID := fmt.Sprintf("shimmy-%s-%d", d.artifact.SHA256[:12], d.runCounter.Add(1))
	scriptInRequest := ""
	if d.cfg.PythonPreloadMode == "off" {
		scriptInRequest = d.script
	}
	request, err := buildAgentPythonRunRequest(runID, method, params, scriptInRequest)
	if err != nil {
		return nil, err
	}

	runContext, cancel := context.WithTimeout(ctx, d.cfg.Timeout)
	defer cancel()
	module, diagnostic, err := d.newInitializedModule(runContext, d.cfg.PythonPreloadMode != "off")
	if err != nil {
		return nil, withAgentPythonDiagnostic(err, diagnostic.String())
	}
	defer module.Close(context.Background())

	payload, err := callAgentPythonExecute(runContext, module, request)
	if err != nil {
		if runContext.Err() != nil {
			err = errors.Join(err, runContext.Err())
		}
		return nil, withAgentPythonDiagnostic(err, diagnostic.String())
	}
	result, err := decodeAgentPythonResponse(payload)
	if err != nil {
		return nil, err
	}
	return map[string]any{"command": method, "result": result}, nil
}

func (d *AgentPythonDispatcher) tryBeginSend() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.started || d.closed {
		return false
	}
	d.pending.Add(1)
	return true
}

func (d *AgentPythonDispatcher) newInitializedModule(ctx context.Context, prepare bool) (api.Module, *agentPythonDiagnosticBuffer, error) {
	diagnostic := &agentPythonDiagnosticBuffer{}
	module, err := d.runtime.InstantiateModule(
		ctx,
		d.compiled,
		wazero.NewModuleConfig().WithName("").WithRandSource(cryptorand.Reader).WithStderr(diagnostic),
	)
	if err != nil {
		return nil, diagnostic, fmt.Errorf("agent-python: instantiate guest: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = module.Close(context.Background())
		}
	}()
	if err := callAgentPythonNoArgs(ctx, module, "_initialize"); err != nil {
		return nil, diagnostic, err
	}
	if err := callAgentPythonStatus(ctx, module, "runtime_init", []byte("{}")); err != nil {
		return nil, diagnostic, err
	}
	if prepare {
		if err := callAgentPythonStatus(ctx, module, "runtime_prepare", []byte(d.script)); err != nil {
			return nil, diagnostic, err
		}
	}
	diagnostic.Reset()
	failed = false
	return module, diagnostic, nil
}

func (d *AgentPythonDispatcher) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	close(d.closedCh)
	d.mu.Unlock()

	d.pending.Wait()
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closeRuntime(ctx)
}

func (d *AgentPythonDispatcher) closeRuntime(ctx context.Context) error {
	var compiledErr, runtimeErr, cacheErr error
	if d.compiled != nil {
		compiledErr = d.compiled.Close(ctx)
		d.compiled = nil
	}
	if d.runtime != nil {
		runtimeErr = d.runtime.Close(ctx)
		d.runtime = nil
	}
	if d.cache != nil {
		cacheErr = d.cache.Close(ctx)
		d.cache = nil
	}
	d.started = false
	return errors.Join(compiledErr, runtimeErr, cacheErr)
}

func agentPythonDeniedHostCall(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 {
	return -1
}

func callAgentPythonNoArgs(ctx context.Context, module api.Module, name string) error {
	function := module.ExportedFunction(name)
	if function == nil {
		return fmt.Errorf("agent-python: required export %q is missing", name)
	}
	if _, err := function.Call(ctx); err != nil {
		return fmt.Errorf("agent-python: call %s: %w", name, err)
	}
	return nil
}

func callAgentPythonStatus(ctx context.Context, module api.Module, name string, data []byte) error {
	results, release, err := callAgentPythonWithBytes(ctx, module, name, data)
	if release != nil {
		defer release()
	}
	if err != nil {
		return err
	}
	if len(results) != 1 || uint32(results[0]) != 0 {
		return fmt.Errorf("agent-python: %s returned non-zero status", name)
	}
	return nil
}

func callAgentPythonExecute(ctx context.Context, module api.Module, request []byte) ([]byte, error) {
	results, release, err := callAgentPythonWithBytes(ctx, module, "execute", request)
	if release != nil {
		defer release()
	}
	if err != nil {
		return nil, err
	}
	if len(results) != 1 {
		return nil, errors.New("agent-python: execute returned an unexpected result count")
	}
	return readAgentPythonResponse(module.Memory(), uint32(results[0]))
}

func callAgentPythonWithBytes(ctx context.Context, module api.Module, name string, data []byte) ([]uint64, func(), error) {
	if len(data) == 0 || len(data) > agentPythonPayloadMax || len(data) > math.MaxUint32 {
		return nil, nil, fmt.Errorf("agent-python: %s input size %d is outside the guest bound", name, len(data))
	}
	allocate := module.ExportedFunction("alloc")
	deallocate := module.ExportedFunction("dealloc")
	function := module.ExportedFunction(name)
	if allocate == nil || deallocate == nil || function == nil {
		return nil, nil, fmt.Errorf("agent-python: required allocation or %s export is missing", name)
	}
	allocated, err := allocate.Call(ctx, uint64(uint32(len(data))))
	if err != nil || len(allocated) != 1 || allocated[0] == 0 {
		return nil, nil, fmt.Errorf("agent-python: guest allocation failed: %w", err)
	}
	pointer := uint32(allocated[0])
	var once sync.Once
	release := func() {
		once.Do(func() {
			releaseContext, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, _ = deallocate.Call(releaseContext, uint64(pointer))
		})
	}
	if !module.Memory().Write(pointer, data) {
		release()
		return nil, nil, errors.New("agent-python: guest input write is out of bounds")
	}
	results, err := function.Call(ctx, uint64(pointer), uint64(uint32(len(data))))
	if err != nil {
		release()
		return nil, nil, fmt.Errorf("agent-python: call %s: %w", name, err)
	}
	return results, release, nil
}

func readAgentPythonResponse(memory api.Memory, pointer uint32) ([]byte, error) {
	if memory == nil {
		return nil, errors.New("agent-python: guest module has no linear memory")
	}
	header, ok := memory.Read(pointer, 4)
	if !ok {
		return nil, errors.New("agent-python: response length prefix is out of bounds")
	}
	length := binary.LittleEndian.Uint32(header)
	if length > agentPythonPayloadMax {
		return nil, fmt.Errorf("agent-python: response payload length %d exceeds limit %d", length, agentPythonPayloadMax)
	}
	if uint64(pointer)+4+uint64(length) > uint64(memory.Size()) {
		return nil, errors.New("agent-python: response frame is out of bounds")
	}
	payload, ok := memory.Read(pointer+4, length)
	if !ok {
		return nil, errors.New("agent-python: response payload is out of bounds")
	}
	return append([]byte(nil), payload...), nil
}

type agentPythonDiagnosticBuffer struct {
	data []byte
}

func (buffer *agentPythonDiagnosticBuffer) Write(data []byte) (int, error) {
	length := len(data)
	if length >= agentPythonDiagnosticMax {
		buffer.data = append(buffer.data[:0], data[length-agentPythonDiagnosticMax:]...)
		return length, nil
	}
	if overflow := len(buffer.data) + length - agentPythonDiagnosticMax; overflow > 0 {
		copy(buffer.data, buffer.data[overflow:])
		buffer.data = buffer.data[:len(buffer.data)-overflow]
	}
	buffer.data = append(buffer.data, data...)
	return length, nil
}

func (buffer *agentPythonDiagnosticBuffer) String() string { return string(buffer.data) }
func (buffer *agentPythonDiagnosticBuffer) Reset()         { buffer.data = buffer.data[:0] }

func withAgentPythonDiagnostic(base error, diagnostic string) error {
	if diagnostic == "" {
		return base
	}
	return fmt.Errorf("%w; guest stderr: %s", base, diagnostic)
}
