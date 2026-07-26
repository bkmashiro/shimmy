# Agent Python Runtime bundle

This directory contains Shimmy's pinned, consumer-verified Agent Python Runtime
`numpy-core` bundle.

## Canonical files

```text
artifacts/agent-python-runtime-numpy-core.wasm
artifacts/manifest.json
artifacts/SHA256SUMS
artifacts/sbom.spdx.json
artifacts/THIRD_PARTY_NOTICES.md
artifacts/extension-selection.json
```

Verify the complete bundle with:

```bash
cd build/python-reactor/artifacts
sha256sum --check SHA256SUMS
```

The manifest binds the artifact to producer commit
`76b49158cc6c4824491561531bfe7e34872cb820`, ABI v1, the `numpy-core` profile,
and SHA-256
`90c27951b2d8c2c7a8b42705b365cb4231c6dad207aad5260d55d2f9a85f1034`.

Shimmy does not build the CPython/NumPy artifact in this directory. Artifact
production remains isolated in `bkmashiro/agent-python-runtime`; this repository
owns the Host adapter, manifest verification, protocol compatibility, and final
consumer E2E gates.
