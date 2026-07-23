package qemurun

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadImageManifestResolvesAndVerifiesArtifacts(t *testing.T) {
	root := t.TempDir()
	kernel := writeArtifact(t, root, "vmlinuz", []byte("kernel"))
	initrd := writeArtifact(t, root, "initramfs.gz", []byte("initrd"))
	rootfs := writeArtifact(t, root, "evaluator.squashfs", []byte("rootfs"))
	manifestPath := writeManifest(t, root, imageManifestFixture{
		SchemaVersion:    1,
		Architecture:     "x86_64",
		SourceLockSHA256: digestFixture([]byte("source-lock")),
		Kernel:           artifactFixture{Path: filepath.Base(kernel), SHA256: digestFixture([]byte("kernel"))},
		Initrd:           artifactFixture{Path: filepath.Base(initrd), SHA256: digestFixture([]byte("initrd"))},
		RootFS:           artifactFixture{Path: filepath.Base(rootfs), SHA256: digestFixture([]byte("rootfs")), Format: "raw"},
	})

	got, err := LoadImageManifest(manifestPath, rootfs)
	if err != nil {
		t.Fatalf("LoadImageManifest: %v", err)
	}
	if got.Kernel != kernel || got.Initrd != initrd || got.RootFS != rootfs || got.RootFSFormat != "raw" {
		t.Fatalf("resolved image = %#v", got)
	}
}

func TestLoadImageManifestRejectsTamperedArtifact(t *testing.T) {
	root := t.TempDir()
	kernel := writeArtifact(t, root, "vmlinuz", []byte("kernel"))
	initrd := writeArtifact(t, root, "initramfs.gz", []byte("initrd"))
	rootfs := writeArtifact(t, root, "evaluator.qcow2", []byte("rootfs"))
	manifestPath := writeManifest(t, root, imageManifestFixture{
		SchemaVersion: 1,
		Architecture:  "x86_64",
		Kernel:        artifactFixture{Path: filepath.Base(kernel), SHA256: digestFixture([]byte("kernel"))},
		Initrd:        artifactFixture{Path: filepath.Base(initrd), SHA256: digestFixture([]byte("initrd"))},
		RootFS:        artifactFixture{Path: filepath.Base(rootfs), SHA256: digestFixture([]byte("rootfs")), Format: "qcow2"},
	})
	if err := os.WriteFile(kernel, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadImageManifest(manifestPath, rootfs)
	if !errors.Is(err, ErrArtifactDigestMismatch) {
		t.Fatalf("error = %v, want ErrArtifactDigestMismatch", err)
	}
}

func TestLoadImageManifestRejectsDifferentConfiguredRootFS(t *testing.T) {
	root := t.TempDir()
	kernel := writeArtifact(t, root, "vmlinuz", []byte("kernel"))
	initrd := writeArtifact(t, root, "initramfs.gz", []byte("initrd"))
	rootfs := writeArtifact(t, root, "manifest.qcow2", []byte("rootfs"))
	other := writeArtifact(t, root, "configured.qcow2", []byte("rootfs"))
	manifestPath := writeManifest(t, root, imageManifestFixture{
		SchemaVersion: 1,
		Architecture:  "x86_64",
		Kernel:        artifactFixture{Path: filepath.Base(kernel), SHA256: digestFixture([]byte("kernel"))},
		Initrd:        artifactFixture{Path: filepath.Base(initrd), SHA256: digestFixture([]byte("initrd"))},
		RootFS:        artifactFixture{Path: filepath.Base(rootfs), SHA256: digestFixture([]byte("rootfs")), Format: "qcow2"},
	})

	_, err := LoadImageManifest(manifestPath, other)
	if !errors.Is(err, ErrRootFSMismatch) {
		t.Fatalf("error = %v, want ErrRootFSMismatch", err)
	}
}

func TestLoadImageManifestRejectsUnsupportedSchemaArchitectureAndFormat(t *testing.T) {
	for name, mutate := range map[string]func(*imageManifestFixture){
		"schema":       func(m *imageManifestFixture) { m.SchemaVersion = 2 },
		"architecture": func(m *imageManifestFixture) { m.Architecture = "aarch64" },
		"format":       func(m *imageManifestFixture) { m.RootFS.Format = "vmdk" },
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			kernel := writeArtifact(t, root, "vmlinuz", []byte("kernel"))
			initrd := writeArtifact(t, root, "initramfs.gz", []byte("initrd"))
			rootfs := writeArtifact(t, root, "root.qcow2", []byte("rootfs"))
			manifest := imageManifestFixture{
				SchemaVersion: 1,
				Architecture:  "x86_64",
				Kernel:        artifactFixture{Path: filepath.Base(kernel), SHA256: digestFixture([]byte("kernel"))},
				Initrd:        artifactFixture{Path: filepath.Base(initrd), SHA256: digestFixture([]byte("initrd"))},
				RootFS:        artifactFixture{Path: filepath.Base(rootfs), SHA256: digestFixture([]byte("rootfs")), Format: "qcow2"},
			}
			mutate(&manifest)
			_, err := LoadImageManifest(writeManifest(t, root, manifest), rootfs)
			if !errors.Is(err, ErrInvalidImageManifest) {
				t.Fatalf("error = %v, want ErrInvalidImageManifest", err)
			}
		})
	}
}

func TestLoadImageManifestRejectsOversizedTrailingWhitespace(t *testing.T) {
	root := t.TempDir()
	kernel := writeArtifact(t, root, "vmlinuz", []byte("kernel"))
	initrd := writeArtifact(t, root, "initramfs.gz", []byte("initrd"))
	rootfs := writeArtifact(t, root, "root.qcow2", []byte("rootfs"))
	manifest := imageManifestFixture{
		SchemaVersion: 1,
		Architecture:  "x86_64",
		Kernel:        artifactFixture{Path: filepath.Base(kernel), SHA256: digestFixture([]byte("kernel"))},
		Initrd:        artifactFixture{Path: filepath.Base(initrd), SHA256: digestFixture([]byte("initrd"))},
		RootFS:        artifactFixture{Path: filepath.Base(rootfs), SHA256: digestFixture([]byte("rootfs")), Format: "qcow2"},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, bytes.Repeat([]byte{' '}, maxManifestBytes)...)
	manifestPath := filepath.Join(root, "oversized.json")
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = LoadImageManifest(manifestPath, rootfs)
	if !errors.Is(err, ErrInvalidImageManifest) {
		t.Fatalf("error = %v, want ErrInvalidImageManifest", err)
	}
}

type artifactFixture struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Format string `json:"format,omitempty"`
}

type imageManifestFixture struct {
	SchemaVersion    int             `json:"schema_version"`
	Architecture     string          `json:"architecture"`
	SourceLockSHA256 string          `json:"source_lock_sha256,omitempty"`
	Kernel           artifactFixture `json:"kernel"`
	Initrd           artifactFixture `json:"initrd"`
	RootFS           artifactFixture `json:"rootfs"`
}

func writeArtifact(t *testing.T, root, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func digestFixture(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeManifest(t *testing.T, root string, manifest imageManifestFixture) string {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
