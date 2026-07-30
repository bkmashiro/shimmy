# Shimmy Python Runtime

Shimmy owns the CPython/WASI guest producer, artifact contract, source lock, and
consumer verifier under `build/python-reactor/producer/`.

Artifacts are generated, never committed. The canonical local output directory is:

```text
dist/shimmy-python/
  shimmy-python-runtime-base.wasm
  shimmy-python-runtime-numpy-core.wasm
  manifest.json
  sources.lock.json
  wasm-shape.json
  SHA256SUMS
  THIRD_PARTY_NOTICES.md
```

Build from locked official sources:

```bash
build/python-reactor/producer/build/build-base.sh \
  --work-dir /tmp/shimmy-python-work \
  --dist-dir "$PWD/dist/shimmy-python" \
  --repository "$(git config --get remote.origin.url | sed -E 's#.*github.com[:/]([^/]+/[^/.]+)(\.git)?#\1#')" \
  --commit "$(git rev-parse HEAD)"
```

The GitHub artifact lane is manual-only through `build.yml` with
`mode=shimmy-python`. Verify a downloaded bundle with:

```bash
scripts/verify-python-reactor-artifact.sh
```

No Python runtime artifact, external project ABI, or custom Host capability
module is stored or consumed from the repository tree.
