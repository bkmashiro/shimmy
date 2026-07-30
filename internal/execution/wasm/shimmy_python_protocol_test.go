package wasm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerifyShimmyPythonArtifact(t *testing.T) {
	directory := t.TempDir()
	artifactPath := filepath.Join(directory, "shimmy-python-runtime-base.wasm")
	artifact := []byte("\x00asm\x01\x00\x00\x00fixture")
	require.NoError(t, os.WriteFile(artifactPath, artifact, 0o600))
	manifestPath := writeShimmyPythonManifestFixture(t, directory, artifactPath, artifact, strings.Repeat("a", 40))

	verified, err := verifyShimmyPythonArtifact(artifactPath, manifestPath, strings.Repeat("a", 40))
	require.NoError(t, err)
	require.Equal(t, "base", verified.Profile)
	require.Equal(t, strings.Repeat("a", 40), verified.ProducerCommit)
	require.Equal(t, artifact, verified.WasmBytes)
}

func TestVerifyShimmyPythonArtifactRejectsDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		match  string
	}{
		{
			name: "wrong project",
			mutate: func(manifest map[string]any) {
				manifest["producer"].(map[string]any)["project"] = "other"
			},
			match: "producer project",
		},
		{
			name: "wrong contract",
			mutate: func(manifest map[string]any) {
				manifest["artifact_contract"] = "other/v1"
			},
			match: "contract",
		},
		{
			name: "wrong commit",
			mutate: func(manifest map[string]any) {
				manifest["producer"].(map[string]any)["commit"] = strings.Repeat("b", 40)
			},
			match: "does not match expected Host commit",
		},
		{
			name: "custom import",
			mutate: func(manifest map[string]any) {
				manifest["wasm"].(map[string]any)["imports"] = []any{
					map[string]any{"module": "forbidden_host", "name": "call", "kind": "function"},
				}
			},
			match: "unexpected import module",
		},
		{
			name: "missing identity",
			mutate: func(manifest map[string]any) {
				manifest["wasm"].(map[string]any)["exports"] = []any{
					map[string]any{"name": "memory", "kind": "memory"},
				}
			},
			match: "missing required exports",
		},
		{
			name: "unknown top field",
			mutate: func(manifest map[string]any) {
				manifest["unexpected"] = true
			},
			match: "unknown field",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			artifactPath := filepath.Join(directory, "shimmy-python-runtime-base.wasm")
			artifact := []byte("\x00asm\x01\x00\x00\x00fixture")
			require.NoError(t, os.WriteFile(artifactPath, artifact, 0o600))
			manifestPath := writeShimmyPythonManifestFixture(t, directory, artifactPath, artifact, strings.Repeat("a", 40))
			var manifest map[string]any
			require.NoError(t, json.Unmarshal(mustReadFile(t, manifestPath), &manifest))
			test.mutate(manifest)
			mutated, err := json.Marshal(manifest)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(manifestPath, mutated, 0o600))

			_, err = verifyShimmyPythonArtifact(artifactPath, manifestPath, strings.Repeat("a", 40))
			require.ErrorContains(t, err, test.match)
		})
	}
}

func TestShimmyPythonRequestAndResponseContract(t *testing.T) {
	payload, err := buildShimmyPythonRequest("eval", map[string]any{"response": 4})
	require.NoError(t, err)
	require.JSONEq(t, `{"method":"eval","params":{"response":4}}`, string(payload))
	require.NotContains(t, string(payload), "script")

	result, err := decodeShimmyPythonResponse([]byte(`{"status":"ok","result":{"correct":true}}`))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"correct": true}, result)

	failed, err := decodeShimmyPythonResponse([]byte(`{"status":"error","error":{"type":"ValueError","message":"bad"}}`))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"error": "bad", "error_type": "ValueError"}, failed)

	_, err = decodeShimmyPythonResponse([]byte(`{"status":"ok","result":{}} {}`))
	require.ErrorContains(t, err, "trailing")
}

func writeShimmyPythonManifestFixture(
	t *testing.T,
	directory string,
	artifactPath string,
	artifact []byte,
	commit string,
) string {
	t.Helper()
	digest := sha256.Sum256(artifact)
	required := []string{
		"memory",
		"_initialize",
		"shimmy_python_runtime_identity",
		"shimmy_python_init",
		"shimmy_python_prepare",
		"alloc",
		"dealloc",
		"evaluate",
	}
	exports := make([]map[string]any, 0, len(required))
	for _, name := range required {
		kind := "function"
		if name == "memory" {
			kind = "memory"
		}
		exports = append(exports, map[string]any{"name": name, "kind": kind})
	}
	manifest := map[string]any{
		"schema":            "shimmy-python-runtime-artifact/v1",
		"artifact_contract": "shimmy-python-runtime/v1",
		"profile":           "base",
		"target":            "wasm32-wasip1",
		"execution_model":   "reactor",
		"identity_u32":      0x53505231,
		"producer": map[string]any{
			"project": "shimmy", "repository": "bkmashiro/shimmy", "commit": commit, "dirty": false,
		},
		"source_date_epoch":  1,
		"source_date_utc":    "1970-01-01T00:00:01Z",
		"source_lock_sha256": strings.Repeat("c", 64),
		"sources": []any{
			map[string]any{
				"name": "cpython", "version": "3.14.6", "kind": "source",
				"url":    "https://www.python.org/ftp/python/3.14.6/Python-3.14.6.tar.xz",
				"sha256": strings.Repeat("d", 64), "size": 1,
				"archive_root": "Python-3.14.6", "license": "Python-2.0",
			},
		},
		"patches": []any{},
		"artifact": map[string]any{
			"name": filepath.Base(artifactPath), "size": len(artifact), "sha256": hex.EncodeToString(digest[:]),
		},
		"wasm": map[string]any{
			"imports": []any{map[string]any{"module": "wasi_snapshot_preview1", "name": "fd_write", "kind": "function"}},
			"exports": exports,
		},
		"limits":       map[string]any{"request_max_bytes": 1 << 20, "response_max_bytes": 1 << 20},
		"capabilities": map[string]any{"environment": false, "filesystem_preopens": false, "network": false, "host_calls": false},
		"validation":   map[string]any{"structure": "passed", "runtime_identity": "pending-consumer-e2e", "base_smoke": "pending-consumer-e2e"},
		"unsupported":  []any{"host calls"},
	}
	encoded, err := json.Marshal(manifest)
	require.NoError(t, err)
	path := filepath.Join(directory, "manifest.json")
	require.NoError(t, os.WriteFile(path, encoded, 0o600))
	return path
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	return contents
}
