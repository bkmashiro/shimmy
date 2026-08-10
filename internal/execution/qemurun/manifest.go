package qemurun

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
	"strings"
)

const maxManifestBytes = 1 << 20

var (
	ErrInvalidImageManifest   = errors.New("qemu runner: invalid image manifest")
	ErrArtifactDigestMismatch = errors.New("qemu runner: artifact digest mismatch")
	ErrRootFSMismatch         = errors.New("qemu runner: configured rootfs differs from manifest")
)

type imageArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Format string `json:"format,omitempty"`
}

type imageManifest struct {
	SchemaVersion    int           `json:"schema_version"`
	Architecture     string        `json:"architecture"`
	SourceLockSHA256 string        `json:"source_lock_sha256,omitempty"`
	Kernel           imageArtifact `json:"kernel"`
	Initrd           imageArtifact `json:"initrd"`
	RootFS           imageArtifact `json:"rootfs"`
}

type ResolvedImage struct {
	Kernel       string
	Initrd       string
	RootFS       string
	RootFSFormat string
}

func LoadImageManifest(manifestPath, configuredRootFS string) (ResolvedImage, error) {
	manifest, err := decodeImageManifest(manifestPath)
	if err != nil {
		return ResolvedImage{}, err
	}
	if manifest.SchemaVersion != 1 {
		return ResolvedImage{}, fmt.Errorf("%w: unsupported schema version %d", ErrInvalidImageManifest, manifest.SchemaVersion)
	}
	if manifest.Architecture != "x86_64" {
		return ResolvedImage{}, fmt.Errorf("%w: unsupported architecture %q", ErrInvalidImageManifest, manifest.Architecture)
	}
	switch manifest.RootFS.Format {
	case "qcow2", "raw":
	default:
		return ResolvedImage{}, fmt.Errorf("%w: unsupported rootfs format %q", ErrInvalidImageManifest, manifest.RootFS.Format)
	}
	if manifest.SourceLockSHA256 != "" {
		decoded, err := hex.DecodeString(strings.TrimSpace(manifest.SourceLockSHA256))
		if err != nil || len(decoded) != sha256.Size {
			return ResolvedImage{}, fmt.Errorf("%w: source_lock_sha256 must contain 64 hex characters", ErrInvalidImageManifest)
		}
	}

	base := filepath.Dir(manifestPath)
	kernel, err := resolveArtifact(base, "kernel", manifest.Kernel)
	if err != nil {
		return ResolvedImage{}, err
	}
	initrd, err := resolveArtifact(base, "initrd", manifest.Initrd)
	if err != nil {
		return ResolvedImage{}, err
	}
	rootfs, err := resolveArtifact(base, "rootfs", manifest.RootFS)
	if err != nil {
		return ResolvedImage{}, err
	}
	if strings.TrimSpace(configuredRootFS) == "" {
		return ResolvedImage{}, fmt.Errorf("%w: configured rootfs is empty", ErrRootFSMismatch)
	}
	manifestInfo, err := os.Stat(rootfs)
	if err != nil {
		return ResolvedImage{}, fmt.Errorf("qemu runner: stat manifest rootfs: %w", err)
	}
	configuredInfo, err := os.Stat(configuredRootFS)
	if err != nil {
		return ResolvedImage{}, fmt.Errorf("qemu runner: stat configured rootfs: %w", err)
	}
	if !os.SameFile(manifestInfo, configuredInfo) {
		return ResolvedImage{}, fmt.Errorf("%w: manifest=%q configured=%q", ErrRootFSMismatch, rootfs, configuredRootFS)
	}

	return ResolvedImage{
		Kernel:       kernel,
		Initrd:       initrd,
		RootFS:       rootfs,
		RootFSFormat: manifest.RootFS.Format,
	}, nil
}

func decodeImageManifest(path string) (imageManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return imageManifest{}, fmt.Errorf("%w: open %q: %v", ErrInvalidImageManifest, path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return imageManifest{}, fmt.Errorf("%w: read %q: %v", ErrInvalidImageManifest, path, err)
	}
	if len(data) > maxManifestBytes {
		return imageManifest{}, fmt.Errorf("%w: manifest exceeds %d bytes", ErrInvalidImageManifest, maxManifestBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest imageManifest
	if err := decoder.Decode(&manifest); err != nil {
		return imageManifest{}, fmt.Errorf("%w: decode %q: %v", ErrInvalidImageManifest, path, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return imageManifest{}, fmt.Errorf("%w: decode %q: %v", ErrInvalidImageManifest, path, err)
	}
	return manifest, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func resolveArtifact(base, name string, artifact imageArtifact) (string, error) {
	if strings.TrimSpace(artifact.Path) == "" {
		return "", fmt.Errorf("%w: %s path is empty", ErrInvalidImageManifest, name)
	}
	path := artifact.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%w: stat %s %q: %v", ErrInvalidImageManifest, name, path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s %q is not a regular file", ErrInvalidImageManifest, name, path)
	}
	expected, err := hex.DecodeString(strings.TrimSpace(artifact.SHA256))
	if err != nil || len(expected) != sha256.Size {
		return "", fmt.Errorf("%w: %s sha256 must contain 64 hex characters", ErrInvalidImageManifest, name)
	}
	actual, err := sha256File(path)
	if err != nil {
		return "", fmt.Errorf("qemu runner: hash %s %q: %w", name, path, err)
	}
	if !equalDigest(actual, expected) {
		return "", fmt.Errorf("%w: %s %q", ErrArtifactDigestMismatch, name, path)
	}
	return path, nil
}

func sha256File(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}

func equalDigest(actual, expected []byte) bool {
	if len(actual) != len(expected) {
		return false
	}
	var different byte
	for i := range actual {
		different |= actual[i] ^ expected[i]
	}
	return different == 0
}
