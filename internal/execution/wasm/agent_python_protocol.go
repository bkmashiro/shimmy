package wasm

// This file carries the consumer copy of the neutral Agent Python Runtime v1
// request/response and artifact contract. The source contract was pinned from
// bkmashiro/agent-python-runtime guest commit
// 9a571176bb58c2d6a41312d01ad789abdd6b82e6 with repository-owner approval.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

const agentPythonPayloadMax = 1024 * 1024

const agentPythonPreparedCall = `_shimmy_data = inputs["params"]
_shimmy_method = inputs["method"]
if _shimmy_method == "preview":
    _shimmy_fn = globals().get("preview_function") or globals().get("evaluation_function")
else:
    _shimmy_fn = globals().get("evaluation_function")
if _shimmy_fn is None:
    raise RuntimeError("no compatible evaluation function is defined")
result = _shimmy_fn(_shimmy_data.get("response"), _shimmy_data.get("answer"), _shimmy_data.get("params", {}))
`

const agentPythonUnpreparedCall = `exec(compile(inputs["script"], "<shimmy-trusted-script>", "exec"), globals(), globals())
` + agentPythonPreparedCall

type AgentPythonArtifact struct {
	WasmBytes      []byte
	Profile        string
	ProducerCommit string
	SHA256         string
	ManifestPath   string
}

type agentPythonManifest struct {
	SchemaVersion   int    `json:"schema_version"`
	ABIVersion      string `json:"abi_version"`
	ArtifactProfile string `json:"artifact_profile"`
	Target          string `json:"target"`
	Artifact        struct {
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
		SHA256   string `json:"sha256"`
	} `json:"artifact"`
	Build struct {
		RepositoryCommit string `json:"repository_commit"`
		SourceDateEpoch  string `json:"source_date_epoch"`
		CompilerTarget   string `json:"compiler_target"`
		ExecutionModel   string `json:"execution_model"`
	} `json:"build"`
	Wasm struct {
		Exports []string `json:"exports"`
		Imports []struct {
			Module string `json:"module"`
			Name   string `json:"name"`
		} `json:"imports"`
	} `json:"wasm"`
}

var agentPythonCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func verifyAgentPythonArtifact(modulePath, manifestPath string) (*AgentPythonArtifact, error) {
	if modulePath == "" {
		return nil, errors.New("agent-python: ModulePath must be set (FUNCTION_WASM_MODULE)")
	}
	if manifestPath == "" {
		manifestPath = filepath.Join(filepath.Dir(modulePath), "manifest.json")
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("agent-python: read manifest %q: %w", manifestPath, err)
	}
	var manifest agentPythonManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("agent-python: parse manifest: %w", err)
	}
	if manifest.SchemaVersion != 2 || manifest.ABIVersion != "v1" {
		return nil, fmt.Errorf("agent-python: unsupported manifest schema/ABI %d/%q", manifest.SchemaVersion, manifest.ABIVersion)
	}
	if manifest.Target != "wasm32-wasip1" || manifest.Build.CompilerTarget != "wasm32-wasip1" || manifest.Build.ExecutionModel != "reactor" {
		return nil, errors.New("agent-python: manifest target must be a wasm32-wasip1 reactor")
	}
	if manifest.ArtifactProfile != "base" && manifest.ArtifactProfile != "numpy-core" {
		return nil, fmt.Errorf("agent-python: unsupported artifact profile %q", manifest.ArtifactProfile)
	}
	if !agentPythonCommitPattern.MatchString(manifest.Build.RepositoryCommit) {
		return nil, errors.New("agent-python: manifest producer commit must be 40 lowercase hex characters")
	}
	if manifest.Build.SourceDateEpoch == "" {
		return nil, errors.New("agent-python: manifest SOURCE_DATE_EPOCH is missing")
	}
	if filepath.Base(manifest.Artifact.Filename) != manifest.Artifact.Filename || manifest.Artifact.Filename != filepath.Base(modulePath) {
		return nil, fmt.Errorf("agent-python: manifest artifact filename %q does not bind module %q", manifest.Artifact.Filename, filepath.Base(modulePath))
	}

	wasmBytes, err := os.ReadFile(modulePath)
	if err != nil {
		return nil, fmt.Errorf("agent-python: read artifact %q: %w", modulePath, err)
	}
	if len(wasmBytes) < 8 || !bytes.Equal(wasmBytes[:8], []byte("\x00asm\x01\x00\x00\x00")) {
		return nil, errors.New("agent-python: artifact is not a WebAssembly core module")
	}
	if int64(len(wasmBytes)) != manifest.Artifact.Size {
		return nil, fmt.Errorf("agent-python: artifact size %d does not match manifest %d", len(wasmBytes), manifest.Artifact.Size)
	}
	digest := sha256.Sum256(wasmBytes)
	digestHex := hex.EncodeToString(digest[:])
	if digestHex != manifest.Artifact.SHA256 {
		return nil, fmt.Errorf("agent-python: artifact SHA-256 %s does not match manifest %s", digestHex, manifest.Artifact.SHA256)
	}

	exports := make(map[string]struct{}, len(manifest.Wasm.Exports))
	for _, name := range manifest.Wasm.Exports {
		exports[name] = struct{}{}
	}
	requiredExports := []string{"memory", "_initialize", "runtime_init", "runtime_prepare", "alloc", "dealloc", "execute"}
	var missing []string
	for _, name := range requiredExports {
		if _, ok := exports[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("agent-python: manifest is missing required exports: %v", missing)
	}

	hostCallCount := 0
	for _, imported := range manifest.Wasm.Imports {
		if imported.Module == "wasi_snapshot_preview1" {
			continue
		}
		if imported.Module == "agent_runtime_v1" && imported.Name == "host_call" {
			hostCallCount++
			continue
		}
		return nil, fmt.Errorf("agent-python: unexpected custom import %q.%q", imported.Module, imported.Name)
	}
	if hostCallCount != 1 {
		return nil, fmt.Errorf("agent-python: expected exactly one agent_runtime_v1.host_call import, got %d", hostCallCount)
	}

	return &AgentPythonArtifact{
		WasmBytes:      wasmBytes,
		Profile:        manifest.ArtifactProfile,
		ProducerCommit: manifest.Build.RepositoryCommit,
		SHA256:         digestHex,
		ManifestPath:   manifestPath,
	}, nil
}

