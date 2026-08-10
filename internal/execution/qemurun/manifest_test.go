package qemurun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadImageManifestVerifiesAllArtifacts(t *testing.T) {
	manifestPath, rootfs := writeImageFixture(t)
	image, err := LoadImageManifest(manifestPath, rootfs)
	require.NoError(t, err)
	assert.Equal(t, rootfs, image.RootFS)
	assert.Equal(t, "raw", image.RootFSFormat)
	assert.FileExists(t, image.Kernel)
	assert.FileExists(t, image.Initrd)
}

func TestLoadImageManifestRejectsDigestMismatch(t *testing.T) {
	manifestPath, rootfs := writeImageFixture(t)
	require.NoError(t, os.WriteFile(rootfs, []byte("changed"), 0o600))
	_, err := LoadImageManifest(manifestPath, rootfs)
	assert.ErrorIs(t, err, ErrArtifactDigestMismatch)
}

func TestLoadImageManifestRejectsDifferentConfiguredRootFS(t *testing.T) {
	manifestPath, _ := writeImageFixture(t)
	other := filepath.Join(t.TempDir(), "other.raw")
	require.NoError(t, os.WriteFile(other, []byte("rootfs"), 0o600))
	_, err := LoadImageManifest(manifestPath, other)
	assert.ErrorIs(t, err, ErrRootFSMismatch)
}

func TestLoadImageManifestRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	for name, body := range map[string]string{
		"unknown":  `{"schema_version":1,"architecture":"x86_64","unknown":true}`,
		"trailing": `{}` + "\n{}",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			_, err := LoadImageManifest(path, "ignored")
			assert.ErrorIs(t, err, ErrInvalidImageManifest)
		})
	}
}

func TestLoadImageManifestRejectsOversizedManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	require.NoError(t, os.WriteFile(path, make([]byte, maxManifestBytes+1), 0o600))
	_, err := LoadImageManifest(path, "ignored")
	assert.True(t, errors.Is(err, ErrInvalidImageManifest))
}

func writeImageFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	artifacts := map[string][]byte{
		"vmlinuz":    []byte("kernel"),
		"initrd.img": []byte("initrd"),
		"rootfs.raw": []byte("rootfs"),
	}
	for name, data := range artifacts {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), data, 0o600))
	}
	digest := func(name string) string {
		sum := sha256.Sum256(artifacts[name])
		return hex.EncodeToString(sum[:])
	}
	manifest := imageManifest{
		SchemaVersion: 1,
		Architecture:  "x86_64",
		Kernel:        imageArtifact{Path: "vmlinuz", SHA256: digest("vmlinuz")},
		Initrd:        imageArtifact{Path: "initrd.img", SHA256: digest("initrd.img")},
		RootFS:        imageArtifact{Path: "rootfs.raw", SHA256: digest("rootfs.raw"), Format: "raw"},
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	manifestPath := filepath.Join(root, "manifest.json")
	require.NoError(t, os.WriteFile(manifestPath, data, 0o600))
	return manifestPath, filepath.Join(root, "rootfs.raw")
}
