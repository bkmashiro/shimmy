package wasm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
)

const shimmyPythonPayloadMax = 1024 * 1024
const shimmyPythonArtifactIdentityV1 = 0x53505231

var shimmyPythonCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var shimmyPythonDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type ShimmyPythonArtifact struct {
	WasmBytes      []byte
	Profile        string
	ProducerCommit string
	Repository     string
	SHA256         string
	ManifestPath   string
}

type shimmyPythonManifest struct {
	Schema             string `json:"schema"`
	ArtifactContract   string `json:"artifact_contract"`
	Profile            string `json:"profile"`
	ProfileConstraints struct {
		LongDoubleParsing      string `json:"longdouble_parsing"`
		LongDoubleNativeParser bool   `json:"longdouble_native_parser"`
	} `json:"profile_constraints"`
	Target         string `json:"target"`
	ExecutionModel string `json:"execution_model"`
	IdentityU32    uint32 `json:"identity_u32"`
	Producer       struct {
		Project    string `json:"project"`
		Repository string `json:"repository"`
		Commit     string `json:"commit"`
		Dirty      bool   `json:"dirty"`
	} `json:"producer"`
	SourceDateEpoch  int64  `json:"source_date_epoch"`
	SourceDateUTC    string `json:"source_date_utc"`
	SourceLockSHA256 string `json:"source_lock_sha256"`
	Sources          []struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Kind        string `json:"kind"`
		URL         string `json:"url"`
		SHA256      string `json:"sha256"`
		Size        int64  `json:"size"`
		ArchiveRoot string `json:"archive_root"`
		License     string `json:"license"`
	} `json:"sources"`
	Patches []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"patches"`
	Artifact struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"artifact"`
	Wasm struct {
		Imports []struct {
			Module string `json:"module"`
			Name   string `json:"name"`
			Kind   string `json:"kind"`
		} `json:"imports"`
		Exports []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"exports"`
	} `json:"wasm"`
	Limits struct {
		RequestMaxBytes  int `json:"request_max_bytes"`
		ResponseMaxBytes int `json:"response_max_bytes"`
	} `json:"limits"`
	Capabilities struct {
		Environment        bool `json:"environment"`
		FilesystemPreopens bool `json:"filesystem_preopens"`
		Network            bool `json:"network"`
		HostCalls          bool `json:"host_calls"`
	} `json:"capabilities"`
	Validation struct {
		Structure       string `json:"structure"`
		RuntimeIdentity string `json:"runtime_identity"`
		BaseSmoke       string `json:"base_smoke"`
	} `json:"validation"`
	Unsupported []string `json:"unsupported"`
}

func verifyShimmyPythonArtifact(modulePath, manifestPath, expectedCommit string) (*ShimmyPythonArtifact, error) {
	if modulePath == "" {
		return nil, errors.New("shimmy-python: ModulePath must be set")
	}
	if !shimmyPythonCommitPattern.MatchString(expectedCommit) {
		return nil, errors.New("shimmy-python: expected Host commit must be 40 lowercase hex characters")
	}
	if manifestPath == "" {
		manifestPath = filepath.Join(filepath.Dir(modulePath), "manifest.json")
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("shimmy-python: read manifest %q: %w", manifestPath, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	var manifest shimmyPythonManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("shimmy-python: parse manifest: %w", err)
	}
	if err := ensureShimmyPythonJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("shimmy-python: manifest %w", err)
	}
	if manifest.Schema != "shimmy-python-runtime-artifact/v1" || manifest.ArtifactContract != "shimmy-python-runtime/v1" {
		return nil, fmt.Errorf("shimmy-python: unsupported manifest schema/contract %q/%q", manifest.Schema, manifest.ArtifactContract)
	}
	if manifest.Profile != "base" && manifest.Profile != "numpy-core" {
		return nil, fmt.Errorf("shimmy-python: unsupported artifact profile %q", manifest.Profile)
	}
	if manifest.Profile == "numpy-core" &&
		(manifest.ProfileConstraints.LongDoubleParsing != "binary64-fallback-on-wasi" ||
			manifest.ProfileConstraints.LongDoubleNativeParser) {
		return nil, errors.New("shimmy-python: NumPy profile constraints are invalid")
	}
	if manifest.Target != "wasm32-wasip1" || manifest.ExecutionModel != "reactor" || manifest.IdentityU32 != shimmyPythonArtifactIdentityV1 {
		return nil, errors.New("shimmy-python: manifest target, execution model, or identity is invalid")
	}
	if manifest.Producer.Project != "shimmy" {
		return nil, fmt.Errorf("shimmy-python: producer project must be shimmy, got %q", manifest.Producer.Project)
	}
	if !strings.HasSuffix(manifest.Producer.Repository, "/shimmy") || strings.Count(manifest.Producer.Repository, "/") != 1 {
		return nil, fmt.Errorf("shimmy-python: invalid producer repository %q", manifest.Producer.Repository)
	}
	if manifest.Producer.Dirty || !shimmyPythonCommitPattern.MatchString(manifest.Producer.Commit) {
		return nil, errors.New("shimmy-python: producer must be a clean 40-hex commit")
	}
	if manifest.Producer.Commit != expectedCommit {
		return nil, fmt.Errorf("shimmy-python: producer commit %s does not match expected Host commit %s", manifest.Producer.Commit, expectedCommit)
	}
	if manifest.SourceDateEpoch <= 0 || !shimmyPythonDigestPattern.MatchString(manifest.SourceLockSHA256) {
		return nil, errors.New("shimmy-python: source date or source-lock digest is invalid")
	}
	parsedTime, err := time.Parse(time.RFC3339, manifest.SourceDateUTC)
	if err != nil || parsedTime.Unix() != manifest.SourceDateEpoch {
		return nil, errors.New("shimmy-python: source date timestamp does not match epoch")
	}
	if len(manifest.Sources) == 0 {
		return nil, errors.New("shimmy-python: source inventory is empty")
	}
	for _, source := range manifest.Sources {
		if source.Name == "" || source.Version == "" || source.Kind == "" || source.Size <= 0 || source.ArchiveRoot == "" || source.License == "" || !shimmyPythonDigestPattern.MatchString(source.SHA256) {
			return nil, fmt.Errorf("shimmy-python: source entry %q is incomplete", source.Name)
		}
		parsed, parseErr := url.Parse(source.URL)
		if parseErr != nil || parsed.Scheme != "https" || !allowedShimmyPythonSourceURL(parsed) {
			return nil, fmt.Errorf("shimmy-python: source entry %q has an unapproved URL", source.Name)
		}
	}
	for _, patch := range manifest.Patches {
		if patch.Path == "" || filepath.IsAbs(patch.Path) || strings.Contains(filepath.ToSlash(patch.Path), "../") || !shimmyPythonDigestPattern.MatchString(patch.SHA256) {
			return nil, fmt.Errorf("shimmy-python: patch entry %q is invalid", patch.Path)
		}
	}
	if manifest.Limits.RequestMaxBytes != shimmyPythonPayloadMax || manifest.Limits.ResponseMaxBytes != shimmyPythonPayloadMax {
		return nil, errors.New("shimmy-python: payload limits do not match Host bounds")
	}
	if manifest.Capabilities.Environment || manifest.Capabilities.FilesystemPreopens || manifest.Capabilities.Network || manifest.Capabilities.HostCalls {
		return nil, errors.New("shimmy-python: artifact requests unsupported Host capabilities")
	}
	if manifest.Validation.Structure != "passed" {
		return nil, errors.New("shimmy-python: producer structure validation did not pass")
	}
	if filepath.Base(manifest.Artifact.Name) != manifest.Artifact.Name || manifest.Artifact.Name != filepath.Base(modulePath) {
		return nil, fmt.Errorf("shimmy-python: manifest artifact name %q does not bind module %q", manifest.Artifact.Name, filepath.Base(modulePath))
	}

	wasmBytes, err := os.ReadFile(modulePath)
	if err != nil {
		return nil, fmt.Errorf("shimmy-python: read artifact %q: %w", modulePath, err)
	}
	if len(wasmBytes) < 8 || !bytes.Equal(wasmBytes[:8], []byte("\x00asm\x01\x00\x00\x00")) {
		return nil, errors.New("shimmy-python: artifact is not a WebAssembly core module")
	}
	if int64(len(wasmBytes)) != manifest.Artifact.Size {
		return nil, fmt.Errorf("shimmy-python: artifact size %d does not match manifest %d", len(wasmBytes), manifest.Artifact.Size)
	}
	digest := sha256.Sum256(wasmBytes)
	digestHex := hex.EncodeToString(digest[:])
	if !shimmyPythonDigestPattern.MatchString(manifest.Artifact.SHA256) || digestHex != manifest.Artifact.SHA256 {
		return nil, fmt.Errorf("shimmy-python: artifact SHA-256 %s does not match manifest %s", digestHex, manifest.Artifact.SHA256)
	}

	exports := make(map[string]string, len(manifest.Wasm.Exports))
	for _, exported := range manifest.Wasm.Exports {
		if _, duplicate := exports[exported.Name]; duplicate {
			return nil, fmt.Errorf("shimmy-python: duplicate manifest export %q", exported.Name)
		}
		exports[exported.Name] = exported.Kind
	}
	requiredExports := map[string]string{
		"memory":                         "memory",
		"_initialize":                    "function",
		"shimmy_python_runtime_identity": "function",
		"shimmy_python_init":             "function",
		"shimmy_python_prepare":          "function",
		"alloc":                          "function",
		"dealloc":                        "function",
		"evaluate":                       "function",
	}
	var missing []string
	for name, kind := range requiredExports {
		if exports[name] != kind {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("shimmy-python: manifest is missing required exports: %v", missing)
	}
	for _, imported := range manifest.Wasm.Imports {
		if imported.Module != "wasi_snapshot_preview1" {
			return nil, fmt.Errorf("shimmy-python: unexpected import module %q", imported.Module)
		}
		if imported.Name == "" || imported.Kind == "" {
			return nil, errors.New("shimmy-python: incomplete import declaration")
		}
	}

	return &ShimmyPythonArtifact{
		WasmBytes:      wasmBytes,
		Profile:        manifest.Profile,
		ProducerCommit: manifest.Producer.Commit,
		Repository:     manifest.Producer.Repository,
		SHA256:         digestHex,
		ManifestPath:   manifestPath,
	}, nil
}

func allowedShimmyPythonSourceURL(parsed *url.URL) bool {
	switch parsed.Hostname() {
	case "www.python.org", "files.pythonhosted.org":
		return true
	case "github.com":
		return strings.HasPrefix(parsed.Path, "/WebAssembly/wasi-sdk/") ||
			strings.HasPrefix(parsed.Path, "/kateinoigakukun/wasi-vfs/") ||
			strings.HasPrefix(parsed.Path, "/bytecodealliance/wasmtime/")
	default:
		return false
	}
}

func verifyShimmyPythonCompiledModule(compiled wazero.CompiledModule) error {
	for _, definition := range compiled.ImportedFunctions() {
		moduleName, name, imported := definition.Import()
		if !imported || moduleName != "wasi_snapshot_preview1" {
			return fmt.Errorf("shimmy-python: compiled module has unexpected function import %q.%q", moduleName, name)
		}
	}
	for _, definition := range compiled.ImportedMemories() {
		moduleName, name, imported := definition.Import()
		if !imported || moduleName != "wasi_snapshot_preview1" {
			return fmt.Errorf("shimmy-python: compiled module has unexpected memory import %q.%q", moduleName, name)
		}
	}
	requiredFunctions := []string{
		"_initialize",
		"shimmy_python_runtime_identity",
		"shimmy_python_init",
		"shimmy_python_prepare",
		"alloc",
		"dealloc",
		"evaluate",
	}
	for _, name := range requiredFunctions {
		if _, ok := compiled.ExportedFunctions()[name]; !ok {
			return fmt.Errorf("shimmy-python: compiled module is missing function export %q", name)
		}
	}
	if _, ok := compiled.ExportedMemories()["memory"]; !ok {
		return errors.New("shimmy-python: compiled module is missing memory export")
	}
	return nil
}

type shimmyPythonRequest struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

func buildShimmyPythonRequest(method string, params map[string]any) ([]byte, error) {
	if method == "" {
		method = "eval"
	}
	if method != "eval" && method != "preview" {
		return nil, fmt.Errorf("shimmy-python: unsupported method %q", method)
	}
	if params == nil {
		params = map[string]any{}
	}
	payload, err := json.Marshal(shimmyPythonRequest{Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("shimmy-python: encode request: %w", err)
	}
	if len(payload) > shimmyPythonPayloadMax {
		return nil, fmt.Errorf("shimmy-python: request exceeds %d-byte Guest bound", shimmyPythonPayloadMax)
	}
	return payload, nil
}

type shimmyPythonResponse struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func decodeShimmyPythonResponse(payload []byte) (map[string]any, error) {
	if len(payload) > shimmyPythonPayloadMax {
		return nil, fmt.Errorf("shimmy-python: response exceeds %d-byte Host bound", shimmyPythonPayloadMax)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var response shimmyPythonResponse
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("shimmy-python: decode response: %w", err)
	}
	if err := ensureShimmyPythonJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("shimmy-python: response %w", err)
	}
	switch response.Status {
	case "ok":
		if response.Error != nil || len(response.Result) == 0 || bytes.Equal(response.Result, []byte("null")) {
			return nil, errors.New("shimmy-python: successful response has invalid result/error fields")
		}
		var result map[string]any
		if err := json.Unmarshal(response.Result, &result); err != nil || result == nil {
			return nil, errors.New("shimmy-python: evaluator result must be a JSON object")
		}
		return result, nil
	case "error":
		if response.Error == nil || response.Error.Type == "" || response.Error.Message == "" || (len(response.Result) != 0 && !bytes.Equal(response.Result, []byte("null"))) {
			return nil, errors.New("shimmy-python: failed response has invalid result/error fields")
		}
		return map[string]any{
			"error":      response.Error.Message,
			"error_type": response.Error.Type,
		}, nil
	default:
		return nil, fmt.Errorf("shimmy-python: unsupported response status %q", response.Status)
	}
}

func ensureShimmyPythonJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("contains invalid trailing JSON: %w", err)
	}
	return errors.New("contains trailing JSON")
}