type agentPythonRunRequest struct {
	RunID  string         `json:"run_id"`
	Code   string         `json:"code"`
	Inputs map[string]any `json:"inputs"`
}

func buildAgentPythonRunRequest(runID, method string, params map[string]any, script string) ([]byte, error) {
	if runID == "" {
		return nil, errors.New("agent-python: run ID is required")
	}
	if method == "" {
		method = "eval"
	}
	if params == nil {
		params = map[string]any{}
	}
	inputs := map[string]any{"method": method, "params": params}
	code := agentPythonPreparedCall
	if script != "" {
		inputs["script"] = script
		code = agentPythonUnpreparedCall
	}
	payload, err := json.Marshal(agentPythonRunRequest{RunID: runID, Code: code, Inputs: inputs})
	if err != nil {
		return nil, fmt.Errorf("agent-python: encode run request: %w", err)
	}
	if len(payload) > agentPythonPayloadMax {
		return nil, fmt.Errorf("agent-python: run request exceeds %d-byte guest bound", agentPythonPayloadMax)
	}
	return payload, nil
}

type agentPythonRunResponse struct {
	Status   string            `json:"status"`
	Result   json.RawMessage   `json:"result"`
	Receipts []json.RawMessage `json:"receipts"`
	Metrics  *struct {
		GuestTimeMS     *float64 `json:"guest_time_ms,omitempty"`
		CapabilityCalls uint32   `json:"capability_calls"`
		ResultBytes     uint32   `json:"result_bytes"`
	} `json:"metrics"`
	Error *struct {
		Code      string  `json:"code"`
		Message   string  `json:"message"`
		ErrorType *string `json:"error_type,omitempty"`
		Traceback *string `json:"traceback,omitempty"`
	} `json:"error"`
}

func decodeAgentPythonResponse(payload []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var response agentPythonRunResponse
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("agent-python: decode response: %w", err)
	}
	if err := ensureAgentPythonJSONEOF(decoder); err != nil {
		return nil, err
	}
	if response.Metrics == nil || (response.Metrics.GuestTimeMS != nil && *response.Metrics.GuestTimeMS < 0) {
		return nil, errors.New("agent-python: response metrics are invalid")
	}
	switch response.Status {
	case "ok":
		if response.Error != nil || len(response.Result) == 0 || bytes.Equal(response.Result, []byte("null")) {
			return nil, errors.New("agent-python: successful response has invalid result/error fields")
		}
		var result map[string]any
		if err := json.Unmarshal(response.Result, &result); err != nil || result == nil {
			return nil, errors.New("agent-python: evaluator result must be a JSON object")
		}
		return result, nil
	case "error":
		if response.Error == nil || response.Error.Code == "" || response.Error.Message == "" || !bytes.Equal(response.Result, []byte("null")) {
			return nil, errors.New("agent-python: failed response has invalid result/error fields")
		}
		result := map[string]any{
			"error":      response.Error.Message,
			"error_code": response.Error.Code,
		}
		if response.Error.ErrorType != nil {
			result["error_type"] = *response.Error.ErrorType
		}
		if response.Error.Traceback != nil {
			result["traceback"] = *response.Error.Traceback
		}
		return result, nil
	default:
		return nil, fmt.Errorf("agent-python: unsupported response status %q", response.Status)
	}
}

func ensureAgentPythonJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("agent-python: decode trailing response JSON: %w", err)
	}
	return errors.New("agent-python: response contains trailing JSON")
}
